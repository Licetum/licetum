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

	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
)

// F6-11: after a replica is promoted that missed the last writes of the old primary, `licetum failover` starts a new epoch: the
// regions drop what they kept under revisions that are now used for other content, and no stale answer is served. The test plays the
// promotion by taking the last write away from the database and putting the revision back.
func TestF6_11_FailoverRaisesTheRevisionsAboveEverythingTheRegionsSaw(t *testing.T) {
	c := startReplicas(t, 2, harness.WithConfig(func(cfg *config.Config) { cfg.Revision.WaitMax = 200 * time.Millisecond }))
	p := c.SeedPolicy("org")
	const user = "grace"
	token := c.UserToken(user)
	ctx := context.Background()

	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, nil)); status != http.StatusOK {
		t.Fatalf("allow: %d %s", status, raw)
	}
	last := c.Revision() // what the "promoted replica" has
	status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, map[string]any{"effect": "deny"}))
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

	c.StopWorker()
	// The promoted standby has the allow but not the denial or its publication.
	if err := db.InTx(ctx, c.Pool, func(tx pgx.Tx) error {
		// Model physical WAL loss: the fixture owner suppresses logical writer triggers.
		if _, err := tx.Exec(ctx, `ALTER TABLE assignment_rule DISABLE TRIGGER licetum_outbox_delete`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM assignment_rule WHERE id=$1`, denyID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM change_log WHERE revision>$1`, last); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE revision>$1`, last); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE revision_state SET revision=$1 WHERE scope='global'`, last); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `ALTER TABLE assignment_rule ENABLE ALWAYS TRIGGER licetum_outbox_delete`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var position db.Position
	if err := db.InTx(ctx, c.Pool, func(tx pgx.Tx) error {
		var err error
		position, err = db.StartEpoch(ctx, tx, db.DefaultFailoverJump)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if position.Epoch != 2 || position.Revision != last+db.DefaultFailoverJump {
		t.Fatalf("new position: %+v", position)
	}

	for _, r := range c.Replicas {
		eventually(t, 15*time.Second, fmt.Sprintf("replica %d to read the raised revision", r.Index), func() bool {
			return r.App.PolicyRevision() >= db.DefaultFailoverJump
		})
		// Right after the failover the answer is the data of the new primary, whatever was cached under the old revisions.
		if res := r.Send(c, harness.Branches(p.Slug, token), token); !res.Sees() || !strings.HasPrefix(res.Header.Get("Licetum-Revision"), "2.") {
			t.Errorf("replica %d after the failover: %d %s", r.Index, res.Status, res.Body)
		}
	}
	// And revisions go on growing from the new base.
	before := c.Revision()
	if status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "another", nil)); status != http.StatusOK || rev != before+1 {
		t.Fatalf("a write after the failover: %d revision %d (was %d): %s", status, rev, before, raw)
	}
}
