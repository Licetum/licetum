//go:build integration

package cluster_test

// F2: the revision of the policy lives in PostgreSQL, in the transaction of the write it numbers; the replicas follow it,
// and an answer is kept only under a revision it reflects. The tests are named after the criteria of the issue.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

func ruleBody(p *harness.Policy, subject string, extra map[string]any) map[string]any {
	body := map[string]any{"type_resource_id": p.Branch, "access": []string{"read"}, "subject": subject}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func batchOf(ops ...map[string]any) map[string]any { return map[string]any{"operations": ops} }

func createRule(body map[string]any) map[string]any {
	return map[string]any{"op": "create", "entity": "assignment_rule", "body": body}
}

// F2-1: a write and its revision commit together: a write that fails leaves the revision as it was, a committed one adds
// exactly one, a batch adds one whatever its size, and a batch that fails adds none.
func TestF2_1_AWriteAndItsRevisionCommitAtomically(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org")

	before := c.Revision()
	status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "bob", nil))
	if status != http.StatusOK || rev != before+1 || c.Revision() != before+1 {
		t.Fatalf("a committed write: status %d, revision %d (was %d, now %d) %s", status, rev, before, c.Revision(), raw)
	}

	// A write that fails: the type does not exist.
	before = c.Revision()
	bad := ruleBody(p, "bob", map[string]any{"type_resource_id": uuid.NewString()})
	if status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", bad); status == http.StatusOK || rev != 0 || c.Revision() != before {
		t.Fatalf("a failed write moved the revision: status %d, revision %d, now %d (was %d) %s", status, rev, c.Revision(), before, raw)
	}

	// A batch of three writes is one revision.
	status, raw, rev = c.AdminWrite(http.MethodPost, "/batch", batchOf(
		createRule(ruleBody(p, "u1", nil)), createRule(ruleBody(p, "u2", nil)), createRule(ruleBody(p, "u3", nil))))
	if status != http.StatusOK || rev != before+1 || c.Revision() != before+1 {
		t.Fatalf("a batch of three: status %d, revision %d (was %d, now %d) %s", status, rev, before, c.Revision(), raw)
	}

	// A batch whose last operation fails applies nothing and takes no revision.
	before = c.Revision()
	var rules int
	if err := c.Pool.QueryRow(context.Background(), `SELECT count(*) FROM assignment_rule`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	status, raw, rev = c.AdminWrite(http.MethodPost, "/batch", batchOf(
		createRule(ruleBody(p, "u4", nil)), createRule(ruleBody(p, "u5", map[string]any{"type_resource_id": uuid.NewString()}))))
	var after int
	if err := c.Pool.QueryRow(context.Background(), `SELECT count(*) FROM assignment_rule`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if status == http.StatusOK || rev != 0 || c.Revision() != before || after != rules {
		t.Fatalf("a failed batch: status %d, revision %d, now %d (was %d), rules %d (was %d) %s", status, rev, c.Revision(), before, after, rules, raw)
	}
}

// F2-2: the revisions of writes that commit one after the other grow strictly, also with 20 writers at once: no two writes
// share a revision and none is skipped.
func TestF2_2_RevisionsOfConcurrentWritesAreStrictlyIncreasing(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org")
	first := c.Revision()

	const writers, each = 20, 5
	var mu sync.Mutex
	var seen []int64
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				status, raw, rev := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, fmt.Sprintf("w%d-%d", w, i), nil))
				if status != http.StatusOK {
					t.Errorf("write: %d %s", status, raw)
					return
				}
				mu.Lock()
				seen = append(seen, rev)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(seen, func(i, j int) bool { return seen[i] < seen[j] })
	for i, rev := range seen {
		if want := first + int64(i) + 1; rev != want {
			t.Fatalf("the revisions of %d writes are %v: the %dth is %d, want %d", len(seen), seen, i, rev, want)
		}
	}
	if len(seen) != writers*each || c.Revision() != first+writers*each {
		t.Fatalf("%d writes took revisions up to %d, the database is at %d", len(seen), seen[len(seen)-1], c.Revision())
	}
}

// F2-3: read-your-writes. A write returns revision R; a check with at_least_as_fresh=R on each of three replicas reflects
// it, every time. (The replicas read the database they write to: a lagging read replica is the subject of F6.)
func TestF2_3_AWriteIsSeenByEveryReplicaThatAsksForItsRevision(t *testing.T) {
	c := startReplicas(t, 3)
	p := c.SeedPolicy("org")
	bob := c.UserToken("bob")
	for _, r := range c.Replicas {
		if res := r.Permission(c, p.Slug, bob); res.Status != http.StatusOK { // every replica has a view, and an answer in memory
			t.Fatalf("bob: %d %s", res.Status, res.Body)
		}
	}

	const iterations = 100
	for i := range iterations {
		// The rule grants bob the branches ...
		status, raw, _ := c.AdminWrite(http.MethodPost, "/assignment_rule", ruleBody(p, "bob", nil))
		if status != http.StatusOK {
			t.Fatalf("grant: %d %s", status, raw)
		}
		var created struct{ ID string }
		_ = json.Unmarshal(raw, &created)
		for _, r := range c.Replicas {
			res := r.Permission(c, p.Slug, bob)
			if !res.Sees() {
				t.Fatalf("iteration %d: replica %d does not see the grant: %d %s", i, r.Index, res.Status, res.Body)
			}
		}
		// ... and is taken back, with the same promise.
		if status, raw, _ := c.AdminWrite(http.MethodDelete, "/assignment_rule/"+created.ID, nil); status != http.StatusOK {
			t.Fatalf("revoke: %d %s", status, raw)
		}
		for _, r := range c.Replicas {
			res := r.Permission(c, p.Slug, bob)
			if res.Status != http.StatusOK || res.Sees() { // a failure is no revocation
				t.Fatalf("iteration %d: replica %d still honours the revoked rule: %d %s", i, r.Index, res.Status, res.Body)
			}
		}
	}
}

// F2-4: no cache poisoning. A request that asks for a revision this process cannot confirm is answered from the data and
// leaves nothing in the cache under a revision it was not computed for.
func TestF2_4_AnAnswerIsNotKeptUnderARevisionItWasNotConfirmedFor(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org")
	r := c.Replicas[0]
	carol := c.UserToken("carol")
	alice := c.UserToken("alice")
	r.Warm(c, harness.Branches(p.Slug, alice), alice)

	ahead := c.Revision() + 1000
	started := time.Now()
	res := r.Send(c, harness.Branches(p.Slug, carol, authzenreq.AtLeast(ahead)), carol)
	if res.Status != http.StatusOK {
		t.Fatalf("a revision from the future: %d %s", res.Status, res.Body)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("waiting for a revision that never comes took %v, want about REVISION__WAIT_MAX (500ms)", took)
	}
	// The same request without the claim of freshness finds nothing kept for carol: it is computed, so it reads the database.
	first := r.Send(c, harness.Branches(p.Slug, carol), carol)
	if first.Status != http.StatusOK {
		t.Fatalf("an ordinary request: %d %s", first.Status, first.Body)
	}
	if first.Trace.DBQueries() == 0 {
		t.Fatalf("the answer for carol was kept by the request that asked for a revision from the future")
	}
	// The ordinary request is kept, so the check above can fail: the next one is answered from memory.
	if again := r.Send(c, harness.Branches(p.Slug, carol), carol); again.Trace.DBQueries() != 0 {
		t.Fatalf("an ordinary request left no answer in the cache: the next one made %d queries (%v), the check proves nothing",
			again.Trace.DBQueries(), again.Trace.DBQueryOps())
	}
}

// F2-5: after a deny rule is created, every replica answers deny within a second, without being asked for a revision.
func TestF2_5_ARevocationReachesEveryReplicaWithinASecond(t *testing.T) {
	c := startReplicas(t, 3)
	p := c.SeedPolicy("org")
	alice := c.UserToken("alice")
	for _, r := range c.Replicas {
		r.Warm(c, harness.Branches(p.Slug, alice), alice)
	}
	c.Must(http.MethodPost, "/assignment_rule", ruleBody(p, "alice", map[string]any{"effect": "deny"}))
	wrote := time.Now()

	var wg sync.WaitGroup
	for _, r := range c.Replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A plain request, with no at_least_as_fresh: the replicas must find the revision by themselves.
			took, ok := waitFor(3*time.Second, func() bool {
				res := r.Send(c, harness.Branches(p.Slug, alice), alice)
				return res.Status == http.StatusOK && !res.Sees()
			})
			if !ok {
				t.Errorf("replica %d still grants after %v", r.Index, took)
			} else if since := time.Since(wrote); since > time.Second {
				t.Errorf("replica %d denied %v after the write, want within 1s", r.Index, since)
			}
		}()
	}
	wg.Wait()
}

// F2-6: a synchronization of the data of one service does not invalidate the answers of the services that did not read it.
func TestF2_6_ASynchronizationOfOneServiceLeavesTheAnswersOfAnotherInPlace(t *testing.T) {
	c := startReplicas(t, 1)
	a := c.SeedPolicy("alpha")
	b := c.SeedPolicyOn("beta", "division-b", "branch-b")
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	for _, p := range []*harness.Policy{a, b} {
		r.Warm(c, harness.Branches(p.Slug, alice), alice)
	}

	// The standard request is the resource search, whose pages are cached as search answers.
	hits := func() float64 {
		return harness.MetricValue(r.Metrics(c), `licetum_search_cache_total{result="hit"}`)
	}
	misses := func() float64 {
		return harness.MetricValue(r.Metrics(c), `licetum_search_cache_total{result="miss"}`)
	}
	dataBefore, dataBeforeB := c.DataRevision(a.ServiceID), c.DataRevision(b.ServiceID)
	missesBefore := misses()

	const syncs = 50
	betaHits := 0
	for i := range syncs {
		// The data of alpha changes: a new branch, then the next one.
		fresh := fmt.Sprintf("n%d", i)
		rows := []map[string]any{
			{"id": "b1", "name": "Branch 1", "parent_id": "d1"}, {"id": "b2", "name": "Branch 2", "parent_id": "d1"},
			{"id": "b3", "name": "Branch 3", "parent_id": "d2"}, {"id": fresh, "name": "New " + fresh, "parent_id": "d2"},
		}
		c.Source.Set("branch", rows...)
		c.Must(http.MethodPost, "/type_resource/"+a.Branch+"/resource/refresh", nil)
		// The replica reads the new revision of alpha's data within a poll interval, and alpha's answer follows it.
		if took, ok := waitFor(3*time.Second, func() bool {
			res := r.Permission(c, a.Slug, alice)
			return res.Status == http.StatusOK && slices.Contains(res.IDs(t), fresh)
		}); !ok {
			t.Fatalf("synchronization %d: alpha's answer did not show the new branch after %v", i, took)
		}
		// Beta's answer, asked after each change of alpha's data: a hit, or a miss.
		before := hits()
		if res := r.Permission(c, b.Slug, alice); res.Status != http.StatusOK {
			t.Fatalf("beta: %d %s", res.Status, res.Body)
		}
		if hits() == before+1 {
			betaHits++
		}
	}
	if c.DataRevision(a.ServiceID) < dataBefore+syncs {
		t.Fatalf("the data revision of alpha went from %d to %d in %d synchronizations that changed its data", dataBefore, c.DataRevision(a.ServiceID), syncs)
	}
	if c.DataRevision(b.ServiceID) != dataBeforeB {
		t.Fatalf("the data revision of beta moved from %d to %d with the synchronizations of alpha", dataBeforeB, c.DataRevision(b.ServiceID))
	}
	t.Logf("beta: %d hits in %d questions; alpha was recomputed %d times", betaHits, syncs, int(misses()-missesBefore))
	if ratio := float64(betaHits) / syncs; ratio < 0.95 {
		t.Fatalf("the hit ratio of beta is %.2f over %d synchronizations of alpha, want at least 0.95", ratio, syncs)
	}
	if recomputed := int(misses() - missesBefore); recomputed < syncs {
		t.Fatalf("alpha was recomputed %d times in %d changes of its data: the test does not prove the answers follow the data", recomputed, syncs)
	}
}

// F2-10: the poller costs at most four statements a second per replica when nothing happens.
func TestF2_10_FollowingTheRevisionCostsFewStatementsAtRest(t *testing.T) {
	c := startReplicas(t, 2)
	c.SeedPolicy("org")
	time.Sleep(time.Second) // the watchers have started
	for _, r := range c.Replicas {
		before := harness.MetricValue(r.Metrics(c), "licetum_db_queries_total")
		time.Sleep(4 * time.Second)
		after := harness.MetricValue(r.Metrics(c), "licetum_db_queries_total")
		if perSecond := (after - before) / 4; perSecond > 4.5 {
			t.Errorf("replica %d sent %.1f statements a second at rest, want at most 4", r.Index, perSecond)
		} else if perSecond < 2 {
			t.Errorf("replica %d sent %.1f statements a second: the revision is not followed", r.Index, perSecond)
		}
	}
}
