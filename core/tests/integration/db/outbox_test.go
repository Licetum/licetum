//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/outbox"
)

func TestContract_WR01_ConcurrentWritersAndSnapshotsSeeGapFreePrefix(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	base, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			errs <- db.InTx(ctx, pool, func(tx pgx.Tx) error { _, err := db.TakeRevision(ctx, tx, outbox.Entry{}); return err })
		})
	}
	for range 30 {
		if err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			for range 2 {
				var revision, count int64
				if err := tx.QueryRow(ctx, `SELECT revision,(SELECT count(*) FROM outbox WHERE revision>$1) FROM revision_state WHERE scope='global'`, base.Revision).Scan(&revision, &count); err != nil {
					return err
				}
				if revision-base.Revision != count {
					return fmt.Errorf("snapshot is not a prefix: revision=%d count=%d base=%d", revision, count, base.Revision)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count, minRev, maxRev int64
	if err := pool.QueryRow(ctx, `SELECT count(*),min(revision),max(revision) FROM outbox WHERE revision>$1`, base.Revision).Scan(&count, &minRev, &maxRev); err != nil {
		t.Fatal(err)
	}
	if count != writers || minRev != base.Revision+1 || maxRev != base.Revision+writers {
		t.Fatalf("not gap-free: %d entries [%d,%d] after %d", count, minRev, maxRev, base.Revision)
	}
}

func TestContract_WR01_LargePayloadThroughSimpleProtocol(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	cfg := pool.Config()
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	simple, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer simple.Close()
	raw, err := json.Marshal(map[string]string{"value": strings.Repeat("a", 2*outbox.ChunkLimit)})
	if err != nil {
		t.Fatal(err)
	}
	entry := outbox.Entry{Streams: []string{outbox.Settings}, Policy: map[string][]outbox.Image{outbox.Settings: {{Table: "log_settings", Key: map[string]any{"id": 1}, Row: raw}}}}
	var position db.Position
	if err := db.InTx(ctx, simple, func(tx pgx.Tx) error { var err error; position, err = db.TakeRevision(ctx, tx, entry); return err }); err != nil {
		t.Fatal(err)
	}
	var count, maxBytes int
	if err := simple.QueryRow(ctx, `SELECT count(*),max(octet_length(c.body)) FROM outbox_chunk c JOIN outbox o ON c.payload=o.payload WHERE o.revision=$1`, position.Revision).Scan(&count, &maxBytes); err != nil {
		t.Fatal(err)
	}
	if count < 1 || maxBytes > outbox.ChunkLimit {
		t.Fatalf("invalid chunk count/size: %d/%d", count, maxBytes)
	}
}

func TestContract_WR01_RollbackReturnsRevisionAndNotifyIsCommitOnly(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `LISTEN licetum_outbox`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = listener.Exec(ctx, `UNLISTEN licetum_outbox`) }()
	base, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("rollback")
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		p, err := db.TakeRevision(ctx, tx, outbox.Entry{})
		if err != nil {
			return err
		}
		if p.Revision != base.Revision+1 {
			return fmt.Errorf("revision = %v", p)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = listener.Conn().WaitForNotification(short)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rollback notification: %v", err)
	}
	var committed db.Position
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		committed, err = db.TakeRevision(ctx, tx, outbox.Entry{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if committed.Revision != base.Revision+1 {
		t.Fatalf("rollback spent a revision: %v after %+v", committed, base)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	n, err := listener.Conn().WaitForNotification(deadline)
	if err != nil {
		t.Fatal(err)
	}
	if n.Payload != fmt.Sprint(committed.Revision) {
		t.Fatalf("notify = %q", n.Payload)
	}
}
