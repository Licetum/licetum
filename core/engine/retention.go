package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/licetum/licetum/core/db"
)

const (
	// retentionEvery is how often directory and outbox retention is applied.
	retentionEvery = time.Hour
	// retentionChunk bounds the rows one DELETE removes, to keep its locks short.
	retentionChunk = 10_000
)

// WatchRetention applies the retention every retentionEvery until ctx ends. Every worker runs it: nothing makes it run once per
// interval across the workers, and none has to, because the deletes of one chunk skip the rows another worker has taken
// (FOR UPDATE SKIP LOCKED), so the workers divide the work and never wait for each other.
func (a *App) WatchRetention(ctx context.Context) {
	ticker := time.NewTicker(retentionEvery)
	defer ticker.Stop()
	for {
		a.RunRetention(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunRetention removes expired directory entries and outbox publications in
// bounded chunks. Several workers may safely divide the work with SKIP LOCKED.
func (a *App) RunRetention(ctx context.Context) {
	a.pruneDirectory(ctx)
	a.pruneOutbox(ctx)
}

// pruneDirectory deletes the entries of the subject directories that were not sent within the retention period of their
// service (a service without one keeps everything), a chunk at a time.
func (a *App) pruneDirectory(ctx context.Context) {
	for ctx.Err() == nil {
		n, err := db.PruneDirectory(ctx, a.DB, retentionChunk)
		if err != nil {
			slog.WarnContext(ctx, "failed to apply the retention", "table", "subject_directory", "error", err)
			return
		}
		if n < retentionChunk {
			return
		}
	}
}

func (a *App) pruneOutbox(ctx context.Context) {
	age := time.Hour
	if a.Cfg != nil && a.Cfg.Outbox.Retention > 0 {
		age = a.Cfg.Outbox.Retention
	}
	for ctx.Err() == nil {
		n, err := db.PruneOutbox(ctx, a.DB, age, retentionChunk)
		if err != nil {
			slog.WarnContext(ctx, "failed to retain outbox", "error", err)
			return
		}
		if n < retentionChunk {
			break
		}
	}
	if a.metrics != nil {
		count, oldest, err := db.OutboxSize(ctx, a.DB)
		if err == nil {
			a.metrics.outboxRows.Set(float64(count))
			a.metrics.outboxOldest.Set(oldest)
		}
	}
}
