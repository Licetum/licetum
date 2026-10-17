package service

import (
	"context"
	"encoding/json"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
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
// and the previous release are told (App.AfterWrite). Inside a batch the write joins the batch's transaction instead, and
// the batch bumps the revision once.
func (a *App) mutate(ctx context.Context, fn func(q db.Querier) (change, error)) error {
	if run := currentBatch(ctx); run != nil {
		c, err := fn(run.tx)
		if err != nil {
			return err
		}
		if protectsAdminAccess(c) {
			run.adminTouched = true
		}
		return recordChange(ctx, run.tx, c)
	}
	var revision int64
	err := db.InTx(ctx, a.DB, func(tx pgx.Tx) error {
		c, err := fn(tx)
		if err != nil {
			return err
		}
		if protectsAdminAccess(c) {
			if err := a.guardAdminAccess(ctx, tx); err != nil {
				return err
			}
		}
		revision, err = recordChangeAndBump(ctx, tx, c)
		return err
	})
	if err != nil {
		return err
	}
	a.AfterWrite(ctx, revision)
	return nil
}

// recordChange writes one change-log record through q. The actor is the subject of the request.
func recordChange(ctx context.Context, q db.Querier, c change) error {
	args, err := changeArgs(ctx, c)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, insertChangeSQL, args...)
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

// recordChangeAndBump writes the change-log record and bumps the revision of the rules in one round trip (a batch in the
// transaction tx) and returns the new revision. The statements of a write are sequential round trips to the primary; the
// admin API of a region far from it feels each.
func recordChangeAndBump(ctx context.Context, tx pgx.Tx, c change) (int64, error) {
	args, err := changeArgs(ctx, c)
	if err != nil {
		return 0, err
	}
	b := &pgx.Batch{}
	b.Queue(insertChangeSQL, args...)
	db.QueueBumpRevision(b, db.ScopeRules)
	results := tx.SendBatch(ctx, b)
	defer func() { _ = results.Close() }()
	if _, err := results.Exec(); err != nil {
		return 0, err
	}
	var revision int64
	if err := results.QueryRow().Scan(&revision); err != nil {
		return 0, err
	}
	return revision, results.Close()
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
