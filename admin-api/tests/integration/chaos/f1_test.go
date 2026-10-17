//go:build chaos

package chaos_test

// F1 under faults: the fast read path must keep its promises when PostgreSQL and Redis are slow, or swallow the traffic.
// The replicas here have the Redis client of the check API in production: 50 ms to read or write, no retries, and Redis
// skipped for a while after three failures in a row.

import (
	"net/http"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
)

const hotTimeout = 50 * time.Millisecond

// everyone adds a rule that gives the users u0, u1, ... read on every branch, and returns their tokens.
func everyone(t *testing.T, c *harness.Cluster, p *harness.Policy, users int) []string {
	t.Helper()
	c.Must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": p.Branch, "access": []string{"read"},
		"predicate": []any{map[string]any{"attr": "preferred_username", "op": "regex", "value": "^u[0-9]+$"}},
	})
	tokens := make([]string, users)
	for i := range tokens {
		tokens[i] = c.UserToken("u" + itoa(i))
	}
	return tokens
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(len(sorted)-1, int(float64(len(sorted))*q))]
}

func skippedCommands(metrics string) float64 {
	return harness.MetricValue(metrics, "licetum_redis_skipped_total")
}

// F1-4: with 150 ms added to PostgreSQL and to Redis, the 99th percentile of a warm request stays within 20 ms.
func TestF1_4_AWarmRequestIgnoresTheLatencyOfPostgreSQLAndRedis(t *testing.T) {
	c, r, p, alice := setup(t)
	c.PostgresFault.Latency(delay, 0)
	c.RedisFault.Latency(delay, 0)
	path := "/service/" + p.Slug + "/permission"
	if res := r.Do(c, http.MethodGet, path, alice, nil); res.Status != http.StatusOK {
		t.Fatalf("warm request: %d %s", res.Status, res.Body)
	}

	// Long enough for the replica to renew its view in the background a few times (each reading takes 300 ms here), and
	// shorter than the ten seconds an answer lives in memory.
	var latencies []time.Duration
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		start := time.Now()
		status, _, err := r.TryGet(c, path, alice, 5*time.Second)
		took := time.Since(start)
		if err != nil || status != http.StatusOK {
			t.Fatalf("status %d, %v", status, err)
		}
		latencies = append(latencies, took)
		time.Sleep(2 * time.Millisecond)
	}
	slices.Sort(latencies)
	t.Logf("%d requests: p50 %v, p99 %v, max %v", len(latencies), percentile(latencies, 0.5), percentile(latencies, 0.99), latencies[len(latencies)-1])
	if p99 := percentile(latencies, 0.99); p99 > 20*slowdown*time.Millisecond {
		t.Fatalf("p99 of a warm request is %v with %v on PostgreSQL and Redis, want at most %v", p99, delay, 20*slowdown*time.Millisecond)
	}
}

func hotSetup(t *testing.T, users int) (*harness.Cluster, *harness.Replica, *harness.Policy, []string) {
	t.Helper()
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies(), harness.WithHotRedis(hotTimeout))
	p := c.SeedPolicy("org", 0)
	tokens := everyone(t, c, p, users)
	r := c.Replicas[0]
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", tokens[0])
	return c, r, p, tokens
}

// F1-10: with Redis swallowing the traffic, once Redis is skipped a request is slowed by no more than 50 ms, and its
// answer is right.
func TestF1_10_ASkippedRedisAddsNoMoreThanItsTimeoutToARequest(t *testing.T) {
	c, r, p, tokens := hotSetup(t, 70)
	path := "/service/" + p.Slug + "/permission"

	// What a request for a subject nobody asked for yet costs while Redis is healthy, and what it answers.
	base := time.Duration(0)
	var want string
	for i, token := range tokens[:10] {
		start := time.Now()
		res := r.Do(c, http.MethodGet, path, token, nil)
		took := time.Since(start)
		if res.Status != http.StatusOK {
			t.Fatalf("healthy: %d %s", res.Status, res.Body)
		}
		if i == 0 {
			want = string(res.Body)
		}
		base = max(base, took)
	}

	c.RedisFault.BlackHole()
	// The first requests pay the timeout and trip the fast-fail; each one is for a subject that is new to the replica, so
	// that it needs Redis (a token and an answer found in memory do not).
	next := 10
	deadline := time.Now().Add(20 * time.Second)
	for skippedCommands(r.Metrics(c)) == 0 {
		if time.Now().After(deadline) || next >= 30 {
			t.Fatal("Redis was never skipped")
		}
		r.TryGet(c, path, tokens[next], 5*time.Second)
		next++
	}
	tokens = tokens[30:]

	var worst time.Duration
	for _, token := range tokens {
		start := time.Now()
		status, body, err := r.TryGet(c, path, token, 5*time.Second)
		took := time.Since(start)
		if err != nil || status != http.StatusOK {
			t.Fatalf("with Redis skipped: status %d, %v", status, err)
		}
		if string(body) != want {
			t.Fatalf("the answer changed with Redis skipped:\n%s\nwant\n%s", body, want)
		}
		worst = max(worst, took)
	}
	t.Logf("healthy: worst %v; with Redis skipped: worst %v", base, worst)
	if worst > base+hotTimeout*slowdown {
		t.Fatalf("a request with Redis skipped took %v, %v more than %v with a healthy Redis: want at most %v more",
			worst, worst-base, base, hotTimeout*slowdown)
	}
}

// F1-9: Redis answering after five seconds under 600 requests a second for thirty seconds: the number of goroutines and
// the memory stay bounded, and the requests are not slowed beyond what F1-10 allows.
func TestF1_9_ASlowRedisDoesNotAccumulateWorkUnderLoad(t *testing.T) {
	const (
		rps      = 600
		duration = 30 * time.Second
		users    = 30
	)
	c, r, p, tokens := hotSetup(t, users)
	path := "/service/" + p.Slug + "/permission"
	for _, token := range tokens { // the tokens and the answers are cached when the fault begins
		if res := r.Do(c, http.MethodGet, path, token, nil); res.Status != http.StatusOK {
			t.Fatalf("warm request: %d %s", res.Status, res.Body)
		}
	}
	time.Sleep(500 * time.Millisecond) // the cache writes the requests left in the background

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutinesBefore := runtime.NumGoroutine()

	revBefore, dataBefore := c.Revision(), c.DataRevision(p.ServiceID)
	c.RedisFault.Latency(5*time.Second, 0)

	var (
		mu        sync.Mutex
		latencies []time.Duration
		failures  atomic.Int64
		wg        sync.WaitGroup
		peak      atomic.Int64
	)
	slots := make(chan struct{}, 200)
	ticker := time.NewTicker(time.Second / rps)
	defer ticker.Stop()
	stop := time.After(duration)
	sampler := time.NewTicker(250 * time.Millisecond)
	defer sampler.Stop()
	i := 0
load:
	for {
		select {
		case <-stop:
			break load
		case <-sampler.C:
			if n := int64(runtime.NumGoroutine()); n > peak.Load() {
				peak.Store(n)
			}
		case <-ticker.C:
			select {
			case slots <- struct{}{}:
			default:
				failures.Add(1) // the replica cannot keep up with the load: 200 requests are in flight
				continue
			}
			token := tokens[i%users]
			i++
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				start := time.Now()
				status, _, err := r.TryGet(c, path, token, 5*time.Second)
				took := time.Since(start)
				if err != nil || status != http.StatusOK {
					failures.Add(1)
					return
				}
				mu.Lock()
				latencies = append(latencies, took)
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	// The cache of the synchronization state is unreachable: a synchronization that finds the rows as they are must not
	// move the revision of the data, or every answer would be recomputed again and again.
	if revNow, dataNow := c.Revision(), c.DataRevision(p.ServiceID); revNow != revBefore || dataNow != dataBefore {
		t.Errorf("the revisions moved with nothing written: rules %d -> %d, data %d -> %d", revBefore, revNow, dataBefore, dataNow)
	}
	c.RedisFault.Reset()

	slices.Sort(latencies)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("%d requests (%d failed): p50 %v, p99 %v, max %v; goroutines %d -> peak %d; heap %d MiB -> %d MiB",
		len(latencies), failures.Load(), percentile(latencies, 0.5), percentile(latencies, 0.99), latencies[len(latencies)-1],
		goroutinesBefore, peak.Load(), before.HeapAlloc>>20, after.HeapAlloc>>20)

	if failures.Load() > 0 {
		t.Errorf("%d requests failed", failures.Load())
	}
	// The workers of the background pool (16), the requests in flight (at most 200 here, each with a connection on both
	// sides), and some room for the runtime: not a goroutine per Redis write, which would be thousands.
	if limit := int64(goroutinesBefore) + 16 + 3*200; peak.Load() > limit {
		t.Errorf("peak of %d goroutines, want at most %d", peak.Load(), limit)
	}
	if after.HeapAlloc > before.HeapAlloc+256<<20 {
		t.Errorf("the heap grew from %d MiB to %d MiB", before.HeapAlloc>>20, after.HeapAlloc>>20)
	}
	if limit := (20*time.Millisecond + hotTimeout) * slowdown; percentile(latencies, 0.99) > limit {
		t.Errorf("p99 is %v, want at most %v", percentile(latencies, 0.99), limit)
	}
}
