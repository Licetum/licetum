//go:build integration

package cluster_test

// F6: a check replica that reads the policy from a read replica of PostgreSQL (DB__READ_URI). The tests need a real streaming
// replica (scripts/pg-replica.sh up, LICETUM_TEST_REPLICA_ADDR) and are skipped without one. pg_wal_replay_pause makes the
// replica lag. Names carry the criterion of issue #38 they prove.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
)

func lagConfig(maxLag time.Duration, action string, staleness time.Duration) harness.Option {
	return harness.WithConfig(func(cfg *config.Config) {
		cfg.DB.MaxReplicaLag = maxLag
		cfg.DB.LagAction = action
		cfg.DB.LagInterval = 200 * time.Millisecond
		cfg.Policy.MaxStaleness = staleness
		cfg.Revision.WaitMax = 200 * time.Millisecond
	})
}

// asUser asks the permission as a person without asking for a revision: what the replica shows.
func asUser(c *harness.Cluster, r *harness.Replica, slug, user string) *harness.Result {
	return r.Do(c, http.MethodGet, "/service/"+slug+"/permission", c.UserToken(user), nil)
}

func hasAccess(res *harness.Result) bool {
	return res.Status == http.StatusOK && strings.Contains(string(res.Body), `"read"`)
}

// F6-3: with at_least_as_fresh the region answers correctly at once, though its replica lags: it waits for the replica, then
// reads from the primary. 0 stale answers in 40 iterations, while the replica replays nothing.
func TestF6_3_AStrongReadIsCurrentWhileTheReplicaLags(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), lagConfig(time.Hour, "unready", 0))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]

	c.PauseReplay()
	var wrong int
	for i := range 40 {
		user := fmt.Sprintf("user-%d", i)
		if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, nil)); status != http.StatusOK {
			t.Fatalf("write: %d %s", status, raw)
		}
		if c.ReplicaRevision() >= c.Revision() {
			t.Fatal("the replica is not lagging: the test proves nothing")
		}
		if res := r.Permission(c, p.Slug, c.UserToken(user)); !hasAccess(res) {
			wrong++
			t.Errorf("iteration %d: a strong read after the write did not see it: %d %s", i, res.Status, res.Body)
		}
	}
	if wrong > 0 {
		t.Fatalf("%d stale answers of 40", wrong)
	}

	// A request that does not ask for the revision is answered from the replica, which has not replayed the write.
	if hasAccess(asUser(c, r, p.Slug, "nobody-wrote-this")) {
		t.Fatal("a person without a rule has access")
	}
	c.ResumeReplay()
	eventually(t, 15*time.Second, "the replica to catch up", func() bool { return c.ReplicaRevision() >= c.Revision() })
	// Nothing was kept under a revision the answer did not reflect: a plain request now sees the last rule too.
	last := "after-resume"
	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, last, nil)); status != http.StatusOK {
		t.Fatalf("write: %d %s", status, raw)
	}
	eventually(t, 15*time.Second, "a plain request to see the write", func() bool { return hasAccess(asUser(c, r, p.Slug, last)) })
}

// F6-4 (unready): the lag above DB__MAX_REPLICA_LAG makes the replica not ready within 15 s, and it recovers by itself.
func TestF6_4_LagMakesTheReplicaUnreadyAndItRecovers(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), lagConfig(time.Second, "unready", 0))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	ready := func() int { return r.Do(c, http.MethodGet, "/readyz", "", nil).Status }
	eventually(t, 15*time.Second, "readiness", func() bool { return ready() == http.StatusOK })

	c.PauseReplay()
	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "dave", nil)); status != http.StatusOK {
		t.Fatalf("write: %d %s", status, raw)
	}
	eventually(t, 15*time.Second, "the replica to report itself not ready", func() bool { return ready() == http.StatusServiceUnavailable })
	if body := string(r.Do(c, http.MethodGet, "/readyz", "", nil).Body); !strings.Contains(body, "replica_lag") {
		t.Errorf("the reason is not the lag: %s", body)
	}
	if !strings.Contains(r.Metrics(c), "licetum_replica_lagging 1") {
		t.Error("licetum_replica_lagging is not 1")
	}
	if res := r.Do(c, http.MethodGet, "/healthz", "", nil); res.Status != http.StatusOK {
		t.Errorf("/healthz = %d", res.Status)
	}

	c.ResumeReplay()
	eventually(t, 15*time.Second, "the replica to be ready again", func() bool { return ready() == http.StatusOK })
	if !strings.Contains(r.Metrics(c), "licetum_replica_lagging 0") {
		t.Error("licetum_replica_lagging is not 0 after the recovery")
	}
}

// F6-4 (primary): with DB__LAG_ACTION=primary the reads move to the primary while the replica lags, the answers are current
// without asking for a revision, and the replica is used again when it has caught up.
func TestF6_4_LagMovesTheReadsToThePrimary(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), lagConfig(time.Second, "primary", 0))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]

	c.PauseReplay()
	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "erin", nil)); status != http.StatusOK {
		t.Fatalf("write: %d %s", status, raw)
	}
	eventually(t, 15*time.Second, "a plain request to see the write through the primary", func() bool { return hasAccess(asUser(c, r, p.Slug, "erin")) })
	if res := r.Do(c, http.MethodGet, "/readyz", "", nil); res.Status != http.StatusOK {
		t.Errorf("with the reads on the primary the replica stays ready: %d %s", res.Status, res.Body)
	}

	c.ResumeReplay()
	eventually(t, 15*time.Second, "the lag to end", func() bool { return strings.Contains(r.Metrics(c), "licetum_replica_lagging 0") })
	if !hasAccess(asUser(c, r, p.Slug, "erin")) {
		t.Error("the answer changed when the reads went back to the replica")
	}
}

// F6-5 / item 4: a replica more behind than POLICY__MAX_STALENESS fails closed (503 policy_unavailable) and recovers.
func TestF6_5_AReplicaBehindTheStalenessLimitFailsClosed(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), lagConfig(time.Second, "unready", 2*time.Second))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	if res := r.Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("before the lag: %d %s", res.Status, res.Body)
	}

	c.PauseReplay()
	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "frank", nil)); status != http.StatusOK {
		t.Fatalf("write: %d %s", status, raw)
	}
	// Within the limit the region goes on answering from what it has.
	if res := asUser(c, r, p.Slug, "alice"); res.Status != http.StatusOK {
		t.Fatalf("within the limit: %d %s", res.Status, res.Body)
	}
	eventually(t, 15*time.Second, "503 policy_unavailable", func() bool {
		res := r.Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice, nil)
		return res.Status == http.StatusServiceUnavailable && strings.Contains(string(res.Body), "policy_unavailable")
	})

	c.ResumeReplay()
	eventually(t, 15*time.Second, "answers again", func() bool { return r.Permission(c, p.Slug, alice).Status == http.StatusOK })
}

func p99(d []time.Duration) time.Duration {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)*99/100]
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// F6-1: in a region 150 ms away from the primary the p99 of a warm request is at most 30 ms: a warm request reads nothing, and the
// processes behind it (the revision poll, the lag) read the replica of the region.
func TestF6_1_AWarmRequestFarFromThePrimaryIsFast(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), harness.WithFaultProxies(), lagConfig(time.Hour, "unready", 0))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice)

	c.PostgresFault.Latency(150*time.Millisecond, 0)
	measure := func(n int) time.Duration {
		d := make([]time.Duration, 0, n)
		for range n {
			start := time.Now()
			res := r.Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice, nil)
			if res.Status != http.StatusOK {
				t.Fatalf("status %d %s", res.Status, res.Body)
			}
			d = append(d, time.Since(start))
		}
		return p99(d)
	}
	measure(100)
	got := measure(1000)
	t.Logf("p99 of a warm request with 150 ms to the primary: %v", got)
	if limit := 30 * time.Millisecond * raceFactor; got > limit {
		t.Fatalf("p99 = %v, want at most %v", got, limit)
	}
}

// F6-2: a deny rule written on the primary is enforced by a region that reads a replica within the SLO (D3: p99 of 5 s), over
// LICETUM_F6_REVOCATIONS revocations (50 here, 200 in the multi-region environment).
func TestF6_2_ARevocationReachesTheRegionWithinTheSLO(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), lagConfig(time.Hour, "unready", 0))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	n := envInt("LICETUM_F6_REVOCATIONS", 50)

	took := make([]time.Duration, 0, n)
	for i := range n {
		user := fmt.Sprintf("revoked-%d", i)
		token := c.UserToken(user)
		if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, user, nil)); status != http.StatusOK {
			t.Fatalf("allow: %d %s", status, raw)
		}
		eventually(t, 15*time.Second, "the grant to reach the region", func() bool { return hasAccess(asUser(c, r, p.Slug, user)) })
		deny := ruleBody(p, user, map[string]any{"effect": "deny"})
		status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", deny)
		if status != http.StatusOK {
			t.Fatalf("deny: %d %s", status, raw)
		}
		start := time.Now()
		deadline := start.Add(30 * time.Second)
		for hasAccess(r.Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", token, nil)) {
			if time.Now().After(deadline) {
				t.Fatalf("revocation %d was not enforced within 30 s", i)
			}
			time.Sleep(10 * time.Millisecond)
		}
		took = append(took, time.Since(start))
	}
	got := p99(took)
	t.Logf("%d revocations, p99 %v", n, got)
	if got > 5*time.Second {
		t.Fatalf("p99 of the propagation = %v, want at most 5 s", got)
	}
}

// F6-5: the link to the replica is cut for 6 s with POLICY__MAX_STALENESS of 3 s: the region goes on answering for a while, then
// fails closed (503); once the link is back a revocation made meanwhile is enforced within the SLO.
func TestF6_5_ALinkCutFailsClosedAfterTheLimitAndRecoversWithTheRevocation(t *testing.T) {
	c := startReplicas(t, 1, harness.WithReadReplica(), harness.WithFaultProxies(), lagConfig(time.Hour, "unready", 3*time.Second))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	if res := r.Permission(c, p.Slug, alice); !hasAccess(res) {
		t.Fatalf("before the cut: %d %s", res.Status, res.Body)
	}
	path := "/service/" + p.Slug + "/permission"

	c.ReplicaFault.Cut()
	cutAt := time.Now()
	// A revocation made while the region cannot see the replica.
	deny := ruleBody(p, "alice", map[string]any{"effect": "deny"})
	if status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", deny); status != http.StatusOK {
		t.Fatalf("deny: %d %s", status, raw)
	}
	var answered, refused int
	var firstRefusal time.Duration
	for time.Since(cutAt) < 6*time.Second {
		res := r.Do(c, http.MethodGet, path, alice, nil)
		switch {
		case res.Status == http.StatusOK:
			if refused > 0 {
				t.Fatalf("an answer at %v after the region had refused one", time.Since(cutAt))
			}
			answered++
		case res.Status == http.StatusServiceUnavailable && strings.Contains(string(res.Body), "policy_unavailable"):
			if refused == 0 {
				firstRefusal = time.Since(cutAt)
			}
			refused++
		default:
			t.Fatalf("status %d %s", res.Status, res.Body)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Logf("answered %d, refused %d from %v after the cut", answered, refused, firstRefusal)
	if answered == 0 || refused == 0 {
		t.Fatalf("answered %d, refused %d: the region must answer within the limit and refuse beyond it", answered, refused)
	}
	if firstRefusal < 2*time.Second || firstRefusal > 5*time.Second {
		t.Errorf("the first refusal at %v, want about 3 s after the cut", firstRefusal)
	}

	c.ReplicaFault.Reset()
	back := time.Now()
	eventually(t, 15*time.Second, "the revocation to be enforced", func() bool {
		res := r.Do(c, http.MethodGet, path, alice, nil)
		return res.Status == http.StatusOK && !hasAccess(res)
	})
	if took := time.Since(back); took > 5*time.Second {
		t.Errorf("the revocation was enforced %v after the link returned, want within 5 s", took)
	}
}

// F6-11: after a replica is promoted that missed the last writes of the old primary, `licetum failover` raises every revision: the
// regions drop what they kept under revisions that are now used for other content, and no stale answer is served. The test plays the
// promotion by taking the last write away from the database and putting the revision back.
func TestF6_11_FailoverRaisesTheRevisionsAboveEverythingTheRegionsSaw(t *testing.T) {
	c := startReplicas(t, 2, lagConfig(time.Hour, "unready", 0))
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
		r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", token)
		if res := r.Permission(c, p.Slug, token); hasAccess(res) {
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
	served := hasAccess(c.Replicas[0].Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", token, nil))
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
		if res := r.Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", token, nil); !hasAccess(res) {
			t.Errorf("replica %d after the failover: %d %s", r.Index, res.Status, res.Body)
		}
	}
	// And revisions go on growing from the new base.
	before := c.Revision()
	if status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "another", nil)); status != http.StatusOK || rev != before+1 {
		t.Fatalf("a write after the failover: %d revision %d (was %d): %s", status, rev, before, raw)
	}
}
