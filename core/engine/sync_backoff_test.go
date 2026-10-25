//go:build integration

package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

// backoffType is a database with the schema and one type of one service: what a row of sync_state hangs on.
func backoffType(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	infra, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infra.Terminate(context.Background()) })
	pool, err := db.Connect(ctx, config.Database{URI: infra.DBURI, PoolSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateUpTo(ctx, pool, db.LatestMigration()); err != nil {
		t.Fatal(err)
	}
	service, typ := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO service (id, name, url, icon, slug) VALUES ($1, 'backoff', '', '', 'backoff')`, []any{service}},
		{`INSERT INTO type_resource (id, service_id, name) VALUES ($1, $2, 'branch')`, []any{typ, service}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return pool, typ
}

// inTx runs fn in a transaction of its own and commits it.
func inTx(t *testing.T, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// delay is how long from now the type is held back, in seconds.
func delay(t *testing.T, pool *pgxpool.Pool, typ uuid.UUID) (secs float64, attempts int) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT EXTRACT(EPOCH FROM (next_due_at - now()))::float8, attempts FROM sync_state WHERE type_resource_id = $1`, typ).Scan(&secs, &attempts)
	if err != nil {
		t.Fatal(err)
	}
	return secs, attempts
}

// The backoff of a source is one row for the whole cluster: every failure that reaches it is counted, whatever the
// number of workers failing at once. (It replaces the failure counter of the Redis breaker, F5-12.)
func TestSyncBackoffCountsEveryConcurrentFailure(t *testing.T) {
	pool, typ := backoffType(t)
	const failures = 24
	var wg sync.WaitGroup
	for range failures {
		wg.Go(func() {
			inTx(t, pool, func(ctx context.Context, tx pgx.Tx) error { return markUnanswered(ctx, tx, typ, "boom") })
		})
	}
	wg.Wait()

	secs, attempts := delay(t, pool, typ)
	if attempts != failures {
		t.Fatalf("attempts = %d after %d concurrent failures, want all of them counted", attempts, failures)
	}
	if secs <= 0 || secs > 600 {
		t.Fatalf("the type is held back for %v s, want a positive time of at most 600", secs)
	}
	states, err := loadSyncStates(context.Background(), pool, []uuid.UUID{typ})
	if err != nil {
		t.Fatal(err)
	}
	if st := states[typ]; !st.Backoff || st.LastError != "boom" {
		t.Fatalf("state = %+v, want a backoff with the reason", st)
	}
}

// The backoff of a type that fails again doubles, from 30 s to the cap of 10 minutes, and next_due_at is set by every
// failure, the first one included: there is no failure that leaves the source unguarded. (F5-12: the failure key always
// has a TTL, a counter without an expiry is repaired.)
func TestSyncBackoffDoublesToTheCapAndIsAlwaysSet(t *testing.T) {
	pool, typ := backoffType(t)
	want := []float64{30, 60, 120, 240, 480, 600, 600, 600}
	for i, w := range want {
		inTx(t, pool, func(ctx context.Context, tx pgx.Tx) error { return markUnanswered(ctx, tx, typ, "boom") })
		secs, attempts := delay(t, pool, typ)
		if attempts != i+1 {
			t.Fatalf("failure %d: attempts = %d", i+1, attempts)
		}
		if secs > w || secs < w-5 {
			t.Fatalf("failure %d: held back for %.1f s, want %v s", i+1, secs, w)
		}
	}
}

// A synchronization that succeeds ends the backoff: the attempts go back to zero and the type is due by the throttle.
// (F5-12: the breaker closes on success.)
func TestSyncBackoffEndsWithASuccess(t *testing.T) {
	pool, typ := backoffType(t)
	for range 3 {
		inTx(t, pool, func(ctx context.Context, tx pgx.Tx) error { return markUnanswered(ctx, tx, typ, "boom") })
	}
	if _, attempts := delay(t, pool, typ); attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	inTx(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return markApplied(ctx, tx, typ, "data", "graph", time.Now().Add(-time.Second))
	})
	secs, attempts := delay(t, pool, typ)
	if attempts != 0 {
		t.Fatalf("attempts = %d after a success, want 0", attempts)
	}
	if secs > syncTTL.Seconds() {
		t.Fatalf("held back for %.1f s after a success, want the throttle of %v", secs, syncTTL)
	}
	states, err := loadSyncStates(context.Background(), pool, []uuid.UUID{typ})
	if err != nil {
		t.Fatal(err)
	}
	if st := states[typ]; st.Backoff || st.LastError != "" {
		t.Fatalf("state = %+v, want no backoff and no error", st)
	}
}

// A fresh job asks a source that fails, like one that answers, at most once per SYNC__FRESH_MIN_INTERVAL: the backoff does
// not stop it (somebody needs the state of the source as of now), the spacing does. (It replaces the Redis breaker as the
// only thing between a stream of consistency=higher requests and a failing source.)
func TestSyncFreshRequestsAskAFailingSourceOncePerInterval(t *testing.T) {
	pool, typ := backoffType(t)
	const interval = 700 * time.Millisecond
	a := &App{DB: pool, Cfg: &config.Config{Sync: config.Sync{FreshMinInterval: interval}}}
	due := []model.Synchronizer{{TypeResourceID: typ}}

	// A source that has not been asked: no wait.
	if took := timed(t, func() error { return a.spaceOutFresh(context.Background(), due) }); took > interval/2 {
		t.Fatalf("a fresh job for a type never asked waited %v", took)
	}
	// It was asked and did not answer just now: the next fresh job waits out the rest of the interval.
	inTx(t, pool, func(ctx context.Context, tx pgx.Tx) error { return markUnanswered(ctx, tx, typ, "boom") })
	if took := timed(t, func() error { return a.spaceOutFresh(context.Background(), due) }); took < interval/2 || took > interval+time.Second {
		t.Fatalf("a fresh job right after a failure waited %v, want about %v", took, interval)
	}
	// Asked long ago: no wait, the backoff notwithstanding.
	if _, err := pool.Exec(context.Background(), `UPDATE sync_state SET fetched_at = now() - interval '1 hour' WHERE type_resource_id = $1`, typ); err != nil {
		t.Fatal(err)
	}
	if took := timed(t, func() error { return a.spaceOutFresh(context.Background(), due) }); took > interval/2 {
		t.Fatalf("a fresh job for a source asked an hour ago waited %v", took)
	}
}

func timed(t *testing.T, fn func() error) time.Duration {
	t.Helper()
	start := time.Now()
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	return time.Since(start)
}
