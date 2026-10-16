//go:build integration

package api_test

// Tests of F4: background writes off the request path (issue #36). Names carry the criterion they prove.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/logx"
)

// replica builds another engine.App on the world's database and Redis, with the given tuning applied to a copy of the config.
func (w *world) replica(tune func(*config.Config)) *engine.App {
	w.t.Helper()
	cfg := *w.cfg
	tune(&cfg)
	app := engine.New(w.pool, w.app.Redis, &cfg)
	w.t.Cleanup(app.Close)
	return app
}

func (w *world) countRows(query string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func TestF4_2_AsyncAuditLosesNothingOnGracefulShutdown(t *testing.T) {
	w := newWorld(t)
	org := uuid.MustParse(w.org)
	app := w.replica(func(c *config.Config) { c.Delegation.AuditMode = "async"; c.Delegation.AuditBuffer = 100_000 })

	const calls = 1200 // more than one statement (500 rows) and more than one flush window
	for i := 0; i < calls; i++ {
		err := app.RecordDelegatedCall(context.Background(), org, engine.DelegatedCall{
			CallerClient: "docs", Subject: fmt.Sprintf("u%d", i), Operation: "check", Questions: 1, Allowed: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	app.Close() // the buffer is flushed on Close
	if got := w.countRows(`SELECT count(*) FROM delegated_call`); got != calls {
		t.Fatalf("stored %d records, answered %d", got, calls)
	}
}

func TestF4_1_AsyncAuditDoesNotWaitForTheDatabase(t *testing.T) {
	w := newWorld(t)
	org := uuid.MustParse(w.org)
	app := w.replica(func(c *config.Config) { c.Delegation.AuditMode = "async"; c.Delegation.AuditBuffer = 100_000 })

	// Hold every insert: a record that waited for the write would not return.
	tx, err := w.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `LOCK TABLE delegated_call IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 100; i++ {
		if err := app.RecordDelegatedCall(context.Background(), org, engine.DelegatedCall{CallerClient: "docs", Subject: "u", Operation: "check"}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("100 async records took %s with the table locked", d)
	}
	_ = tx.Rollback(context.Background())
}

func TestF4_1_StrictAuditIsWrittenBeforeTheAnswer(t *testing.T) {
	w := newWorld(t)
	org := uuid.MustParse(w.org)
	if err := w.app.RecordDelegatedCall(context.Background(), org, engine.DelegatedCall{CallerClient: "docs", Subject: "u", Operation: "check"}); err != nil {
		t.Fatal(err)
	}
	if got := w.countRows(`SELECT count(*) FROM delegated_call`); got != 1 {
		t.Fatalf("strict mode wrote %d records on return, want 1", got)
	}
}

func TestF4_2_AsyncAuditCountsWhatItDrops(t *testing.T) {
	w := newWorld(t)
	org := uuid.MustParse(w.org)
	app := w.replica(func(c *config.Config) { c.Delegation.AuditMode = "async"; c.Delegation.AuditBuffer = 3 })

	tx, err := w.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `LOCK TABLE delegated_call IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = app.RecordDelegatedCall(context.Background(), org, engine.DelegatedCall{CallerClient: "docs", Subject: "u", Operation: "check"})
	}
	text := scrape(app)
	if v := harness.MetricValue(text, "licetum_audit_dropped_total "); v != 2 {
		t.Fatalf("dropped = %v, want 2\n%s", v, grepLines(text, "licetum_audit"))
	}
	if v := harness.MetricValue(text, "licetum_audit_buffer_records "); v != 3 {
		t.Fatalf("buffered = %v, want 3", v)
	}
	_ = tx.Rollback(context.Background())
	app.Close()
	if got := w.countRows(`SELECT count(*) FROM delegated_call`); got != 3 {
		t.Fatalf("stored %d, want the 3 that were buffered", got)
	}
}

func TestF4_3_KnownSubjectChangesOncePerIntervalAcrossReplicas(t *testing.T) {
	w := newWorld(t)
	// round notes the subject once on each of five fresh replicas (a process queues a subject once per interval itself).
	round := func() {
		for i := 0; i < 5; i++ {
			r := w.replica(func(*config.Config) {})
			r.NoteSubject("alice", engine.Claims{"preferred_username": "alice", "name": "Alice"})
			r.FlushSubjects(context.Background())
		}
	}
	seen := func() int {
		var n int
		if err := w.pool.QueryRow(context.Background(), `SELECT seen_count FROM known_subject WHERE subject = 'alice'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	round()
	if got := seen(); got != 1 {
		t.Fatalf("5 replicas within the interval: seen_count = %d, want 1", got)
	}
	// The interval passes: one change cluster-wide, not one per replica.
	if _, err := w.pool.Exec(context.Background(), `UPDATE known_subject SET last_seen = now() - interval '11 minutes'`); err != nil {
		t.Fatal(err)
	}
	round()
	if got := seen(); got != 2 {
		t.Fatalf("after the interval seen_count = %d, want 2", got)
	}
}

func TestF4_4_RetentionRemovesOldRecordsInChunks(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO delegated_call (at, service_id, caller_client, subject, operation)
		SELECT now() - interval '400 days', $1, 'c', 's' || g, 'check' FROM generate_series(1, 25000) g`, w.org); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO delegated_call (service_id, caller_client, subject, operation) VALUES ($1, 'c', 'fresh', 'check')`, w.org); err != nil {
		t.Fatal(err)
	}
	app := w.replica(func(c *config.Config) { c.Delegation.AuditRetentionDays = 365 })
	app.RunRetention(ctx)
	if got := w.countRows(`SELECT count(*) FROM delegated_call`); got != 1 {
		t.Fatalf("%d records left, want only the fresh one", got)
	}
}

func TestF4_4_RetentionRunsOncePerIntervalAcrossReplicas(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := func() {
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO delegated_call (at, service_id, caller_client, subject, operation)
			VALUES (now() - interval '400 days', $1, 'c', 'old', 'check')`, w.org); err != nil {
			t.Fatal(err)
		}
	}
	tune := func(c *config.Config) { c.Delegation.AuditRetentionDays = 365 }
	a, b := w.replica(tune), w.replica(tune)

	old()
	go a.WatchRetention(ctx)
	for deadline := time.Now().Add(5 * time.Second); w.countRows(`SELECT count(*) FROM delegated_call`) != 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first replica did not apply the retention")
		}
	}
	// Within the interval another replica leaves the table alone.
	old()
	go b.WatchRetention(ctx)
	time.Sleep(time.Second)
	if got := w.countRows(`SELECT count(*) FROM delegated_call`); got != 1 {
		t.Fatalf("a second replica applied the retention within the interval: %d records", got)
	}
}

func TestF4_5_ALogSettingsChangeReachesEveryReplicaThroughTheRevisionPoll(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var switches []*logx.Switch
	for i := 0; i < 3; i++ {
		sw, err := logx.New(io.Discard, logx.DefaultOptions(slog.LevelInfo))
		if err != nil {
			t.Fatal(err)
		}
		app := w.replica(func(c *config.Config) { c.Revision.PollInterval = 250 * time.Millisecond })
		app.Logs = sw
		go app.WatchRevision(ctx)
		switches = append(switches, sw)
	}
	if switches[0].Options().Format == "logfmt" {
		t.Fatal("the default format is already logfmt")
	}
	started := time.Now()
	w.must("PUT", "/settings/logs", map[string]any{
		"format": "logfmt", "level": "info", "subject": "plain", "sample_allowed": 1,
		"fields": []map[string]any{},
		"events": map[string]bool{"access": true, "decision": true, "sync": true},
	})
	for i, sw := range switches {
		for sw.Options().Format != "logfmt" {
			if time.Since(started) > 2*time.Second {
				t.Fatalf("replica %d did not apply the settings within 2 s", i)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestF4_6_WaitersOfTheChangeLogShareOnePoller(t *testing.T) {
	w := newWorld(t)
	// The trace of the context counts the statements of the waiters only, not the background work of the process.
	ctx, trace := engine.WithTrace(context.Background())
	started := time.Now()
	var wg sync.WaitGroup
	results := make(chan int, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := w.app.WaitChanges(ctx, service.ChangeFilter{After: 1 << 40, Limit: 10}, 3*time.Second)
			if err != nil {
				t.Error(err)
			}
			results <- len(out)
		}()
	}
	wg.Wait()
	close(results)
	for n := range results {
		if n != 0 {
			t.Fatalf("a waiter was woken with %d records", n)
		}
	}
	// At most one query per second per process, plus the one that starts the first second.
	if q, limit := trace.DBQueries(), int(math.Ceil(time.Since(started).Seconds()))+1; q > limit {
		t.Fatalf("100 waiters made %d queries in %s, want at most %d", q, time.Since(started), limit)
	}
}

func TestF4_6_AWaiterSeesARecordThatComesWhileItWaits(t *testing.T) {
	w := newWorld(t)
	done := make(chan []string, 1)
	go func() {
		out, _ := w.app.WaitChanges(context.Background(), service.ChangeFilter{After: w.lastChange(), Limit: 10}, 10*time.Second)
		ids := []string{}
		for _, c := range out {
			ids = append(ids, c.Entity)
		}
		done <- ids
	}()
	time.Sleep(300 * time.Millisecond)
	w.must("POST", "/group", map[string]any{"name": "late"})
	select {
	case ids := <-done:
		if len(ids) == 0 {
			t.Fatal("the waiter returned empty")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter did not see the record")
	}
}

func (w *world) lastChange() int64 {
	var id int64
	if err := w.pool.QueryRow(context.Background(), `SELECT COALESCE(max(id), 0) FROM authz_change`).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func grepLines(text, sub string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
