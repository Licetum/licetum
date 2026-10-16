package engine

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// retentionEvery is how often the retention of the audit and of the known subjects is applied.
	retentionEvery = time.Hour
	// retentionChunk bounds the rows one DELETE removes, to keep its locks short.
	retentionChunk = 10_000
	retentionKey   = "authz:retention:lock"
)

// WatchRetention applies the retention every retentionEvery until ctx ends. A Redis lock held for a little less than the
// interval makes it run once per interval across the replicas; without Redis the work runs anyway (it is idempotent).
func (a *App) WatchRetention(ctx context.Context) {
	ticker := time.NewTicker(retentionEvery)
	defer ticker.Stop()
	for {
		a.runRetentionGuarded(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runRetentionGuarded(ctx context.Context) {
	ok, err := a.Redis.SetNX(ctx, retentionKey, "1", retentionEvery-5*time.Minute).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		slog.WarnContext(ctx, "retention lock unavailable, running without it", "error", err)
	} else if !ok {
		return
	}
	a.RunRetention(ctx)
}

// RunRetention removes the delegated-call records and the known subjects older than their retention, chunk by chunk
// until nothing is left.
func (a *App) RunRetention(ctx context.Context) {
	if days := a.Cfg.Delegation.AuditRetentionDays; days > 0 {
		a.pruneInChunks(ctx, "delegated_call", `
			DELETE FROM delegated_call WHERE ctid IN (
				SELECT ctid FROM delegated_call WHERE at < now() - make_interval(days => $1) LIMIT $2)`, days)
	}
	if days := a.Cfg.Subjects.RetentionDays; a.Cfg.Subjects.Enabled && days > 0 {
		a.pruneInChunks(ctx, "known_subject", `
			DELETE FROM known_subject WHERE ctid IN (
				SELECT ctid FROM known_subject WHERE last_seen < now() - make_interval(days => $1) LIMIT $2)`, days)
	}
}

func (a *App) pruneInChunks(ctx context.Context, table, query string, days int) {
	for ctx.Err() == nil {
		tag, err := a.DB.Exec(ctx, query, days, retentionChunk)
		if err != nil {
			slog.WarnContext(ctx, "failed to apply the retention", "table", table, "error", err)
			return
		}
		if tag.RowsAffected() < retentionChunk {
			return
		}
	}
}
