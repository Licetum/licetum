package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The targets of rules and of role items are checked alike by the single routes and by a batch. A rule or an item
// grants access on its target, so one on a platform policy (a self-service type) needs write on that policy as a whole:
// a point write on the entity does not reach it. A change of the target of a rule needs what creating a rule needs,
// write on the rules policy as a whole. A point write on a rule manages who it applies to and when, not what it covers.

// authorizePlatformTargets checks that the caller may write, as a whole, every platform policy the types are on.
func authorizePlatformTargets(ctx context.Context, app *service.App, typeIDs ...uuid.UUID) error {
	for _, typeID := range typeIDs {
		policy, isPlatform, err := app.PlatformPolicyOfType(ctx, typeID)
		if err != nil {
			return err
		}
		if isPlatform && !accessFrom(ctx).CanWholeType(policy, model.ActionWrite) {
			return apperr.Forbidden(i18n.AccessDenied, model.ActionWrite, policy)
		}
	}
	return nil
}

// idsOf lists the ids the pointers hold, leaving out the nil ones.
func idsOf(ids ...*uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != nil {
			out = append(out, *id)
		}
	}
	return out
}

// authorizeRuleCreate checks a new rule: the platform policy its type is on, if it is one.
func authorizeRuleCreate(ctx context.Context, app *service.App, in *model.AssignmentRuleInput) error {
	if err := authorizePlatformTargets(ctx, app, idsOf(in.TypeResourceID)...); err != nil {
		return err
	}
	return authorizeRoleItems(ctx, app, in.RoleID)
}

// authorizeRoleItems checks the platform policies the items of a role are on. A rule that grants the role grants them
// too, so a point write on such a rule does not reach to changing who it applies to.
func authorizeRoleItems(ctx context.Context, app *service.App, roleID *uuid.UUID) error {
	if roleID == nil {
		return nil
	}
	items, err := app.ListRolePermissions(ctx, *roleID)
	if err != nil {
		// A role the batch creates is not there yet: the items it gets are checked as they are created.
		var notFound *apperr.Error
		if errors.As(err, &notFound) && notFound.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	for _, item := range items {
		if err := authorizePlatformTargets(ctx, app, item.TypeResourceID); err != nil {
			return err
		}
	}
	return nil
}

// authorizeRuleUpdate checks a change of a rule. old is the rule as it is stored, or nil for a rule the batch created
// earlier: the create checked its target, and its caller held the whole-type right that the create needed.
func authorizeRuleUpdate(ctx context.Context, app *service.App, old *model.AssignmentRule, in *model.AssignmentRuleInput) error {
	targets := idsOf(in.TypeResourceID)
	roles := []*uuid.UUID{in.RoleID}
	if old != nil {
		targets = append(targets, idsOf(old.TypeResourceID)...)
		roles = append(roles, old.RoleID)
	}
	if err := authorizePlatformTargets(ctx, app, targets...); err != nil {
		return err
	}
	for _, role := range roles {
		if err := authorizeRoleItems(ctx, app, role); err != nil {
			return err
		}
	}
	if old != nil && service.RetargetsRule(old, in) && !accessFrom(ctx).CanWholeType(model.PlatformAssignmentRule, model.ActionWrite) {
		return apperr.Forbidden(i18n.AccessDenied, model.ActionWrite, model.PlatformAssignmentRule)
	}
	return nil
}

// authorizeRuleDelete checks the removal of a rule: a rule on a platform policy needs that policy as a whole.
func authorizeRuleDelete(ctx context.Context, app *service.App, old *model.AssignmentRule) error {
	if err := authorizePlatformTargets(ctx, app, idsOf(old.TypeResourceID)...); err != nil {
		return err
	}
	return authorizeRoleItems(ctx, app, old.RoleID)
}

// operationInput reads the input of a create or an update of a batch the way the batch applies it: decoded, then
// validated, so that the checks see the defaults the write gets.
func operationInput[T any](body any) (*T, error) {
	in, err := service.DecodeOperationBody[T](body)
	if err != nil {
		return nil, err
	}
	if v, ok := any(in).(model.Validator); ok {
		if err := v.Validate(); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// authorizeOperationTargets makes, for one operation of a batch, the checks of the single routes that write the same
// entity. An entity the batch created earlier is not read back: the create of it made the checks.
func authorizeOperationTargets(ctx context.Context, app *service.App, op model.BatchOperation, created map[uuid.UUID]bool) error {
	var id, parent uuid.UUID
	if op.ID != nil {
		id = *op.ID
	}
	if op.Parent != nil {
		parent = *op.Parent
	}
	switch op.Entity {
	case model.BatchAssignmentRule:
		if op.Op == model.BatchDelete {
			if created[id] {
				return nil
			}
			old, err := app.AssignmentRuleOf(ctx, id)
			if err != nil {
				return err
			}
			return authorizeRuleDelete(ctx, app, old)
		}
		in, err := operationInput[model.AssignmentRuleInput](op.Body)
		if err != nil {
			// The batch refuses an invalid operation with its index when it applies it, and nothing is written.
			return nil
		}
		if op.Op == model.BatchCreate {
			return authorizeRuleCreate(ctx, app, in)
		}
		var old *model.AssignmentRule
		if !created[id] {
			if old, err = app.AssignmentRuleOf(ctx, id); err != nil {
				return err
			}
		}
		return authorizeRuleUpdate(ctx, app, old, in)
	case model.BatchRolePermission:
		if op.Op == model.BatchDelete {
			if created[id] {
				return nil
			}
			typeID, err := app.RolePermissionTarget(ctx, parent, id)
			if err != nil {
				return err
			}
			return authorizePlatformTargets(ctx, app, typeID)
		}
		in, err := operationInput[model.RolePermissionInput](op.Body)
		if err != nil {
			return nil
		}
		targets := []uuid.UUID{in.TypeResourceID}
		if op.Op == model.BatchUpdate && !created[id] {
			typeID, err := app.RolePermissionTarget(ctx, parent, id)
			if err != nil {
				return err
			}
			targets = append(targets, typeID)
		}
		return authorizePlatformTargets(ctx, app, targets...)
	}
	return nil
}
