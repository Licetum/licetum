//go:build integration

package cluster_test

// F2: the revision of the policy lives in PostgreSQL, in the transaction of the write it numbers; the replicas follow it,
// and an answer is kept only under a revision it reflects. The tests are named after the criteria of the issue.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/redisx"
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
	p := c.SeedPolicy("org", 0)

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
	p := c.SeedPolicy("org", 0)
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
	p := c.SeedPolicy("org", 0)
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
			if _, ok := res.JSON(t)["branch"]; !ok {
				t.Fatalf("iteration %d: replica %d does not see the grant: %s", i, r.Index, res.Body)
			}
		}
		// ... and is taken back, with the same promise.
		if status, raw, _ := c.AdminWrite(http.MethodDelete, "/assignment_rule/"+created.ID, nil); status != http.StatusOK {
			t.Fatalf("revoke: %d %s", status, raw)
		}
		for _, r := range c.Replicas {
			res := r.Permission(c, p.Slug, bob)
			if _, ok := res.JSON(t)["branch"]; ok {
				t.Fatalf("iteration %d: replica %d still honours the revoked rule: %s", i, r.Index, res.Body)
			}
		}
	}
}

// F2-4: no cache poisoning. A request that asks for a revision this process cannot confirm is answered from the data and
// leaves nothing in the cache under a revision it was not computed for.
func TestF2_4_AnAnswerIsNotKeptUnderARevisionItWasNotConfirmedFor(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	carol := c.UserToken("carol")
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", c.UserToken("alice"))

	keys := func() []string {
		got, err := c.Redis.Keys(context.Background(), "authz:permission:*").Result()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		return got
	}
	before := keys()
	ahead := c.Revision() + 1000
	started := time.Now()
	res := r.Do(c, http.MethodGet, fmt.Sprintf("/service/%s/permission?at_least_as_fresh=%d", p.Slug, ahead), carol, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("a revision from the future: %d %s", res.Status, res.Body)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("waiting for a revision that never comes took %v, want about REVISION__WAIT_MAX (500ms)", took)
	}
	time.Sleep(300 * time.Millisecond) // the cache writes the request would have left in the background
	after := keys()
	for _, key := range after {
		if strings.Contains(key, ":carol:") {
			t.Fatalf("an answer for carol was kept: %v (before %v)", after, before)
		}
	}
	// The same request without the claim of freshness is kept, so the check above can fail.
	r.Permission(c, p.Slug, carol)
	r.Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", carol, nil)
	time.Sleep(300 * time.Millisecond)
	found := false
	for _, key := range keys() {
		found = found || strings.Contains(key, ":carol:")
	}
	if !found {
		t.Fatal("an ordinary request left no answer in the cache: the check proves nothing")
	}
}

// F2-5: after a deny rule is created, every replica answers deny within a second, without being asked for a revision.
func TestF2_5_ARevocationReachesEveryReplicaWithinASecond(t *testing.T) {
	c := startReplicas(t, 3)
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	for _, r := range c.Replicas {
		r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice)
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
				res := r.Do(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice, nil)
				_, granted := res.JSON(t)["branch"]
				return res.Status == http.StatusOK && !granted
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
	a := c.SeedPolicy("alpha", 0)
	b := c.SeedPolicyOn("beta", 0, "division-b", "branch-b")
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	for _, p := range []*harness.Policy{a, b} {
		r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice)
	}
	waitForKeys(t, c, "authz:permission:*")

	hits := func() float64 {
		return harness.MetricValue(r.Metrics(c), `licetum_permission_cache_total{result="hit"}`)
	}
	misses := func() float64 {
		return harness.MetricValue(r.Metrics(c), `licetum_permission_cache_total{result="miss"}`)
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
			return res.Status == http.StatusOK && strings.Contains(string(res.Body), `"`+fresh+`"`)
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

// F2-8: the Redis of the cache is lost while serving (FLUSHALL): the answers stay the same, and no request fails.
func TestF2_8_FlushingRedisWhileServingChangesNoAnswer(t *testing.T) {
	c := startReplicas(t, 2)
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	want := map[int]string{}
	for _, r := range c.Replicas {
		res := r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice)
		want[r.Index] = string(res.Body)
	}

	dataBefore := c.DataRevision(p.ServiceID)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, r := range c.Replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				status, body, err := r.TryGet(c, "/service/"+p.Slug+"/permission", alice, 10*time.Second)
				if err != nil || status != http.StatusOK || string(body) != want[r.Index] {
					t.Errorf("replica %d: status %d, %v, body %s (want %s)", r.Index, status, err, body, want[r.Index])
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	for range 3 {
		if err := c.Redis.FlushAll(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(400 * time.Millisecond)
	}
	// A cold replica memory too: everything is read again, from the database and the sources.
	for _, r := range c.Replicas {
		r.App.ClearMemoryCaches()
	}
	time.Sleep(600 * time.Millisecond)
	close(stop)
	wg.Wait()
	// The synchronization state was lost with Redis and found the rows as they were: the data did not move, so no cached
	// answer of the other replicas was made stale by it.
	if got := c.DataRevision(p.ServiceID); got != dataBefore {
		t.Errorf("the data revision went from %d to %d with nothing written to the source", dataBefore, got)
	}
}

// F2-9: the replicas of the previous release follow the Redis counters; the write of this release bumps them too.
func TestF2_9_APreviousReleaseCanStillFollowTheWrites(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org", 0)
	ctx := context.Background()
	before, err := redisx.GetVersion(ctx, c.Redis)
	if err != nil {
		t.Fatal(err)
	}
	c.Must(http.MethodPost, "/assignment_rule", ruleBody(p, "bob", nil))
	after, err := redisx.GetVersion(ctx, c.Redis)
	if err != nil || after != before+1 {
		t.Fatalf("the cache version of the previous release went from %d to %d (%v) with a write, want +1", before, after, err)
	}
	// A change of the data moves the hash of the data versions the previous release reads.
	c.Source.Set("branch", map[string]any{"id": "b1", "name": "Branch 1", "parent_id": "d1"})
	c.Must(http.MethodPost, "/type_resource/"+p.Branch+"/resource/refresh", nil)
	v, err := c.Redis.HGet(ctx, "authz:data_versions", p.ServiceID).Int64()
	if err != nil || v < 1 {
		t.Fatalf("the data versions of the previous release: %d, %v", v, err)
	}
	// And the keys of the answers carry the new format, so the two releases do not read each other's.
	r := c.Replicas[0]
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", c.UserToken("alice"))
	waitForKeys(t, c, "authz:permission:t8:r*")
	if old, _ := c.Redis.Keys(ctx, "authz:permission:t7:*").Result(); len(old) != 0 {
		t.Fatalf("this release wrote keys of the format of the previous one: %v", old)
	}
}

// F2-10: the poller costs at most four statements a second per replica when nothing happens.
func TestF2_10_FollowingTheRevisionCostsFewStatementsAtRest(t *testing.T) {
	c := startReplicas(t, 2)
	c.SeedPolicy("org", 0)
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
