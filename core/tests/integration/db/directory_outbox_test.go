//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/outbox"
)

func TestDirectoryPublishesOnlyContentChanges(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	before, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	entry := db.DirectoryEntry{Type: "user", ID: "alice", DisplayName: "Alice"}
	if _, err := db.UpsertDirectory(ctx, pool, service, []db.DirectoryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatal("a changing directory batch did not spend exactly one revision")
	}
	var streams []string
	var services int
	if err := pool.QueryRow(ctx, `SELECT streams,cardinality(services) FROM outbox WHERE revision=$1`, after.Revision).Scan(&streams, &services); err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0] != outbox.Directory(service) || services != 0 {
		t.Fatal("directory entry has incorrect publication scope")
	}
	if _, err := db.UpsertDirectory(ctx, pool, service, []db.DirectoryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != after.Revision {
		t.Fatal("an unchanged sending spent a revision")
	}
	if err := db.DeleteDirectoryEntry(ctx, pool, service, "user", "alice"); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Revision != after.Revision+1 {
		t.Fatal("directory deletion did not spend one revision")
	}
	if n, err := db.PruneDirectory(ctx, pool, 100); err != nil || n != 0 {
		t.Fatal("empty retention failed", err)
	}
	last, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if last.Revision != deleted.Revision {
		t.Fatal("empty retention spent a revision")
	}
}
