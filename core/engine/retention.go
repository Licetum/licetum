package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/licetum/licetum/core/db"
)

const (
	// retentionEvery is how often the retention of the left-over known subjects and of the directories is applied.
	retentionEvery = time.Hour
	// retentionChunk bounds the rows one DELETE removes, to keep its locks short.
	retentionChunk = 10_000
	// knownSubjectKeepDays is how long a row of the retired registry of known subjects is kept.
	knownSubjectKeepDays = 90
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

// RunRetention removes the rows left in the retired registry of known subjects, and the directory entries that were not sent
// within the retention period of their service, chunk by chunk until nothing is left. It may run in several processes at once.
// The audit of the calls on behalf of a person is gone with those calls: nothing writes delegated_call, and its table goes with
// the schema of P3.
func (a *App) RunRetention(ctx context.Context) {
	// The registry of known subjects is gone (the subject directory replaced it) and nothing writes its table any more; what the
	// previous release left in it is dropped after the period it was kept for. The table itself goes with the schema of P3.
	a.pruneInChunks(ctx, "known_subject", `
		DELETE FROM known_subject WHERE ctid IN (
			SELECT ctid FROM known_subject WHERE last_seen < now() - make_interval(days => $1) LIMIT $2 FOR UPDATE SKIP LOCKED)`, knownSubjectKeepDays)
	a.pruneDirectory(ctx)
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
