package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PruneOutbox removes complete aged payloads, retaining the newest header even
// when it is old. Concurrent workers divide chunks using SKIP LOCKED.
func PruneOutbox(ctx context.Context, pool *pgxpool.Pool, age time.Duration, limit int) (int64, error) {
	var count int64
	err := pool.QueryRow(ctx, `WITH old AS (
 DELETE FROM outbox WHERE revision IN (
  SELECT revision FROM outbox WHERE committed_at<clock_timestamp()-make_interval(secs=>$1)
  AND revision<(SELECT max(revision) FROM outbox) ORDER BY revision LIMIT $2 FOR UPDATE SKIP LOCKED
 ) RETURNING payload
), chunks AS(DELETE FROM outbox_chunk c USING old WHERE c.payload=old.payload)
SELECT count(*) FROM old`, age.Seconds(), limit).Scan(&count)
	return count, err
}

func OutboxSize(ctx context.Context, pool *pgxpool.Pool) (int64, float64, error) {
	var count int64
	var age float64
	err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(extract(epoch FROM clock_timestamp()-min(committed_at)),0) FROM outbox`).Scan(&count, &age)
	return count, age, err
}
