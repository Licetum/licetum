package loadtest

import (
	"context"
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
	// topStatementCount is how many statements the report names.
	topStatementCount = 8
	// keepDatabaseEnv keeps the run's database on the external server, to read its plans afterwards.
	keepDatabaseEnv = "LICETUM_LOADTEST_KEEP_DB"
	// extraSQLEnv names a file of SQL run once the warm-up is done and before the ramp: the experiments (an index, the
	// settings of the planner) are applied to the synchronized data, the way they would be in a running service.
	extraSQLEnv = "LICETUM_LOADTEST_EXTRA_SQL"
)

// Options are the settings of a run.
type Options struct {
	Profile      Profile
	CheckBin     string        // the licetum binary that serves the check API
	K6Bin        string        // the k6 binary that sends the traffic
	OutDir       string        // where the reports, the k6 summaries and the server log are written
	Mix          Mix           // the share of each request
	Consistency  string        // minimize_latency (caches allowed) or higher (every request is computed)
	RateStart    float64       // requests per second of the first step
	RateFactor   float64       // the rate of the next step is this many times the last one
	Step         time.Duration // how long a step lasts
	MaxSteps     int           // the ramp ends after this many steps
	SLOP99       time.Duration // a step passes while p99 stays within it
	MaxErrorRate float64       // and the share of responses that are not 2xx stays within it
	Concurrency  int           // the most virtual users k6 uses: the requests in flight
	DBPool       int           // the most connections of the check server to PostgreSQL: the product default is 10 plus 20 of overflow
	AccessLog    bool          // whether the check server writes its access log (off: the decisions are measured, not the logs)
	RequireRate  float64       // the run fails when the sustained rate is below it
}

// DefaultOptions are the settings of a run that the flags then change.
func DefaultOptions() Options {
	mix, _ := ParseMix(DefaultMix) // a constant that parses
	return Options{
		Profile:      profiles["smoke"],
		CheckBin:     "../core/bin/licetum",
		K6Bin:        "k6",
		OutDir:       "reports/loadtest",
		Mix:          mix,
		Consistency:  "minimize_latency",
		RateStart:    50,
		RateFactor:   2,
		Step:         15 * time.Second,
		MaxSteps:     8,
		SLOP99:       100 * time.Millisecond,
		MaxErrorRate: 0.01,
		Concurrency:  250,
		DBPool:       30,
	}
}

func (o Options) validate() error {
	if err := o.Profile.validate(); err != nil {
		return err
	}
	switch {
	case o.Consistency != "minimize_latency" && o.Consistency != "higher":
		return fmt.Errorf("consistency %q: want minimize_latency or higher", o.Consistency)
	case o.RateStart < 1 || o.RateFactor <= 1:
		return fmt.Errorf("the rate starts at 1 or more and grows by a factor above 1")
	case o.Step < time.Second || o.MaxSteps < 1:
		return fmt.Errorf("the steps last a second or more and there is at least one")
	case o.Concurrency < 1 || o.DBPool < 1:
		return fmt.Errorf("concurrency and the database pool are at least 1")
	case o.SLOP99 <= 0 || o.MaxErrorRate < 0:
		return fmt.Errorf("the p99 objective is above 0 and the error budget is not negative")
	}
	return nil
}

// Run seeds the policy of the profile, starts the check server, and ramps the traffic up until a step misses its
// targets. The report is written to OutDir whatever the verdict, so a failing run can be read. It returns an error when
// the run fails its requirement, or when the infrastructure could not be set up.
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
			Mix:          opts.Mix.String(),
			Consistency:  opts.Consistency,
			Concurrency:  opts.Concurrency,
			DBPool:       opts.DBPool,
			AccessLog:    opts.AccessLog,
			RateStart:    opts.RateStart,
			RateFactor:   opts.RateFactor,
			StepSeconds:  opts.Step.Seconds(),
			SLOP99MS:     ms(opts.SLOP99),
			MaxErrorRate: opts.MaxErrorRate,
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

	slog.Info("seeding", "profile", opts.Profile.Name)
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

	obs := enableStatements(ctx, observer{pool: e.pool, check: check})
	rep.DBStatements = obs.statements

	warm := warmer{
		base:        check.base,
		client:      newClient(1, warmUpTimeout),
		tokens:      tokens,
		backend:     backend,
		consistency: opts.Consistency,
	}
	runner, err := newK6Runner(k6Path, workDir, opts.OutDir, tokens, backend, opts.Profile, opts.Mix, opts.Consistency)
	if err != nil {
		return nil, err
	}

	rep.WarmUp, err = warm.warmUp(ctx)
	if err == nil {
		slog.Info("warmed up", "phases", rep.WarmUp)
		err = applyExtraSQL(ctx, e, &rep.Settings)
	}
	if err == nil {
		ramp := rampSettings{runner: runner, base: check.base, obs: obs, opts: opts}
		rep.TopStatements, rep.Steps, err = ramp.run(ctx)
	}
	rep.finish(opts.RequireRate)
	if werr := rep.write(opts.OutDir); werr != nil {
		return rep, werr
	}
	if err != nil {
		return rep, err
	}
	if !rep.Passed {
		if rep.SustainedRPS == 0 {
			return rep, fmt.Errorf("no step met its targets: the first rate, %.0f requests/s, already fails", opts.RateStart)
		}
		return rep, fmt.Errorf("sustained %.0f requests/s, the run requires %.0f", rep.SustainedRPS, opts.RequireRate)
	}
	return rep, nil
}

// rampSettings are what the ramp needs besides the options.
type rampSettings struct {
	runner *k6Runner
	base   string
	obs    observer
	opts   Options
}

// run ramps the traffic and returns the statements the database ran during it (none when they are not measured) and
// the steps. The seeding and the warm-up are before the ramp, so they are not in the statements of the run.
func (r rampSettings) run(ctx context.Context) ([]Statement, []Step, error) {
	var before statementCounts
	if r.obs.statements {
		var err error
		if before, err = snapshotStatements(ctx, r.obs.pool); err != nil {
			return nil, nil, err
		}
	}
	steps, err := r.steps(ctx)
	if !r.obs.statements || err != nil {
		return nil, steps, err
	}
	after, err := snapshotStatements(ctx, r.obs.pool)
	if err != nil {
		return nil, steps, err
	}
	return since(before, after, topStatementCount), steps, nil
}

// steps sends the traffic at the start rate, then at each rate the factor makes of the last, until a step misses its
// targets or the steps run out. Each step is read against the counters before and after it. The rate is a whole number
// of arrivals per second, as k6 takes it.
func (r rampSettings) steps(ctx context.Context) ([]Step, error) {
	var steps []Step
	rate := r.opts.RateStart
	for i := range r.opts.MaxSteps {
		arrivals := int(math.Round(rate))
		before, err := r.obs.read(ctx)
		if err != nil {
			return steps, err
		}
		sum, _, err := r.runner.run(ctx, stepSettings{
			base:        r.base,
			rate:        arrivals,
			duration:    r.opts.Step,
			preVUs:      preallocated(arrivals, r.opts.Concurrency),
			maxVUs:      r.opts.Concurrency,
			sloP99:      r.opts.SLOP99,
			maxErrorRte: r.opts.MaxErrorRate,
			name:        fmt.Sprintf("step-%02d-%d-rps", i+1, arrivals),
		})
		if err != nil {
			return steps, err
		}
		after, err := r.obs.read(ctx)
		if err != nil {
			return steps, err
		}
		step := stepFromK6(sum, float64(arrivals), r.opts.Step, r.opts.Mix, before, after)
		step.judge(r.opts.MaxErrorRate, r.opts.SLOP99)
		steps = append(steps, step)
		slog.Info("step", "asked", arrivals, "answered", step.AchievedRPS, "p99_ms", step.Latency.P99,
			"errors", step.ErrorRate, "dropped", step.Dropped, "cpu_cores", step.Server.CPUCores,
			"allowed", step.AllowedShare, "passed", step.Passed, "reasons", step.Reasons)
		if !step.Passed || ctx.Err() != nil {
			break
		}
		rate *= r.opts.RateFactor
	}
	return steps, nil
}

// newClient is an HTTP client for the warm-up: its timeout bounds one request.
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

// preallocated is how many virtual users k6 starts before a step: the arrivals in flight at once (Little's law: the rate
// times the time of an answer, taken as a fifth of a second), not the most it may use. Starting too many makes every
// one of them initialize in the first seconds of the step.
func preallocated(rate, maxVUs int) int {
	return min(maxVUs, max(10, rate/5))
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
