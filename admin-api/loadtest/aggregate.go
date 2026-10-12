package loadtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// instabilityLimit is the spread between the runs of one configuration above which a percentile is marked unstable
// instead of being averaged silently: (max-min)/median of the p99 across the valid runs.
const instabilityLimit = 0.15

// loadedRun is one run read back from its directory.
type loadedRun struct {
	dir    string
	report Report
	window Measurement
}

// AggregateReport reads the runs in the given directories (each holds a result.json, written by a fixed-rate run) and
// writes one report: the runs grouped by configuration, the median and the spread of every percentile across the valid
// runs, the runs that were not valid with their reasons, and the limitations of the measurement. Runs of different
// configurations are never mixed in one table.
func AggregateReport(dirs []string) (string, error) {
	groups := map[string][]loadedRun{}
	var order []string
	var environments = map[string]Environment{}
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, "result.json")) //nolint:gosec // the report directories the caller names
		if err != nil {
			return "", fmt.Errorf("read %s: %w", dir, err)
		}
		var rep Report
		if err := json.Unmarshal(raw, &rep); err != nil {
			return "", fmt.Errorf("read %s/result.json: %w", dir, err)
		}
		if rep.Settings.Ramp || len(rep.Measurements) != 1 {
			return "", fmt.Errorf("%s: the aggregate takes fixed-rate runs (one measured window each)", dir)
		}
		key := configKey(rep)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], loadedRun{dir: dir, report: rep, window: rep.Measurements[0]})
		if _, ok := environments[dir]; !ok {
			if env, err := readEnvironment(dir); err == nil {
				environments[dir] = env
			}
		}
	}
	sort.Strings(order)

	var b strings.Builder
	b.WriteString("# Load test report\n\n")
	b.WriteString("Generated from the result directories of the runs. Each configuration is a table of its own; the numbers are the\n")
	b.WriteString("median of the valid runs, with the min-max range in brackets. A percentile is shown only when every run had enough samples\n")
	b.WriteString("for it; a spread above 15% of the median is marked unstable.\n\n")
	for _, key := range order {
		writeGroup(&b, groups[key], environments)
	}
	writeLimitations(&b)
	return b.String(), nil
}

// configKey is what makes two runs comparable: the same policy size (overrides included), the same server setup, the same
// traffic, the same rate and window, and the same experiments applied to the run.
func configKey(r Report) string {
	s, p := r.Settings, r.Profile
	return fmt.Sprintf("%s (%d subjects, %d groups, %d rules, %d resources) | mode %s | mix %s | consistency %s | rate %.0f | "+
		"measure %.0f s | warm %.0f s | oracle %.0f/s | db pool %d | access log %s | replicas %d | writes every %.0f s | "+
		"redis paused %.0f s | extra SQL %s",
		p.Name, p.Users, p.Groups, p.Rules, p.Resources, s.Mode, s.Mix, s.Consistency, s.Rate, s.MeasureSecs, s.WarmSeconds,
		s.OracleRate, s.DBPool, onOff(s.AccessLog), s.Replicas, s.WritesEverySec, s.PauseRedisSecs, nameOrNone(s.ExtraSQL))
}

// nameOrNone is the name of a file, or "none" when there is none.
func nameOrNone(name string) string {
	if name == "" {
		return "none"
	}
	return name
}

// writeGroup writes the table of one configuration: its runs, the validity of each, and the median and range of every
// operation's percentiles across the valid runs.
func writeGroup(b *strings.Builder, runs []loadedRun, environments map[string]Environment) {
	sort.Slice(runs, func(i, j int) bool { return runs[i].dir < runs[j].dir })
	first := runs[0].report
	fmt.Fprintf(b, "## %s\n\n", configKey(first))
	fmt.Fprintf(b, "Policy: %d subjects, %d groups, %d rules, %d resources (%d folders, %d documents). Seed %d. Runs: %d.\n\n",
		first.Profile.Users, first.Profile.Groups, first.RulesWritten, first.Profile.Resources,
		first.Profile.folders(), first.Profile.documents(), first.Settings.Seed, len(runs))
	if env, ok := environments[runs[0].dir]; ok {
		fmt.Fprintf(b, "Environment of the first run (`%s`): %s, %d logical CPUs, %s, Go %s, k6 %s, PostgreSQL %s, Redis %s, git %s%s.\n\n",
			filepath.Base(runs[0].dir), env.CPUModel, env.CPUs, env.OS, env.GoVersion, env.K6Version, env.PostgresVer,
			env.RedisVersion, shortCommit(env.GitCommit), dirtyNote(env.GitDirty))
	}

	b.WriteString("### Runs\n\n| run | seed | valid | requests | answered/s | errors | dropped | generator CPU | oracle mismatches | reasons |\n")
	b.WriteString("|---|---:|---|---:|---:|---:|---:|---:|---:|---|\n")
	var valid []loadedRun
	for _, r := range runs {
		w := r.window
		reasons := "–"
		if !w.Valid {
			reasons = strings.Join(w.InvalidReasons, "; ")
		} else {
			valid = append(valid, r)
		}
		fmt.Fprintf(b, "| %s | %d | %s | %d | %.1f | %.2f%% | %d | %.0f%% | %d | %s |\n", filepath.Base(r.dir), r.report.Settings.Seed,
			yesNo(w.Valid), w.Requests, w.AchievedRPS, 100*w.ErrorRate, w.Dropped, 100*w.K6CPUShare, w.Oracle.Mismatches, reasons)
	}
	b.WriteString("\n")

	if len(valid) == 0 {
		b.WriteString("No valid run: no percentile is published for this configuration.\n\n")
		return
	}
	b.WriteString("### Latency by request (valid runs)\n\n")
	b.WriteString("| request | samples (min-max) | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | spread |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for i, name := range opNames {
		var samples []float64
		series := map[string][]float64{"p50": nil, "p90": nil, "p95": nil, "p99": nil, "p999": nil}
		published := map[string]bool{"p50": true, "p90": true, "p95": true, "p99": true, "p999": true}
		for _, r := range valid {
			op := r.window.Operations[i]
			samples = append(samples, float64(op.Latency.Samples))
			for _, pc := range []struct {
				key string
				q   float64
				v   float64
			}{{"p50", 0.5, op.Latency.P50}, {"p90", 0.9, op.Latency.P90}, {"p95", 0.95, op.Latency.P95},
				{"p99", 0.99, op.Latency.P99}, {"p999", 0.999, op.Latency.P999}} {
				if _, ok := op.Latency.Quantile(pc.q, pc.v); !ok {
					published[pc.key] = false
					continue
				}
				series[pc.key] = append(series[pc.key], pc.v)
			}
		}
		if slices.Max(samples) == 0 {
			continue
		}
		cell := func(key string) string {
			if !published[key] || len(series[key]) == 0 {
				return "–"
			}
			med, lo, hi := medianRange(series[key])
			return fmt.Sprintf("%.1f [%.1f–%.1f]", med, lo, hi)
		}
		spread := "–"
		if published["p99"] && len(series["p99"]) > 1 {
			med, lo, hi := medianRange(series["p99"])
			if med > 0 && (hi-lo)/med > instabilityLimit {
				spread = fmt.Sprintf("**unstable** (%.0f%%)", 100*(hi-lo)/med)
			} else {
				spread = fmt.Sprintf("%.0f%%", 100*(hi-lo)/max(med, 1e-9))
			}
		}
		smin, smax := slices.Min(samples), slices.Max(samples)
		fmt.Fprintf(b, "| %s | %.0f–%.0f | %s | %s | %s | %s | %s | %s |\n", name, smin, smax,
			cell("p50"), cell("p90"), cell("p95"), cell("p99"), cell("p999"), spread)
	}
	b.WriteString("\nThe spread is (max-min)/median of the p99 across the valid runs. A p99 whose quantile had too few samples in any run is \"–\".\n\n")
}

// writeLimitations states what the numbers do not say. It is the same for every configuration; the report says it in
// full because a reader of a published result should not have to guess.
func writeLimitations(b *strings.Builder) {
	b.WriteString("## Limitations\n\n")
	b.WriteString("- One host for everything: the check server, k6, PostgreSQL and Redis share its CPUs and memory (k6 and the server\n")
	b.WriteString("  can be pinned with `-k6-cpus` and `-check-cpus`, the containers are not limited). The absolute numbers are those of this host.\n")
	b.WriteString("- The data is generated by one shape: folders of documents, one group per department, a fixed mix of rules. Deep hierarchies,\n")
	b.WriteString("  resources with several parents, nested groups, roles and `resource_where` rules are not in the generator yet.\n")
	b.WriteString("- The source of the synchronization is a PostgreSQL table in the same database: a network source (HTTP, another database\n")
	b.WriteString("  server) has other costs.\n")
	b.WriteString("- The oracle checks the answers against the semantics of the generated policy, implemented from the rule formulas. It catches\n")
	b.WriteString("  an engine that disagrees with them; it is not an independent authorization engine.\n")
	b.WriteString("- The oracle's own requests are real load on the server, and are counted in its metrics but not in the k6 numbers.\n")
	b.WriteString("- k6 assigns the iterations of a window to its virtual users itself: the seeds fix the choices of each virtual user, not the\n")
	b.WriteString("  order in which the requests run.\n")
	b.WriteString("- A window is measured after one warm-up window; the caches' TTL is 60 s, so the warm-up is at least that long.\n\n")
	b.WriteString("## How to reproduce\n\n")
	b.WriteString("```bash\n")
	b.WriteString("task loadtest:build\n")
	b.WriteString("cd admin-api && go run ./cmd/licetum-loadtest run -profile smoke -mode warm -seed 1 -out reports/x/run-1\n")
	b.WriteString("go run ./cmd/licetum-loadtest aggregate -out report.md reports/x/run-*\n")
	b.WriteString("```\n")
}

// medianRange is the median of values and their minimum and maximum.
func medianRange(values []float64) (float64, float64, float64) {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	med := sorted[n/2]
	if n%2 == 0 {
		med = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return med, sorted[0], sorted[n-1]
}

func readEnvironment(dir string) (Environment, error) {
	var env Environment
	raw, err := os.ReadFile(filepath.Join(dir, "environment.json")) //nolint:gosec // the report directories the caller names
	if err != nil {
		return env, err
	}
	return env, json.Unmarshal(raw, &env)
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func dirtyNote(dirty bool) string {
	if dirty {
		return " (working tree had changes)"
	}
	return ""
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "**no**"
}
