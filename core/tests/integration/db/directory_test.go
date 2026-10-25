//go:build integration

package db_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/licetum/licetum/core/db"
)

func entry(typ, id string) db.DirectoryEntry {
	return db.DirectoryEntry{Type: typ, ID: id, DisplayName: id, Properties: map[string]any{"k": id}}
}

// The scan goes by type and then bytewise by id, whatever the collation of the database, and a cursor continues after the key it
// names: no entry is skipped or repeated, and sending an entry again does not move it.
func TestDirectoryScanIsBytewiseAndAKeysetContinuesAfterItsKey(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	var entries []db.DirectoryEntry
	for _, id := range []string{"b", "B", "a", "Ä", "_x", "10", "9", "alice", "Alice", "é"} {
		entries = append(entries, entry("user", id))
	}
	entries = append(entries, entry("agent", "z"), entry("user", "a b"))
	if _, err := db.UpsertDirectory(ctx, pool, service, entries); err != nil {
		t.Fatal(err)
	}

	read := func(limit int) []string {
		var got []string
		var after *db.DirectoryKey
		for {
			page, err := db.DirectoryAfter(ctx, pool, service, "", after, limit)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page {
				got = append(got, e.Type+"/"+e.ID)
			}
			if len(page) < limit {
				return got
			}
			last := page[len(page)-1]
			after = &db.DirectoryKey{Type: last.Type, ID: last.ID}
		}
	}
	// Bytewise: the type first, then digits, capitals, "_", lower case, and the UTF-8 of the accented letters last.
	want := []string{"agent/z", "user/10", "user/9", "user/Alice", "user/B", "user/_x", "user/a", "user/a b", "user/alice", "user/b",
		"user/\u00c4", "user/\u00e9"}
	whole := read(100)
	if !slices.Equal(whole, want) {
		t.Fatalf("the scan is %v, want %v", whole, want)
	}
	if len(whole) != len(entries) {
		t.Fatalf("read %d of %d entries: %v", len(whole), len(entries), whole)
	}
	for _, size := range []int{1, 2, 5} {
		if got := read(size); !slices.Equal(got, whole) {
			t.Fatalf("pages of %d: %v\nwant %v", size, got, whole)
		}
	}

	// A type filters; sending an entry again does not move it.
	users, err := db.DirectoryAfter(ctx, pool, service, "user", nil, 100)
	if err != nil || len(users) != len(entries)-1 {
		t.Fatalf("users: %d, %v", len(users), err)
	}
	if _, err := db.UpsertDirectory(ctx, pool, service, []db.DirectoryEntry{entry("user", "a")}); err != nil {
		t.Fatal(err)
	}
	if got := read(3); !slices.Equal(got, whole) {
		t.Fatalf("sending an entry again moved it: %v", got)
	}
}

// Retention deletes what it is asked to, a chunk at a time, and counts what it did.
func TestPruneDirectoryDeletesInChunksAndOnlyWhatARetentionPeriodCovers(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	var entries []db.DirectoryEntry
	for i := range 25 {
		entries = append(entries, entry("user", fmt.Sprintf("u%02d", i)))
	}
	if _, err := db.UpsertDirectory(ctx, pool, service, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at = now() - interval '90 days' WHERE subject_id < 'u20'`); err != nil {
		t.Fatal(err)
	}

	// No retention period: nothing is covered.
	if n, err := db.PruneDirectory(ctx, pool, 10); err != nil || n != 0 {
		t.Fatalf("pruned %d without a period (%v)", n, err)
	}
	days := 30
	if err := db.PutDirectorySettings(ctx, pool, service, db.DirectorySettings{RetentionDays: &days}); err != nil {
		t.Fatal(err)
	}
	var chunks []int64
	for {
		n, err := db.PruneDirectory(ctx, pool, 8)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, n)
		if n < 8 {
			break
		}
	}
	if !slices.Equal(chunks, []int64{8, 8, 4}) {
		t.Fatalf("the chunks were %v, want 8, 8, 4 for the 20 entries that were not sent", chunks)
	}
	left, err := db.DirectoryAfter(ctx, pool, service, "", nil, 100)
	if err != nil || len(left) != 5 {
		t.Fatalf("%d entries left (%v), want the 5 that were sent lately", len(left), err)
	}

	// The period can be taken away again, and read back.
	if err := db.PutDirectorySettings(ctx, pool, service, db.DirectorySettings{}); err != nil {
		t.Fatal(err)
	}
	if s, err := db.GetDirectorySettings(ctx, pool, service); err != nil || s.RetentionDays != nil {
		t.Fatalf("the settings after removing the period: %+v, %v", s, err)
	}
}

// Workers that run the retention at the same time divide the expired entries between them: each is deleted once, by one of
// them, and a worker that meets entries another one has taken leaves them to it instead of waiting.
func TestPruneDirectoryRunsOfSeveralWorkersDeleteEveryExpiredEntryOnce(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	const total = 400
	var entries []db.DirectoryEntry
	for i := range total {
		entries = append(entries, entry("user", fmt.Sprintf("u%03d", i)))
	}
	if _, err := db.UpsertDirectory(ctx, pool, service, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at = now() - interval '90 days'`); err != nil {
		t.Fatal(err)
	}
	days := 30
	if err := db.PutDirectorySettings(ctx, pool, service, db.DirectorySettings{RetentionDays: &days}); err != nil {
		t.Fatal(err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		deleted int64
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := db.PruneDirectory(ctx, pool, 25)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				deleted += n
				mu.Unlock()
				if n < 25 {
					return
				}
			}
		}()
	}
	wg.Wait()
	// A worker that stops on a short chunk may leave entries that another one had taken and was still deleting: one more pass
	// finds nothing, and the sum is exactly the number of entries.
	n, err := db.PruneDirectory(ctx, pool, total)
	if err != nil {
		t.Fatal(err)
	}
	deleted += n
	if deleted != total {
		t.Fatalf("the workers deleted %d entries, want %d, each once", deleted, total)
	}
	left, err := db.DirectoryAfter(ctx, pool, service, "", nil, total)
	if err != nil || len(left) != 0 {
		t.Fatalf("%d entries left (%v), want none", len(left), err)
	}
}

// The entries another worker is deleting are skipped: the retention does not wait for the transaction that holds them.
func TestPruneDirectorySkipsTheEntriesAnotherWorkerHasTakenWithoutWaiting(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	var entries []db.DirectoryEntry
	for i := range 10 {
		entries = append(entries, entry("user", fmt.Sprintf("u%02d", i)))
	}
	if _, err := db.UpsertDirectory(ctx, pool, service, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at = now() - interval '90 days'`); err != nil {
		t.Fatal(err)
	}
	days := 30
	if err := db.PutDirectorySettings(ctx, pool, service, db.DirectorySettings{RetentionDays: &days}); err != nil {
		t.Fatal(err)
	}

	// Another worker holds four of the entries in a transaction that has not ended.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM subject_directory_seen WHERE service_id = $1 AND subject_id < 'u04' FOR UPDATE`, service); err != nil {
		t.Fatal(err)
	}

	done := make(chan int64, 1)
	go func() {
		n, err := db.PruneDirectory(ctx, pool, 100)
		if err != nil {
			t.Error(err)
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n != 6 {
			t.Fatalf("deleted %d entries while four were taken, want the other 6", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the retention waits for the entries another worker holds")
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := db.PruneDirectory(ctx, pool, 100); err != nil || n != 4 {
		t.Fatalf("deleted %d entries after the others were released (%v), want the remaining 4", n, err)
	}
}
