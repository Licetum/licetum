package loadtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultTolerance is how far a run may be from its budget before it fails: 15 %, the spread rule of the load test
// (see notebook/README.md).
const DefaultTolerance = 0.15

// Budget is what a configuration of the load test (a profile, a mode, a latency added to PostgreSQL and Redis) is
// allowed to cost, calibrated from a baseline run. A run is compared with it at ReferenceRPS, the highest ramp step the
// baseline sustained: the latencies and the per-request counts are those of that step, so the comparison is at the same
// load. A zero P99MS is not judged (the step had too few samples for a p99, see MinSamples).
type Budget struct {
	Profile        string  `yaml:"profile"`
	Mode           string  `yaml:"mode"`
	AddedLatencyMS float64 `yaml:"added_latency_ms"`
	// RPS is the sustained rate the configuration must keep: a run below RPS*(1-tolerance) fails.
	RPS float64 `yaml:"rps"`
	// ReferenceRPS is the step the other figures belong to.
	ReferenceRPS float64 `yaml:"reference_rps"`
	// These are ceilings: a run above value*(1+tolerance) fails.
	P50MS                     float64 `yaml:"p50_ms"`
	P99MS                     float64 `yaml:"p99_ms"`
	DBQueriesPerRequest       float64 `yaml:"db_queries_per_request"`
	RedisRoundTripsPerRequest float64 `yaml:"redis_round_trips_per_request"`
	// Note says where the figures come from (the commit and the machine of the baseline).
	Note string `yaml:"note,omitempty"`
}

// Budgets is the content of budgets.yaml.
type Budgets struct {
	Tolerance float64  `yaml:"tolerance"`
	Budgets   []Budget `yaml:"budgets"`
}

func (b Budget) key() string {
	return fmt.Sprintf("%s/%s/+%.0fms", b.Profile, b.Mode, b.AddedLatencyMS)
}

// LoadBudgets reads a budgets file.
func LoadBudgets(path string) (Budgets, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the file the caller names
	if err != nil {
		return Budgets{}, err
	}
	var out Budgets
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return Budgets{}, fmt.Errorf("read %s: %w", path, err)
	}
	if out.Tolerance == 0 {
		out.Tolerance = DefaultTolerance
	}
	if out.Tolerance < 0 || out.Tolerance >= 1 {
		return Budgets{}, fmt.Errorf("%s: tolerance %v: want a share between 0 and 1", path, out.Tolerance)
	}
	seen := map[string]bool{}
	for _, b := range out.Budgets {
		if seen[b.key()] {
			return Budgets{}, fmt.Errorf("%s: two budgets for %s", path, b.key())
		}
		seen[b.key()] = true
	}
	return out, nil
}

// LoadReport reads the result.json of a run directory.
func LoadReport(dir string) (Report, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "result.json")) //nolint:gosec // the report directory the caller names
	if err != nil {
		return Report{}, err
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Report{}, fmt.Errorf("read %s/result.json: %w", dir, err)
	}
	return rep, nil
}

// referenceStep is the measured window of a run at the given rate (the step of a ramp), or the only window of a
// fixed-rate run when the rate is 0.
func referenceStep(rep Report, rate float64) (Measurement, bool) {
	for _, m := range rep.Measurements {
		if rate == 0 || m.RateAsked == rate {
			return m, true
		}
	}
	return Measurement{}, false
}

// sustained is the rate a run kept: the highest passing step of a ramp, or the rate of a valid fixed-rate window.
func sustained(rep Report) float64 {
	if rep.Settings.Ramp {
		return rep.SustainedRPS
	}
	if len(rep.Measurements) == 1 && rep.Measurements[0].Valid {
		return rep.Measurements[0].AchievedRPS
	}
	return 0
}

// Calibrate makes the budget of a run: its sustained rate, and the latencies and counts of the step it sustained. It
// is how budgets.yaml is made from a baseline; a run that sustained nothing, or whose oracle found a wrong answer, makes
// no budget.
func Calibrate(rep Report, note string) (Budget, error) {
	rate := sustained(rep)
	if rate == 0 {
		return Budget{}, fmt.Errorf("%s: the run sustained no rate", runName(rep))
	}
	ref := rate
	if !rep.Settings.Ramp {
		ref = 0
	}
	step, ok := referenceStep(rep, ref)
	if !ok {
		return Budget{}, fmt.Errorf("%s: no window at %.0f requests/s", runName(rep), ref)
	}
	if step.Oracle.Mismatches > 0 {
		return Budget{}, fmt.Errorf("%s: the oracle found %d wrong answers", runName(rep), step.Oracle.Mismatches)
	}
	b := Budget{
		Profile: rep.Settings.ProfileName, Mode: rep.Settings.Mode, AddedLatencyMS: rep.Settings.AddedLatencyMS,
		RPS: rate, ReferenceRPS: step.RateAsked, P50MS: round2(step.Overall.P50),
		DBQueriesPerRequest: round2(step.Server.DBQueriesPerRequest), RedisRoundTripsPerRequest: round2(step.Server.RedisRoundTripsPerRequest),
		Note: note,
	}
	if p99, ok := step.Overall.Quantile(0.99, step.Overall.P99); ok {
		b.P99MS = round2(p99)
	}
	return b, nil
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }

func runName(rep Report) string {
	return fmt.Sprintf("%s/%s/+%.0fms", rep.Settings.ProfileName, rep.Settings.Mode, rep.Settings.AddedLatencyMS)
}

// Finding is one comparison of a run with its budget.
type Finding struct {
	Config   string
	Figure   string
	Budget   float64
	Measured float64
	Verdict  string // ok | miss | better | skipped
	Detail   string
}

// CheckBudgets compares the runs with the budgets. ok is false when any figure is more than the tolerance on the wrong
// side of its budget, when a run has no budget, when a run is not valid, or when its oracle found a wrong answer. A
// figure more than the tolerance on the right side is "better": it does not fail, and the report asks for the budget to
// be set again, so that an improvement is not lost to the next regression.
func CheckBudgets(budgets Budgets, runs map[string]Report) (findings []Finding, ok bool) {
	ok = true
	names := make([]string, 0, len(runs))
	for name := range runs {
		names = append(names, name)
	}
	sort.Strings(names)
	tol := budgets.Tolerance
	for _, name := range names {
		rep := runs[name]
		cfg := runName(rep)
		budget, found := findBudget(budgets, rep)
		if !found {
			findings = append(findings, Finding{Config: cfg, Figure: "budget", Verdict: "miss", Detail: "no budget for this configuration in budgets.yaml"})
			ok = false
			continue
		}
		add := func(f Finding) {
			f.Config = cfg
			if f.Verdict == "miss" {
				ok = false
			}
			findings = append(findings, f)
		}

		wrong := 0
		for _, m := range rep.Measurements {
			wrong += m.Oracle.Mismatches
		}
		if wrong > 0 {
			add(Finding{Figure: "oracle", Measured: float64(wrong), Verdict: "miss", Detail: "wrong answers"})
		}

		rate := sustained(rep)
		floor := budget.RPS * (1 - tol)
		switch {
		case rate < floor:
			add(Finding{Figure: "sustained rps", Budget: budget.RPS, Measured: rate, Verdict: "miss", Detail: fmt.Sprintf("below %.0f", floor)})
		case rate > budget.RPS*(1+tol):
			add(Finding{Figure: "sustained rps", Budget: budget.RPS, Measured: rate, Verdict: "better", Detail: "set the budget again"})
		default:
			add(Finding{Figure: "sustained rps", Budget: budget.RPS, Measured: rate, Verdict: "ok"})
		}
		if rate < floor {
			continue // the other figures belong to a step the run did not sustain
		}

		step, has := referenceStep(rep, budget.ReferenceRPS)
		if !has {
			add(Finding{Figure: "reference step", Budget: budget.ReferenceRPS, Verdict: "miss", Detail: "the run has no window at the rate the budget is set at"})
			continue
		}
		if !step.Valid {
			add(Finding{Figure: "reference step", Budget: budget.ReferenceRPS, Verdict: "miss", Detail: "the window is not valid: " + strings.Join(step.InvalidReasons, "; ")})
			continue
		}
		ceiling := func(figure string, limit, got float64) {
			switch {
			case limit <= 0:
				add(Finding{Figure: figure, Verdict: "skipped", Detail: "no budget"})
			case got < 0:
				add(Finding{Figure: figure, Budget: limit, Measured: got, Verdict: "miss", Detail: "not measured"})
			case got > limit*(1+tol):
				add(Finding{Figure: figure, Budget: limit, Measured: got, Verdict: "miss", Detail: fmt.Sprintf("above %.2f", limit*(1+tol))})
			case got < limit*(1-tol):
				add(Finding{Figure: figure, Budget: limit, Measured: got, Verdict: "better", Detail: "set the budget again"})
			default:
				add(Finding{Figure: figure, Budget: limit, Measured: got, Verdict: "ok"})
			}
		}
		ceiling("p50 ms", budget.P50MS, step.Overall.P50)
		if p99, published := step.Overall.Quantile(0.99, step.Overall.P99); published {
			ceiling("p99 ms", budget.P99MS, p99)
		} else if budget.P99MS > 0 {
			add(Finding{Figure: "p99 ms", Budget: budget.P99MS, Verdict: "skipped", Detail: fmt.Sprintf("%d samples, a p99 needs %d", step.Overall.Samples, MinSamples(0.99))})
		}
		ceiling("db queries per request", budget.DBQueriesPerRequest, step.Server.DBQueriesPerRequest)
		ceiling("redis round trips per request", budget.RedisRoundTripsPerRequest, step.Server.RedisRoundTripsPerRequest)
	}
	return findings, ok
}

func findBudget(budgets Budgets, rep Report) (Budget, bool) {
	for _, b := range budgets.Budgets {
		if b.Profile == rep.Settings.ProfileName && b.Mode == rep.Settings.Mode && b.AddedLatencyMS == rep.Settings.AddedLatencyMS {
			return b, true
		}
	}
	return Budget{}, false
}

// BudgetMarkdown is the table of findings, for the job summary.
func BudgetMarkdown(findings []Finding, ok bool, tolerance float64) string {
	var b strings.Builder
	verdict := "**Within the budgets.**"
	if !ok {
		verdict = "**Outside the budgets.**"
	}
	fmt.Fprintf(&b, "# Load test budgets\n\n%s A figure fails when it is more than %.0f %% on the wrong side of its budget.\n\n", verdict, 100*tolerance)
	b.WriteString("| configuration | figure | budget | measured | verdict | detail |\n|---|---|---:|---:|---|---|\n")
	for _, f := range findings {
		budget, measured := "–", "–"
		if f.Budget != 0 {
			budget = fmt.Sprintf("%.2f", f.Budget)
		}
		if f.Verdict != "skipped" && (f.Measured != 0 || f.Figure != "budget") {
			measured = fmt.Sprintf("%.2f", f.Measured)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", f.Config, f.Figure, budget, measured, f.Verdict, f.Detail)
	}
	return b.String()
}

// BudgetsYAML writes budgets as the file budgets.yaml, with the comment that explains it.
func BudgetsYAML(budgets Budgets) ([]byte, error) {
	sort.Slice(budgets.Budgets, func(i, j int) bool { return budgets.Budgets[i].key() < budgets.Budgets[j].key() })
	body, err := yaml.Marshal(budgets)
	if err != nil {
		return nil, err
	}
	header := "# Budgets of the check API load test (admin-api/loadtest), see notebook/README.md.\n" +
		"#\n" +
		"# One entry per profile, mode and latency added to PostgreSQL and Redis. A nightly run is compared with its entry by\n" +
		"# `licetum-loadtest budget`; a figure more than `tolerance` on the wrong side fails the workflow:\n" +
		"#   rps                 the sustained rate (the highest ramp step that met its objectives): a floor\n" +
		"#   reference_rps       the step the other figures belong to (the sustained step of the baseline)\n" +
		"#   p50_ms, p99_ms      latency of all the requests of that step: ceilings (p99 0: the step had too few samples)\n" +
		"#   db_queries_per_request, redis_round_trips_per_request   counted by the server: ceilings\n" +
		"# The figures come from the baseline of block F0 and are set again, on purpose, when a block improves them\n" +
		"# (`licetum-loadtest budget -calibrate`). A run that is better than its budget by more than the tolerance does not\n" +
		"# fail; its report asks for the budget to be set again.\n"
	return append([]byte(header), body...), nil
}
