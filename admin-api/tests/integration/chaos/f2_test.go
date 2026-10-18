//go:build chaos

package chaos_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
)

// F2-7: a revision moves under many hot keys at 600 requests a second. The misses queue for the computation slots instead of
// exhausting the database pool: the pool is never over its size, no request fails, and the p99 stays within a budget.
func TestF2_7_ARevisionBumpUnderHotKeysDoesNotExhaustThePool(t *testing.T) {
	const (
		users = 1500 / loadDivisor
		rps   = 600 / loadDivisor
	)
	c := harness.StartReplicas(t, infra, 1)
	p := c.SeedPolicy("org", 0)
	c.Must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": p.Branch, "access": []string{"read"},
		"predicate": []any{map[string]any{"attr": "preferred_username", "op": "regex", "value": "^u[0-9]+$"}},
	})
	r := c.Replicas[0]
	tokens := make([]string, users)
	for i := range tokens {
		tokens[i] = c.UserToken(fmt.Sprintf("u%d", i))
	}
	// Every key is hot: its answer is in memory and in Redis.
	var warm sync.WaitGroup
	next := make(chan string)
	for range 8 {
		warm.Add(1)
		go func() {
			defer warm.Done()
			for token := range next {
				if status, _, err := r.Try(c, harness.Branches(p.Slug, token), token, 30*time.Second); err != nil || status != http.StatusOK {
					t.Errorf("warm request: %d %v", status, err)
				}
			}
		}()
	}
	for _, token := range tokens {
		next <- token
	}
	close(next)
	warm.Wait()
	time.Sleep(500 * time.Millisecond)

	var (
		mu        sync.Mutex
		latencies []time.Duration
		failures  atomic.Int64
		wg        sync.WaitGroup
		peak      atomic.Int32
	)
	missesBefore := harness.MetricValue(r.Metrics(c), `licetum_permission_cache_total{result="miss"}`)
	revisionBefore := c.Revision()
	slots := make(chan struct{}, 300)
	ticker := time.NewTicker(time.Second / rps)
	defer ticker.Stop()
	sampler := time.NewTicker(5 * time.Millisecond)
	defer sampler.Stop()
	begin := time.Now()
	bumped := false
	maxConns := r.Pool.Config().MaxConns
	for i := 0; time.Since(begin) < 8*time.Second; {
		select {
		case <-sampler.C:
			if n := r.Pool.Stat().AcquiredConns(); n > peak.Load() {
				peak.Store(n)
			}
		case <-ticker.C:
			if !bumped && time.Since(begin) > 2*time.Second {
				bumped = true
				// A write that moves the revision: every cached answer is stale at once.
				go c.Must(http.MethodPost, "/assignment_rule", map[string]any{
					"type_resource_id": p.Branch, "access": []string{"read"}, "subject": "someone-else"})
			}
			select {
			case slots <- struct{}{}:
			default:
				failures.Add(1)
				continue
			}
			token := tokens[i%users]
			i++
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				start := time.Now()
				status, _, err := r.Try(c, harness.Branches(p.Slug, token), token, 30*time.Second)
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
	misses := int(harness.MetricValue(r.Metrics(c), `licetum_permission_cache_total{result="miss"}`) - missesBefore)
	slices.Sort(latencies)
	t.Logf("%d requests (%d failed): p50 %v, p99 %v, max %v; pool peak %d of %d", len(latencies), failures.Load(),
		percentile(latencies, 0.5), percentile(latencies, 0.99), latencies[len(latencies)-1], peak.Load(), maxConns)
	t.Logf("the revision went from %d to %d; %d answers were recomputed", revisionBefore, c.Revision(), misses)
	if c.Revision() != revisionBefore+1 || misses < users/2 {
		t.Errorf("the write did not invalidate the hot keys: revision %d -> %d, %d misses", revisionBefore, c.Revision(), misses)
	}
	if failures.Load() > 0 {
		t.Errorf("%d requests failed or could not be started", failures.Load())
	}
	if int32(peak.Load()) > maxConns {
		t.Errorf("the pool had %d connections acquired, its size is %d", peak.Load(), maxConns)
	}
	if limit := 10 * time.Second * slowdown; percentile(latencies, 0.99) > limit {
		t.Errorf("p99 is %v, want at most %v", percentile(latencies, 0.99), limit)
	}
}
