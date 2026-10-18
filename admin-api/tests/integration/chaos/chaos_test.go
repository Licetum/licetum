//go:build chaos

package chaos_test

// Tier C: fault injection. Toxiproxy sits in front of PostgreSQL and Redis; each test proves that a mode takes effect
// (F0-4): a request through the proxy slows down, fails or hangs as configured, and recovers when the fault ends.
// Run with `go test -tags chaos ./tests/integration/chaos/`.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	support "github.com/licetum/licetum/core/testsupport"
)

var infra *support.Infrastructure

func TestMain(m *testing.M) {
	i, err := support.Start(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "integration infrastructure: %v\n", err)
		os.Exit(1)
	}
	infra = i
	code := m.Run()
	if err := i.Terminate(context.Background()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "terminate integration infrastructure: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

const delay = 150 * time.Millisecond

// setup starts one replica behind the proxies with a policy, and warms its caches. A request of a warm replica needs
// neither PostgreSQL nor Redis (F1), so the tests that prove a fault takes effect ask with consistency=higher
// (harness.Replica.Fresh), which reads both.
func setup(t *testing.T) (*harness.Cluster, *harness.Replica, *harness.Policy, string) {
	t.Helper()
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	r.Warm(c, http.MethodGet, "/service/"+p.Slug+"/permission", alice)
	return c, r, p, alice
}

// stranger returns the token of a person nothing is cached for, in this process or in Redis: its request verifies the
// token and computes the answer, so it reads Redis (a request of a warm replica, or a consistency=higher one, may not).
func stranger(c *harness.Cluster, n *int) string {
	*n++
	return c.UserToken(fmt.Sprintf("stranger-%d", *n))
}

func timed(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

func TestF0_4_LatencyOnRedisSlowsTheRequest(t *testing.T) {
	c, r, p, _ := setup(t)
	var n int
	fast := timed(func() { r.Permission(c, p.Slug, stranger(c, &n)) })

	c.RedisFault.Latency(delay, 0)
	var res *harness.Result
	token := stranger(c, &n)
	slow := timed(func() { res = r.Permission(c, p.Slug, token) })
	// Every round trip waits the delay in each direction.
	min := time.Duration(res.Trace.RedisRoundTrips()) * 2 * delay
	if res.Status != http.StatusOK || res.Trace.RedisRoundTrips() == 0 || slow < min {
		t.Fatalf("with %v on Redis the request took %v (status %d), want at least %v for %d round trips; it took %v before",
			delay, slow, res.Status, min, res.Trace.RedisRoundTrips(), fast)
	}

	c.RedisFault.Reset()
	token = stranger(c, &n)
	if again := timed(func() { r.Permission(c, p.Slug, token) }); again > slow/2 {
		t.Fatalf("after the fault ended the request still took %v", again)
	}
}

func TestF0_4_LatencyOnPostgresSlowsTheRequest(t *testing.T) {
	c, r, p, alice := setup(t)
	c.PostgresFault.Latency(delay, 0)
	var res *harness.Result
	slow := timed(func() { res = r.Fresh(c, p.Slug, alice) })
	min := time.Duration(res.Trace.DBQueries()) * 2 * delay
	if res.Status != http.StatusOK || slow < min {
		t.Fatalf("with %v on PostgreSQL the request took %v (status %d), want at least %v for %d queries",
			delay, slow, res.Status, min, res.Trace.DBQueries())
	}
	c.PostgresFault.Reset()
}

func TestF0_4_CuttingPostgresFailsTheRequestAndItRecovers(t *testing.T) {
	c, r, p, alice := setup(t)
	c.PostgresFault.Cut()
	// A request that finds its answer in memory does not need PostgreSQL.
	if warm := r.Permission(c, p.Slug, alice); warm.Status != http.StatusOK {
		t.Fatalf("a warm request with PostgreSQL cut: %d %s", warm.Status, warm.Body)
	}
	var res *harness.Result
	took := timed(func() { res = r.Fresh(c, p.Slug, alice) })
	if res.Status == http.StatusOK {
		t.Fatalf("a request was answered with PostgreSQL cut: %s", res.Body)
	}
	if took > 10*time.Second {
		t.Fatalf("a cut link must fail fast, the request took %v", took)
	}
	if !strings.Contains(r.Metrics(c), `licetum_db_queries_total{operation="select",result="error"}`) {
		t.Fatal("the failed queries are not in /metrics")
	}

	c.PostgresFault.Reset()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if res = r.Permission(c, p.Slug, alice); res.Status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the replica did not recover after PostgreSQL came back: %d %s", res.Status, res.Body)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestF0_4_CuttingRedisIsSeenByTheReplica(t *testing.T) {
	c, r, p, alice := setup(t)
	c.RedisFault.Cut()
	// Redis is a cache for the reads: the answer is computed from the data, and every Redis call fails. With a token verified
	// in memory and consistency=higher, the only Redis call is the write of the answer, in the background after the response.
	r.Fresh(c, p.Slug, alice)
	deadline := time.Now().Add(10 * time.Second)
	for !redisErrors(r.Metrics(c)) {
		if time.Now().After(deadline) {
			t.Fatal("no Redis error was counted with Redis cut")
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.RedisFault.Reset()
	deadline = time.Now().Add(15 * time.Second)
	for {
		res := r.Permission(c, p.Slug, alice)
		if res.Status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery after Redis came back: %d %s", res.Status, res.Body)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func redisErrors(metrics string) bool {
	for _, line := range strings.Split(metrics, "\n") {
		if strings.HasPrefix(line, "licetum_redis_ops_total{") && strings.Contains(line, `result="error"`) {
			return true
		}
	}
	return false
}

func TestF0_4_BlackHoleOnRedisMakesTheRequestHang(t *testing.T) {
	c, r, p, alice := setup(t)
	var n int
	c.RedisFault.BlackHole()
	_, _, err := r.TryGet(c, "/service/"+p.Slug+"/permission", stranger(c, &n), time.Second)
	if err == nil {
		t.Fatal("the request was answered although Redis swallows the traffic")
	}
	c.RedisFault.Reset()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, _, err := r.TryPermission(c, p.Slug, alice, 5*time.Second)
		if err == nil && status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery after the black hole ended: status %d, %v", status, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestF0_4_BlackHoleOnPostgresMakesTheRequestHang(t *testing.T) {
	c, r, p, alice := setup(t)
	c.PostgresFault.BlackHole()
	_, _, err := r.TryGet(c, "/service/"+p.Slug+"/permission?consistency=higher", alice, time.Second)
	if err == nil {
		t.Fatal("the request was answered although PostgreSQL swallows the traffic")
	}
	c.PostgresFault.Reset()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, _, err := r.TryPermission(c, p.Slug, alice, 5*time.Second)
		if err == nil && status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery after the black hole ended: status %d, %v", status, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// READONLY: a Redis that is a replica refuses writes. REPLICAOF to an address nobody listens on makes an instance a
// replica of nothing: it keeps serving reads and answers every write with a READONLY error.
func TestF0_4_ReadOnlyRedisFailsTheWrites(t *testing.T) {
	ctx := context.Background()
	container, err := rediscontainer.Run(ctx, "redis:7-alpine",
		testcontainers.WithWaitStrategy(wait.ForListeningPort("6379/tcp").WithStartupTimeout(30*time.Second)))
	if err != nil {
		t.Fatalf("start a Redis: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}

	c := harness.StartReplicas(t, infra, 1, harness.WithReplicaRedis(uri))
	p := c.SeedPolicy("org", 0)
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	if res := r.Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("with a writable Redis: %d %s", res.Status, res.Body)
	}

	code, out, err := container.Exec(ctx, []string{"redis-cli", "REPLICAOF", "127.0.0.1", "1"})
	if err != nil || code != 0 {
		t.Fatalf("REPLICAOF: %d %v %v", code, out, err)
	}
	if err := r.Redis.Set(ctx, "probe", "x", 0).Err(); err == nil || !strings.Contains(err.Error(), "READONLY") {
		t.Fatalf("a write to the replica should fail with READONLY: %v", err)
	}
	// The answer is still given (Redis is a cache), and the write of the cache entry fails in the background.
	if res := r.Fresh(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("a read-only Redis must not fail the request: %d %s", res.Status, res.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !failedWrite(r.Metrics(c)) {
		if time.Now().After(deadline) {
			t.Fatalf("no failed write in /metrics:\n%s", r.Metrics(c))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func failedWrite(metrics string) bool {
	for _, line := range strings.Split(metrics, "\n") {
		if strings.HasPrefix(line, "licetum_redis_ops_total{") && strings.Contains(line, `result="error"`) &&
			(strings.Contains(line, `operation="set"`) || strings.Contains(line, `operation="pipeline"`)) {
			return true
		}
	}
	return false
}
