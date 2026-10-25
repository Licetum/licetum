//go:build integration

package cluster_test

// F1: the fast read path. A warm request sends no query to PostgreSQL and no command to Redis; a request answered from
// Redis makes one round trip; what a cache miss costs does not grow with the policy; a deleted service or a changed
// slug is seen by every replica within a second; the key set of the identity provider is fetched once per TTL, and a
// flood of bad tokens does not reach the provider.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

type request struct {
	name string
	req  authzenreq.Request
}

// requestsOf are the requests of the check API of a service, from the subject, that read a policy and an answer: the resources
// of a type, a page of them, an evaluation and a batch of evaluations with their explanation.
func requestsOf(p *harness.Policy, subject string) []request {
	questions := []authzenreq.Question{
		{Resource: authzenreq.On("branch", "b1"), Action: "read"}, {Resource: authzenreq.Whole("branch"), Action: "read"},
	}
	return []request{
		{"resources", authzenreq.SearchResource(p.Slug, subject, "branch", "read")},
		{"a page of resources", authzenreq.SearchResource(p.Slug, subject, "branch", "read", authzenreq.Page(2, ""))},
		{"evaluation", authzenreq.Evaluation(p.Slug, subject, authzenreq.On("branch", "b1"), "read", authzenreq.Explain())},
		{"evaluations", authzenreq.Evaluations(p.Slug, subject, questions, authzenreq.Explain())},
	}
}

// settle sends the request until it costs the same twice in a row and the background work of the earlier ones has
// ended, then returns the next one: the cost of a request that finds everything cached.
func settle(t *testing.T, c *harness.Cluster, r *harness.Replica, q request, token string) *harness.Result {
	t.Helper()
	var last *harness.Result
	same := 0
	for range 40 {
		res := r.Send(c, q.req, token)
		if res.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", q.name, res.Status, res.Body)
		}
		if last != nil && last.Trace.DBQueries() == res.Trace.DBQueries() && last.Trace.RedisRoundTrips() == res.Trace.RedisRoundTrips() {
			same++
		} else {
			same = 0
		}
		last = res
		if same >= 2 {
			break
		}
		time.Sleep(60 * time.Millisecond)
	}
	return r.Send(c, q.req, token)
}

// F1-1: a request whose token and answer are cached sends no query to PostgreSQL (and none to Redis).
func TestF1_1_AWarmRequestSendsNoQueryToPostgreSQL(t *testing.T) {
	c := startReplicas(t, 2)
	p := c.SeedPolicy("org")
	alice := c.UserToken("alice")
	for _, r := range c.Replicas {
		for _, q := range requestsOf(p, "alice") {
			res := settle(t, c, r, q, alice)
			if got := res.Trace.DBQueries(); got != 0 {
				t.Errorf("replica %d, %s: %d queries to PostgreSQL (%v), want 0", r.Index, q.name, got, res.Trace.DBQueryOps())
			}
			if got := res.Trace.RedisRoundTrips(); got != 0 {
				t.Errorf("replica %d, %s: %d round trips to Redis (%v), want 0", r.Index, q.name, got, res.Trace.RedisOps())
			}
		}
	}
}

// F1-2: with the answer not in memory but in Redis, a request makes one round trip to Redis and sends no query.
func TestF1_2_ARequestAnsweredFromRedisMakesOneRoundTrip(t *testing.T) {
	c := startReplicas(t, 2)
	p := c.SeedPolicy("org")
	alice := c.UserToken("alice")
	for _, r := range c.Replicas {
		for _, q := range requestsOf(p, "alice") {
			settle(t, c, r, q, alice)
			r.App.ClearAnswerMemoryCache()
			res := r.Send(c, q.req, alice)
			if res.Status != http.StatusOK {
				t.Fatalf("%s: %d %s", q.name, res.Status, res.Body)
			}
			if got := res.Trace.DBQueries(); got != 0 {
				t.Errorf("replica %d, %s: %d queries (%v), want 0", r.Index, q.name, got, res.Trace.DBQueryOps())
			}
			if got := res.Trace.RedisRoundTrips(); got != 1 {
				t.Errorf("replica %d, %s: %d round trips to Redis (%v), want 1", r.Index, q.name, got, res.Trace.RedisOps())
			}
			// The answer from Redis is kept in memory: the next request does not go to Redis.
			if again := r.Send(c, q.req, alice); again.Trace.RedisRoundTrips() != 0 || again.Trace.DBQueries() != 0 {
				t.Errorf("replica %d, %s: the answer was not kept in memory: db=%d redis=%d", r.Index, q.name, again.Trace.DBQueries(), again.Trace.RedisRoundTrips())
			}
		}
	}
}

// insertRules adds n rules for other people to a type, straight into the table: nearly all with a subject, 1 in 100 with
// a predicate (the ones that are scanned).
func insertRules(t *testing.T, c *harness.Cluster, typeID string, n int) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.Pool.Exec(ctx, `
		INSERT INTO assignment_rule (type_resource_id, access, subject, predicate, enabled)
		SELECT $1::uuid, ARRAY['read'], 'filler-' || g, '[]'::jsonb, true FROM generate_series(1, $2) g`, typeID, n); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pool.Exec(ctx, `
		INSERT INTO assignment_rule (type_resource_id, access, subject, predicate, enabled)
		SELECT $1::uuid, ARRAY['read'], NULL, '[{"attr":"department","op":"eq","value":"hr"}]'::jsonb, true
		FROM generate_series(1, $2)`, typeID, max(1, n/100)); err != nil {
		t.Fatal(err)
	}
}

// missCost is the number of queries to PostgreSQL that the first answer of a subject costs on a policy with n rules.
func missCost(t *testing.T, n int) (queries int, ops map[string]int) {
	t.Helper()
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org")
	c.Must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": p.Branch, "access": []string{"read"}, "subject": "bob",
	})
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	r.Warm(c, harness.Branches(p.Slug, alice), alice)

	insertRules(t, c, p.Branch, n)
	rev, err := db.BumpRevision(context.Background(), c.Pool, db.ScopeRules)
	if err != nil {
		t.Fatal(err)
	}
	// The replica reads the new revision, and with it the policy (the snapshot is built here, not in the request to measure).
	started := time.Now()
	if res := r.Send(c, harness.Branches(p.Slug, alice, authzenreq.AtLeast(rev)), alice); res.Status != http.StatusOK {
		t.Fatalf("alice: %d %s", res.Status, res.Body)
	}
	t.Logf("%d rules: the snapshot was ready after %v", n, time.Since(started))
	if !r.App.PolicySnapshotLoaded() {
		t.Fatal("the replica has no snapshot of the policy")
	}

	res := r.Permission(c, p.Slug, c.UserToken("bob"))
	if res.Status != http.StatusOK {
		t.Fatalf("bob: %d %s", res.Status, res.Body)
	}
	return res.Trace.DBQueries(), res.Trace.DBQueryOps()
}

// F1-3: what a cache miss costs in queries is the same for a thousand rules and for a hundred thousand, and small.
func TestF1_3_ACacheMissCostsTheSameNumberOfQueriesWhateverTheSizeOfThePolicy(t *testing.T) {
	small, smallOps := missCost(t, 1_000)
	large, largeOps := missCost(t, 100_000)
	t.Logf("a miss: %d queries %v with 1 000 rules, %d queries %v with 100 000 rules", small, smallOps, large, largeOps)
	if small != large {
		t.Errorf("a miss costs %d queries with 1 000 rules and %d with 100 000", small, large)
	}
	if large > 8 {
		t.Errorf("a miss costs %d queries, want at most 8", large)
	}
}

// waitFor polls until f returns true, at most timeout; it returns how long it took.
func waitFor(timeout time.Duration, f func() bool) (time.Duration, bool) {
	start := time.Now()
	for time.Since(start) < timeout {
		if f() {
			return time.Since(start), true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Since(start), false
}

// F1-5: after the slug of a service changes or the service is deleted, every replica refuses the old slug within a second: they
// follow the revision. A client of the renamed service is a valid credential that gives nothing on the old slug (the one 403); the
// clients of a deleted service leave the directory with it, so their key is no credential any more (401).
func TestF1_5_ReplicasFollowTheRevisionForSlugsAndDeletedServices(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
		do   func(c *harness.Cluster, p *harness.Policy)
	}{
		{"slug changed", http.StatusForbidden, func(c *harness.Cluster, p *harness.Policy) {
			payload := c.ServicePayload("org-renamed")
			c.Must(http.MethodPatch, "/service/"+p.ServiceID, payload)
		}},
		{"service deleted", http.StatusUnauthorized, func(c *harness.Cluster, p *harness.Policy) {
			// A service goes when it has no types left, and a type when nothing refers to it.
			c.Must(http.MethodDelete, "/assignment_rule/"+p.Rule, nil)
			for _, typeID := range []string{p.Branch, p.Division} {
				base := "/service/" + p.ServiceID + "/type_resource/" + typeID
				status, raw := c.AdminDo(http.MethodGet, base+"/resource/synchronizer", nil)
				var syncs []struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(raw, &syncs); err != nil || status != http.StatusOK {
					c.T.Fatalf("list the synchronizers: %d %s", status, raw)
				}
				for _, s := range syncs {
					c.Must(http.MethodDelete, base+"/resource/synchronizer/"+s.ID, nil)
				}
				c.Must(http.MethodDelete, base, nil)
			}
			c.Must(http.MethodDelete, "/service/"+p.ServiceID, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := startReplicas(t, 3)
			p := c.SeedPolicy("org")
			alice := c.UserToken("alice")
			for _, r := range c.Replicas {
				r.Warm(c, harness.Branches(p.Slug, alice), alice)
			}
			tc.do(c, p)
			wrote := time.Now()

			var wg sync.WaitGroup
			for _, r := range c.Replicas {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// Plain requests, the way a client sends them: no min_revision.
					took, ok := waitFor(3*time.Second, func() bool {
						return r.Send(c, harness.Branches(p.Slug, alice), alice).Status == tc.want
					})
					if !ok {
						t.Errorf("replica %d still answers for the old slug after %v", r.Index, took)
					} else if since := time.Since(wrote); since > time.Second {
						t.Errorf("replica %d answered %d %v after the write, want within 1s", r.Index, tc.want, since)
					}
				}()
			}
			wg.Wait()
		})
	}
}

// pepJWTCluster starts a cluster whose service org has a PEP client that authenticates with the tokens of the fake identity
// provider, and returns it with the policy and a function that asks the first replica as that client.
func pepJWTCluster(t *testing.T, replicas int, opts ...harness.Option) (*harness.Cluster, *harness.Policy, func(token string) *harness.Result) {
	t.Helper()
	c := startReplicas(t, replicas, opts...)
	p := c.SeedPolicy("org")
	c.RegisterPEPIssuer()
	c.JWTClient(p.Slug, "checkout")
	ask := func(token string) *harness.Result {
		return c.Replicas[0].Send(c, authzenreq.SearchResource(p.Slug, "alice", "branch", "read"), token)
	}
	return c, p, ask
}

// F1-6: a thousand requests with distinct valid tokens of one key ask the provider for its key set once.
func TestF1_6_DistinctValidTokensOfOneKeyFetchTheKeySetOnce(t *testing.T) {
	c, _, ask := pepJWTCluster(t, 1)
	c.IdP.ResetCounters()

	const total = 1000
	tokens := make([]string, total)
	for i := range tokens {
		tokens[i] = c.PEPToken("checkout", fmt.Sprintf("instance-%d", i))
	}
	var wg sync.WaitGroup
	next := make(chan string)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for token := range next {
				if res := ask(token); res.Status != http.StatusOK {
					t.Errorf("a valid token: %d %s", res.Status, res.Body)
				}
			}
		}()
	}
	for _, token := range tokens {
		next <- token
	}
	close(next)
	wg.Wait()
	if got := c.IdP.JWKSRequests(); got != 1 {
		t.Fatalf("%d JWKS requests for %d valid tokens of one key, want 1", got, total)
	}
}

// signedWith signs a token with a key the provider does not know (any kid).
func signedWith(t *testing.T, c *harness.Cluster, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": c.IdP.Issuer(), "aud": harness.PEPAudience, "azp": "checkout", "sub": "mallory", "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = kid
	tok.Header["typ"] = "at+jwt"
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// F1-7: a thousand garbage or random-key tokens answer 401 and cost the provider at most one request per interval; the same bad
// token again and again does not reach the provider, PostgreSQL or Redis.
func TestF1_7_BadTokensDoNotAmplifyTheLoadOnTheProvider(t *testing.T) {
	c, _, ask := pepJWTCluster(t, 1)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c.IdP.ResetCounters()

	var wg sync.WaitGroup
	next := make(chan string)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for token := range next {
				if res := ask(token); res.Status != http.StatusUnauthorized {
					t.Errorf("a bad token: %d %s", res.Status, res.Body)
				}
			}
		}()
	}
	for i := range 1000 {
		if i%2 == 0 {
			next <- fmt.Sprintf("garbage-%d", i)
		} else {
			next <- signedWith(t, c, key, fmt.Sprintf("random-%d", i))
		}
	}
	close(next)
	wg.Wait()
	if got := c.IdP.JWKSRequests(); got > 1 {
		t.Fatalf("%d JWKS requests for 1000 bad tokens, want at most 1 per AUTH__JWKS_MIN_REFRESH", got)
	}

	// The same bad token (a known key id, a wrong signature) again: it is refused from the key set in memory.
	known := signedWith(t, c, key, c.IdP.KeyID())
	if first := ask(known); first.Status != http.StatusUnauthorized {
		t.Fatalf("a token with a wrong signature: %d %s", first.Status, first.Body)
	}
	before := c.IdP.JWKSRequests()
	for i := range 100 {
		res := ask(known)
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("repeat %d: %d %s", i, res.Status, res.Body)
		}
		if res.Trace.RedisRoundTrips() != 0 || res.Trace.DBQueries() != 0 {
			t.Fatalf("repeat %d of a bad token cost db=%d redis=%d, want none", i, res.Trace.DBQueries(), res.Trace.RedisRoundTrips())
		}
	}
	if got := c.IdP.JWKSRequests(); got != before {
		t.Fatalf("the same bad token reached the provider: %d JWKS requests, were %d", got, before)
	}
}

// F1-8: after the provider rotates its key, a valid token with the new key id is accepted after exactly one refetch.
func TestF1_8_KeyRotationCostsOneRefetch(t *testing.T) {
	c, _, ask := pepJWTCluster(t, 1, harness.WithConfig(func(cfg *config.Config) { cfg.Auth.JWKSMinRefresh = 50 * time.Millisecond }))
	c.IdP.ResetCounters() // the admin API asked for the key set too, to set the policy up
	if res := ask(c.PEPToken("checkout", "first")); res.Status != http.StatusOK {
		t.Fatalf("the first token: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 1 {
		t.Fatalf("%d JWKS requests for the first token, want 1", got)
	}

	c.IdP.RotateKey(true)
	time.Sleep(100 * time.Millisecond) // past AUTH__JWKS_MIN_REFRESH
	for _, name := range []string{"carol", "dave", "erin"} {
		if res := ask(c.PEPToken("checkout", name)); res.Status != http.StatusOK {
			t.Fatalf("%s, a token of the new key: %d %s", name, res.Status, res.Body)
		}
	}
	if got := c.IdP.JWKSRequests(); got != 2 {
		t.Fatalf("%d JWKS requests after the rotation, want exactly 1 more (2 in all)", got)
	}
	// The old key is still accepted, from the set that was fetched.
}

// A policy that does not fit SNAPSHOT__MAX_BYTES is not copied: the queries read it, the answers are the same, and the
// fallback is counted.
func TestF1_ThePolicyThatDoesNotFitIsReadWithQueries(t *testing.T) {
	c := startReplicas(t, 1, harness.WithConfig(func(cfg *config.Config) { cfg.Snapshot.MaxBytes = 2_000 }))
	p := c.SeedPolicy("org")
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	res := r.Permission(c, p.Slug, alice)
	if res.Status != http.StatusOK {
		t.Fatalf("alice: %d %s", res.Status, res.Body)
	}
	if ids := res.IDs(t); len(ids) != 3 {
		t.Fatalf("alice should see the three branches: %s", res.Body)
	}
	if r.App.PolicySnapshotLoaded() {
		t.Fatal("a snapshot was built although the policy does not fit")
	}
	if got := harness.MetricValue(r.Metrics(c), "licetum_snapshot_fallback_total"); got == 0 {
		t.Fatal("licetum_snapshot_fallback_total did not count the fallback")
	}
	// A request that is answered from memory still costs nothing; one that is not reads the policy with queries.
	warm := r.Warm(c, harness.Branches(p.Slug, alice), alice)
	if warm.Trace.DBQueries() != 1 { // the slug: without a snapshot it is looked up
		t.Errorf("a warm request without a snapshot made %d queries (%v), want the lookup of the slug", warm.Trace.DBQueries(), warm.Trace.DBQueryOps())
	}
}

// SNAPSHOT__MAX_BYTES below zero turns the snapshot off altogether.
func TestF1_TheSnapshotCanBeTurnedOff(t *testing.T) {
	c := startReplicas(t, 1, harness.WithConfig(func(cfg *config.Config) { cfg.Snapshot.MaxBytes = -1 }))
	p := c.SeedPolicy("org")
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	warm := r.Warm(c, harness.Branches(p.Slug, alice), alice)
	if r.App.PolicySnapshotLoaded() {
		t.Fatal("a snapshot was built although it is turned off")
	}
	// Without the snapshot the slug is looked up and the revisions are read; the answer is still kept in memory.
	if warm.Trace.DBQueries() != 2 || warm.Trace.RedisRoundTrips() != 0 {
		t.Errorf("without the snapshot a warm request made db=%d redis=%d (%v, %v), want 2 and 0", warm.Trace.DBQueries(),
			warm.Trace.RedisRoundTrips(), warm.Trace.RedisOps(), warm.Trace.DBQueryOps())
	}
}
