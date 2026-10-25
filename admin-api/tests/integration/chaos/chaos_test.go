//go:build chaos

package chaos_test

// Tier C: fault injection. Toxiproxy sits in front of PostgreSQL; each test proves that a mode takes effect (F0-4): a
// request through the proxy slows down, fails or hangs as configured, and recovers when the fault ends.
// Run with `go test -tags chaos ./tests/integration/chaos/`.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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
// no PostgreSQL (F1), so the tests that prove a fault takes effect ask for the freshest answer (harness.Replica.Fresh),
// which reads it.
func setup(t *testing.T) (*harness.Cluster, *harness.Replica, *harness.Policy, string) {
	t.Helper()
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org")
	alice := c.UserToken("alice")
	r := c.Replicas[0]
	r.Warm(c, harness.Branches(p.Slug, alice), alice)
	return c, r, p, alice
}

func timed(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
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

func TestF0_4_BlackHoleOnPostgresMakesTheRequestHang(t *testing.T) {
	c, r, p, alice := setup(t)
	c.PostgresFault.BlackHole()
	_, _, err := r.Try(c, harness.Branches(p.Slug, alice), alice, time.Second, harness.Higher())
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
