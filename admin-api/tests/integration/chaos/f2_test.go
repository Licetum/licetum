//go:build chaos

package chaos_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/outbox"

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
	p := c.SeedPolicy("org")
	c.Must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": p.Branch, "access": []string{"read"},
		"predicate": []any{map[string]any{"attr": "preferred_username", "op": "regex", "value": "^u[0-9]+$"}},
	})
	r := c.Replicas[0]
	tokens := make([]string, users)
	for i := range tokens {
		tokens[i] = c.UserToken(fmt.Sprintf("u%d", i))
	}
	// Every key is hot: its answer is in memory.
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
	missesBefore := harness.MetricValue(r.Metrics(c), `licetum_search_cache_total{result="miss"}`)
	revisionBefore := c.Revision()
	slots := make(chan struct{}, 300)
	ticker := time.NewTicker(time.Second / rps)
	defer ticker.Stop()
	sampler := time.NewTicker(5 * time.Millisecond)
	defer sampler.Stop()
	begin := time.Now()
	type writeResult struct {
		status int
		body   []byte
	}
	writeDone := make(chan writeResult, 1)
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
				go func() {
					status, body := c.AdminDo(http.MethodPost, "/assignment_rule", map[string]any{
						"type_resource_id": p.Branch, "access": []string{"read"}, "subject": "someone-else"})
					writeDone <- writeResult{status: status, body: body}
				}()
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
	if !bumped {
		t.Fatal("the write was not started")
	}
	result := <-writeDone
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(result.body, &created); err != nil || result.status != http.StatusOK || created.ID == "" {
		t.Fatalf("admin write failed: %d %s (%v)", result.status, result.body, err)
	}
	writeRevision := c.LastRevision.Load()
	revisionAfter := c.Revision()
	// The global position also includes data publications of the platform's own
	// resource sources. Require exactly one admin publication, classify every
	// additional header, and keep the gap-free check across the complete range.
	rows, err := c.Pool.Query(context.Background(), `SELECT revision,streams FROM outbox WHERE revision>$1 AND revision<=$2 ORDER BY revision`, revisionBefore, revisionAfter)
	if err != nil {
		t.Fatal(err)
	}
	count, admin := int64(0), 0
	serviceStream := outbox.Service(uuid.MustParse(p.ServiceID))
	for rows.Next() {
		var revision int64
		var streams []string
		if err := rows.Scan(&revision, &streams); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		count++
		if revision != revisionBefore+count {
			t.Errorf("publication gap: revision %d after %d headers from %d", revision, count, revisionBefore)
		}
		t.Logf("publication %d: %v", revision, streams)
		if slices.Contains(streams, serviceStream) {
			admin++
			if revision != writeRevision || !slices.Equal(streams, []string{serviceStream}) {
				t.Errorf("unexpected admin publication: %d %v (write %d)", revision, streams, writeRevision)
			}
		} else {
			hasData := false
			for _, stream := range streams {
				if strings.HasPrefix(stream, "data:") {
					hasData = true
				} else if stream != outbox.Catalog {
					t.Errorf("unexpected additional stream %q", stream)
				}
			}
			if !hasData {
				t.Errorf("additional header %d has no data stream: %v", revision, streams)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != revisionAfter-revisionBefore || admin != 1 || writeRevision <= revisionBefore || writeRevision > revisionAfter {
		t.Errorf("publication range %d..%d: %d headers, %d admin headers, write %d", revisionBefore, revisionAfter, count, admin, writeRevision)
	}
	var history int
	if err := c.Pool.QueryRow(context.Background(), `SELECT count(*) FROM change_log WHERE revision=$1 AND entity='assignment_rule' AND entity_id=$2 AND action='create'`, writeRevision, created.ID).Scan(&history); err != nil || history != 1 {
		t.Errorf("the single admin publication must audit the created rule: records=%d error=%v", history, err)
	}
	misses := int(harness.MetricValue(r.Metrics(c), `licetum_search_cache_total{result="miss"}`) - missesBefore)
	slices.Sort(latencies)
	t.Logf("%d requests (%d failed): p50 %v, p99 %v, max %v; pool peak %d of %d", len(latencies), failures.Load(),
		percentile(latencies, 0.5), percentile(latencies, 0.99), latencies[len(latencies)-1], peak.Load(), maxConns)
	t.Logf("the revision went from %d to %d; %d answers were recomputed", revisionBefore, revisionAfter, misses)
	if misses < users/2 {
		t.Errorf("the write did not invalidate the hot keys: revision %d -> %d, %d misses", revisionBefore, revisionAfter, misses)
	}
	if failures.Load() > 0 {
		t.Errorf("%d requests failed or could not be started", failures.Load())
	}
	if peak.Load() > maxConns {
		t.Errorf("the pool had %d connections acquired, its size is %d", peak.Load(), maxConns)
	}
	if limit := 10 * time.Second * slowdown; percentile(latencies, 0.99) > limit {
		t.Errorf("p99 is %v, want at most %v", percentile(latencies, 0.99), limit)
	}
}
