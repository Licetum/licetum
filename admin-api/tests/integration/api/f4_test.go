//go:build integration

package api_test

// Tests of F4: work off the request path (issue #36): retention, the log settings, the change log. Names carry the criterion they prove.

import (
	"context"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/service"
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

func TestF4_4_RetentionRemovesOldRecordsInChunks(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO known_subject (subject, last_seen)
		SELECT 's' || g, now() - interval '400 days' FROM generate_series(1, 25000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO known_subject (subject) VALUES ('fresh')`); err != nil {
		t.Fatal(err)
	}
	app := w.replica(func(c *config.Config) {})
	app.RunRetention(ctx)
	if got := w.countRows(`SELECT count(*) FROM known_subject`); got != 1 {
		t.Fatalf("%d records left, want only the fresh one", got)
	}
}

// The registry of known subjects is retired: nothing writes its table, and what the previous release left in it is dropped after
// 90 days, with no setting.
func TestF4_4_RetentionDropsWhatIsLeftOfTheRetiredRegistryAfterNinetyDays(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `INSERT INTO known_subject (subject, last_seen) VALUES ('old', now() - interval '200 days'), ('fresh', now())`); err != nil {
		t.Fatal(err)
	}
	app := w.replica(func(c *config.Config) {})
	app.RunRetention(ctx)
	if got := w.countRows(`SELECT count(*) FROM known_subject WHERE subject IN ('old', 'fresh')`); got != 1 {
		t.Fatalf("%d subjects left, want only the fresh one", got)
	}
}

// Every worker applies the retention at its start and then every interval; there is no lock that makes it run once. Two workers that
// start together divide the old rows and neither waits for the other: all of them are gone, and each was deleted by one.
func TestF4_4_WorkersApplyTheRetentionAtTheSameTimeAndDivideTheWork(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO known_subject (subject, last_seen)
		SELECT 's' || g, now() - interval '400 days' FROM generate_series(1, 30000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO known_subject (subject) VALUES ('fresh')`); err != nil {
		t.Fatal(err)
	}
	tune := func(c *config.Config) {}
	a, b := w.replica(tune), w.replica(tune)

	started := time.Now()
	go a.WatchRetention(ctx)
	go b.WatchRetention(ctx)
	for w.countRows(`SELECT count(*) FROM known_subject`) != 1 {
		if time.Since(started) > 30*time.Second {
			t.Fatalf("%d records left, want only the fresh one", w.countRows(`SELECT count(*) FROM known_subject`))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := w.countRows(`SELECT count(*) FROM known_subject WHERE subject = 'fresh'`); got != 1 {
		t.Fatal("the fresh record was deleted")
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
