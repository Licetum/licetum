package engine

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The answer cache is the memory of the process and nothing else: an answer is served until its Exp (a minute at most, or the
// next validity boundary), not for a shorter time that a second level would have made up for, and is computed again after it.
func TestAnAnswerIsServedFromMemoryUntilItsExpiry(t *testing.T) {
	clk := &manualClock{t: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	a := &App{answersL1: newLRU[any](16), clock: clk.Now}
	results := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "memory_test_results"}, []string{"result"})
	registry := prometheus.NewRegistry()
	registry.MustRegister(results)
	state := cacheState{ok: true, version: 1, tag: "t"}

	var computations int
	compute := func(context.Context) (computed[int], error) {
		computations++
		return computed[int]{value: computations, ttl: 50 * time.Second, hold: 50 * time.Second}, nil
	}
	ask := func() int {
		t.Helper()
		got, err := cachedAnswer(context.Background(), a, "k", state, MinimizeLatency, results, compute)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := ask(); got != 1 {
		t.Fatalf("the first request: answer %d, want the first computation", got)
	}
	for _, after := range []time.Duration{time.Second, 11 * time.Second, 30 * time.Second, 7 * time.Second} {
		clk.Advance(after)
		if got := ask(); got != 1 || computations != 1 {
			t.Fatalf("%v into the 50 seconds of the answer: answer %d after %d computations, want the first answer kept", after, got, computations)
		}
	}
	clk.Advance(2 * time.Second) // past Exp
	if got := ask(); got != 2 || computations != 2 {
		t.Fatalf("after its Exp the answer was %d after %d computations, want a second computation", got, computations)
	}
	if hits, misses := resultCount(t, registry, "hit"), resultCount(t, registry, "miss"); hits != 4 || misses != 2 {
		t.Fatalf("%v hits and %v misses, want 4 and 2", hits, misses)
	}
}
