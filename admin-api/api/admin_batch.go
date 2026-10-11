package api

import (
	"context"
	"net/http"

	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// ChangeIDPath names a record of the change log.
type ChangeIDPath struct {
	ChangeID int64 `path:"change_id" minimum:"1" doc:"Id of the change record (see GET /changes)"`
}

// registerBatch registers the batch executor and the history of the policy: reverting one change, and rolling back
// to a position of the change log. A batch and a history write are checked operation by operation, with the policy
// of the admin route that does the same write.
func (h *adminHandlers) registerBatch(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "run_batch",
		Method:      http.MethodPost,
		Path:        "/batch",
		Summary:     "Apply several writes in one transaction",
		Description: "Applies the operations in order, in one transaction: all of them take effect, or none. Each operation is a create, an update or a delete of an assignment rule, a group, a group member, a role, a role item or an access group, with the same body its endpoint takes. The caller needs the policy of each write, as on the admin routes. A failing operation answers with its index in extra.operation, and nothing is applied. The change log records every operation, and the cache is invalidated once, after the commit.",
		Errors:      []int{http.StatusConflict, http.StatusUnprocessableEntity},
		Tags:        []string{"Batch"},
	}, signedIn()), func(ctx context.Context, in *struct{ Body model.BatchInput }) (*server.Out[model.BatchResponse], error) {
		if err := service.CheckOperations(in.Body.Operations); err != nil {
			return nil, err
		}
		if err := authorizeOperations(ctx, h.app, in.Body.Operations); err != nil {
			return nil, err
		}
		results, err := h.app.RunBatch(ctx, in.Body.Operations)
		if err != nil {
			return nil, err
		}
		return server.OK(model.BatchResponse{Results: results})
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "revert_change",
		Method:      http.MethodPost,
		Path:        "/changes/{change_id}/revert",
		Summary:     "Revert one change",
		Description: "Puts the entity of one record of the change log back as it was before the change: a create is deleted, a delete is created again with its id (and, for a role, its items), an update is written back with its previous body. The revert is a change of its own, in the log. Only the entities a batch writes can be reverted (409 otherwise). With dry_run, the operation is returned and nothing is applied.",
		Errors:      []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
		Tags:        []string{"Batch"},
	}, signedIn()), func(ctx context.Context, in *struct {
		ChangeIDPath
		Body model.RevertInput
	}) (*server.Out[model.RevertResponse], error) {
		ops, err := h.app.PlanRevert(ctx, in.ChangeID)
		if err != nil {
			return nil, err
		}
		return applyPlan(ctx, h.app, ops, in.Body.DryRun)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "rollback_changes",
		Method:      http.MethodPost,
		Path:        "/changes/rollback",
		Summary:     "Roll the policy back to a position of the change log",
		Description: "Undoes every change after the position `to`, newest first, so that the policy goes back to what it was at that position. All of it happens in one transaction: if one change cannot be undone (a change to a service, a resource type or a synchronizer is not a batch entity), nothing is applied and the answer is 409. The undo is recorded in the change log like any write. With dry_run, the operations are returned and nothing is applied.",
		Errors:      []int{http.StatusConflict, http.StatusUnprocessableEntity},
		Tags:        []string{"Batch"},
	}, signedIn()), func(ctx context.Context, in *struct{ Body model.RollbackInput }) (*server.Out[model.RevertResponse], error) {
		ops, err := h.app.PlanRollback(ctx, in.Body.To)
		if err != nil {
			return nil, err
		}
		return applyPlan(ctx, h.app, ops, in.Body.DryRun)
	})
}

// applyPlan checks the caller may write what the operations write, then applies them (or, for a dry run, only returns them).
func applyPlan(ctx context.Context, app *service.App, ops []model.BatchOperation, dryRun bool) (*server.Out[model.RevertResponse], error) {
	if ops == nil {
		ops = []model.BatchOperation{}
	}
	if err := service.CheckOperations(ops); err != nil {
		return nil, err
	}
	if err := authorizeOperations(ctx, app, ops); err != nil {
		return nil, err
	}
	out := model.RevertResponse{DryRun: dryRun, Operations: ops, Results: []model.BatchResult{}}
	if dryRun || len(ops) == 0 {
		return server.OK(out)
	}
	results, err := app.RunBatch(ctx, ops)
	if err != nil {
		return nil, err
	}
	out.Results = results
	return server.OK(out)
}

// authorizeOperations checks that the caller may do every operation: the first one it may not do refuses the lot. The
// access of the caller was read before the batch, so an entity the batch creates is not in it yet: the operations on
// that entity are allowed by the right that created it (the whole type).
func authorizeOperations(ctx context.Context, app *service.App, ops []model.BatchOperation) error {
	access := accessFrom(ctx)
	created := map[uuid.UUID]bool{}
	for _, op := range ops {
		if !operationAllowed(access, op, created) {
			return apperr.Forbidden(i18n.BatchForbidden, op.Entity)
		}
		if err := authorizeOperationTargets(ctx, app, op, created); err != nil {
			return err
		}
		if op.Op == model.BatchCreate && op.ID != nil {
			created[*op.ID] = true
		}
	}
	return nil
}

// operationAllowed applies to an operation the policy of the admin route that does the same write. Creating a group,
// a member, a rule or a role needs write on the whole type; changing or deleting one needs write on it. A role item is
// the role's (write on the role), and an access group is the resource type's (write on the type). created holds the
// entities the batch created before this operation.
func operationAllowed(access *service.Access, op model.BatchOperation, created map[uuid.UUID]bool) bool {
	if access == nil {
		return false
	}
	var id, parent uuid.UUID
	if op.ID != nil {
		id = *op.ID
	}
	if op.Parent != nil {
		parent = *op.Parent
	}
	var typ model.PlatformType
	var target uuid.UUID
	switch op.Entity {
	case model.BatchAssignmentRule:
		typ, target = model.PlatformAssignmentRule, id
	case model.BatchGroup:
		typ, target = model.PlatformGroup, id
	case model.BatchGroupMember:
		typ, target = model.PlatformGroupMember, id
	case model.BatchRole:
		typ, target = model.PlatformRole, id
	case model.BatchRolePermission:
		typ, target = model.PlatformRole, parent
	case model.BatchAccessGroup:
		typ, target = model.PlatformTypeResource, parent
	default:
		return false
	}
	belongsToParent := op.Entity == model.BatchRolePermission || op.Entity == model.BatchAccessGroup
	switch {
	case op.Op == model.BatchCreate && !belongsToParent, created[target]:
		return access.CanWholeType(typ, model.ActionWrite)
	}
	return access.Can(typ, model.ActionWrite, target.String())
}
