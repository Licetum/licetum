package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// manualClock is the clock of the answer cache in the tests below: hours of validity windows pass in microseconds.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// resultCount reads the counter of the result label from the registry the cache metrics are in.
func resultCount(t *testing.T, registry *prometheus.Registry, result string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "result" && label.GetValue() == result {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

var errPostgreSQLGone = errors.New("the test's PostgreSQL is gone")

const (
	// maxComputeStep is the longest the fake computation spends before and after the moment it measures its validity from.
	maxComputeStep = 500 * time.Millisecond
	// ttlPrecision is how far past a validity boundary an answer that is served in the usual way may still be: the second
	// the ttl is rounded up to (an answer is worth keeping for a second at least), and the computation that ended after the
	// moment the ttl was measured from. It is the precision the result cache always had; only the stale answers, which the
	// process serves for as long as PostgreSQL is gone, are held to the boundary itself.
	ttlPrecision = time.Second + maxComputeStep
)

// TestStaleAnswersNeverOutliveAValidityBoundary (F5-6, the property behind "the replica keeps answering from its copy"): over
// random policies with validity windows, random request times (many of them close to a boundary), random computation times
// and random periods without PostgreSQL, through the real cachedAnswer:
//
//   - safety: an answer served stale (the entry is past its time and the process cannot compute another) is the answer of
//     the moment, that is, no validity boundary has passed since it was computed;
//   - liveness: while PostgreSQL is gone and no boundary has passed since the last computation, the answer is served (until
//     the 15 minutes memory keeps it), not refused;
//   - an answer served in the usual way is behind a boundary by at most ttlPrecision.
//
// The oracle is the world itself: the answer is the number of boundaries that have passed. A failure names the seed.
func TestStaleAnswersNeverOutliveAValidityBoundary(t *testing.T) {
	var stale, refusedAfterGone, recomputed int
	for seed := uint64(1); seed <= 200; seed++ {
		s, r, c := runHoldScenario(t, seed)
		stale += s
		refusedAfterGone += r
		recomputed += c
	}
	// The property must not hold for want of material: the scenarios did serve stale answers, did meet PostgreSQL being gone
	// with an answer in memory, and did compute.
	if stale < 1000 || refusedAfterGone < 100 || recomputed < 1000 {
		t.Fatalf("the scenarios are too tame: %d stale answers served, %d refusals once an answer was no longer due, %d computations",
			stale, refusedAfterGone, recomputed)
	}
	t.Logf("200 scenarios: %d stale answers served, %d refusals after the hold, %d computations", stale, refusedAfterGone, recomputed)
}

func runHoldScenario(t *testing.T, seed uint64) (stale, refused, computations int) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := &manualClock{t: start}
	a := &App{answersL1: newLRU[any](16), clock: clk.Now}
	registry := prometheus.NewRegistry()
	results := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hold_test_results"}, []string{"result"})
	registry.MustRegister(results)
	state := cacheState{ok: true, degraded: true, version: 1, tag: "t"}

	// The world: the moments a rule or a membership starts or stops being in force. The answer changes at each of them.
	var bounds []time.Time
	for i := rng.IntN(8); i > 0; i-- {
		at := start.Add(time.Duration(rng.Int64N(int64(3 * time.Hour))))
		bounds = append(bounds, at)
		if rng.IntN(3) == 0 {
			bounds = append(bounds, at.Add(time.Duration(rng.Int64N(int64(3*time.Second)))+time.Millisecond)) // a window that is short
		}
	}
	slices.SortFunc(bounds, time.Time.Compare)
	truth := func(at time.Time) int {
		n := 0
		for _, b := range bounds {
			if !b.After(at) {
				n++
			}
		}
		return n
	}
	nextAfter := func(at time.Time) *time.Time {
		for _, b := range bounds {
			if b.After(at) {
				return &b
			}
		}
		return nil
	}
	lastBefore := func(at time.Time) (time.Time, bool) {
		var out time.Time
		found := false
		for _, b := range bounds {
			if !b.After(at) {
				out, found = b, true
			}
		}
		return out, found
	}

	pgGone := false
	var staleSeen float64
	var callAt time.Time
	var last struct {
		ok                    bool
		started, written      time.Time
		holdUntil, memoryUntl time.Time
	}
	compute := func(context.Context) (computed[int], error) {
		if pgGone {
			return computed[int]{}, errPostgreSQLGone
		}
		computations++
		clk.Advance(time.Duration(rng.Int64N(int64(maxComputeStep))))
		measured := clk.Now() // the moment the validity is measured from (snapshot.validity: time.Now() inside the preparation)
		ttl, hold := validityUntil(nextAfter(measured), measured)
		value := truth(measured)
		clk.Advance(time.Duration(rng.Int64N(int64(maxComputeStep))))
		last.ok = true
		last.started, last.written = callAt, clk.Now()
		last.holdUntil = callAt.Add(hold)
		last.memoryUntl = last.written.Add(maxStaleAnswerTTL)
		return computed[int]{value: value, ttl: ttl, hold: hold}, nil
	}

	for step, steps := 0, 150+rng.IntN(150); step < steps; step++ {
		switch d := rng.IntN(10); {
		case d < 4:
			clk.Advance(time.Duration(rng.Int64N(int64(500*time.Millisecond))) + time.Millisecond)
		case d < 7:
			clk.Advance(time.Duration(rng.Int64N(int64(20 * time.Second))))
		case d < 9:
			clk.Advance(time.Duration(rng.Int64N(int64(5 * time.Minute))))
		default:
			clk.Advance(time.Duration(rng.Int64N(int64(40 * time.Minute))))
		}
		if rng.IntN(3) == 0 { // land on a boundary: just before it or just after it
			if b := nextAfter(clk.Now().Add(-2 * time.Second)); b != nil {
				if target := b.Add(time.Duration(rng.Int64N(int64(3*time.Second))) - 1500*time.Millisecond); target.After(clk.Now()) {
					clk.Advance(target.Sub(clk.Now()))
				}
			}
		}
		if rng.IntN(5) == 0 && step > 0 {
			pgGone = !pgGone
		}
		callAt = clk.Now()
		got, err := cachedAnswer(context.Background(), a, "k", state, MinimizeLatency, results, compute)
		staleNow := resultCount(t, registry, "stale")
		servedStale := staleNow > staleSeen
		staleSeen = staleNow
		after := clk.Now()

		if err != nil {
			if !errors.Is(err, errPostgreSQLGone) {
				t.Fatalf("seed %d step %d: unexpected error %v", seed, step, err)
			}
			if last.ok && callAt.Before(last.holdUntil) && callAt.Before(last.memoryUntl) {
				t.Fatalf("seed %d step %d at %v: the answer computed at %v is due until %v (no boundary before it), but the request was refused",
					seed, step, callAt.Sub(start), last.started.Sub(start), last.holdUntil.Sub(start))
			}
			if last.ok {
				refused++
			}
			continue
		}
		want := truth(callAt)
		switch {
		case servedStale:
			stale++
			if pgGone && !callAt.Before(last.holdUntil) {
				t.Fatalf("seed %d step %d at %v: a stale answer was served after its hold ended at %v", seed, step, callAt.Sub(start), last.holdUntil.Sub(start))
			}
			if got != want {
				b, _ := lastBefore(callAt)
				t.Fatalf("seed %d step %d at %v: a stale answer (%d) was served after the boundary at %v changed it to %d (computed at %v, held until %v)",
					seed, step, callAt.Sub(start), got, b.Sub(start), want, last.started.Sub(start), last.holdUntil.Sub(start))
			}
		case got != truth(after):
			if b, found := lastBefore(after); !found || after.Sub(b) > ttlPrecision {
				t.Fatalf("seed %d step %d at %v: an answer (%d) was served %v after the boundary that changed it to %d, more than %v",
					seed, step, callAt.Sub(start), got, after.Sub(b), truth(after), ttlPrecision)
			}
		}
	}
	return stale, refused, computations
}

// TestValidityIsTheExactDistanceToTheNextRelevantBoundary: whatever the boundaries are (past, other types, role assignments,
// memberships, under a second away), snapshot.validity stops the hold at the nearest one that can change the answer and never
// later, and the ttl is that distance within the one-second floor and the cap.
func TestValidityIsTheExactDistanceToTheNextRelevantBoundary(t *testing.T) {
	for seed := uint64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewPCG(seed, 77))
		now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
		types := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		relevant := map[uuid.UUID]bool{}
		for _, id := range types {
			if rng.IntN(2) == 0 {
				relevant[id] = true
			}
		}
		snap := &snapshot{}
		var nearest *time.Time
		for i := rng.IntN(12); i > 0; i-- {
			b := validityBound{at: now.Add(time.Duration(rng.Int64N(int64(4*time.Hour))) - time.Hour)}
			if rng.IntN(5) == 0 {
				b.at = now // right now: already in force, not a boundary
			}
			switch rng.IntN(4) {
			case 0:
				b.member = true
			case 1:
				b.role = true
			case 2:
				// a rule on a type that may be nobody's concern
				id := types[rng.IntN(len(types))]
				b.typeID = &id
			default:
				b.typeID = nil // a bound with no type and no role or membership flag reaches nobody
			}
			snap.bounds = append(snap.bounds, b)
			counts := b.member || b.role || (b.typeID != nil && relevant[*b.typeID])
			if counts && b.at.After(now) && (nearest == nil || b.at.Before(*nearest)) {
				at := b.at
				nearest = &at
			}
		}
		ttl, hold := snap.validity(relevant, now)
		if nearest == nil {
			if ttl != ResultCacheTTL || hold != holdUnbounded {
				t.Fatalf("seed %d: no boundary can change the answer, got ttl %v hold %v", seed, ttl, hold)
			}
			continue
		}
		distance := nearest.Sub(now)
		if hold != distance {
			t.Fatalf("seed %d: hold %v, want the distance to the nearest boundary that counts, %v", seed, hold, distance)
		}
		if want := min(ResultCacheTTL, max(time.Second, distance)); ttl != want {
			t.Fatalf("seed %d: ttl %v, want %v", seed, ttl, want)
		}
	}
}
