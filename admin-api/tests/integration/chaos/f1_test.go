//go:build chaos

package chaos_test

// F1 under faults: the fast read path must keep its promises when PostgreSQL is slow.

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
)

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

// F1-4: with 150 ms added to PostgreSQL, the 99th percentile of a warm request stays within 20 ms.
func TestF1_4_AWarmRequestIgnoresTheLatencyOfPostgreSQL(t *testing.T) {
	c, r, p, alice := setup(t)
	c.PostgresFault.Latency(delay, 0)
	asked := harness.Branches(p.Slug, alice)
	if res := r.Send(c, asked, alice); res.Status != http.StatusOK {
		t.Fatalf("warm request: %d %s", res.Status, res.Body)
	}

	// Long enough for the replica to renew its view in the background a few times (each reading takes 300 ms here), and
	// shorter than the minute an answer lives in memory.
	var latencies []time.Duration
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		start := time.Now()
		status, _, err := r.Try(c, asked, alice, 5*time.Second)
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
		t.Fatalf("p99 of a warm request is %v with %v on PostgreSQL, want at most %v", p99, delay, 20*slowdown*time.Millisecond)
	}
}
