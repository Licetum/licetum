package loadtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Series of the check server the report reads (see engine/metrics.go). Keys are the series as /metrics prints them.
const (
	keyPermHit      = `licetum_permission_cache_total{result="hit"}`
	keyPermMiss     = `licetum_permission_cache_total{result="miss"}`
	keyCheckHit     = `licetum_check_cache_total{result="hit"}`
	keyCheckMiss    = `licetum_check_cache_total{result="miss"}`
	keyComputeSum   = "licetum_permission_compute_seconds_sum"
	keyComputeCount = "licetum_permission_compute_seconds_count"
	keyFetchSum     = "licetum_source_fetch_seconds_sum"
	keyFetchCount   = "licetum_source_fetch_seconds_count"
)

// serverOperation is the operation id each request kind carries in the server's metrics.
var serverOperation = [numOps]string{
	opPermission:   "get_permission",
	opCheck1:       "check_access",
	opCheck10:      "check_access",
	opCheck100:     "check_access",
	opAuthzen:      "authzen_evaluation",
	opAuthzenBatch: "authzen_evaluations",
	opOnBehalf:     "delegated_check",
}

// Operation is the measured distribution of one kind of request. Its Latency says how many samples it has, and a
// percentile is published only with enough of them (see Latency.Quantile).
//
// (The type itself is declared in measure.go.)

// Server is what the check server and its database did during a window, read from their own counters. A figure with
// nothing to divide, or not measured, is -1.
type Server struct {
	CPUCores                float64            `json:"cpu_cores"`
	PermissionCacheHit      float64            `json:"permission_cache_hit_ratio"`
	CheckCacheHit           float64            `json:"check_cache_hit_ratio"`
	PermissionComputeMS     float64            `json:"permission_compute_avg_ms"`
	SourceFetchMS           float64            `json:"source_fetch_avg_ms"`
	OperationMS             map[string]float64 `json:"operation_avg_ms"`
	DBBlocksReadPerRequest  float64            `json:"db_blocks_read_per_request"`
	DBBufferHitRatio        float64            `json:"db_buffer_hit_ratio"`
	DBCommitsPerRequest     float64            `json:"db_commits_per_request"`
	DBStatementMSPerRequest float64            `json:"db_statement_ms_per_request"`
	DBCallsPerRequest       float64            `json:"db_calls_per_request"`
}

// Settings are the options of the run that the numbers depend on.
type Settings struct {
	ProfileName    string  `json:"profile"`
	Mode           string  `json:"mode"`
	Seed           int64   `json:"seed"`
	Mix            string  `json:"mix"`
	Consistency    string  `json:"consistency"`
	Concurrency    int     `json:"concurrency"`
	DBPool         int     `json:"db_pool"`
	AccessLog      bool    `json:"access_log"`
	Ramp           bool    `json:"ramp"`
	Rate           float64 `json:"rate"`
	RateStart      float64 `json:"rate_start"`
	RateFactor     float64 `json:"rate_factor"`
	WarmSeconds    float64 `json:"warm_seconds"`
	MeasureSecs    float64 `json:"measure_seconds"`
	SLOP99MS       float64 `json:"slo_p99_ms"`
	MaxErrorRate   float64 `json:"max_error_rate"`
	OracleRate     float64 `json:"oracle_rate"`
	K6CPUs         string  `json:"k6_cpus"`
	WritesEverySec float64 `json:"writes_every_seconds"`
	CheckCPUs      string  `json:"check_cpus"`
	RequiredRate   float64 `json:"required_rate"`
	RawPointsKept  bool    `json:"raw_points_kept"`
	// ExtraSQL is the file applied after the warm-up, when the run is an experiment (empty otherwise).
	ExtraSQL string `json:"extra_sql,omitempty"`
}

// Machine is where the run happened, and with which tools: the numbers of a 2-CPU runner and a 16-CPU workstation are
// not comparable, and neither are the numbers of two k6 versions.
type Machine struct {
	CPUs      int    `json:"cpus"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
	K6        string `json:"k6"`
}

// Report is the result of a run: the measured windows (one, or the steps of a ramp), with what the server and the
// database did, and the verdict. SustainedRPS is the highest ramp step that passed.
type Report struct {
	Started       time.Time     `json:"started"`
	Finished      time.Time     `json:"finished"`
	Profile       Profile       `json:"profile"`
	Settings      Settings      `json:"settings"`
	Machine       Machine       `json:"machine"`
	Seeding       []phase       `json:"seeding"`
	RulesWritten  int           `json:"rules_written"`
	WarmUp        []phase       `json:"warm_up"`
	DBStatements  bool          `json:"db_statements"`
	TopStatements []Statement   `json:"top_statements,omitempty"`
	Storage       []TableSize   `json:"storage,omitempty"`
	Measurements  []Measurement `json:"measurements"`
	SustainedRPS  float64       `json:"sustained_rps"`
	RequiredRPS   float64       `json:"required_rps"`
	Passed        bool          `json:"passed"`
}

// finish sets the verdict. A fixed-rate run passes when its window is valid. A ramp passes when some step passed, the
// highest of them is the sustained rate, and it reaches the required rate.
func (r *Report) finish(required float64, ramp bool) {
	r.RequiredRPS = required
	r.SustainedRPS = 0
	if ramp {
		for _, m := range r.Measurements {
			if m.passes() {
				r.SustainedRPS = max(r.SustainedRPS, m.RateAsked)
			}
		}
		r.Passed = r.SustainedRPS > 0 && r.SustainedRPS >= required
	} else {
		r.Passed = len(r.Measurements) == 1 && r.Measurements[0].Valid
	}
	r.Finished = time.Now()
}

// write saves the report as result.json and summary.md in dir.
func (r *Report) write(dir string) error {
	if err := writeJSON(filepath.Join(dir, "result.json"), r); err != nil {
		return fmt.Errorf("write result.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(r.Markdown()), 0o600); err != nil {
		return fmt.Errorf("write summary.md: %w", err)
	}
	return nil
}

// Markdown is the summary of the run: the windows with their operations, the validity of each, and where the time went.
func (r *Report) Markdown() string {
	var b strings.Builder
	p := r.Profile
	fmt.Fprintf(&b, "# Load test: %s, %s mode, seed %d\n\n", p.Name, r.Settings.Mode, r.Settings.Seed)
	fmt.Fprintf(&b, "Policy: %d subjects in %d groups, %d assignment rules, %d resources (%d folders, %d documents).\n",
		p.Users, p.Groups, r.RulesWritten, p.Resources, p.folders(), p.documents())
	fmt.Fprintf(&b, "Check server: DB pool %d connections, consistency `%s`, access log %s, CPUs `%s`. Requests: `%s`.\n",
		r.Settings.DBPool, r.Settings.Consistency, onOff(r.Settings.AccessLog), cpuLabel(r.Settings.CheckCPUs), r.Settings.Mix)
	fmt.Fprintf(&b, "Load generator: k6 %s, at most %d virtual users, CPUs `%s`. Machine: %d CPUs, %s/%s, %s.\n",
		strings.TrimPrefix(r.Machine.K6, "k6 v"), r.Settings.Concurrency, cpuLabel(r.Settings.K6CPUs), r.Machine.CPUs, r.Machine.OS,
		r.Machine.Arch, r.Machine.GoVersion)
	fmt.Fprintf(&b, "Warm-up: %.0f s before the measurement. Measured window: %.0f s per window. Environment: `environment.json`.\n\n",
		r.Settings.WarmSeconds, r.Settings.MeasureSecs)

	verdict := "**Measured window is not valid.**"
	if r.Passed && !r.Settings.Ramp {
		verdict = "**Measured window is valid.**"
	}
	if r.Settings.Ramp {
		verdict = "**Sustained: none.** The first step already missed its targets."
		if r.SustainedRPS > 0 {
			verdict = fmt.Sprintf("**Sustained: %.0f requests/s** (p99 within %.0f ms, errors within %.2f%%).",
				r.SustainedRPS, r.Settings.SLOP99MS, 100*r.Settings.MaxErrorRate)
		}
	}
	fmt.Fprintf(&b, "%s\n\n", verdict)

	b.WriteString("## Seeding and warm-up\n\n| phase | seconds |\n|---|---:|\n")
	for _, ph := range append(append([]phase{}, r.Seeding...), r.WarmUp...) {
		fmt.Fprintf(&b, "| %s | %.2f |\n", ph.Name, ph.Seconds)
	}

	for _, m := range r.Measurements {
		b.WriteString("\n")
		m.markdown(&b, r.Settings.Ramp)
	}

	if r.DBStatements && len(r.TopStatements) > 0 {
		b.WriteString("\n## Statements with the most time during the measurement\n\n")
		b.WriteString("| total ms | calls | mean ms | statement |\n|---:|---:|---:|---|\n")
		for _, st := range r.TopStatements {
			fmt.Fprintf(&b, "| %.0f | %d | %.2f | `%s` |\n", st.TotalMS, st.Calls, st.MeanMS, strings.ReplaceAll(st.Query, "|", `\|`))
		}
	}
	if len(r.Storage) > 0 {
		b.WriteString("\n## Storage after the run\n\n| table | rows | total bytes | index bytes |\n|---|---:|---:|---:|\n")
		for _, t := range r.Storage {
			fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", t.Table, t.Rows, t.TotalBytes, t.IndexBytes)
		}
	}
	return b.String()
}

// markdown writes one measured window: its operations with their sample counts, the answers, the validity, and the
// server's share of the time.
func (m Measurement) markdown(b *strings.Builder, ramp bool) {
	title := fmt.Sprintf("## Window at %.0f requests/s", m.RateAsked)
	if !ramp {
		title = fmt.Sprintf("## Measured window at %.0f requests/s", m.RateAsked)
	}
	b.WriteString(title + "\n\n")
	b.WriteString("| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, op := range m.Operations {
		if op.Requests == 0 {
			continue
		}
		l := op.Latency
		fmt.Fprintf(b, "| %s | %d | %s | %s | %s | %s | %s | %s | %s |\n", op.Name, l.Samples, num(l.Mean), num(l.P50),
			num(l.P90), num(l.P95), publish(l, 0.99, l.P99), publish(l, 0.999, l.P999), num(l.Max))
	}
	b.WriteString("\nA percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 (\"–\" below that).\n\n")
	fmt.Fprintf(b, "- requests %d, answered 2xx %d, other %d (error rate %.2f%%), answered %.1f requests/s\n",
		m.Requests, m.Answered2xx, m.AnsweredOther, 100*m.ErrorRate, m.AchievedRPS)
	fmt.Fprintf(b, "- dropped iterations %d; allowed share of the decisions %s over %d decisions\n",
		m.Dropped, pct(m.AllowedShare), m.Decisions)
	fmt.Fprintf(b, "- load generator CPU %.0f%% of %d cores; server CPU %s cores; permission cache hit %s, check cache hit %s\n",
		100*m.K6CPUShare, m.K6Cores, num(m.Server.CPUCores), pct(m.Server.PermissionCacheHit), pct(m.Server.CheckCacheHit))
	fmt.Fprintf(b, "- oracle: %d answers checked (%d expected granted), %d mismatches\n",
		m.Oracle.Checked, m.Oracle.Granted, m.Oracle.Mismatches)
	if m.Valid {
		b.WriteString("- validity: valid\n")
	} else {
		fmt.Fprintf(b, "- validity: **invalid**: %s\n", strings.Join(m.InvalidReasons, "; "))
	}
	if ramp {
		if m.passes() {
			b.WriteString("- step: pass\n")
		} else {
			fmt.Fprintf(b, "- step: **fail**: %s\n", strings.Join(m.FailReasons, "; "))
		}
	}
}

// publish is the value of a percentile, or "–" when its quantile has too few samples.
func publish(l Latency, q, value float64) string {
	v, ok := l.Quantile(q, value)
	if !ok {
		return "–"
	}
	return num(v)
}

// serverSide reads what the check server and its database did between two readings.
func serverSide(before, after counters, requests int, elapsed time.Duration) Server {
	d := func(key string) float64 { return delta(before.series, after.series, key) }
	hit, miss := d(keyPermHit), d(keyPermMiss)
	checkHit, checkMiss := d(keyCheckHit), d(keyCheckMiss)
	srv := Server{
		CPUCores:            (after.cpu - before.cpu) / elapsed.Seconds(),
		PermissionCacheHit:  ratio(hit, hit+miss),
		CheckCacheHit:       ratio(checkHit, checkHit+checkMiss),
		PermissionComputeMS: avgMS(d(keyComputeSum), d(keyComputeCount)),
		SourceFetchMS:       avgMS(d(keyFetchSum), d(keyFetchCount)),
		OperationMS:         map[string]float64{},
	}
	for i, name := range opNames {
		op := serverOperation[i]
		srv.OperationMS[name] = avgMS(
			d(fmt.Sprintf(`licetum_http_request_duration_seconds_sum{operation=%q}`, op)),
			d(fmt.Sprintf(`licetum_http_request_duration_seconds_count{operation=%q}`, op)),
		)
	}
	reads := delta(before.db, after.db, "blks_read")
	hits := delta(before.db, after.db, "blks_hit")
	commits := delta(before.db, after.db, "xact_commit")
	srv.DBBlocksReadPerRequest = perRequest(reads, requests)
	srv.DBBufferHitRatio = ratio(hits, hits+reads)
	srv.DBCommitsPerRequest = perRequest(commits, requests)
	srv.DBStatementMSPerRequest, srv.DBCallsPerRequest = -1, -1
	if _, measured := after.db["calls"]; measured {
		srv.DBStatementMSPerRequest = perRequest(delta(before.db, after.db, "exec_ms"), requests)
		srv.DBCallsPerRequest = perRequest(delta(before.db, after.db, "calls"), requests)
	}
	return srv
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// ratio is num/den, or -1 when there is nothing to divide.
func ratio(num, den float64) float64 {
	if den <= 0 {
		return -1
	}
	return num / den
}

// avgMS is the average of a histogram's sum over its count, in milliseconds, or -1 without samples.
func avgMS(sum, count float64) float64 {
	if count <= 0 {
		return -1
	}
	return 1000 * sum / count
}

func perRequest(n float64, requests int) float64 {
	if requests == 0 {
		return -1
	}
	return n / float64(requests)
}

func num(v float64) string {
	if v < 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f", v)
}

func pct(v float64) string {
	if v < 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f%%", 100*v)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// machine describes the host the run happened on and the version of the load generator.
func machine(k6 string) Machine {
	return Machine{
		CPUs:      runtime.NumCPU(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		K6:        k6,
	}
}

// runtimeCPUs is the number of logical CPUs of the host.
func runtimeCPUs() int { return runtime.NumCPU() }

// cpuLabel is the CPU list a process is pinned to, or "all" when it is not pinned.
func cpuLabel(set string) string {
	if set == "" {
		return "all"
	}
	return set
}
