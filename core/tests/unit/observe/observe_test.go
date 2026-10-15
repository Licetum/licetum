package observe_test

// Unit tests of the counting hooks (F0-2): the pgx tracer, the go-redis hook and what the engine makes of them (the
// Trace of a request and the Prometheus counters). No database is needed: Redis is a small RESP server in the test.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

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

// fakeRedis answers every command with +OK, except GET, which is a nil bulk string, and FAIL, which is an error.
// Commands arriving together are answered together, as a real server does.
func fakeRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()
	return ln.Addr().String()
}

func serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.HasPrefix(line, "*") {
			continue
		}
		n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
		var args []string
		for i := 0; i < n; i++ {
			sizeLine, err := r.ReadString('\n')
			if err != nil {
				return
			}
			size, _ := strconv.Atoi(strings.TrimSpace(sizeLine[1:]))
			buf := make([]byte, size+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			args = append(args, string(buf[:size]))
		}
		switch strings.ToLower(args[0]) {
		case "incr":
			_, _ = conn.Write([]byte(":1\r\n"))
		case "get":
			_, _ = conn.Write([]byte("$-1\r\n"))
		case "fail":
			_, _ = conn.Write([]byte("-ERR failed\r\n"))
		case "hello":
			_, _ = conn.Write([]byte("-ERR unknown command\r\n"))
		default:
			_, _ = conn.Write([]byte("+OK\r\n"))
		}
	}
}

func newApp(t *testing.T) (*engine.App, *redis.Client) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: fakeRedis(t), Protocol: 2, DisableIdentity: true})
	t.Cleanup(func() { _ = rdb.Close() })
	app := engine.New(nil, rdb, &config.Config{})
	return app, rdb
}

func scrape(t *testing.T, app *engine.App) string {
	t.Helper()
	rec := httptest.NewRecorder()
	app.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func TestRedisCommandsAndPipelinesAreRoundTripsOfATrace(t *testing.T) {
	app, rdb := newApp(t)
	ctx, trace := engine.WithTrace(context.Background())

	if err := rdb.Get(ctx, "k").Err(); !errors.Is(err, redis.Nil) {
		t.Fatalf("get: %v", err)
	}
	if err := rdb.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	pipe := rdb.Pipeline()
	pipe.Set(ctx, "a", 1, 0)
	pipe.Set(ctx, "b", 2, 0)
	pipe.Incr(ctx, "c")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	if got := trace.RedisRoundTrips(); got != 3 {
		t.Fatalf("round trips = %d (%v), want 3: a pipeline is one", got, trace.RedisOps())
	}
	ops := trace.RedisOps()
	if ops["get"] != 1 || ops["set"] != 1 || ops["pipeline"] != 1 {
		t.Fatalf("ops = %v", ops)
	}

	// The same counts are in /metrics, with the size of the pipeline as a label; a missing key is not an error.
	body := scrape(t, app)
	for _, line := range []string{
		`licetum_redis_ops_total{commands="1",operation="get",result="ok"} 1`,
		`licetum_redis_ops_total{commands="1",operation="set",result="ok"} 1`,
		`licetum_redis_ops_total{commands="3",operation="pipeline",result="ok"} 1`,
		`licetum_redis_op_seconds_count{operation="pipeline"} 1`,
	} {
		if !strings.Contains(body, line) {
			t.Errorf("/metrics lacks %q", line)
		}
	}
}

func TestRedisFailureIsCounted(t *testing.T) {
	app, rdb := newApp(t)
	if err := rdb.Do(context.Background(), "fail").Err(); err == nil {
		t.Fatal("the fake server should have failed")
	}
	if body := scrape(t, app); !strings.Contains(body, `licetum_redis_ops_total{commands="1",operation="fail",result="error"} 1`) {
		t.Errorf("a failed command is not counted as an error:\n%s", body)
	}
}

func TestRedisWithoutTraceIsOnlyInMetrics(t *testing.T) {
	app, rdb := newApp(t)
	if err := rdb.Set(context.Background(), "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if body := scrape(t, app); !strings.Contains(body, `licetum_redis_ops_total{commands="1",operation="set",result="ok"} 1`) {
		t.Errorf("a command outside a request is not in the metrics:\n%s", body)
	}
}

func TestEngineObservesOneClientOnce(t *testing.T) {
	// A second App on the same client replaces the observer instead of adding a second hook: a command is counted once.
	_, rdb := newApp(t)
	engine.New(nil, rdb, &config.Config{})
	_ = rdb
	ctx, trace := engine.WithTrace(context.Background())
	if err := rdb.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got := trace.RedisRoundTrips(); got != 1 {
		t.Fatalf("a command was counted %d times: %v", got, trace.RedisOps())
	}
}

func TestWithTraceKeepsTheTraceOfTheContext(t *testing.T) {
	ctx, first := engine.WithTrace(context.Background())
	again, second := engine.WithTrace(ctx)
	if first != second || again != ctx {
		t.Fatal("WithTrace replaced the Trace the context carries")
	}
}

func TestConnectionSetupIsTrafficButNotTheCostOfARequest(t *testing.T) {
	// A client on database 2 sends SELECT (inside a pipeline of its own) when it opens a connection, with the context of the
	// request that needed the connection. That is in the metrics as connection_setup and not in the Trace.
	rdb := redis.NewClient(&redis.Options{Addr: fakeRedis(t), Protocol: 2, DisableIdentity: true, DB: 2, ClientName: "licetum-test"})
	t.Cleanup(func() { _ = rdb.Close() })
	app := engine.New(nil, rdb, &config.Config{})
	ctx, trace := engine.WithTrace(context.Background())
	if err := rdb.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got := trace.RedisRoundTrips(); got != 1 || trace.RedisOps()["set"] != 1 {
		t.Fatalf("the Trace of a request that opened a connection: %d %v, want only the SET", got, trace.RedisOps())
	}
	if body := scrape(t, app); !strings.Contains(body, `operation="connection_setup"`) {
		t.Errorf("the setup traffic is not in /metrics:\n%s", body)
	}
}
