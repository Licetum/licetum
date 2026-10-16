//go:build integration

package cluster_test

// F5: resilience and operations. The tests are named after the criteria of the issue "F5: resilience and operations".

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
)

// eventually polls f every 50 ms until it returns true or the time is up.
func eventually(t *testing.T, within time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if f() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not within %v: %s", within, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readyz(c *harness.Cluster, r *harness.Replica) *harness.Result {
	c.T.Helper()
	return r.Do(c, http.MethodGet, "/readyz", "", nil)
}

func healthz(c *harness.Cluster, r *harness.Replica) *harness.Result {
	c.T.Helper()
	return r.Do(c, http.MethodGet, "/healthz", "", nil)
}

// errorCode is extra.code of an error answer.
func errorCode(t *testing.T, res *harness.Result) string {
	t.Helper()
	var body struct {
		Detail string `json:"detail"`
		Extra  struct {
			Code string `json:"code"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(res.Body, &body); err != nil {
		t.Fatalf("decode the error %s: %v", res.Body, err)
	}
	return body.Extra.Code
}

// F5-1: /readyz is 503 when PostgreSQL is unreachable or the schema is older than the binary, and 200 otherwise; /healthz
// is 200 in both cases.
func TestF5_1_ReadyzFollowsPostgreSQLAndTheSchema(t *testing.T) {
	c := startReplicas(t, 1, harness.WithFaultProxies())
	c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	ctx := context.Background()

	if res := readyz(c, r); res.Status != http.StatusOK {
		t.Fatalf("a healthy replica: /readyz is %d %s", res.Status, res.Body)
	}

	// PostgreSQL cannot be reached.
	c.PostgresFault.Cut()
	eventually(t, 5*time.Second, "/readyz is 503 while PostgreSQL is cut", func() bool {
		return readyz(c, r).Status == http.StatusServiceUnavailable
	})
	// The first refusal may name the snapshot: the result of the database check is kept for a second, and a revision that moved
	// just before the cut (the synchronization of the policy just seeded) leaves the copy of the policy to be read again. The
	// reason is the database once that result has expired.
	var res *harness.Result
	eventually(t, 5*time.Second, "/readyz names the database as the reason", func() bool {
		res = readyz(c, r)
		return res.Status == http.StatusServiceUnavailable && strings.Contains(string(res.Body), "database")
	})
	if code := errorCode(t, res); code != "not_ready" {
		t.Fatalf("503 with %s, want not_ready", res.Body)
	}
	if h := healthz(c, r); h.Status != http.StatusOK {
		t.Fatalf("/healthz is %d with PostgreSQL cut: liveness must not depend on it", h.Status)
	}
	c.PostgresFault.Reset()
	eventually(t, 8*time.Second, "/readyz is 200 again once PostgreSQL is back", func() bool {
		return readyz(c, r).Status == http.StatusOK
	})

	// The schema is older than the binary: the last migration is not applied.
	latest := db.LatestMigration()
	if _, err := c.Pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = $1`, latest); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "/readyz is 503 with an old schema", func() bool {
		return readyz(c, r).Status == http.StatusServiceUnavailable
	})
	if res := readyz(c, r); !strings.Contains(string(res.Body), "schema") {
		t.Fatalf("503 with %s, want the reason schema", res.Body)
	}
	if h := healthz(c, r); h.Status != http.StatusOK {
		t.Fatalf("/healthz is %d with an old schema", h.Status)
	}

	// The schema is the binary's again, and then newer than it: a pod of the previous release stays in service while the
	// new schema is applied (a rolling update migrates first).
	if _, err := c.Pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, latest); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "/readyz is 200 with the schema of the binary", func() bool {
		return readyz(c, r).Status == http.StatusOK
	})
	if _, err := c.Pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, latest+1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond) // the result is reused for a second
	if res := readyz(c, r); res.Status != http.StatusOK {
		t.Fatalf("a schema newer than the binary: /readyz is %d %s, want 200", res.Status, res.Body)
	}
}

// F5-1: the result of the check is reused for a second, so that probes do not become queries.
func TestF5_1_ReadyzIsReusedForASecond(t *testing.T) {
	c := startReplicas(t, 1, harness.WithFaultProxies())
	c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	eventually(t, 5*time.Second, "/readyz is 200", func() bool { return readyz(c, r).Status == http.StatusOK })

	time.Sleep(1100 * time.Millisecond) // the result is old now: this probe checks again ...
	if res := readyz(c, r); res.Status != http.StatusOK {
		t.Fatalf("/readyz is %d %s", res.Status, res.Body)
	}
	c.PostgresFault.Cut()
	// ... and this one, a moment later, reuses its result, although PostgreSQL has gone.
	if res := readyz(c, r); res.Status != http.StatusOK {
		t.Fatalf("/readyz right after the check is %d %s, want the result of the check reused", res.Status, res.Body)
	}
	time.Sleep(1100 * time.Millisecond)
	if res := readyz(c, r); res.Status != http.StatusServiceUnavailable {
		t.Fatalf("/readyz a second later is %d %s, want the check made again", res.Status, res.Body)
	}
}

// F5-1: Redis is not part of readiness. Without it the replica is degraded, which the metric says.
func TestF5_1_RedisIsNotPartOfReadiness(t *testing.T) {
	c := startReplicas(t, 1, harness.WithFaultProxies(), harness.WithHotRedis(50*time.Millisecond))
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	path := "/service/" + p.Slug + "/permission"

	degraded := func() float64 {
		return harness.MetricValue(r.Metrics(c), `licetum_degraded{component="redis"}`)
	}
	if res := readyz(c, r); res.Status != http.StatusOK || degraded() != 0 {
		t.Fatalf("a healthy replica: /readyz %d, licetum_degraded{redis} %v", res.Status, degraded())
	}

	c.RedisFault.Cut()
	// Requests for subjects the replica has not seen need Redis (the token cache), fail to reach it, and trip the fast-fail.
	eventually(t, 10*time.Second, "licetum_degraded{component=\"redis\"} is 1 with Redis cut", func() bool {
		r.Do(c, http.MethodGet, path, c.UserToken("user-"+time.Now().Format("150405.000000")), nil)
		return degraded() == 1
	})
	if res := readyz(c, r); res.Status != http.StatusOK {
		t.Fatalf("/readyz is %d %s with Redis cut: Redis is not part of readiness", res.Status, res.Body)
	}

	c.RedisFault.Reset()
	eventually(t, 15*time.Second, "licetum_degraded{component=\"redis\"} is 0 once Redis is back", func() bool {
		r.Do(c, http.MethodGet, path, c.UserToken("back-"+time.Now().Format("150405.000000")), nil)
		return degraded() == 0
	})
}

// F5-3 (shutdown): once the process is draining it says so on /readyz, asks for the connection to be closed, and goes on
// serving requests, which the balancer still sends for a while.
func TestF5_3_ADrainingReplicaIsNotReadyButServes(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	if res := r.Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("before draining: %d %s", res.Status, res.Body)
	}

	r.App.SetDraining()

	res := readyz(c, r)
	if res.Status != http.StatusServiceUnavailable || !strings.Contains(string(res.Body), "draining") {
		t.Fatalf("/readyz while draining is %d %s, want 503 with the reason draining", res.Status, res.Body)
	}
	if h := healthz(c, r); h.Status != http.StatusOK {
		t.Fatalf("/healthz while draining is %d: liveness must stay 200", h.Status)
	}
	if res := r.Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("a request while draining: %d %s, want it served", res.Status, res.Body)
	}

	// The response asks the client to open its next connection elsewhere.
	resp, err := http.Get(r.Server.URL + "/service/" + p.Slug + "/permission")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !resp.Close {
		t.Fatal("the response of a draining replica does not carry Connection: close")
	}
}

// F5-8 (admission): above HTTP__MAX_INFLIGHT requests the others are refused at once with 503 and Retry-After, the probes
// and the metrics are always answered, and what was admitted is served.
func TestF5_8_RequestsAboveTheLimitAreRefusedAtOnce(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	if res := r.Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("warm request: %d %s", res.Status, res.Body)
	}

	const limit, senders = 2, 8
	r.App.Cfg.HTTP.MaxInflight = limit
	// consistency=higher asks the sources again, and the source answers slowly: the requests stay in flight.
	c.Source.SetDelay(1500 * time.Millisecond)

	var wg sync.WaitGroup
	results := make([]*harness.Result, senders)
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.Fresh(c, p.Slug, alice)
		}()
	}
	// While the replica is full, probes and metrics are answered.
	eventually(t, 5*time.Second, "the replica is serving its limit", func() bool { return r.App.Inflight() >= limit })
	if res := readyz(c, r); res.Status != http.StatusOK {
		t.Errorf("/readyz while the replica is full: %d %s", res.Status, res.Body)
	}
	if res := healthz(c, r); res.Status != http.StatusOK {
		t.Errorf("/healthz while the replica is full: %d", res.Status)
	}
	if !strings.Contains(r.Metrics(c), "licetum_shed_total") {
		t.Error("/metrics while the replica is full does not answer")
	}
	wg.Wait()

	served, refused := 0, 0
	for _, res := range results {
		switch res.Status {
		case http.StatusOK:
			served++
		case http.StatusServiceUnavailable:
			refused++
			if res.Header.Get("Retry-After") != "1" {
				t.Errorf("a refused request has Retry-After %q, want 1", res.Header.Get("Retry-After"))
			}
			if code := errorCode(t, res); code != "overloaded" {
				t.Errorf("a refused request has code %q, want overloaded", code)
			}
		default:
			t.Errorf("status %d %s", res.Status, res.Body)
		}
	}
	t.Logf("%d senders, limit %d: %d served, %d refused", senders, limit, served, refused)
	if served < limit || refused == 0 || served+refused != senders {
		t.Fatalf("%d served and %d refused of %d with a limit of %d", served, refused, senders, limit)
	}
	if shed := harness.MetricValue(r.Metrics(c), "licetum_shed_total"); int(shed) != refused {
		t.Errorf("licetum_shed_total is %v, want %d", shed, refused)
	}
	if n := r.App.Inflight(); n != 0 {
		t.Errorf("%d requests still counted in flight", n)
	}
}

// F5-8 (deadline): a request that does not finish within HTTP__REQUEST_TIMEOUT is answered 503 request_timeout, and does
// not hold its place until the work it waits for ends.
func TestF5_8_ARequestIsAnsweredWhenItsDeadlinePasses(t *testing.T) {
	c := startReplicas(t, 1)
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	alice := c.UserToken("alice")
	if res := r.Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("warm request: %d %s", res.Status, res.Body)
	}

	r.App.Cfg.HTTP.RequestTimeout = 400 * time.Millisecond
	c.Source.SetDelay(3 * time.Second)
	start := time.Now()
	res := r.Fresh(c, p.Slug, alice)
	took := time.Since(start)
	c.Source.SetDelay(0)

	if res.Status != http.StatusServiceUnavailable || errorCode(t, res) != "request_timeout" {
		t.Fatalf("a request past its deadline: %d %s, want 503 request_timeout", res.Status, res.Body)
	}
	if took > 2*time.Second {
		t.Fatalf("the request was answered after %v, the deadline is 400 ms", took)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Error("a request that timed out carries no Retry-After")
	}
}

// F5-9: a pool never holds more connections than DB__POOL_SIZE + DB__MAX_OVERFLOW, however many requests wait for one, and
// the metrics say how many did wait.
func TestF5_9_APoolNeverExceedsItsSizeAndTheMetricsSayWhenItIsTooSmall(t *testing.T) {
	const poolSize, overflow = 2, 1
	c := startReplicas(t, 3, harness.WithConfig(func(cfg *config.Config) {
		cfg.DB.PoolSize, cfg.DB.MaxOverflow = poolSize, overflow
		cfg.Compute.Concurrency = 16 // more computations than connections: they wait for the pool
	}))
	p := c.SeedPolicy("org", 0)
	path := "/service/" + p.Slug + "/permission?consistency=higher"
	users := make([]string, 24)
	for i := range users {
		users[i] = c.UserToken("pool-user-" + string(rune('a'+i)))
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, r := range c.Replicas {
		for i := range users {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					r.Do(c, http.MethodGet, path, users[i], nil)
				}
			}()
		}
	}
	over := 0.0
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range c.Replicas {
			m := r.Metrics(c)
			held := harness.MetricValue(m, `licetum_db_pool_connections{pool="request",state="acquired"}`) +
				harness.MetricValue(m, `licetum_db_pool_connections{pool="request",state="idle"}`) +
				harness.MetricValue(m, `licetum_db_pool_connections{pool="request",state="constructing"}`)
			if held > poolSize+overflow {
				over = held
			}
			if max := harness.MetricValue(m, `licetum_db_pool_max_connections{pool="request"}`); max != poolSize+overflow {
				t.Errorf("licetum_db_pool_max_connections is %v, want %d", max, poolSize+overflow)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if over > 0 {
		t.Fatalf("a pool held %v connections, the limit is %d", over, poolSize+overflow)
	}

	waited := 0.0
	for _, r := range c.Replicas {
		waited += harness.MetricValue(r.Metrics(c), `licetum_db_pool_empty_acquire_total{pool="request"}`)
	}
	if waited == 0 {
		t.Fatal("no acquire waited for a connection of a pool of 3 under 24 concurrent users: the metric does not move")
	}
}

// F5-6: what a replica keeps answering from memory while PostgreSQL is gone ends at the next moment a rule starts or stops
// being in force: it does not serve the allowance of a rule that has expired, nor the absence of a rule that has started,
// nor the allowance of a person against whom a denial has started. The replica cannot compute the new answer, so it refuses.
func TestF5_6_AnAnswerKeptWithoutPostgreSQLEndsAtTheNextValidityBoundary(t *testing.T) {
	c := startReplicas(t, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org", 0)
	r := c.Replicas[0]
	path := "/service/" + p.Slug + "/permission"
	r.Warm(c, http.MethodGet, path, c.UserToken("alice")) // the types are synchronized

	// The rules change at a whole second (the format of the times is RFC 3339 to the second), far enough ahead for the
	// answers to be asked for and PostgreSQL to be cut.
	boundary := time.Now().Add(10 * time.Second).UTC().Truncate(time.Second)
	at := boundary.Format(time.RFC3339)
	rule := func(subject string, extra map[string]any) {
		body := map[string]any{"type_resource_id": p.Branch, "access": []string{"read"}, "subject": subject}
		for k, v := range extra {
			body[k] = v
		}
		c.Must(http.MethodPost, "/assignment_rule", body)
	}
	rule("bob", map[string]any{"expires_at": at})                    // an allowance that ends
	rule("carol", map[string]any{"valid_from": at})                  // an allowance that starts
	rule("dave", nil)                                                // an allowance ...
	rule("dave", map[string]any{"effect": "deny", "valid_from": at}) // ... that a denial overrides from then on

	people := []string{"bob", "carol", "dave"}
	before := map[string]string{}
	for _, name := range people {
		res := r.Permission(c, p.Slug, c.UserToken(name))
		if res.Status != http.StatusOK {
			t.Fatalf("%s before the boundary: %d %s", name, res.Status, res.Body)
		}
		before[name] = string(res.Body)
	}
	if before["bob"] != before["dave"] || before["bob"] == before["carol"] {
		t.Fatalf("before the boundary bob and dave can read and carol cannot:\nbob   %s\ncarol %s\ndave  %s", before["bob"], before["carol"], before["dave"])
	}
	if time.Until(boundary) < 5*time.Second {
		t.Fatalf("only %v to the boundary: the setup was too slow for the test", time.Until(boundary))
	}

	c.PostgresFault.Cut()
	type sample struct {
		at     time.Duration // from the boundary
		status int
		body   string
	}
	timeline := map[string][]sample{}
	tokens := map[string]string{}
	for _, name := range people {
		tokens[name] = c.UserToken(name)
	}
	for time.Now().Before(boundary.Add(3 * time.Second)) {
		for _, name := range people {
			start := time.Now()
			status, body, err := r.TryGet(c, path, tokens[name], 3*time.Second)
			if err != nil {
				t.Fatalf("%s at %v: %v", name, start.Sub(boundary), err)
			}
			timeline[name] = append(timeline[name], sample{at: start.Sub(boundary), status: status, body: string(body)})
		}
		time.Sleep(40 * time.Millisecond)
	}

	const margin = 1500 * time.Millisecond // the precision of a cached answer: a second at least, and the computation
	for _, name := range people {
		answered, refused := 0, 0
		for _, s := range timeline[name] {
			switch {
			case s.at < -time.Second:
				// Before the boundary the replica answers from its memory: PostgreSQL being gone is no reason to refuse.
				if s.status != http.StatusOK || s.body != before[name] {
					t.Fatalf("%s at %v before the boundary: %d %s, want the answer it had", name, s.at, s.status, s.body)
				}
				answered++
			case s.at > margin:
				// After it the answer it had is wrong, and there is no other to give: the replica refuses.
				if s.status != http.StatusServiceUnavailable {
					t.Fatalf("%s at %v after the boundary: %d %s, want 503 (the answer it had is not true any more)", name, s.at, s.status, s.body)
				}
				refused++
			}
		}
		if answered < 5 || refused < 5 {
			t.Fatalf("%s: %d answers before and %d refusals after the boundary: the timeline is too thin", name, answered, refused)
		}
	}

	// PostgreSQL is back: the answers are those of the rules as they are now, the reverse of the ones before.
	c.PostgresFault.Reset()
	want := map[string]string{"bob": before["carol"], "carol": before["bob"], "dave": before["carol"]}
	for _, name := range people {
		eventually(t, 20*time.Second, name+" is answered again, as the rules stand now", func() bool {
			status, body, err := r.TryGet(c, path, tokens[name], 3*time.Second)
			return err == nil && status == http.StatusOK && string(body) == want[name]
		})
	}
}
