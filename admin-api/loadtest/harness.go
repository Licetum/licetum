package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"

	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

const (
	// warmUpTimeout bounds the warm-up: the first permission of a type synchronizes the whole type.
	warmUpTimeout = 30 * time.Minute
	// requestTimeout ends a request of the oracle that got no answer: such a request counts as a mismatch.
	requestTimeout = 10 * time.Second
	// topStatementCount is how many statements the report names.
	topStatementCount = 8
	// keepDatabaseEnv keeps the run's database on the external server, to read its plans afterwards.
	keepDatabaseEnv = "LICETUM_LOADTEST_KEEP_DB"
	// extraSQLEnv names a file of SQL run once the warm-up is done and before the measurement: an experiment (an index,
	// a setting of the planner) is applied to the synchronized data, the way it would be in a running service.
	extraSQLEnv = "LICETUM_LOADTEST_EXTRA_SQL"
	// sampleEvery is how often the server is read for the time series of a measured window.
	sampleEvery = 2 * time.Second
)

// Options are the settings of a run.
type Options struct {
	Profile      Profile
	CheckBin     string        // the licetum binary that serves the check API
	K6Bin        string        // the k6 binary that sends the traffic
	OutDir       string        // where the results, the environment, the time series and the k6 logs are written
	Mode         string        // warm (the caches answer), cold (every request is computed) or mix (a skewed traffic)
	Seed         int64         // the seed of the choices of the traffic and of the oracle
	Mix          Mix           // the share of each request
	Consistency  string        // minimize_latency (caches allowed); the cold mode asks with higher
	Rate         float64       // arrivals per second of a fixed-rate run
	Ramp         bool          // ramp the rate instead: start at RateStart, multiply by RateFactor, up to MaxSteps
	RateStart    float64       // the first rate of a ramp
	RateFactor   float64       // the rate of the next step is this many times the last one
	MaxSteps     int           // the ramp ends after this many steps
	Warm         time.Duration // the warm-up window, outside the measurement (at least the TTL of the caches)
	Measure      time.Duration // the measured window (each step of a ramp)
	SLOP99       time.Duration // a ramp step passes while p99 stays within it
	MaxErrorRate float64       // and the share of responses that are not 2xx stays within it
	Concurrency  int           // the most virtual users k6 uses: the requests in flight
	DBPool       int           // the most connections of the check server to PostgreSQL
	AccessLog    bool          // whether the check server writes its access log (off: the decisions are measured, not the logs)
	OracleRate   float64       // the answers per second the oracle checks against the expected decisions
	K6CPUs       string        // the CPUs k6 is pinned to (taskset), empty for none
	CheckCPUs    string        // the CPUs the check server is pinned to, empty for none
	RawPoints    bool          // keep the points of every request (for the measurements that need timing per second)
	RequireRate  float64       // a ramp run fails when the sustained rate is below it
}

// DefaultOptions are the settings of a run that the flags then change.
func DefaultOptions() Options {
	mix, _ := ParseMix(DefaultMix) // a constant that parses
	return Options{
		Profile:      profiles["smoke"],
		CheckBin:     "../core/bin/licetum",
		K6Bin:        "k6",
		OutDir:       "reports/loadtest",
		Mode:         "mix",
		Seed:         1,
		Mix:          mix,
		Consistency:  "minimize_latency",
		Rate:         50,
		RateStart:    50,
		RateFactor:   2,
		MaxSteps:     4,
		Warm:         60 * time.Second,
		Measure:      5 * time.Minute,
		SLOP99:       100 * time.Millisecond,
		MaxErrorRate: 0.01,
		Concurrency:  250,
		DBPool:       30,
		OracleRate:   5,
	}
}

func (o Options) validate() error {
	if err := o.Profile.validate(); err != nil {
		return err
	}
	switch {
	case o.Mode != "warm" && o.Mode != "cold" && o.Mode != "mix":
		return fmt.Errorf("mode %q: want warm, cold or mix", o.Mode)
	case o.Consistency != "minimize_latency" && o.Consistency != "higher":
		return fmt.Errorf("consistency %q: want minimize_latency or higher", o.Consistency)
	case o.Rate < 1 && !o.Ramp:
		return fmt.Errorf("the rate is at least 1 arrival per second")
	case o.Ramp && (o.RateStart < 1 || o.RateFactor <= 1 || o.MaxSteps < 1):
		return fmt.Errorf("a ramp starts at 1 or more, grows by a factor above 1, and has at least one step")
	case o.Measure < time.Second:
		return fmt.Errorf("the measured window lasts a second or more")
	case o.Warm < 0:
		return fmt.Errorf("the warm-up is not negative")
	case o.Concurrency < 1 || o.DBPool < 1:
		return fmt.Errorf("concurrency and the database pool are at least 1")
	case o.SLOP99 <= 0 || o.MaxErrorRate < 0:
		return fmt.Errorf("the p99 objective is above 0 and the error budget is not negative")
	}
	return nil
}

// Run seeds the policy of the profile, starts the check server, warms it up, and then measures: one window at the
// rate, or a ramp of windows. Every window is a k6 process between two readings of the server, with the oracle checking
// sampled answers during it. The report is written to OutDir whatever the verdict. It returns an error when the run
// fails its requirement, or when the infrastructure could not be set up.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	k6Path, err := exec.LookPath(opts.K6Bin)
	if err != nil {
		return nil, fmt.Errorf("k6 is not installed (https://grafana.com/docs/k6/latest/set-up/install-k6/), or pass -k6-bin: %w", err)
	}
	if err := os.MkdirAll(opts.OutDir, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", opts.OutDir, err)
	}
	// The tokens and the script go to a directory of their own, removed when the run ends: the report directory is kept.
	workDir, err := os.MkdirTemp("", "licetum-loadtest-")
	if err != nil {
		return nil, fmt.Errorf("create the work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	rep := &Report{
		Started: time.Now(),
		Profile: opts.Profile,
		Settings: Settings{
			Mode:          opts.Mode,
			Seed:          opts.Seed,
			Mix:           opts.Mix.String(),
			Consistency:   opts.Consistency,
			Concurrency:   opts.Concurrency,
			DBPool:        opts.DBPool,
			AccessLog:     opts.AccessLog,
			Ramp:          opts.Ramp,
			Rate:          opts.Rate,
			RateStart:     opts.RateStart,
			RateFactor:    opts.RateFactor,
			WarmSeconds:   opts.Warm.Seconds(),
			MeasureSecs:   opts.Measure.Seconds(),
			SLOP99MS:      ms(opts.SLOP99),
			MaxErrorRate:  opts.MaxErrorRate,
			OracleRate:    opts.OracleRate,
			K6CPUs:        opts.K6CPUs,
			CheckCPUs:     opts.CheckCPUs,
			RequiredRate:  opts.RequireRate,
			ProfileName:   opts.Profile.Name,
			ExtraSQL:      "",
			RawPointsKept: opts.RawPoints,
		},
		Machine: machine(k6Version(k6Path)),
	}

	// The server preloads pg_stat_statements, so the time of each statement is known; an external server may not.
	infra, err := support.StartWith(ctx, testcontainers.WithCmdArgs("-c", "shared_preload_libraries=pg_stat_statements"))
	if err != nil {
		return nil, fmt.Errorf("start postgres and redis (Docker, or LICETUM_TEST_POSTGRES_URI): %w", err)
	}
	defer func() {
		if os.Getenv(keepDatabaseEnv) != "" {
			// Kept for reading the plans of the run's statements: the database is the one of the run, on the server
			// named by LICETUM_TEST_POSTGRES_URI.
			if u, err := url.Parse(infra.DBURI); err == nil {
				slog.Info("keeping the database of the run", "database", strings.TrimPrefix(u.Path, "/"))
			}
			return
		}
		if err := infra.Terminate(context.Background()); err != nil {
			slog.Warn("terminate the infrastructure", "error", err)
		}
	}()

	e, err := newEnv(ctx, infra)
	if err != nil {
		return nil, err
	}
	defer e.close()

	slog.Info("seeding", "profile", opts.Profile.Name, "seed", opts.Seed)
	fx, err := seed(ctx, e, opts.Profile)
	if err != nil {
		return nil, err
	}
	rep.Seeding, rep.RulesWritten = fx.phases, fx.rules
	slog.Info("seeded", "rules", fx.rules, "folders", fx.folders, "documents", fx.documents)

	tokens, err := e.idp.tokens(opts.Profile.Users, opts.Profile.Groups)
	if err != nil {
		return nil, err
	}
	backend, err := e.idp.clientToken(backendClient)
	if err != nil {
		return nil, err
	}
	if _, err := e.admin.PutLogSettings(ctx, logSettings(opts.AccessLog)); err != nil {
		return nil, fmt.Errorf("set the log settings of the check server: %w", err)
	}

	check, err := startCheck(ctx, e, opts)
	if err != nil {
		return nil, err
	}
	defer check.stop()

	env, err := collectEnvironment(ctx, e, opts, opts.CheckBin)
	if err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(opts.OutDir, "environment.json"), env); err != nil {
		return nil, err
	}

	obs := enableStatements(ctx, observer{pool: e.pool, check: check})
	rep.DBStatements = obs.statements

	warm := warmer{
		base:        check.base,
		client:      newClient(1, warmUpTimeout),
		tokens:      tokens,
		backend:     backend,
		consistency: opts.Consistency,
	}
	rep.WarmUp, err = warm.warmUp(ctx)
	if err != nil {
		return finishRun(ctx, rep, e, opts, err, nil)
	}
	slog.Info("warmed up", "phases", rep.WarmUp)

	if err := applyExtraSQL(ctx, e, &rep.Settings); err != nil {
		return finishRun(ctx, rep, e, opts, err, nil)
	}

	runner, err := newK6Runner(k6Path, workDir, opts.OutDir, opts.K6CPUs, tokens, backend, opts.Profile, opts.Mix, opts.Consistency)
	if err != nil {
		return finishRun(ctx, rep, e, opts, err, nil)
	}
	mr := measurer{runner: runner, check: check, obs: obs, opts: opts, tokens: tokens, outDir: opts.OutDir}

	// The warm-up window: the caches fill, and the synchronizations settle, before anything is measured.
	if opts.Warm > 0 {
		warmRate := opts.Rate
		if opts.Ramp {
			warmRate = opts.RateStart
		}
		if _, err := mr.window(ctx, "warmup", warmRate, opts.Warm, false); err != nil {
			return finishRun(ctx, rep, e, opts, err, nil)
		}
	}

	var stmtBefore statementCounts
	if obs.statements {
		if stmtBefore, err = snapshotStatements(ctx, e.pool); err != nil {
			return finishRun(ctx, rep, e, opts, err, nil)
		}
	}
	if opts.Ramp {
		rep.Measurements, err = mr.ramp(ctx)
	} else {
		var m Measurement
		m, err = mr.window(ctx, "measure-01", opts.Rate, opts.Measure, true)
		rep.Measurements = []Measurement{m}
	}
	return finishRun(ctx, rep, e, opts, err, stmtBefore)
}

// finishRun ends a run: the statements of the ramp, the sizes of the tables, the verdict, and the report on disk. Errors
// that stopped the run are returned with the report, so a failing run can be read.
func finishRun(ctx context.Context, rep *Report, e *env, opts Options, runErr error, stmtBefore statementCounts) (*Report, error) {
	if runErr == nil && stmtBefore != nil {
		if after, err := snapshotStatements(ctx, e.pool); err == nil {
			rep.TopStatements = since(stmtBefore, after, topStatementCount)
		}
	}
	if runErr == nil {
		if sizes, err := tableSizes(ctx, e.pool); err == nil {
			rep.Storage = sizes
		} else {
			slog.Warn("size the tables", "error", err)
		}
	}
	rep.finish(opts.RequireRate, opts.Ramp)
	if werr := rep.write(opts.OutDir); werr != nil {
		return rep, werr
	}
	if runErr != nil {
		return rep, runErr
	}
	if opts.Ramp && !rep.Passed {
		if rep.SustainedRPS == 0 {
			return rep, fmt.Errorf("no step met its targets: the first rate, %.0f requests/s, already fails", opts.RateStart)
		}
		return rep, fmt.Errorf("sustained %.0f requests/s, the run requires %.0f", rep.SustainedRPS, opts.RequireRate)
	}
	if !opts.Ramp && !rep.Passed {
		return rep, fmt.Errorf("the measured window is not valid: %s", strings.Join(rep.Measurements[0].InvalidReasons, "; "))
	}
	return rep, nil
}

// measurer runs the measured windows: each one reads the server, sends the traffic with k6, samples the oracle and the
// time series during it, and reads the server again.
type measurer struct {
	runner *k6Runner
	check  *checkServer
	obs    observer
	opts   Options
	tokens []string
	outDir string
}

// window measures one window at a rate. A window kept apart from the measurement (the warm-up) is only sent.
func (m measurer) window(ctx context.Context, name string, rate float64, length time.Duration, measured bool) (Measurement, error) {
	seconds := int(math.Round(length.Seconds()))
	w := window{
		name:     name,
		mode:     m.opts.Mode,
		seed:     m.opts.Seed,
		rate:     int(math.Round(rate)),
		seconds:  seconds,
		preVUs:   preallocated(int(math.Round(rate)), m.opts.Concurrency),
		maxVUs:   m.opts.Concurrency,
		base:     m.check.base,
		rawEvery: m.opts.RawPoints && measured,
	}
	if !measured {
		_, err := m.runner.run(ctx, w)
		return Measurement{}, err
	}

	before, err := m.obs.read(ctx)
	if err != nil {
		return Measurement{}, err
	}
	stopSampler, err := startSampler(ctx, m.obs, sampleEvery, filepath.Join(m.outDir, name+".timeseries.jsonl"))
	if err != nil {
		return Measurement{}, err
	}
	stopOracle := make(chan struct{})
	oracleDone := make(chan Oracle, 1)
	go func() {
		oracleDone <- runOracle(ctx, m.check.base, serviceSlug, m.opts.Profile, m.tokens, uint64(max(m.opts.Seed, 0)), m.opts.OracleRate, stopOracle)
	}()

	res, runErr := m.runner.run(ctx, w)
	close(stopOracle)
	oracle := <-oracleDone
	stopSampler()
	if runErr != nil {
		return Measurement{}, runErr
	}
	after, err := m.obs.read(ctx)
	if err != nil {
		return Measurement{}, err
	}

	meas := measurementFrom(res, w, cpuCores(m.opts.K6CPUs, m.opts.Concurrency))
	meas.Oracle = oracle
	meas.Server = serverSide(before, after, meas.Requests, res.elapsed)
	meas.judgeMeasurement()
	slog.Info("window", "name", name, "asked", rate, "answered", meas.AchievedRPS, "p99_ms", meas.p99(), "errors", meas.ErrorRate,
		"dropped", meas.Dropped, "k6_cpu", meas.K6CPUShare, "oracle_mismatches", oracle.Mismatches, "valid", meas.Valid)
	return meas, nil
}

// ramp measures the windows of a ramp: the rate is multiplied by the factor after each step, until a step fails its
// objectives (or is invalid) or the steps run out.
func (m measurer) ramp(ctx context.Context) ([]Measurement, error) {
	var out []Measurement
	rate := m.opts.RateStart
	for i := range m.opts.MaxSteps {
		meas, err := m.window(ctx, fmt.Sprintf("measure-%02d", i+1), rate, m.opts.Measure, true)
		if err != nil {
			return out, err
		}
		meas.judgeStep(m.opts.MaxErrorRate, m.opts.SLOP99)
		out = append(out, meas)
		if !meas.Passed || ctx.Err() != nil {
			break
		}
		rate *= m.opts.RateFactor
	}
	return out, nil
}

// cpuCores is how many CPUs the generator may use: the pinned set, or every CPU of the host.
func cpuCores(set string, concurrency int) int {
	_ = concurrency
	if set == "" {
		return runtimeCPUs()
	}
	n := 0
	for _, part := range strings.Split(set, ",") {
		if a, b, ok := strings.Cut(strings.TrimSpace(part), "-"); ok {
			var lo, hi int
			_, _ = fmt.Sscanf(a, "%d", &lo)
			_, _ = fmt.Sscanf(b, "%d", &hi)
			n += hi - lo + 1
		} else if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}

// measurementFrom reads the summary of a window: the operations of the mix, the answers, the dropped iterations, and the
// CPU of the generator against its cores.
func measurementFrom(res k6Result, w window, cores int) Measurement {
	s := res.summary
	m := Measurement{
		Mode:          w.mode,
		Seed:          w.seed,
		RateAsked:     float64(w.rate),
		WindowSeconds: float64(w.seconds),
		Requests:      int(s.value("http_reqs", "count") + 0.5),
		Answered2xx:   int(s.value("answered_2xx_measure", "count") + 0.5),
		AnsweredOther: int(s.value("answered_other_measure", "count") + 0.5),
		ErrorRate:     s.value("failed_measure", "value"),
		Dropped:       int(s.value("dropped_iterations", "count") + 0.5),
		Decisions:     int(s.value("decisions_measure", "count") + 0.5),
		K6Cores:       cores,
	}
	m.Overall = s.latency("http_req_duration", m.Requests)
	allowed := int(s.value("allowed_measure", "count") + 0.5)
	m.AllowedShare = ratio(float64(allowed), float64(m.Decisions))
	if m.WindowSeconds > 0 {
		m.AchievedRPS = float64(m.Answered2xx) / m.WindowSeconds
	}
	if res.elapsed > 0 && cores > 0 {
		m.K6CPUShare = res.cpu.Seconds() / (res.elapsed.Seconds() * float64(cores))
	}
	for i, name := range opNames {
		requests := int(s.value("requests_"+name+"_measure", "count") + 0.5)
		m.Operations = append(m.Operations, Operation{
			Name:     opNames[i],
			Requests: requests,
			Latency:  s.latency("latency_"+name+"_measure", requests),
		})
	}
	return m
}

// startCheck runs `licetum check` against the infrastructure of the run. Its pool is the one the options name, with no
// overflow, so the number of connections to PostgreSQL is the setting under test. Its output goes to check.log.
func startCheck(ctx context.Context, e *env, opts Options) (*checkServer, error) {
	addr, err := freeAddr()
	if err != nil {
		return nil, err
	}
	logFile, err := os.Create(filepath.Join(opts.OutDir, "check.log"))
	if err != nil {
		return nil, fmt.Errorf("create the server log: %w", err)
	}
	bin, args := opts.CheckBin, []string{"check"}
	if opts.CheckCPUs != "" {
		bin, args = "taskset", []string{"-c", opts.CheckCPUs, opts.CheckBin, "check"}
	}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the binary is the one the run was asked to measure
	cmd.Env = append(os.Environ(), checkServerEnv(e, opts)...)
	cmd.Env = append(cmd.Env, "HTTP__ADDR="+addr)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start %s: %w", opts.CheckBin, err)
	}
	cs := &checkServer{cmd: cmd, base: "http://" + addr, log: logFile, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(cs.done)
	}()
	if err := cs.waitReady(ctx, time.Minute); err != nil {
		cs.stop()
		return nil, err
	}
	return cs, nil
}

// applyExtraSQL runs the file named by extraSQLEnv, if any, and records its name in the settings of the report.
func applyExtraSQL(ctx context.Context, e *env, settings *Settings) error {
	path := os.Getenv(extraSQLEnv)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the file the run was asked to apply
	if err != nil {
		return fmt.Errorf("read the extra SQL: %w", err)
	}
	// Without arguments pgx sends the text as it is, so the file may hold several statements.
	if _, err := e.pool.Exec(ctx, string(raw)); err != nil {
		return fmt.Errorf("apply the extra SQL %s: %w", path, err)
	}
	settings.ExtraSQL = filepath.Base(path)
	slog.Info("applied the extra SQL", "file", settings.ExtraSQL)
	return nil
}

// writeJSON writes a value as indented JSON to a file of the report directory.
func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// newClient is an HTTP client for the warm-up and the oracle: its timeout bounds one request.
func newClient(concurrency int, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency,
			MaxIdleConnsPerHost: concurrency,
			MaxConnsPerHost:     concurrency,
		},
	}
}

// preallocated is how many virtual users k6 starts before a window: the rate itself, at least 50, and never more than the
// most it may use. A virtual user that is started during the window initializes while an arrival waits for it, and an
// arrival that waits too long is dropped, which makes the window invalid.
func preallocated(rate, maxVUs int) int {
	return min(maxVUs, max(50, rate))
}

// logSettings are the logs of the check server during a run: no decision lines, the access log only when asked for,
// and the synchronizations kept, since their cost is part of what is measured.
func logSettings(accessLog bool) model.LogSettings {
	return model.LogSettings{
		Format:        "json",
		Level:         "info",
		Subject:       "omit",
		SampleAllowed: 1,
		Events:        model.LogEvents{Access: accessLog, Decision: false, Sync: true},
	}
}
