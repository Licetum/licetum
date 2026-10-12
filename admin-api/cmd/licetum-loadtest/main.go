// licetum-loadtest measures the check API of Licetum at a given size of policy:
//
//	licetum-loadtest -profile medium -check-bin ../core/bin/licetum
//
// It seeds a policy of the profile, starts the check binary, and ramps the traffic up until a step misses its latency or
// error targets. k6 sends the traffic (permission, check, AuthZEN and on-behalf requests) at the rate of each step; the
// harness reads the server's counters around each step. The results are written to -out as result.json and summary.md;
// in GitHub Actions the summary is also added to the job summary. The admin API takes no load: it is only used to write
// the policy, once. Docker is needed for PostgreSQL and Redis, unless LICETUM_TEST_POSTGRES_URI and
// LICETUM_TEST_REDIS_URI name running servers, and k6 must be installed (or named by -k6-bin).
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
	opts := loadtest.DefaultOptions()
	profile := flag.String("profile", "smoke", "size of the policy and of the subjects: "+strings.Join(loadtest.ProfileNames(), ", "))
	users := flag.Int("users", 0, "subjects, instead of the profile's")
	groups := flag.Int("groups", 0, "groups, instead of the profile's")
	rules := flag.Int("rules", 0, "assignment rules, instead of the profile's")
	resources := flag.Int("resources", 0, "resources, instead of the profile's")
	mix := flag.String("mix", loadtest.DefaultMix, "share of each request, as name=weight: permission, check, authzen, authzen_batch, on_behalf")
	flag.StringVar(&opts.Consistency, "consistency", opts.Consistency, "minimize_latency (caches allowed) or higher (every request is computed)")
	flag.Float64Var(&opts.RateStart, "rate-start", opts.RateStart, "requests per second of the first step")
	flag.Float64Var(&opts.RateFactor, "rate-factor", opts.RateFactor, "the rate of each step is this many times the previous one")
	flag.DurationVar(&opts.Step, "step", opts.Step, "length of a step")
	flag.IntVar(&opts.MaxSteps, "max-steps", opts.MaxSteps, "the ramp stops after this many steps")
	flag.DurationVar(&opts.SLOP99, "slo-p99", opts.SLOP99, "a step passes while its p99 stays within this")
	flag.Float64Var(&opts.MaxErrorRate, "max-error-rate", opts.MaxErrorRate, "a step passes while the share of responses that are not 2xx stays within this")
	flag.IntVar(&opts.Concurrency, "concurrency", opts.Concurrency, "the most virtual users k6 uses: the requests in flight")
	flag.IntVar(&opts.DBPool, "db-pool", opts.DBPool, "the most connections of the check server to PostgreSQL (its default: 10 plus 20 of overflow)")
	flag.BoolVar(&opts.AccessLog, "access-log", opts.AccessLog, "let the check server write its access log (it costs CPU)")
	flag.StringVar(&opts.CheckBin, "check-bin", opts.CheckBin, "the licetum binary that serves the check API")
	flag.StringVar(&opts.K6Bin, "k6-bin", opts.K6Bin, "the k6 binary that sends the traffic (grafana.com/docs/k6)")
	flag.StringVar(&opts.OutDir, "out", opts.OutDir, "where the results are written")
	flag.Float64Var(&opts.RequireRate, "require-rate", 0, "the run fails when the sustained rate is below this (for CI)")
	flag.Parse()

	prof, err := loadtest.LookupProfile(*profile)
	if err != nil {
		fail(err)
	}
	override(&prof.Users, *users)
	override(&prof.Groups, *groups)
	override(&prof.Rules, *rules)
	override(&prof.Resources, *resources)
	opts.Profile = prof
	if opts.Mix, err = loadtest.ParseMix(*mix); err != nil {
		fail(err)
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
	if err != nil {
		fail(err)
	}
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
	defer f.Close()
	if _, err := f.WriteString(summary + "\n"); err != nil {
		slog.Warn("write the job summary", "error", err)
	}
}

func fail(err error) {
	slog.Error("load test", "error", err)
	os.Exit(1)
}
