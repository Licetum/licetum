package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// ChangeFilter selects the records of the change log: those after a position, optionally of one service or of one kind
// of entity.
type ChangeFilter struct {
	After   int64
	Service *uuid.UUID
	Entity  string
	Limit   int
}

// changeHeadEvery is the most often a process asks the log where it ends, however many clients wait on it.
const changeHeadEvery = time.Second

// changeHead caches the id of the last record of the change log for changeHeadEvery.
type changeHead struct {
	mu   sync.Mutex
	id   int64
	read time.Time
	err  error // the failure of the reading at read: it is the answer for changeHeadEvery too, so a down database is asked once
}

// get returns the id of the last record, at most changeHeadEvery old, and when it was read.
func (h *changeHead) get(ctx context.Context, a *App) (int64, time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.read.IsZero() && time.Since(h.read) < changeHeadEvery {
		return h.id, h.read, h.err
	}
	// The query serves every waiter of the process, so it does not end with the request that happened to run it.
	qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	started := time.Now()
	var id int64
	if err := a.DB.QueryRow(qctx, `SELECT COALESCE(max(id), 0) FROM authz_change`).Scan(&id); err != nil {
		h.read, h.err = started, err
		return 0, started, err
	}
	h.id, h.read, h.err = id, started, nil
	return id, started, nil
}

// changeColumns are the columns collectChanges reads.
const changeColumns = `id, at, actor, service_id, entity, entity_id, action, before, after`

// ListChanges returns the records of the change log in order, starting after the position f.After.
func (a *App) ListChanges(ctx context.Context, f ChangeFilter) ([]model.ChangeRecord, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT `+changeColumns+`
		FROM authz_change
		WHERE id > $1 AND ($2::uuid IS NULL OR service_id = $2) AND ($3 = '' OR entity = $3)
		ORDER BY id
		LIMIT $4`, f.After, engine.ToNullUUID(f.Service), f.Entity, f.Limit)
	if err != nil {
		return nil, err
	}
	return collectChanges(rows)
}

// WaitChanges is ListChanges for a client that follows the log: when no record comes after the position yet, it waits
// up to wait for the first one. The waiters of a process share one query for the end of the log, asked at most once a
// second; a waiter reads its own records only when the log has moved past what it has seen. The records of every
// replica are seen, because the log is the table.
func (a *App) WaitChanges(ctx context.Context, f ChangeFilter, wait time.Duration) ([]model.ChangeRecord, error) {
	if wait <= 0 {
		return a.ListChanges(ctx, f)
	}
	deadline := time.Now().Add(wait)
	seen := f.After // the log is known to hold nothing of interest in (f.After, seen]
	for {
		head, read, err := a.changes.get(ctx, a)
		if err != nil {
			return nil, err
		}
		if head > seen {
			out, err := a.ListChanges(ctx, f)
			if err != nil || len(out) > 0 {
				return out, err
			}
			// Nothing for this filter up to the head read above (the ListChanges ran later, so it covers it).
			seen = head
		}
		next := read.Add(changeHeadEvery)
		if !time.Now().Before(deadline) {
			return []model.ChangeRecord{}, nil
		}
		timer := time.NewTimer(min(time.Until(next), time.Until(deadline)) + time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return []model.ChangeRecord{}, nil
		case <-timer.C:
		}
	}
}

// getChange reads one record of the change log.
func (a *App) getChange(ctx context.Context, id int64) (model.ChangeRecord, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+changeColumns+` FROM authz_change WHERE id = $1`, id)
	if err != nil {
		return model.ChangeRecord{}, err
	}
	out, err := collectChanges(rows)
	if err != nil {
		return model.ChangeRecord{}, err
	}
	if len(out) == 0 {
		return model.ChangeRecord{}, apperr.NotFound(i18n.ChangeNotFound, id)
	}
	return out[0], nil
}

// changesAfter reads the records after a position, newest first: the order in which they are undone.
func (a *App) changesAfter(ctx context.Context, position int64) ([]model.ChangeRecord, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+changeColumns+` FROM authz_change WHERE id > $1 ORDER BY id DESC`, position)
	if err != nil {
		return nil, err
	}
	return collectChanges(rows)
}

// collectChanges reads the rows of changeColumns.
func collectChanges(rows pgx.Rows) ([]model.ChangeRecord, error) {
	defer rows.Close()
	out := []model.ChangeRecord{}
	for rows.Next() {
		var c model.ChangeRecord
		var serviceID, entityID uuid.NullUUID
		var before, after []byte
		if err := rows.Scan(&c.ID, &c.At, &c.Actor, &serviceID, &c.Entity, &entityID, &c.Action, &before, &after); err != nil {
			return nil, err
		}
		c.ServiceID = engine.NullUUID(serviceID)
		c.EntityID = engine.NullUUID(entityID)
		var err error
		if c.Before, err = decodeSnapshot(before); err != nil {
			return nil, err
		}
		if c.After, err = decodeSnapshot(after); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// decodeSnapshot reads an entity of the change log (nil for none).
func decodeSnapshot(raw []byte) (any, error) {
	if raw == nil {
		return nil, nil
	}
	var v any
	err := json.Unmarshal(raw, &v)
	return v, err
}
