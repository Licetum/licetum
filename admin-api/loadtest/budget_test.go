package loadtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// step builds a passing ramp window at a rate with the figures a budget looks at.
func step(rate, p50, p99, db, redis float64) Measurement {
	passed := true
	return Measurement{
		RateAsked: rate, Valid: true, Passed: &passed, AchievedRPS: rate,
		Overall: Latency{Samples: 20000, P50: p50, P99: p99},
		Server:  Server{DBQueriesPerRequest: db, RedisRoundTripsPerRequest: redis},
	}
}

func run(profile, mode string, latency, sustainedRPS float64, steps ...Measurement) Report {
	return Report{
		Settings:     Settings{ProfileName: profile, Mode: mode, AddedLatencyMS: latency, Ramp: true},
		Measurements: steps, SustainedRPS: sustainedRPS, Passed: true,
	}
}

func baseline() Budgets {
	return Budgets{Tolerance: DefaultTolerance, Budgets: []Budget{{
		Profile: "medium", Mode: "warm", RPS: 800, ReferenceRPS: 800,
		P50MS: 4, P99MS: 20, DBQueriesPerRequest: 2, RedisRoundTripsPerRequest: 3,
	}}}
}

func verdicts(findings []Finding) map[string]string {
	out := map[string]string{}
	for _, f := range findings {
		out[f.Figure] = f.Verdict
	}
	return out
}

func TestRunWithinBudgetPasses(t *testing.T) {
	// 10 % off on every figure is inside the 15 % tolerance.
	rep := run("medium", "warm", 0, 800, step(400, 3, 10, 2, 3), step(800, 4.4, 22, 2.2, 3.3))
	findings, ok := CheckBudgets(baseline(), map[string]Report{"warm": rep})
	if !ok {
		t.Fatalf("a run within 15 %% failed: %+v", findings)
	}
}

func TestEachFigureFailsPastTheTolerance(t *testing.T) {
	cases := map[string]struct {
		rep    Report
		figure string
	}{
		"rps":     {run("medium", "warm", 0, 400, step(400, 4, 20, 2, 3)), "sustained rps"},
		"p50":     {run("medium", "warm", 0, 800, step(800, 4.7, 20, 2, 3)), "p50 ms"},
		"p99":     {run("medium", "warm", 0, 800, step(800, 4, 23.1, 2, 3)), "p99 ms"},
		"queries": {run("medium", "warm", 0, 800, step(800, 4, 20, 2.4, 3)), "db queries per request"},
		"redis":   {run("medium", "warm", 0, 800, step(800, 4, 20, 2, 3.5)), "redis round trips per request"},
	}
	for name, c := range cases {
		findings, ok := CheckBudgets(baseline(), map[string]Report{name: c.rep})
		if ok || verdicts(findings)[c.figure] != "miss" {
			t.Errorf("%s: a miss past the tolerance was not caught: ok=%v %+v", name, ok, findings)
		}
	}
}

func TestBetterThanTheBudgetDoesNotFailButAsksForANewBudget(t *testing.T) {
	rep := run("medium", "warm", 0, 800, step(800, 2, 10, 1, 1))
	findings, ok := CheckBudgets(baseline(), map[string]Report{"w": rep})
	v := verdicts(findings)
	if !ok || v["db queries per request"] != "better" || v["p50 ms"] != "better" {
		t.Fatalf("improvement: ok=%v %+v", ok, findings)
	}
}

func TestMissingBudgetOracleAndInvalidWindowFail(t *testing.T) {
	if _, ok := CheckBudgets(baseline(), map[string]Report{"x": run("large", "warm", 0, 800, step(800, 4, 20, 2, 3))}); ok {
		t.Error("a run without a budget passed")
	}
	if _, ok := CheckBudgets(baseline(), map[string]Report{"x": run("medium", "warm", 100, 800, step(800, 4, 20, 2, 3))}); ok {
		t.Error("the added latency is part of the configuration: +100 ms has no budget here")
	}
	wrong := step(800, 4, 20, 2, 3)
	wrong.Oracle.Mismatches = 1
	if _, ok := CheckBudgets(baseline(), map[string]Report{"x": run("medium", "warm", 0, 800, wrong)}); ok {
		t.Error("a wrong answer passed")
	}
	invalid := step(800, 4, 20, 2, 3)
	invalid.Valid, invalid.InvalidReasons = false, []string{"iterations dropped"}
	if _, ok := CheckBudgets(baseline(), map[string]Report{"x": run("medium", "warm", 0, 800, invalid)}); ok {
		t.Error("an invalid reference window passed")
	}
}

func TestP99IsSkippedWithoutEnoughSamples(t *testing.T) {
	few := step(800, 4, 500, 2, 3)
	few.Overall.Samples = 500
	findings, ok := CheckBudgets(baseline(), map[string]Report{"x": run("medium", "warm", 0, 800, few)})
	if !ok || verdicts(findings)["p99 ms"] != "skipped" {
		t.Fatalf("a p99 from 500 samples was judged: ok=%v %+v", ok, findings)
	}
}

func TestCalibrateAndRoundTripThroughYAML(t *testing.T) {
	rep := run("medium", "mix", 100, 200, step(100, 700, 900, 2.5, 3.1), step(200, 710.126, 950.5, 2.51, 3.11))
	b, err := Calibrate(rep, "test")
	if err != nil {
		t.Fatal(err)
	}
	if b.RPS != 200 || b.ReferenceRPS != 200 || b.P50MS != 710.13 || b.P99MS != 950.5 || b.AddedLatencyMS != 100 {
		t.Fatalf("budget: %+v", b)
	}
	raw, err := BudgetsYAML(Budgets{Tolerance: 0.15, Budgets: []Budget{b}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "# Budgets of the check API load test") {
		t.Fatalf("no header: %s", raw)
	}
	path := filepath.Join(t.TempDir(), "budgets.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBudgets(path)
	if err != nil || len(loaded.Budgets) != 1 || loaded.Budgets[0] != b {
		t.Fatalf("round trip: %+v %v", loaded, err)
	}
	// The run that made the budget is within it.
	if findings, ok := CheckBudgets(loaded, map[string]Report{"r": rep}); !ok {
		t.Fatalf("the baseline fails its own budget: %+v", findings)
	}
	// No rate sustained, or a wrong answer, makes no budget.
	if _, err := Calibrate(run("medium", "warm", 0, 0, step(100, 1, 1, 1, 1)), ""); err == nil {
		t.Error("a budget from a run that sustained nothing")
	}
}

func TestSumDelta(t *testing.T) {
	before := map[string]float64{`q_total{a="1"}`: 1, `q_total{a="2"}`: 2, "other": 9}
	after := map[string]float64{`q_total{a="1"}`: 4, `q_total{a="2"}`: 2, `q_total{a="3"}`: 5, "other": 99}
	if got := sumDelta(before, after, "q_total{", nil); got != 8 {
		t.Fatalf("sum = %v, want 8", got)
	}
	if got := sumDelta(before, after, "q_total{", func(s string) bool { return !strings.Contains(s, `"3"`) }); got != 3 {
		t.Fatalf("filtered sum = %v, want 3", got)
	}
}

func TestSustainedIsTheHighestStepBeforeTheFirstFailure(t *testing.T) {
	failed := step(40, 1, 1, 1, 1)
	no := false
	failed.Passed = &no
	rep := Report{Settings: Settings{Ramp: true}, Measurements: []Measurement{step(10, 1, 1, 1, 1), step(20, 1, 1, 1, 1), failed, step(80, 1, 1, 1, 1)}}
	rep.finish(0, true)
	if rep.SustainedRPS != 20 {
		t.Fatalf("a step that passes above a failed one is not capacity: sustained %v, want 20", rep.SustainedRPS)
	}
}

func TestStepsMarkdownListsEveryWindow(t *testing.T) {
	rep := run("medium", "warm", 0, 20, step(10, 1, 2, 3, 4), step(20, 5, 6, 7, 8))
	md := StepsMarkdown(rep)
	for _, want := range []string{"medium/warm/+0ms, sustained 20", "| 10 |", "| 20 |", "| 3.00 | 4.00 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("steps table lacks %q:\n%s", want, md)
		}
	}
}

func TestCalibrateRunsTakesTheLowestRateAndTheHighestCosts(t *testing.T) {
	// One run sustains 80 requests/s, the other only 40: the budget is at 40, with the worse of the two windows at 40.
	a := run("medium", "warm", 0, 80, step(40, 3, 10, 2, 3), step(80, 4, 20, 2, 3))
	b := run("medium", "warm", 0, 40, step(40, 5, 12, 2.5, 3.5))
	got, err := CalibrateRuns([]Report{a, b}, "n")
	if err != nil {
		t.Fatal(err)
	}
	if got.RPS != 40 || got.ReferenceRPS != 40 || got.P50MS != 5 || got.DBQueriesPerRequest != 2.5 || got.RedisRoundTripsPerRequest != 3.5 {
		t.Fatalf("budget: %+v", got)
	}
	// A p99 is in the budget only when every run published one at that step.
	if got.P99MS != 12 {
		t.Fatalf("p99: %v", got.P99MS)
	}
	few := step(40, 5, 12, 2.5, 3.5)
	few.Overall.Samples = 100
	got, err = CalibrateRuns([]Report{a, run("medium", "warm", 0, 40, few)}, "n")
	if err != nil || got.P99MS != 0 {
		t.Fatalf("a p99 that one run could not publish: %+v %v", got, err)
	}
	if _, err := CalibrateRuns([]Report{a, run("medium", "warm", 0, 0, step(40, 1, 1, 1, 1))}, ""); err == nil {
		t.Fatal("a run that sustained nothing made a budget")
	}
}

func TestLatencySlackAbsorbsJitterButNotCounts(t *testing.T) {
	b := baseline()
	b.LatencySlackMS = 3
	// p50 budget 4 ms: 15 % is 4.6 ms, the slack makes it 7.6 ms; 6.9 ms (the jitter seen on the runner) passes, 8 ms does not.
	if _, ok := CheckBudgets(b, map[string]Report{"x": run("medium", "warm", 0, 800, step(800, 6.9, 20, 2, 3))}); !ok {
		t.Error("jitter inside the slack failed")
	}
	if _, ok := CheckBudgets(b, map[string]Report{"x": run("medium", "warm", 0, 800, step(800, 8, 20, 2, 3))}); ok {
		t.Error("a p50 past the slack passed")
	}
	// The slack is for latencies only: a count is held to the tolerance.
	if _, ok := CheckBudgets(b, map[string]Report{"x": run("medium", "warm", 0, 800, step(800, 4, 20, 2.4, 3))}); ok {
		t.Error("the slack covered a count")
	}
}
