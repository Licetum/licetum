package loadtest

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

// TestStepFromK6 reads a summary k6 wrote for one step: 50 requests per second for 15 seconds, all of them answered.
func TestStepFromK6(t *testing.T) {
	raw, err := os.ReadFile("testdata/k6-summary.json")
	if err != nil {
		t.Fatal(err)
	}
	var sum k6Summary
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("read the summary: %v", err)
	}

	mix, _ := ParseMix(DefaultMix)
	step := stepFromK6(sum, 50, 15*time.Second, mix, counters{}, counters{})
	if step.Requests != 751 || step.OK != 751 || step.ErrorRate != 0 {
		t.Errorf("requests %d, ok %d, error rate %v", step.Requests, step.OK, step.ErrorRate)
	}
	if math.Abs(step.Latency.P99-47.61) > 0.01 || math.Abs(step.Latency.P50-26.07) > 0.01 {
		t.Errorf("latency p50 %v, p99 %v", step.Latency.P50, step.Latency.P99)
	}
	if step.Decisions != 1309 || math.Abs(step.AllowedShare-70.0/1309) > 1e-9 {
		t.Errorf("decisions %d, allowed share %v", step.Decisions, step.AllowedShare)
	}
	if step.Dropped != 0 || len(step.ByOperation) != len(opNames) {
		t.Errorf("dropped %d, requests by operation %d", step.Dropped, len(step.ByOperation))
	}
	if step.AchievedRPS < 49 || step.AchievedRPS > 51 {
		t.Errorf("answered %v requests/s, want about 50", step.AchievedRPS)
	}
	step.judge(0.01, 100*time.Millisecond)
	if !step.Passed {
		t.Errorf("a step within its objectives failed: %v", step.Reasons)
	}
}

// TestJudgeDropped checks that arrivals k6 could not start make the step saturated, whatever its latency.
func TestJudgeDropped(t *testing.T) {
	s := Step{TargetRPS: 50, AchievedRPS: 50, Latency: Latency{P99: 10}, Dropped: 3}
	s.judge(0.01, 100*time.Millisecond)
	if s.Passed || len(s.Reasons) != 1 {
		t.Fatalf("a step that dropped arrivals passed: %+v", s)
	}
}

func TestPreallocated(t *testing.T) {
	cases := []struct{ rate, maxVUs, want int }{
		{5, 250, 10},   // the floor
		{400, 250, 80}, // a fifth of the rate
		{400, 50, 50},  // never more than the most users
	}
	for _, c := range cases {
		if got := preallocated(c.rate, c.maxVUs); got != c.want {
			t.Errorf("preallocated(%d, %d) = %d, want %d", c.rate, c.maxVUs, got, c.want)
		}
	}
}
