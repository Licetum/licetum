package loadtest

import (
	"encoding/json"
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
	opCheck:        "check_access",
	opAuthzen:      "authzen_evaluation",
	opAuthzenBatch: "authzen_evaluations",
	opOnBehalf:     "delegated_check",
}

// Latency is a distribution of request times, in milliseconds.
type Latency struct {
	P50  float64 `json:"p50"`
	P90  float64 `json:"p90"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

// Server is what the check server and its database did during a step, read from their own counters. A figure with
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

// Step is one rate of the ramp: what was asked, what came back, what the server spent, and whether it met the targets.
type Step struct {
	TargetRPS    float64            `json:"target_rps"`
	Seconds      float64            `json:"seconds"`
	Requests     int                `json:"requests"`
	OK           int                `json:"ok"`
	ErrorRate    float64            `json:"error_rate"`
	AchievedRPS  float64            `json:"achieved_rps"`
	Dropped      int                `json:"dropped_iterations"`
	Latency      Latency            `json:"latency_ms"`
	ByOperation  map[string]Latency `json:"by_operation_ms"`
	Decisions    int                `json:"decisions"`
	AllowedShare float64            `json:"allowed_share"`
	Server       Server             `json:"server"`
	Passed       bool               `json:"passed"`
	Reasons      []string           `json:"reasons,omitempty"`
}

// Settings are the options of the run that the numbers depend on.
type Settings struct {
	Mix          string  `json:"mix"`
	Consistency  string  `json:"consistency"`
	Concurrency  int     `json:"concurrency"`
	DBPool       int     `json:"db_pool"`
	AccessLog    bool    `json:"access_log"`
	RateStart    float64 `json:"rate_start"`
	RateFactor   float64 `json:"rate_factor"`
	StepSeconds  float64 `json:"step_seconds"`
	SLOP99MS     float64 `json:"slo_p99_ms"`
	MaxErrorRate float64 `json:"max_error_rate"`
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

// Report is the result of a run. SustainedRPS is the highest rate whose step met the targets.
type Report struct {
	Started       time.Time   `json:"started"`
	Finished      time.Time   `json:"finished"`
	Profile       Profile     `json:"profile"`
	Settings      Settings    `json:"settings"`
	Machine       Machine     `json:"machine"`
	Seeding       []phase     `json:"seeding"`
	RulesWritten  int         `json:"rules_written"`
	WarmUp        []phase     `json:"warm_up"`
	DBStatements  bool        `json:"db_statements"`
	TopStatements []Statement `json:"top_statements,omitempty"`
	Steps         []Step      `json:"steps"`
	SustainedRPS  float64     `json:"sustained_rps"`
	RequiredRPS   float64     `json:"required_rps"`
	Passed        bool        `json:"passed"`
}

// stepFromK6 turns the summary of a step into the step. target is the rate asked, dur the length of the step, and
// before and after the readings of the server around it.
func stepFromK6(s k6Summary, target float64, dur time.Duration, mix Mix, before, after counters) Step {
	requests := s.value("http_reqs", "count")
	// http_req_failed is a rate whose value is the share of the requests that failed (its passes and fails count the
	// other way round, as k6 counts the true values).
	errorRate := s.value("http_req_failed", "value")
	ok := requests * (1 - errorRate)
	decisions := s.value("decisions", "count")
	allowed := s.value("allowed", "count")
	step := Step{
		TargetRPS:    target,
		Seconds:      dur.Seconds(),
		Requests:     int(requests + 0.5),
		OK:           int(ok + 0.5),
		ErrorRate:    errorRate,
		AchievedRPS:  s.value("http_reqs", "rate") * (1 - errorRate),
		Dropped:      int(s.value("dropped_iterations", "count") + 0.5),
		Latency:      s.trend("http_req_duration"),
		ByOperation:  map[string]Latency{},
		Decisions:    int(decisions + 0.5),
		AllowedShare: ratio(allowed, decisions),
		Server:       serverSide(before, after, int(requests+0.5), dur),
	}
	for i, name := range opNames {
		if mix[i] > 0 {
			step.ByOperation[name] = s.trend("latency_" + name)
		}
	}
	return step
}

// judge sets the verdict of the step. It passes when the errors stay in the budget, p99 stays within the objective, and
// the answers keep up with the rate asked. Dropped iterations are the arrivals k6 could not start because every virtual
// user was still waiting for an answer: the step is saturated, whatever its latency.
func (s *Step) judge(maxErrorRate float64, sloP99 time.Duration) {
	s.Reasons = nil
	if s.ErrorRate > maxErrorRate {
		s.Reasons = append(s.Reasons, fmt.Sprintf("errors %.2f%% above %.2f%%", 100*s.ErrorRate, 100*maxErrorRate))
	}
	if s.Latency.P99 > ms(sloP99) {
		s.Reasons = append(s.Reasons, fmt.Sprintf("p99 %.1f ms above %v", s.Latency.P99, sloP99))
	}
	if s.AchievedRPS < 0.95*s.TargetRPS {
		s.Reasons = append(s.Reasons, fmt.Sprintf("%.0f answers/s, under 95%% of the %.0f asked", s.AchievedRPS, s.TargetRPS))
	}
	if s.Dropped > 0 {
		s.Reasons = append(s.Reasons, fmt.Sprintf("%d arrivals dropped: every virtual user was waiting", s.Dropped))
	}
	s.Passed = len(s.Reasons) == 0
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

// finish sets the sustained rate and the verdict of the run: the highest step that passed, and whether it reaches the
// required rate. A run in which no step passed has no sustained rate and fails.
func (r *Report) finish(required float64) {
	r.RequiredRPS = required
	r.SustainedRPS = 0
	for _, s := range r.Steps {
		if s.Passed {
			r.SustainedRPS = max(r.SustainedRPS, s.TargetRPS)
		}
	}
	r.Passed = r.SustainedRPS > 0 && r.SustainedRPS >= required
	r.Finished = time.Now()
}

// write saves the report as result.json and summary.md in dir.
func (r *Report) write(dir string) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write result.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(r.Markdown()), 0o600); err != nil {
		return fmt.Errorf("write summary.md: %w", err)
	}
	return nil
}

// Markdown is the summary of the run: the volume, the ramp, and where the time went at the sustained rate (or at the
// first rate, when none was sustained).
func (r *Report) Markdown() string {
	var b strings.Builder
	p := r.Profile
	fmt.Fprintf(&b, "# Load test: %s\n\n", p.Name)
	fmt.Fprintf(&b, "Policy: %d subjects in %d groups, %d assignment rules, %d resources (%d folders, %d documents).\n",
		p.Users, p.Groups, r.RulesWritten, p.Resources, p.folders(), p.documents())
	fmt.Fprintf(&b, "Check server: DB pool %d connections, consistency `%s`, access log %s. Requests: `%s`.\n",
		r.Settings.DBPool, r.Settings.Consistency, onOff(r.Settings.AccessLog), r.Settings.Mix)
	fmt.Fprintf(&b, "Load generator: k6 %s, at most %d virtual users. Machine: %d CPUs, %s/%s, %s.\n\n",
		strings.TrimPrefix(r.Machine.K6, "k6 v"), r.Settings.Concurrency, r.Machine.CPUs, r.Machine.OS, r.Machine.Arch,
		r.Machine.GoVersion)

	verdict := "**Sustained: none.** The first step already missed its targets."
	if r.SustainedRPS > 0 {
		verdict = fmt.Sprintf("**Sustained: %.0f requests/s** (p99 within %.0f ms, errors within %.2f%%).",
			r.SustainedRPS, r.Settings.SLOP99MS, 100*r.Settings.MaxErrorRate)
	}
	fmt.Fprintf(&b, "%s\n\n", verdict)

	b.WriteString("## Seeding and first synchronization\n\n| phase | seconds |\n|---|---:|\n")
	for _, ph := range append(append([]phase{}, r.Seeding...), r.WarmUp...) {
		fmt.Fprintf(&b, "| %s | %.2f |\n", ph.Name, ph.Seconds)
	}

	b.WriteString("\n## Ramp\n\n")
	b.WriteString("| asked/s | answered/s | errors | dropped | allowed | p50 ms | p95 ms | p99 ms | max ms | server CPU (cores) | permission cache hit | check cache hit | verdict |\n")
	b.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, s := range r.Steps {
		verdict := "pass"
		if !s.Passed {
			verdict = "**" + strings.Join(s.Reasons, "; ") + "**"
		}
		fmt.Fprintf(&b, "| %.0f | %.0f | %.2f%% | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			s.TargetRPS, s.AchievedRPS, 100*s.ErrorRate, s.Dropped, pct(s.AllowedShare),
			num(s.Latency.P50), num(s.Latency.P95), num(s.Latency.P99), num(s.Latency.Max),
			num(s.Server.CPUCores), pct(s.Server.PermissionCacheHit), pct(s.Server.CheckCacheHit), verdict)
	}

	if at := r.breakdownStep(); at != nil {
		fmt.Fprintf(&b, "\n## Where the time goes at %.0f requests/s\n\n", at.TargetRPS)
		b.WriteString("| request | p50 ms | p99 ms | server avg ms |\n|---|---:|---:|---:|\n")
		for _, name := range opNames {
			lat, ok := at.ByOperation[name]
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", name, num(lat.P50), num(lat.P99), num(at.Server.OperationMS[name]))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "- permission computed on a cache miss: %s ms on average\n", num(at.Server.PermissionComputeMS))
		fmt.Fprintf(&b, "- source fetched per synchronization: %s ms on average\n", num(at.Server.SourceFetchMS))
		fmt.Fprintf(&b, "- database: %s blocks read per request, buffer hit ratio %s, %s commits per request\n",
			num(at.Server.DBBlocksReadPerRequest), pct(at.Server.DBBufferHitRatio), num(at.Server.DBCommitsPerRequest))
		if r.DBStatements {
			fmt.Fprintf(&b, "- database statements: %s ms of statement time and %s calls per request\n",
				num(at.Server.DBStatementMSPerRequest), num(at.Server.DBCallsPerRequest))
		} else {
			b.WriteString("- database statements: not measured (the server does not preload pg_stat_statements)\n")
		}
	}

	if r.DBStatements && len(r.TopStatements) > 0 {
		b.WriteString("\n## Statements with the most time during the ramp\n\n")
		b.WriteString("| total ms | calls | mean ms | statement |\n|---:|---:|---:|---|\n")
		for _, st := range r.TopStatements {
			fmt.Fprintf(&b, "| %.0f | %d | %.2f | `%s` |\n", st.TotalMS, st.Calls, st.MeanMS, strings.ReplaceAll(st.Query, "|", `\|`))
		}
	}
	return b.String()
}

// breakdownStep is the step the breakdown describes: the highest one that passed, or the first when none did.
func (r *Report) breakdownStep() *Step {
	var best *Step
	for i := range r.Steps {
		if r.Steps[i].Passed {
			best = &r.Steps[i]
		}
	}
	if best == nil && len(r.Steps) > 0 {
		best = &r.Steps[0]
	}
	return best
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
