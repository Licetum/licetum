//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/outbox"
)

func TestOutboxRetentionRemovesWholePayloadsButKeepsHistoryAndHead(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	raw, err := json.Marshal(map[string]string{"large": strings.Repeat("a", 2*outbox.ChunkLimit)})
	if err != nil {
		t.Fatal(err)
	}
	var old, young, head db.Position
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		old, err = db.TakeRevision(ctx, tx, outbox.Entry{Streams: []string{outbox.Settings}, Policy: map[string][]outbox.Image{outbox.Settings: {{Table: "log_settings", Key: map[string]any{"id": 1}, Row: raw}}}})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO change_log(revision,seq,entity,action) VALUES($1,1,'log_settings','update')`, old.Revision)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*db.Position{&young, &head} {
		if err := db.InTx(ctx, pool, func(tx pgx.Tx) error { var err error; *p, err = db.TakeRevision(ctx, tx, outbox.Entry{}); return err }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox SET committed_at=clock_timestamp()-interval '2 hours' WHERE revision=ANY($1::bigint[])`, []int64{old.Revision, head.Revision}); err != nil {
		t.Fatal(err)
	}
	if n, err := db.PruneOutbox(ctx, pool, time.Hour, 1); err != nil || n != 1 {
		t.Fatalf("retention count=%d err=%v", n, err)
	}
	var chunks, headers, history int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbox_chunk),(SELECT count(*) FROM outbox WHERE revision=ANY($1::bigint[])),(SELECT count(*) FROM change_log WHERE revision=$2)`, []int64{young.Revision, head.Revision}, old.Revision).Scan(&chunks, &headers, &history); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 || headers != 2 || history != 1 {
		t.Fatalf("retention split data: chunks=%d retained headers=%d history=%d", chunks, headers, history)
	}
	if n, err := db.PruneOutbox(ctx, pool, time.Hour, 100); err != nil || n != 0 {
		t.Fatalf("aged head was removed: %d %v", n, err)
	}
}
