package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// revertibleEntity are the kinds of entity a change can be undone for: the kinds a batch writes. Each of their change
// records holds the entity as it was and as it became, which is what an undo needs.
var revertibleEntity = map[string]bool{
	model.BatchAssignmentRule: true,
	model.BatchGroup:          true,
	model.BatchGroupMember:    true,
	model.BatchRole:           true,
	model.BatchRolePermission: true,
	model.BatchAccessGroup:    true,
}

// PlanRevert returns the operations that put the entity of one change back as it was before the change. It applies
// nothing: RunBatch does, once the caller may write what the operations write.
func (a *App) PlanRevert(ctx context.Context, changeID int64) ([]model.BatchOperation, error) {
	c, err := a.getChange(ctx, changeID)
	if err != nil {
		return nil, err
	}
	return undoOperations(c)
}

// PlanRollback returns the operations that undo every change after the position, newest first, so that the policy goes
// back to what it was at that position. It applies nothing. A change to an entity that a batch does not write (a
// service, a resource type, a synchronizer) cannot be undone: the whole rollback is refused, rather than done in part.
func (a *App) PlanRollback(ctx context.Context, position int64) ([]model.BatchOperation, error) {
	changes, err := a.changesAfter(ctx, position)
	if err != nil {
		return nil, err
	}
	var ops []model.BatchOperation
	for _, c := range changes {
		undo, err := undoOperations(c)
		if err != nil {
			return nil, err
		}
		ops = append(ops, undo...)
	}
	return ops, nil
}

// undoOperations returns the operations that put the entity of one change back as it was before the change: a create
// is deleted, a delete is created again with its id, and an update is written back with its previous body.
func undoOperations(c model.ChangeRecord) ([]model.BatchOperation, error) {
	if c.EntityID == nil || !revertibleEntity[c.Entity] {
		return nil, apperr.Conflict(i18n.ChangeNotRevertible, c.ID, c.Entity)
	}
	id := *c.EntityID
	switch c.Action {
	case actionCreate:
		return []model.BatchOperation{{Op: model.BatchDelete, Entity: c.Entity, ID: &id,
			Parent: parentOfSnapshot(c.Entity, c.After)}}, nil
	case actionUpdate:
		return []model.BatchOperation{{Op: model.BatchUpdate, Entity: c.Entity, ID: &id,
			Parent: parentOfSnapshot(c.Entity, c.Before), Body: c.Before}}, nil
	default:
		return []model.BatchOperation{{Op: model.BatchCreate, Entity: c.Entity, ID: &id,
			Parent: parentOfSnapshot(c.Entity, c.Before), Body: c.Before}}, nil
	}
}

// parentOfSnapshot reads the parent of an entity from its snapshot: the group of a group member, the role of an item,
// the resource type of an access group. Nil for the kinds that have none.
func parentOfSnapshot(entity string, snapshot any) *uuid.UUID {
	m, ok := snapshot.(map[string]any)
	if !ok {
		return nil
	}
	var field string
	switch entity {
	case model.BatchGroupMember:
		field = "group_id"
	case model.BatchRolePermission:
		field = "role_id"
	case model.BatchAccessGroup:
		field = "type_resource_id"
	default:
		return nil
	}
	s, _ := m[field].(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}
