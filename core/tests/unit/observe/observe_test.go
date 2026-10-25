package observe_test

// Unit tests of the counting hooks (F0-2): the pgx tracer and what the engine makes of it (the Trace of a request and
// the Prometheus counters). No database is needed.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
)

type queryEvent struct {
	operation string
	err       error
}

type recorder struct {
	mu      sync.Mutex
	queries []queryEvent
}

func (r *recorder) ObserveQuery(_ context.Context, operation string, _ time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, queryEvent{operation, err})
}

func TestOperationIsTheFirstSQLKeyword(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                         "select",
		"  \n\tselect id FROM t":           "select",
		"insert into t values ($1)":        "insert",
		"UPDATE t SET a = 1":               "update",
		"-- a comment\nDELETE FROM t":      "delete",
		"/* hint */ WITH x AS (SELECT 1) ": "with",
		"begin":                            "begin",
		"COMMIT":                           "commit",
		"(SELECT 1) UNION (SELECT 2)":      "select",
		"":                                 "other",
		"-- only a comment":                "other",
		"/* unterminated":                  "other",
		"123":                              "other",
	}
	for sql, want := range cases {
		if got := db.Operation(sql); got != want {
			t.Errorf("Operation(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestTracerReportsQueriesBatchesAndCopies(t *testing.T) {
	tracer := &db.Tracer{}
	rec := &recorder{}
	// The tracer is attached through a pool in production; its methods are what pgx calls.
	attach(t, tracer, rec)

	ctx := context.Background()
	qctx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tracer.TraceQueryEnd(qctx, nil, pgx.TraceQueryEndData{})
	qctx = tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "insert into t values (1)"})
	boom := errors.New("boom")
	tracer.TraceQueryEnd(qctx, nil, pgx.TraceQueryEndData{Err: boom})

	bctx := tracer.TraceBatchStart(ctx, nil, pgx.TraceBatchStartData{})
	tracer.TraceBatchQuery(bctx, nil, pgx.TraceBatchQueryData{SQL: "update t set a = 1"})
	tracer.TraceBatchQuery(bctx, nil, pgx.TraceBatchQueryData{SQL: "delete from t"})
	tracer.TraceBatchEnd(bctx, nil, pgx.TraceBatchEndData{})

	cctx := tracer.TraceCopyFromStart(ctx, nil, pgx.TraceCopyFromStartData{})
	tracer.TraceCopyFromEnd(cctx, nil, pgx.TraceCopyFromEndData{})

	want := []queryEvent{{"select", nil}, {"insert", boom}, {"update", nil}, {"delete", nil}, {"copy", nil}}
	if len(rec.queries) != len(want) {
		t.Fatalf("observed %+v, want %+v", rec.queries, want)
	}
	for i, w := range want {
		if rec.queries[i] != w {
			t.Errorf("event %d = %+v, want %+v", i, rec.queries[i], w)
		}
	}
}

// attach hands the observer to the tracer the way db.Observe does for a pool.
func attach(t *testing.T, tracer *db.Tracer, o db.Observer) {
	t.Helper()
	tracer.SetObserver(o)
}

func TestTracerWithoutObserverCountsNothing(t *testing.T) {
	tracer := &db.Tracer{}
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{}) // must not panic
}

func scrape(t *testing.T, app *engine.App) string {
	t.Helper()
	rec := httptest.NewRecorder()
	app.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func TestMetricsCountTheDatabaseAndNothingElse(t *testing.T) {
	// The process depends on PostgreSQL alone: it reports no series of a cache server, and licetum_degraded has one
	// component.
	body := scrape(t, engine.New(nil, &config.Config{}))
	if strings.Contains(body, "licetum_redis") || strings.Contains(body, `component="redis"`) {
		t.Errorf("/metrics still names Redis:\n%s", body)
	}
	if !strings.Contains(body, `licetum_degraded{component="postgres"} 0`) {
		t.Errorf("/metrics lacks licetum_degraded for postgres:\n%s", body)
	}
}

func TestWithTraceKeepsTheTraceOfTheContext(t *testing.T) {
	ctx, first := engine.WithTrace(context.Background())
	again, second := engine.WithTrace(ctx)
	if first != second || again != ctx {
		t.Fatal("WithTrace replaced the Trace the context carries")
	}
}
