// licetum-loadtest measures the check API of Licetum at a size of policy. Two commands:
//
//	licetum-loadtest run -profile 10k -mode warm -seed 1          one measured window at a fixed rate
//	licetum-loadtest run -profile 10k -ramp -rate-start 50        the steps of a ramp, until one misses its objectives
//	licetum-loadtest aggregate -out report.md reports/x/run-*     the report of several runs of one configuration
//	licetum-loadtest budget -budgets budgets.yaml reports/x/*     compare runs with their budgets (fails when one misses by over 15 %)
//	licetum-loadtest budget -calibrate -out budgets.yaml reports/x/*   write the budgets from baseline runs
//
// A run seeds the policy, starts the check binary as its own process, warms it up, and then measures: k6 sends the
// traffic at the rate, the harness reads the server's counters around each window, and an oracle checks sampled answers
// against the decisions the generated policy must give. The results go to -out: environment.json, result.json,
// summary.md, the time series of the server, the k6 summaries and logs. The admin API takes no load: it writes the
// policy once. Docker is needed for PostgreSQL, unless LICETUM_TEST_POSTGRES_URI names a
// running server, and k6 must be installed (or named by -k6-bin).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/licetum/licetum/admin-api/loadtest"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCommand(os.Args[2:])
	case "aggregate":
		err = aggregateCommand(os.Args[2:])
	case "budget":
		err = budgetCommand(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
	}
	if err != nil {
		slog.Error("load test", "error", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: licetum-loadtest run [flags] | aggregate [flags] DIR... | budget [flags] DIR...")
	os.Exit(2)
}

func runCommand(args []string) error {
	opts := loadtest.DefaultOptions()
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	profile := fs.String("profile", "smoke", "size of the policy and of the subjects: "+strings.Join(loadtest.ProfileNames(), ", "))
	users := fs.Int("users", 0, "subjects, instead of the profile's")
	groups := fs.Int("groups", 0, "groups, instead of the profile's")
	rules := fs.Int("rules", 0, "assignment rules, instead of the profile's")
	resources := fs.Int("resources", 0, "resources, instead of the profile's")
	fs.StringVar(&opts.Mode, "mode", opts.Mode, "warm (the caches answer), cold (the evaluations ask what no earlier request of the run asked, so no cached answer fits them; a search repeats per subject) or mix (a skewed traffic)")
	fs.Int64Var(&opts.Seed, "seed", opts.Seed, "the seed of the traffic and of the oracle")
	mix := fs.String("mix", loadtest.DefaultMix, "share of each request, as name=weight: search_resource, evaluation, evaluations10, evaluations100")
	fs.Float64Var(&opts.Rate, "rate", opts.Rate, "arrivals per second of a fixed-rate run")
	fs.BoolVar(&opts.Ramp, "ramp", opts.Ramp, "ramp the rate instead of one fixed rate")
	fs.Float64Var(&opts.RateStart, "rate-start", opts.RateStart, "the first rate of a ramp")
	fs.Float64Var(&opts.RateFactor, "rate-factor", opts.RateFactor, "the rate of each ramp step is this many times the previous one")
	fs.IntVar(&opts.MaxSteps, "max-steps", opts.MaxSteps, "the ramp ends after this many steps")
	fs.DurationVar(&opts.Warm, "warm", opts.Warm, "the warm-up window, outside the measurement (at least the caches' TTL, 60 s)")
	fs.DurationVar(&opts.Measure, "measure", opts.Measure, "the measured window (each step of a ramp)")
	fs.DurationVar(&opts.SLOP99, "slo-p99", opts.SLOP99, "a ramp step passes while its p99 stays within this")
	fs.Float64Var(&opts.MaxErrorRate, "max-error-rate", opts.MaxErrorRate, "a ramp step passes while the share of responses that are not 2xx stays within this")
	fs.IntVar(&opts.Concurrency, "concurrency", opts.Concurrency, "the most virtual users k6 uses: the requests in flight")
	fs.IntVar(&opts.DBPool, "db-pool", opts.DBPool, "the most connections of the check server to PostgreSQL (its default: 10 plus 20 of overflow)")
	fs.BoolVar(&opts.AccessLog, "access-log", opts.AccessLog, "let the check server write its access log (it costs CPU)")
	fs.Float64Var(&opts.OracleRate, "oracle-rate", opts.OracleRate, "answers per second checked against the expected decisions (0 for none)")
	fs.StringVar(&opts.K6CPUs, "k6-cpus", opts.K6CPUs, "the CPUs k6 is pinned to, as taskset lists them (e.g. 10-11); empty for none")
	fs.StringVar(&opts.CheckCPUs, "check-cpus", opts.CheckCPUs, "the CPUs the check server is pinned to (e.g. 0-7); empty for none")
	fs.BoolVar(&opts.RawPoints, "raw-points", opts.RawPoints, "keep the point of every request (a large file, for the timing of the answers)")
	fs.DurationVar(&opts.AddedLatency, "added-latency", opts.AddedLatency, "latency added to each round trip from the check servers to PostgreSQL, through Toxiproxy, from the end of the warm-up (0 for none)")
	fs.BoolVar(&opts.RampThrough, "ramp-through", opts.RampThrough, "with -ramp: go on after a step fails, up to -max-steps, to see the server above its limit")
	fs.IntVar(&opts.Replicas, "replicas", opts.Replicas, "check servers sharing the database and the cache; the load is spread over them")
	fs.DurationVar(&opts.WritesEvery, "writes-every", opts.WritesEvery, "an admin write (a rule created, then deleted) every this long during the window (0 for none)")
	fs.Float64Var(&opts.RequireRate, "require-rate", 0, "a ramp run fails when the sustained rate is below this (for CI)")
	fs.StringVar(&opts.CheckBin, "check-bin", opts.CheckBin, "the licetum binary that serves the check API")
	fs.StringVar(&opts.K6Bin, "k6-bin", opts.K6Bin, "the k6 binary that sends the traffic (grafana.com/docs/k6)")
	fs.StringVar(&opts.OutDir, "out", opts.OutDir, "where the results are written")
	if err := fs.Parse(args); err != nil {
		return err
	}

	prof, err := loadtest.LookupProfile(*profile)
	if err != nil {
		return err
	}
	override(&prof.Users, *users)
	override(&prof.Groups, *groups)
	override(&prof.Rules, *rules)
	override(&prof.Resources, *resources)
	opts.Profile = prof
	if opts.Mix, err = loadtest.ParseMix(*mix); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	rep, err := loadtest.Run(ctx, opts)
	if rep != nil {
		summary := rep.Markdown()
		fmt.Println(summary)
		appendJobSummary(summary)
		fmt.Printf("results: %s (%.0fs)\n", opts.OutDir, time.Since(started).Seconds())
	}
	return err
}

func aggregateCommand(args []string) error {
	fs := flag.NewFlagSet("aggregate", flag.ExitOnError)
	out := fs.String("out", "", "the report to write (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("aggregate: name the directories of the runs")
	}
	text, err := loadtest.AggregateReport(fs.Args())
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Print(text)
		return nil
	}
	return os.WriteFile(*out, []byte(text), 0o600)
}

func budgetCommand(args []string) error {
	fs := flag.NewFlagSet("budget", flag.ExitOnError)
	path := fs.String("budgets", "loadtest/budgets.yaml", "the budgets to compare the runs with")
	calibrate := fs.Bool("calibrate", false, "write budgets from the runs instead of comparing them")
	out := fs.String("out", "", "with -calibrate: the file to write (default: stdout)")
	note := fs.String("note", "", "with -calibrate: where the figures come from (commit, machine), kept in each entry")
	steps := fs.Bool("steps", false, "print the table of the steps of each run (rate, validity, latency, queries and round trips per request) instead of comparing")
	slack := fs.Float64("latency-slack", 5, "with -calibrate: milliseconds a latency may exceed its budget on top of the tolerance")
	tolerance := fs.Float64("tolerance", loadtest.DefaultTolerance, "with -calibrate: the share a figure may be off its budget")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("budget: name the directories of the runs")
	}
	runs := map[string]loadtest.Report{}
	for _, dir := range fs.Args() {
		rep, err := loadtest.LoadReport(dir)
		if err != nil {
			return err
		}
		runs[dir] = rep
	}

	if *steps {
		for _, dir := range fs.Args() {
			fmt.Println(loadtest.StepsMarkdown(runs[dir]))
		}
		return nil
	}

	if *calibrate {
		budgets := loadtest.Budgets{Tolerance: *tolerance, LatencySlackMS: *slack}
		// The runs of one configuration make one budget (see loadtest.CalibrateRuns).
		groups := map[string][]loadtest.Report{}
		var order []string
		for _, dir := range fs.Args() {
			rep := runs[dir]
			key := fmt.Sprintf("%s/%s/%v", rep.Settings.ProfileName, rep.Settings.Mode, rep.Settings.AddedLatencyMS)
			if _, seen := groups[key]; !seen {
				order = append(order, key)
			}
			groups[key] = append(groups[key], rep)
		}
		for _, key := range order {
			b, err := loadtest.CalibrateRuns(groups[key], *note)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			budgets.Budgets = append(budgets.Budgets, b)
		}
		raw, err := loadtest.BudgetsYAML(budgets)
		if err != nil {
			return err
		}
		if *out == "" {
			fmt.Print(string(raw))
			return nil
		}
		return os.WriteFile(*out, raw, 0o600)
	}

	budgets, err := loadtest.LoadBudgets(*path)
	if err != nil {
		return err
	}
	findings, ok := loadtest.CheckBudgets(budgets, runs)
	summary := loadtest.BudgetMarkdown(findings, ok, budgets.Tolerance)
	fmt.Println(summary)
	appendJobSummary(summary)
	if !ok {
		return fmt.Errorf("the runs are outside their budgets")
	}
	return nil
}

// override replaces a value of the profile with the flag when the flag is set.
func override(field *int, flagValue int) {
	if flagValue > 0 {
		*field = flagValue
	}
}

// appendJobSummary adds the summary to the GitHub Actions job summary, when the run is in one.
func appendJobSummary(summary string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //nolint:gosec // the file the runner gives the job
	if err != nil {
		slog.Warn("open the job summary", "error", err)
		return
	}
	// The close is checked: on a file opened for writing, the error of the close can be the one that loses the data.
	if _, err := f.WriteString(summary + "\n"); err != nil {
		slog.Warn("write the job summary", "error", err)
	}
	if err := f.Close(); err != nil {
		slog.Warn("close the job summary", "error", err)
	}
}
