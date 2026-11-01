//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/db"
)

func TestFailoverPublishesAnAtomicEpochBoundary(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	before, err := db.LoadLocalVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	aborted := errors.New("abort promotion")
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := db.StartEpoch(ctx, tx, db.DefaultFailoverJump); err != nil {
			return err
		}
		return aborted
	}); !errors.Is(err, aborted) {
		t.Fatal(err)
	}
	unchanged, err := db.LoadLocalVersions(ctx, pool)
	if err != nil || unchanged != before {
		t.Fatalf("rollback: %+v %v", unchanged, err)
	}
	var position db.Position
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		position, err = db.StartEpoch(ctx, tx, db.DefaultFailoverJump)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if position.Epoch != before.Epoch+1 || position.Revision != before.Revision+db.DefaultFailoverJump {
		t.Fatalf("position %+v, before %+v", position, before)
	}
	var epoch int32
	var kind string
	var services, streams int
	var policy string
	if err := pool.QueryRow(ctx, `SELECT epoch,kind,cardinality(services),cardinality(streams),policy::text FROM outbox WHERE revision=$1`, position.Revision).Scan(&epoch, &kind, &services, &streams, &policy); err != nil {
		t.Fatal(err)
	}
	if epoch != position.Epoch || kind != "epoch_start" || services != 0 || streams != 0 || policy != "{}" {
		t.Fatalf("boundary %d %s %d %d %s", epoch, kind, services, streams, policy)
	}
	for _, jump := range []int64{0, -1} {
		if err := db.InTx(ctx, pool, func(tx pgx.Tx) error { _, err := db.StartEpoch(ctx, tx, jump); return err }); err == nil {
			t.Fatal("accepted nonpositive jump")
		}
	}
}
