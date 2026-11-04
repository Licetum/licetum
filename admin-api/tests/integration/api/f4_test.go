//go:build integration

package api_test

// Tests of F4: work off the request path (issue #36): retention, the log settings, the change log. Names carry the criterion they prove.

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/logx"
)

// replica builds another engine.App on the world's database, with the given tuning applied to a copy of the config.
func (w *world) replica(tune func(*config.Config)) *engine.App {
	w.t.Helper()
	cfg := *w.cfg
	tune(&cfg)
	app := engine.New(w.pool, &cfg)
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

// seedRetainedDirectory creates old directory content in bulk without paying
// one fixture API call per subject; the explicit fixture header covers the rows.
func seedRetainedDirectory(w *world, old int) {
	w.t.Helper()
	w.must("PUT", "/service/"+w.org+"/subjects/settings", map[string]any{"retention_days": 90})
	w.exec(`INSERT INTO subject_directory(service_id,subject_type,subject_id)
  SELECT $1::uuid,'user','s'||g FROM generate_series(1,$2) g
  UNION ALL SELECT $1::uuid,'user','fresh'`, w.org, old)
	// Give the FK probes of the bulk seen-time insert current table statistics.
	w.exec(`ANALYZE subject_directory`)
	w.exec(`INSERT INTO subject_directory_seen(service_id,subject_type,subject_id,seen_at)
  SELECT service_id,subject_type,subject_id,
   CASE WHEN subject_id='fresh' THEN now() ELSE now()-interval '400 days' END
  FROM subject_directory WHERE service_id=$1`, w.org)
}

func TestF4_4_RetentionRemovesOldRecordsInChunks(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	seedRetainedDirectory(w, 25000)
	app := w.replica(func(c *config.Config) {})
	app.RunRetention(ctx)
	if got := w.countRows(`SELECT count(*) FROM subject_directory WHERE service_id=$1`, w.org); got != 1 {
		t.Fatalf("%d directory rows left, want only the fresh one", got)
	}
}

func TestF4_4_DirectoryRetentionCanKeepEntriesWithoutExpiry(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	seedRetainedDirectory(w, 1)
	path := "/service/" + w.org + "/subjects/settings"
	w.must("PUT", path, map[string]any{"retention_days": nil})
	app := w.replica(func(c *config.Config) {})
	app.RunRetention(ctx)
	if got := w.countRows(`SELECT count(*) FROM subject_directory WHERE service_id=$1`, w.org); got != 2 {
		t.Fatalf("retention disabled: %d rows, want 2", got)
	}
	w.must("PUT", path, map[string]any{"retention_days": 90})
	app.RunRetention(ctx)
	if got := w.countRows(`SELECT count(*) FROM subject_directory WHERE service_id=$1`, w.org); got != 1 {
		t.Fatalf("retention enabled: %d rows, want 1", got)
	}
}

// Workers divide expired directory entries with SKIP LOCKED and publish their
// own chunks; a newly sent subject must survive their overlapping runs.
func TestF4_4_WorkersApplyTheRetentionAtTheSameTimeAndDivideTheWork(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seedRetainedDirectory(w, 30000)
	tune := func(c *config.Config) {}
	a, b := w.replica(tune), w.replica(tune)
	started := time.Now()
	go a.WatchRetention(ctx)
	go b.WatchRetention(ctx)
	for w.countRows(`SELECT count(*) FROM subject_directory WHERE service_id=$1`, w.org) != 1 {
		if time.Since(started) > 30*time.Second {
			t.Fatal("directory retention did not drain the expired entries")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := w.countRows(`SELECT count(*) FROM subject_directory WHERE service_id=$1 AND subject_id='fresh'`, w.org); got != 1 {
		t.Fatal("the fresh directory entry was deleted")
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
			out, err := w.app.WaitChanges(ctx, service.ChangeFilter{AfterSeq: 2147483647, AfterRevision: 1 << 40, Limit: 10}, 3*time.Second)
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
		out, _ := w.app.WaitChanges(context.Background(), service.ChangeFilter{AfterSeq: 2147483647, AfterRevision: w.lastChange(), Limit: 10}, 10*time.Second)
		ids := []string{}
		for _, c := range out {
			ids = append(ids, c.Entity)
		}
		done <- ids
	}()
	time.Sleep(300 * time.Millisecond)
	w.must("POST", "/group", map[string]any{"service_id": w.org, "name": "late"})
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
	if err := w.pool.QueryRow(context.Background(), `SELECT COALESCE(max(revision), 0) FROM change_log`).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}
