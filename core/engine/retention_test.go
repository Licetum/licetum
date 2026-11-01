//go:build integration

package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
)

// Directory rows another worker is deleting are skipped: retention does not wait for the
// transaction that holds them, and takes them once it has ended.
func TestDirectoryRetentionSkipsTheRowsAnotherWorkerHoldsWithoutWaiting(t *testing.T) {
	pool, typ := backoffType(t)
	ctx := context.Background()
	var service uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT service_id FROM type_resource WHERE id=$1`, typ).Scan(&service); err != nil {
		t.Fatal(err)
	}
	days := 90
	if err := db.PutDirectorySettings(ctx, pool, service, db.DirectorySettings{RetentionDays: &days}); err != nil {
		t.Fatal(err)
	}
	entries := []db.DirectoryEntry{{Type: "user", ID: "recent"}}
	for i := range 10 {
		entries = append(entries, db.DirectoryEntry{Type: "user", ID: fmt.Sprintf("u%02d", i)})
	}
	if _, err := db.UpsertDirectory(ctx, pool, service, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at=now()-interval '200 days' WHERE subject_id LIKE 'u%'`); err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM subject_directory`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Another worker holds four of the rows in a transaction that has not ended.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM subject_directory WHERE subject_id LIKE 'u%' AND subject_id < 'u04' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}

	a := &App{DB: pool}
	done := make(chan struct{})
	go func() {
		a.RunRetention(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the retention waits for the rows another worker holds")
	}
	if got := count(); got != 5 {
		t.Fatalf("%d rows left while four were held, want the 4 held and the recent one", got)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	a.RunRetention(ctx)
	if got := count(); got != 1 {
		t.Fatalf("%d rows left after the others were released, want the recent one", got)
	}
}
