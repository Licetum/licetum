package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// Actions of the change log.
const (
	actionCreate = "create"
	actionUpdate = "update"
	actionDelete = "delete"
)

// change is one record of the change log: the entity a write touched, and what it was and what it became.
type change struct {
	entity    string
	entityID  uuid.UUID
	serviceID *uuid.UUID // the service the entity belongs to; nil for the platform-wide entities
	action    string
	before    any // nil for a create
	after     any // nil for a delete
}

// batchKey marks a context whose writes all run in one transaction (see RunBatch).
type batchKey struct{}

// batchRun is the state of a running batch: its transaction, and whether a write in it can take admin access away
// (checked once, when all the operations have been applied).
type batchRun struct {
	tx           pgx.Tx
	adminTouched bool
	changes      []change
}

// withBatch returns a context whose writes run in tx, the transaction of a batch.
func withBatch(ctx context.Context, tx pgx.Tx) (context.Context, *batchRun) {
	run := &batchRun{tx: tx}
	return context.WithValue(ctx, batchKey{}, run), run
}

func currentBatch(ctx context.Context) *batchRun {
	run, _ := ctx.Value(batchKey{}).(*batchRun)
	return run
}

// mutate runs a write in one transaction together with its change-log record and the bump of the revision of the rules
// (db.BumpRevision): the revision commits with the write, so it cannot reach a replica before the data does. fn does its
// statements through q (the transaction) and returns the change it made. Once the transaction has committed, the process
// is told (App.AfterWrite). Inside a batch the write joins the batch's transaction instead, and
// the batch bumps the revision once.
func (a *App) mutate(ctx context.Context, fn func(context.Context, db.Querier) (change, error)) error {
	if run := currentBatch(ctx); run != nil {
		c, err := fn(ctx, run.tx)
		if err != nil {
			return err
		}
		if protectsAdminAccess(c) {
			run.adminTouched = true
		}
		return recordChange(ctx, run.tx, c)
	}
	position, err := db.PolicyTx(ctx, a.DB, nil, func(ctx context.Context, tx pgx.Tx) ([]db.Change, error) {
		bctx, run := withBatch(ctx, tx)
		c, err := fn(bctx, tx)
		if err != nil {
			return nil, err
		}
		if protectsAdminAccess(c) || run.adminTouched {
			if err := a.guardAdminAccess(bctx, tx); err != nil {
				return nil, err
			}
		}
		if err := recordChange(bctx, tx, c); err != nil {
			return nil, err
		}
		return publicationChanges(bctx, run.changes)
	})
	if err != nil {
		return err
	}
	a.AfterWrite(ctx, position.Revision, position.Epoch)
	return nil
}

// recordChange writes one change-log record through q. The actor is the subject of the request.
func recordChange(ctx context.Context, q db.Querier, c change) error {
	args, err := changeArgs(ctx, c)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, insertChangeSQL, args...)
	if err == nil {
		if run := currentBatch(ctx); run != nil {
			run.changes = append(run.changes, c)
		}
	}
	return err
}

const insertChangeSQL = `
	INSERT INTO authz_change (actor, service_id, entity, entity_id, action, before, after)
	VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)`

func changeArgs(ctx context.Context, c change) ([]any, error) {
	before, err := snapshot(c.before)
	if err != nil {
		return nil, err
	}
	after, err := snapshot(c.after)
	if err != nil {
		return nil, err
	}
	return []any{engine.ActorFrom(ctx), engine.ToNullUUID(c.serviceID), c.entity, uuidOrNull(c.entityID), c.action, before, after}, nil
}

// publicationChanges keeps the historical API entity and the canonical database row
// distinct. Compound writes report each affected row, including removed links.
func publicationChanges(ctx context.Context, changes []change) ([]db.Change, error) {
	out := make([]db.Change, 0, len(changes))
	for _, c := range changes {
		before, err := snapshot(c.before)
		if err != nil {
			return nil, err
		}
		after, err := snapshot(c.after)
		if err != nil {
			return nil, err
		}
		table := c.entity
		if table == "group" {
			table = "user_group"
		}
		owner := c.serviceID
		if owner == nil && table != "log_settings" {
			id := model.PlatformServiceID
			owner = &id
		}
		key := map[string]any{"id": c.entityID}
		var id *uuid.UUID
		if c.entityID != uuid.Nil {
			v := c.entityID
			id = &v
		} else if table == "log_settings" {
			key["id"] = 1
		} else {
			return nil, fmt.Errorf("missing row id for %s", table)
		}
		change := db.Change{Entity: c.entity, ID: id, ServiceID: c.serviceID, Actor: engine.ActorFrom(ctx), Action: c.action, Touched: []db.Touched{{Table: table, Key: key, ServiceID: owner, Deleted: c.action == actionDelete}}}
		if before != nil {
			change.Before = json.RawMessage(*before)
		}
		if after != nil {
			change.After = json.RawMessage(*after)
		}
		if table == "type_resource" {
			parents := map[uuid.UUID]bool{}
			for i, v := range []any{c.before, c.after} {
				raw, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				var row struct {
					Parents []model.TypeParent `json:"parents"`
				}
				if err := json.Unmarshal(raw, &row); err != nil {
					return nil, err
				}
				for _, p := range row.Parents {
					parents[p.TypeResourceID] = i == 1
				}
			}
			for parent, present := range parents {
				change.Touched = append(change.Touched, db.Touched{Table: "type_resource_parent", Key: map[string]any{"type_resource_id": c.entityID, "parent_id": parent}, ServiceID: owner, Deleted: !present})
			}
		}
		out = append(out, change)
	}
	return out, nil
}

// snapshot is the JSON of an entity for the change log (NULL for none).
func snapshot(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s := string(raw)
	return &s, nil
}

// uuidOrNull is NULL for the zero id (an entity without one, such as the log settings).
func uuidOrNull(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

// serviceOfType returns the service that owns a resource type (nil when it has none).
func serviceOfType(ctx context.Context, q db.Querier, typeResourceID uuid.UUID) (*uuid.UUID, error) {
	var serviceID uuid.UUID
	err := q.QueryRow(ctx, `SELECT service_id FROM type_resource WHERE id = $1`, typeResourceID).Scan(&serviceID)
	if err != nil {
		return nil, err
	}
	return &serviceID, nil
}

// idTaken is the conflict of a create whose given id belongs to another entity (see db.IsPrimaryKeyViolation): the
// caller picks another id, or none, and the write is refused as a whole.
func idTaken(id *uuid.UUID) error {
	var s string
	if id != nil {
		s = id.String()
	}
	return apperr.Conflict(i18n.EntityIDTaken, s)
}
