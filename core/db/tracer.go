package db

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Observer is told about every statement a pool runs: what kind it is (see Operation), how long it took and
// whether it failed. The engine implements it to count queries per request and to fill the Prometheus metrics.
type Observer interface {
	ObserveQuery(ctx context.Context, operation string, took time.Duration, err error)
}

// Tracer is the pgx tracer every pool made by Connect carries. It always counts statements (for the Observer, once
// one is attached with Observe); with DB__ECHO it also logs each SQL text, as SQLAlchemy echo=True does.
type Tracer struct {
	echo     bool
	observer atomic.Pointer[observerBox]
}

type observerBox struct{ o Observer }

// Observe attaches o to the tracer of pool and returns whether the pool has one (a pool that was not made by Connect
// does not, and then nothing is counted). One observer per pool: a later call replaces the earlier one.
func Observe(pool *pgxpool.Pool, o Observer) bool {
	if pool == nil {
		return false
	}
	// Config returns a copy of the configuration, but the tracer in it is the one the pool's connections use.
	t, ok := pool.Config().ConnConfig.Tracer.(*Tracer)
	if !ok {
		return false
	}
	t.SetObserver(o)
	return true
}

// SetObserver attaches o directly to the tracer (Observe does it for a pool).
func (t *Tracer) SetObserver(o Observer) { t.observer.Store(&observerBox{o: o}) }

// Operation names the kind of a statement: its first SQL keyword in lower case, after any leading whitespace and
// comments ("select", "insert", "update", "delete", "with", "begin", "commit", ...). "other" when there is none.
func Operation(sql string) string {
	s := sql
	for {
		s = strings.TrimLeft(s, " \t\r\n;(")
		switch {
		case strings.HasPrefix(s, "--"):
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return "other"
			}
			s = s[i+1:]
			continue
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s, "*/")
			if i < 0 {
				return "other"
			}
			s = s[i+2:]
			continue
		}
		break
	}
	end := 0
	for end < len(s) && (s[end] >= 'a' && s[end] <= 'z' || s[end] >= 'A' && s[end] <= 'Z') {
		end++
	}
	if end == 0 {
		return "other"
	}
	return strings.ToLower(s[:end])
}

type uncountedKey struct{}

// Uncounted returns ctx for a statement that is not part of the work of a request and must not be counted: the reading of
// a gauge at a scrape of /metrics (the scrape itself is not counted).
func Uncounted(ctx context.Context) context.Context {
	return context.WithValue(ctx, uncountedKey{}, true)
}

type queryStartKey struct{}

type queryStart struct {
	at        time.Time
	operation string
}

func (t *Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.echo {
		slog.DebugContext(ctx, "sql", "query", data.SQL, "args", data.Args)
	}
	if t.observer.Load() == nil || ctx.Value(uncountedKey{}) != nil {
		return ctx // nobody counts this pool, or the statement is not to be counted: no context value, no clock, no parsing
	}
	return context.WithValue(ctx, queryStartKey{}, &queryStart{at: time.Now(), operation: Operation(data.SQL)})
}

func (t *Tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	box := t.observer.Load()
	start, _ := ctx.Value(queryStartKey{}).(*queryStart)
	if box == nil || start == nil {
		return
	}
	box.o.ObserveQuery(ctx, start.operation, time.Since(start.at), data.Err)
}

// A batch is one round trip, but every statement in it is a query for the count; the time of a statement is the
// time since the previous one finished.
type batchKey struct{}

type batchState struct{ last time.Time }

func (t *Tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	if t.observer.Load() == nil {
		return ctx
	}
	return context.WithValue(ctx, batchKey{}, &batchState{last: time.Now()})
}

func (t *Tracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	if t.echo {
		slog.DebugContext(ctx, "sql", "query", data.SQL, "args", data.Args)
	}
	box := t.observer.Load()
	state, _ := ctx.Value(batchKey{}).(*batchState)
	if box == nil || state == nil {
		return
	}
	now := time.Now()
	box.o.ObserveQuery(ctx, Operation(data.SQL), now.Sub(state.last), data.Err)
	state.last = now
}

func (t *Tracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func (t *Tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	if t.observer.Load() == nil {
		return ctx
	}
	return context.WithValue(ctx, queryStartKey{}, &queryStart{at: time.Now(), operation: "copy"})
}

func (t *Tracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	box := t.observer.Load()
	start, _ := ctx.Value(queryStartKey{}).(*queryStart)
	if box == nil || start == nil {
		return
	}
	box.o.ObserveQuery(ctx, start.operation, time.Since(start.at), data.Err)
}
