package db

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/outbox"
)

// AdminWriteLock serializes policy validation and writes across processes.
const AdminWriteLock int64 = 0x6c6963657461646d

// PolicyTx holds admin_write before validation, then takes one final revision for
// the entire write. The optional recheck hook is filled by the P4 binding model.
func PolicyTx(ctx context.Context, pool *pgxpool.Pool, recheck func(context.Context, pgx.Tx) error, write func(context.Context, pgx.Tx) ([]Change, error)) (Position, error) {
	var position Position
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		b := &pgx.Batch{}
		b.Queue(`SELECT pg_advisory_xact_lock($1)`, AdminWriteLock)
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return err
		}
		if recheck != nil {
			if err := recheck(ctx, tx); err != nil {
				return err
			}
		}
		changes, err := write(ctx, tx)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			return nil
		}
		entry, err := ProjectChanges(ctx, tx, changes)
		if err != nil {
			return err
		}
		position, err = finishPolicy(ctx, tx, entry, changes)
		return err
	})
	return position, err
}

// ProjectChanges deduplicates touched rows and reads their final canonical images
// before taking the revision lock. The final state of a batch is projected once.
func ProjectChanges(ctx context.Context, q outbox.Reader, changes []Change) (outbox.Entry, error) {
	entry := outbox.Entry{Kind: outbox.Change, Policy: map[string][]outbox.Image{}}
	owners := []uuid.UUID{}
	touched := map[string]Touched{}
	for _, c := range changes {
		for _, t := range c.Touched {
			key, err := json.Marshal(t.Key)
			if err != nil {
				return entry, err
			}
			touched[t.Table+string(key)] = t
		}
	}
	names := make([]string, 0, len(touched))
	for key := range touched {
		names = append(names, key)
	}
	slices.Sort(names)
	for _, key := range names {
		t := touched[key]
		stream, err := outbox.Stream(t.Table, t.ServiceID)
		if err != nil {
			return entry, err
		}
		if stream == "" {
			continue
		}
		var image outbox.Image
		if t.Deleted {
			raw, err := outbox.Tombstone(t.Key)
			if err != nil {
				return entry, err
			}
			image = outbox.Image{Table: t.Table, Key: t.Key, Row: raw}
		} else {
			image, err = outbox.Project(ctx, q, t.Table, t.Key)
			if err != nil {
				return entry, err
			}
		}
		entry.Policy[stream] = append(entry.Policy[stream], image)
		if (stream == outbox.Catalog || len(stream) > 4 && stream[:4] == "svc:") && t.ServiceID != nil {
			owners = append(owners, *t.ServiceID)
		}
	}
	for stream := range entry.Policy {
		entry.Streams = append(entry.Streams, stream)
	}
	slices.Sort(entry.Streams)
	var err error
	entry.Services, err = outbox.ServicesBelow(ctx, q, owners)
	return entry, err
}

// FinishChanges finalizes a transaction whose lock is already held by its writer.
func FinishChanges(ctx context.Context, tx pgx.Tx, changes []Change) (Position, error) {
	entry, err := ProjectChanges(ctx, tx, changes)
	if err != nil {
		return Position{}, err
	}
	return finishPolicy(ctx, tx, entry, changes)
}
