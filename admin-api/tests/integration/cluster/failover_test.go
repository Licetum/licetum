//go:build integration

package cluster_test

// F6-11: `licetum failover` after a promotion that lost the last writes of the old primary. It needs no read replica: the test plays
// the promotion by taking the last write away from the database and putting the revision back.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
)

// F6-11: after a replica is promoted that missed the last writes of the old primary, `licetum failover` raises every revision: the
// regions drop what they kept under revisions that are now used for other content, and no stale answer is served. The test plays the
// promotion by taking the last write away from the database and putting the revision back.
func TestF6_11_FailoverRaisesTheRevisionsAboveEverythingTheRegionsSaw(t *testing.T) {
	c := startReplicas(t, 2, harness.WithConfig(func(cfg *config.Config) { cfg.Revision.WaitMax = 200 * time.Millisecond }))
	p := c.SeedPolicy("org", 0)
	const user = "grace"
	token := c.UserToken(user)
	ctx := context.Background()

	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, nil)); status != http.StatusOK {
		t.Fatalf("allow: %d %s", status, raw)
	}
	last := c.Revision() // what the "promoted replica" has
	status, raw, deniedAt := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, map[string]any{"effect": "deny"}))
	if status != http.StatusOK {
		t.Fatalf("deny: %d %s", status, raw)
	}
	var denyID string
	{
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		denyID, _ = body["id"].(string)
	}
	// Both regions enforce the denial and keep the answer under the revision of the denial.
	for _, r := range c.Replicas {
		r.Warm(c, harness.Branches(p.Slug, token), token)
		if res := r.Permission(c, p.Slug, token); res.Sees() {
			t.Fatalf("the denial is not enforced: %s", res.Body)
		}
	}

	// The promotion: the new primary never received the denial, nor its revision.
	if _, err := c.Pool.Exec(ctx, `DELETE FROM assignment_rule WHERE id = $1`, denyID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pool.Exec(ctx, `UPDATE revision_state SET revision = $1 WHERE scope = 'rules'`, last); err != nil {
		t.Fatal(err)
	}
	// The regions notice that the revision went backwards.
	for _, r := range c.Replicas {
		eventually(t, 15*time.Second, fmt.Sprintf("replica %d to read the older revision", r.Index), func() bool { return r.App.PolicyRevision() == last })
		if !strings.Contains(r.Metrics(c), "licetum_revision_regressions_total 1") {
			t.Errorf("replica %d did not count the revision going backwards", r.Index)
		}
	}
	// The next write of the new primary gets the revision the denial had: the same number for other content.
	if status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "someone-else", nil)); status != http.StatusOK || rev != deniedAt {
		t.Fatalf("the write after the promotion: %d revision %d, want %d: %s", status, rev, deniedAt, raw)
	}
	for _, r := range c.Replicas {
		eventually(t, 15*time.Second, fmt.Sprintf("replica %d to read the reused revision", r.Index), func() bool { return r.App.PolicyRevision() == deniedAt })
	}
	served := c.Replicas[0].Send(c, harness.Branches(p.Slug, token), token).Sees()
	t.Logf("without the failover the region serves the answer cached under the reused revision: stale=%v", !served)

	// licetum failover.
	if _, err := db.BumpAllRevisions(ctx, c.Pool, db.DefaultFailoverJump); err != nil {
		t.Fatal(err)
	}
	for _, r := range c.Replicas {
		eventually(t, 15*time.Second, fmt.Sprintf("replica %d to read the raised revision", r.Index), func() bool {
			return r.App.PolicyRevision() >= db.DefaultFailoverJump
		})
		// Right after the failover the answer is the data of the new primary, whatever was cached under the old revisions.
		if res := r.Send(c, harness.Branches(p.Slug, token), token); !res.Sees() {
			t.Errorf("replica %d after the failover: %d %s", r.Index, res.Status, res.Body)
		}
	}
	// And revisions go on growing from the new base.
	before := c.Revision()
	if status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "another", nil)); status != http.StatusOK || rev != before+1 {
		t.Fatalf("a write after the failover: %d revision %d (was %d): %s", status, rev, before, raw)
	}
}
