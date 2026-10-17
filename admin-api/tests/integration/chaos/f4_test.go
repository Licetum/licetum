//go:build chaos

package chaos_test

// Tier C of F4 (issue #36): the audit sink with PostgreSQL slow or gone. The engine is built here on a pool that reaches
// PostgreSQL through the fault proxy; a delegated call needs no token or identity provider for what is measured, the
// record is the sink's business (the cases with a real process and with an unreachable context are in the api tier).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
)

// auditApp is an engine on the proxied PostgreSQL in the given audit mode.
func auditApp(t *testing.T, c *harness.Cluster, mode string) *engine.App {
	t.Helper()
	cfg := *c.Cfg
	cfg.DB.URI = c.PostgresFault.Rewrite(c.Cfg.DB.URI)
	cfg.Delegation.AuditMode = mode
	cfg.Delegation.AuditBuffer = 100_000
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	rdb, err := redisx.Connect(ctx, c.Infra.RedisURI)
	if err != nil {
		t.Fatal(err)
	}
	app := engine.New(pool, rdb, &cfg)
	t.Cleanup(app.Close)
	return app
}

func recordCall(app *engine.App, org uuid.UUID, ctx context.Context) error {
	return app.RecordDelegatedCall(ctx, org, engine.DelegatedCall{CallerClient: "docs", Subject: "u", Operation: "check", Questions: 1, Allowed: 1})
}

func storedCalls(t *testing.T, c *harness.Cluster) int {
	t.Helper()
	var n int
	if err := c.Pool.QueryRow(context.Background(), `SELECT count(*) FROM delegated_call`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func scrapeApp(app *engine.App) string {
	rec := httptest.NewRecorder()
	app.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func p99(d []time.Duration) time.Duration {
	slices.Sort(d)
	return d[len(d)*99/100]
}

// F4-1: with 500 ms added to PostgreSQL the audit does not slow a delegated call in async mode, and strict mode is unchanged
// (it waits for the write, so it pays the latency).
func TestF4_1_AsyncAuditDoesNotPayThePostgreSQLLatency(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org", 0)
	org := uuid.MustParse(p.ServiceID)
	async, strict := auditApp(t, c, "async"), auditApp(t, c, "strict")
	// Connections are made before the latency is added: what is measured is the write, not the handshake.
	if err := recordCall(strict, org, t.Context()); err != nil {
		t.Fatal(err)
	}
	base := make([]time.Duration, 200)
	for i := range base {
		base[i] = timed(func() { _ = recordCall(async, org, t.Context()) })
	}

	c.PostgresFault.Latency(500*time.Millisecond, 0)
	slow := make([]time.Duration, 200)
	for i := range slow {
		slow[i] = timed(func() {
			if err := recordCall(async, org, t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	strictTook := timed(func() {
		if err := recordCall(strict, org, t.Context()); err != nil {
			t.Error(err)
		}
	})
	t.Logf("async p99 %v without latency, %v with 500 ms; one strict call took %v", p99(base), p99(slow), strictTook)
	if got, limit := p99(slow), p99(base)+5*time.Millisecond; got > limit {
		t.Errorf("async p99 with 500 ms on PostgreSQL is %v, want at most %v (the p99 without it plus 5 ms)", got, limit)
	}
	if strictTook < 500*time.Millisecond {
		t.Errorf("a strict call took %v with 500 ms on PostgreSQL: it did not wait for the write", strictTook)
	}
	c.PostgresFault.Reset()
}

// F4-2 (strict): PostgreSQL gone before the insert: the call fails, no answer is given and nothing is stored.
func TestF4_2_StrictAuditFailsWhilePostgreSQLIsGone(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org", 0)
	org := uuid.MustParse(p.ServiceID)
	app := auditApp(t, c, "strict")
	if err := recordCall(app, org, t.Context()); err != nil {
		t.Fatal(err)
	}
	before := storedCalls(t, c)

	c.PostgresFault.Cut()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := recordCall(app, org, ctx); err == nil {
		t.Fatal("strict mode answered a call whose record could not be written")
	}
	c.PostgresFault.Reset()
	if got := storedCalls(t, c); got != before {
		t.Fatalf("%d records stored, want %d: a refused call left a record", got, before)
	}
}

// F4-2 (async): PostgreSQL gone for a while: every answered call is stored or counted as dropped once it is back.
func TestF4_2_AsyncAuditStoredPlusDroppedEqualsAnswered(t *testing.T) {
	c := harness.StartReplicas(t, infra, 1, harness.WithFaultProxies())
	p := c.SeedPolicy("org", 0)
	org := uuid.MustParse(p.ServiceID)
	app := auditApp(t, c, "async")
	before := storedCalls(t, c)

	const calls = 3000
	c.PostgresFault.Cut()
	for i := 0; i < calls; i++ {
		if err := recordCall(app, org, t.Context()); err != nil {
			t.Fatal(err)
		}
		if i == calls/2 {
			time.Sleep(time.Second) // a flush window passes with PostgreSQL gone
		}
	}
	c.PostgresFault.Reset()
	app.Close() // flushes what is left

	stored := storedCalls(t, c) - before
	dropped := int(harness.MetricValue(scrapeApp(app), "licetum_audit_dropped_total "))
	t.Logf("answered %d, stored %d, dropped %d", calls, stored, dropped)
	if stored+dropped != calls {
		t.Fatalf("stored %d + dropped %d != answered %d", stored, dropped, calls)
	}
}
