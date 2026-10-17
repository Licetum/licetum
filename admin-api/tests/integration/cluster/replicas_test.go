//go:build integration

package cluster_test

import (
	"net/http"
	"testing"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
)

// startReplicas is the entry point of the tests of this package: n check API replicas on the shared PostgreSQL and
// Redis (see harness.StartReplicas).
func startReplicas(t *testing.T, n int, opts ...harness.Option) *harness.Cluster {
	t.Helper()
	return harness.StartReplicas(t, infra, n, opts...)
}

// F0-1: three replicas answer the same request identically.
func TestF0_1_ThreeReplicasAnswerTheSameRequestIdentically(t *testing.T) {
	c := startReplicas(t, 3)
	if len(c.Replicas) != 3 {
		t.Fatalf("started %d replicas, want 3", len(c.Replicas))
	}
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")

	var first []byte
	for _, r := range c.Replicas {
		res := r.Permission(c, p.Slug, alice)
		if res.Status != http.StatusOK {
			t.Fatalf("replica %d: %d %s", r.Index, res.Status, res.Body)
		}
		if first == nil {
			first = res.Body
			continue
		}
		if string(res.Body) != string(first) {
			t.Fatalf("replica %d answered differently:\n%s\nwant\n%s", r.Index, res.Body, first)
		}
	}
	branches, _ := c.Replicas[0].Permission(c, p.Slug, alice).JSON(t)["branch"].(map[string]any)
	if branches["type"] != "sync" || len(branches["resources"].([]any)) != 3 {
		t.Fatalf("alice should see the three branches: %s", first)
	}

	// The replicas are separate processes in all but name: each has its own pool, client and counters.
	seen := map[any]bool{}
	for _, r := range c.Replicas {
		if seen[r.Pool] || seen[r.Redis] || seen[r.App] {
			t.Fatalf("replica %d shares its pool, client or App with another", r.Index)
		}
		seen[r.Pool], seen[r.Redis], seen[r.App] = true, true, true
	}

	// A change made through the admin API reaches every replica: the revision is in the shared state.
	c.Must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": p.Division, "access": []string{"read"}, "subject": "alice",
	})
	for _, r := range c.Replicas {
		got := r.Permission(c, p.Slug, alice).JSON(t)
		if _, ok := got["division"]; !ok {
			t.Fatalf("replica %d does not see the new rule: %v", r.Index, got)
		}
	}
}
