package service

import (
	"context"
	"encoding/json"
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

// changePollInterval is how often WaitChanges looks at the log again.
const changePollInterval = 250 * time.Millisecond

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
// up to wait for the first one. It polls the log itself, so the records of every replica are seen.
func (a *App) WaitChanges(ctx context.Context, f ChangeFilter, wait time.Duration) ([]model.ChangeRecord, error) {
	deadline := time.Now().Add(wait)
	for {
		out, err := a.ListChanges(ctx, f)
		if err != nil || len(out) > 0 || !time.Now().Before(deadline) {
			return out, err
		}
		select {
		case <-ctx.Done():
			return out, nil
		case <-time.After(changePollInterval):
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
