package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// RunBatch applies the operations in one transaction, in order: all of them take effect, or none. An operation sees
// the writes of the ones before it. An operation that fails is reported with its index (apperr.Error.AtOperation).
// The revision of the rules is bumped once, in the same transaction.
func (a *App) RunBatch(ctx context.Context, ops []model.BatchOperation) ([]model.BatchResult, error) {
	if err := CheckOperations(ops); err != nil {
		return nil, err
	}
	results := make([]model.BatchResult, 0, len(ops))
	var revision int64
	err := db.InTx(ctx, a.DB, func(tx pgx.Tx) error {
		bctx, run := withBatch(ctx, tx)
		for i, op := range ops {
			id, err := a.applyOperation(bctx, op)
			if err != nil {
				if e, ok := apperr.As(err); ok {
					return e.AtOperation(i)
				}
				return err
			}
			results = append(results, model.BatchResult{Index: i, Op: op.Op, Entity: op.Entity, ID: id})
		}
		// The operations may replace the admin access one by the other; what counts is what is left at the end.
		if run.adminTouched {
			if err := a.guardAdminAccess(ctx, tx); err != nil {
				return err
			}
		}
		if len(ops) == 0 {
			return nil
		}
		var err error
		revision, err = db.BumpRevision(ctx, tx, db.ScopeRules)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(ops) > 0 {
		a.AfterWrite(ctx, revision)
	}
	return results, nil
}

// CheckOperations checks the shape of the operations before anything is written or authorized: the operation and the
// kind of entity are known, an update and a delete name their entity, and the kinds that belong to another entity
// name it. The error names the operation that is wrong (apperr.Error.Extra.Operation).
func CheckOperations(ops []model.BatchOperation) error {
	for i, op := range ops {
		if err := checkOperation(op); err != nil {
			if e, ok := apperr.As(err); ok {
				return e.AtOperation(i)
			}
			return err
		}
	}
	return nil
}

func checkOperation(op model.BatchOperation) error {
	switch op.Op {
	case model.BatchCreate, model.BatchUpdate, model.BatchDelete:
	default:
		return apperr.Unprocessable(i18n.BatchOperationInvalid, op.Op)
	}
	switch op.Entity {
	case model.BatchAssignmentRule, model.BatchGroup, model.BatchRole:
	case model.BatchGroupMember, model.BatchRolePermission, model.BatchAccessGroup:
		if op.Parent == nil {
			return apperr.Unprocessable(i18n.BatchParentRequired, op.Entity)
		}
	default:
		return apperr.Unprocessable(i18n.BatchOperationInvalid, op.Entity)
	}
	if op.Op != model.BatchCreate && op.ID == nil {
		return apperr.Unprocessable(i18n.BatchIDRequired)
	}
	return nil
}

// entityOps are the writes of one kind of entity, through the functions of its admin API. parent is the entity the
// operation belongs to (uuid.Nil when the kind has none).
type entityOps[T any] struct {
	create func(ctx context.Context, parent uuid.UUID, in *T, id *uuid.UUID) (uuid.UUID, error)
	update func(ctx context.Context, parent, id uuid.UUID, in *T) (uuid.UUID, error)
	remove func(ctx context.Context, parent, id uuid.UUID) (uuid.UUID, error)
}

// applyOperation runs one operation through the function the admin API uses for it, and returns the id of the entity.
func (a *App) applyOperation(ctx context.Context, op model.BatchOperation) (uuid.UUID, error) {
	// CheckOperations has checked the shape: an update or a delete has its id, and a kind with a parent has it.
	var id, parent uuid.UUID
	if op.ID != nil {
		id = *op.ID
	}
	if op.Parent != nil {
		parent = *op.Parent
	}

	switch op.Entity {
	case model.BatchAssignmentRule:
		return runEntity(ctx, op, id, parent, entityOps[model.AssignmentRuleInput]{
			create: func(ctx context.Context, _ uuid.UUID, in *model.AssignmentRuleInput, id *uuid.UUID) (uuid.UUID, error) {
				r, err := a.createAssignmentRule(ctx, in, id)
				if err != nil {
					return uuid.Nil, err
				}
				return r.ID, nil
			},
			update: func(ctx context.Context, _, id uuid.UUID, in *model.AssignmentRuleInput) (uuid.UUID, error) {
				_, err := a.UpdateAssignmentRule(ctx, id, in)
				return id, err
			},
			remove: func(ctx context.Context, _, id uuid.UUID) (uuid.UUID, error) {
				return a.DeleteAssignmentRule(ctx, id)
			},
		})
	case model.BatchGroup:
		return runEntity(ctx, op, id, parent, entityOps[model.GroupInput]{
			create: func(ctx context.Context, _ uuid.UUID, in *model.GroupInput, id *uuid.UUID) (uuid.UUID, error) {
				g, err := a.createGroup(ctx, in, id)
				if err != nil {
					return uuid.Nil, err
				}
				return g.ID, nil
			},
			update: func(ctx context.Context, _, id uuid.UUID, in *model.GroupInput) (uuid.UUID, error) {
				_, err := a.UpdateGroup(ctx, id, in)
				return id, err
			},
			remove: func(ctx context.Context, _, id uuid.UUID) (uuid.UUID, error) {
				return a.DeleteGroup(ctx, id)
			},
		})
	case model.BatchGroupMember:
		return runEntity(ctx, op, id, parent, entityOps[model.GroupMemberInput]{
			create: func(ctx context.Context, group uuid.UUID, in *model.GroupMemberInput, id *uuid.UUID) (uuid.UUID, error) {
				m, err := a.createGroupMember(ctx, group, in, id)
				if err != nil {
					return uuid.Nil, err
				}
				return m.ID, nil
			},
			update: func(ctx context.Context, group, id uuid.UUID, in *model.GroupMemberInput) (uuid.UUID, error) {
				_, err := a.UpdateGroupMember(ctx, group, id, in)
				return id, err
			},
			remove: func(ctx context.Context, group, id uuid.UUID) (uuid.UUID, error) {
				return a.DeleteGroupMember(ctx, group, id)
			},
		})
	case model.BatchRole:
		return runEntity(ctx, op, id, parent, entityOps[model.RoleInput]{
			create: func(ctx context.Context, _ uuid.UUID, in *model.RoleInput, id *uuid.UUID) (uuid.UUID, error) {
				r, err := a.createRole(ctx, in, id)
				if err != nil {
					return uuid.Nil, err
				}
				return r.ID, nil
			},
			update: func(ctx context.Context, _, id uuid.UUID, in *model.RoleInput) (uuid.UUID, error) {
				_, err := a.UpdateRole(ctx, id, in)
				return id, err
			},
			remove: func(ctx context.Context, _, id uuid.UUID) (uuid.UUID, error) {
				return a.DeleteRole(ctx, id)
			},
		})
	case model.BatchRolePermission:
		return runEntity(ctx, op, id, parent, entityOps[model.RolePermissionInput]{
			create: func(ctx context.Context, role uuid.UUID, in *model.RolePermissionInput, id *uuid.UUID) (uuid.UUID, error) {
				p, err := a.createRolePermission(ctx, role, in, id)
				if err != nil {
					return uuid.Nil, err
				}
				return p.ID, nil
			},
			update: func(ctx context.Context, role, id uuid.UUID, in *model.RolePermissionInput) (uuid.UUID, error) {
				_, err := a.UpdateRolePermission(ctx, role, id, in)
				return id, err
			},
			remove: func(ctx context.Context, role, id uuid.UUID) (uuid.UUID, error) {
				return a.DeleteRolePermission(ctx, role, id)
			},
		})
	case model.BatchAccessGroup:
		return runEntity(ctx, op, id, parent, entityOps[model.AccessGroupInput]{
			create: func(ctx context.Context, typeID uuid.UUID, in *model.AccessGroupInput, id *uuid.UUID) (uuid.UUID, error) {
				g, err := a.createAccessGroup(ctx, typeID, in, id)
				if err != nil {
					return uuid.Nil, err
				}
				return g.ID, nil
			},
			update: func(ctx context.Context, typeID, id uuid.UUID, in *model.AccessGroupInput) (uuid.UUID, error) {
				_, err := a.UpdateAccessGroup(ctx, typeID, id, in)
				return id, err
			},
			remove: func(ctx context.Context, typeID, id uuid.UUID) (uuid.UUID, error) {
				return a.DeleteAccessGroup(ctx, typeID, id)
			},
		})
	}
	return uuid.Nil, apperr.Unprocessable(i18n.BatchOperationInvalid, op.Entity)
}

// runEntity runs an operation on an entity whose input is T: the body of a create or an update is read into the input
// and validated (when the input validates itself), then the function of the operation runs.
func runEntity[T any](ctx context.Context, op model.BatchOperation, id, parent uuid.UUID, e entityOps[T]) (uuid.UUID, error) {
	switch op.Op {
	case model.BatchDelete:
		return e.remove(ctx, parent, id)
	case model.BatchCreate, model.BatchUpdate:
		in, err := decodeBody[T](op.Body)
		if err != nil {
			return uuid.Nil, err
		}
		if v, ok := any(in).(model.Validator); ok {
			if err := v.Validate(); err != nil {
				return uuid.Nil, err
			}
		}
		if op.Op == model.BatchUpdate {
			return e.update(ctx, parent, id, in)
		}
		return e.create(ctx, parent, in, op.ID)
	}
	return uuid.Nil, apperr.Unprocessable(i18n.BatchOperationInvalid, op.Op)
}

// DecodeOperationBody reads the body of a batch operation as the input of its entity, the way the batch applies it, so
// that the API checks the very input the batch writes.
func DecodeOperationBody[T any](body any) (*T, error) {
	return decodeBody[T](body)
}

// decodeBody reads the body of an operation into the input of its entity: the JSON the endpoint takes.
func decodeBody[T any](body any) (*T, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, apperr.Unprocessable(i18n.BatchBodyInvalid, err)
	}
	in := new(T)
	if err := json.Unmarshal(raw, in); err != nil {
		return nil, apperr.Unprocessable(i18n.BatchBodyInvalid, err)
	}
	return in, nil
}
