package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRoundTripsCountABatchOnceAndExcludeReadOnlyTransactions(t *testing.T) {
	ctx, counter := CountRoundTrips(context.Background())
	tracer := &Tracer{}
	query := func(sql string) { tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: sql}) }
	query("SELECT preflight")
	query("begin isolation level repeatable read read only")
	query("SELECT snapshot")
	query("commit")
	query("begin")
	batch := &pgx.Batch{}
	batch.Queue("SELECT lock")
	batch.Queue("UPDATE row")
	tracer.TraceBatchStart(ctx, nil, pgx.TraceBatchStartData{Batch: batch})
	tracer.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{SQL: "SELECT lock"})
	tracer.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{SQL: "UPDATE row"})
	tracer.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})
	tracer.TraceCopyFromStart(ctx, nil, pgx.TraceCopyFromStartData{})
	query("commit")
	if got := counter.Counts(); got != (RoundTripCounts{Total: 8, Write: 4, Transactions: 1}) {
		t.Fatalf("counts=%+v", got)
	}
	_, other := CountRoundTrips(ctx)
	if other.Counts() != (RoundTripCounts{}) {
		t.Fatal("a fresh request inherited another request's count")
	}
}
