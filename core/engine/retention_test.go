//go:build integration

package engine

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The rows of the retired registry that another worker is deleting are skipped: the retention does not wait for the
// transaction that holds them, and takes them once it has ended.
func TestRetentionOfKnownSubjectsSkipsTheRowsAnotherWorkerHoldsWithoutWaiting(t *testing.T) {
	pool, _ := backoffType(t)
	ctx := context.Background()
	for i := range 10 {
		if _, err := pool.Exec(ctx, `INSERT INTO known_subject (subject, last_seen) VALUES ($1, now() - interval '200 days')`, fmt.Sprintf("u%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO known_subject (subject) VALUES ('recent')`); err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM known_subject`).Scan(&n); err != nil {
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
	if _, err := tx.Exec(ctx, `SELECT 1 FROM known_subject WHERE subject < 'u04' FOR UPDATE`); err != nil {
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
