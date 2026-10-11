package service

import (
	"context"
	"errors"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// checkResourceTarget: a type without a synchronizer has no resources to point at — a permission on it
// grants access to the type as a whole (no resource_id, no resource_from_claim and no resource_where), and
// /permission returns such a type as not_sync. Used for inline rules and for the items of roles alike.
func (a *App) checkResourceTarget(ctx context.Context, q db.Querier, typeResourceID uuid.UUID, resourceID *string,
	fromClaim *model.ResourceFromClaim, where []model.PredicateCondition) error {
	if resourceID == nil && fromClaim == nil && len(where) == 0 {
		return nil
	}
	ok, err := a.hasSynchronizer(ctx, q, typeResourceID)
	if err != nil || ok {
		return err
	}
	return apperr.BadRequest(i18n.AssignmentRuleNeedsSynchronizer, typeResourceID)
}

// checkRuleTarget checks the target of an inline rule (see checkResourceTarget).
func (a *App) checkRuleTarget(ctx context.Context, q db.Querier, in *model.AssignmentRuleInput) error {
	return a.checkResourceTarget(ctx, q, *in.TypeResourceID, in.ResourceID, in.ResourceFromClaim, in.ResourceWhere)
}

// checkAssignmentRuleRefs checks the referenced entities: the role, or the type with its access groups and the lookup
// type of a claim; the target (see checkRuleTarget) when checkTarget is set; the group the rule is bound to and the
// group the rule is guarded by.
func (a *App) checkAssignmentRuleRefs(ctx context.Context, q db.Querier, in *model.AssignmentRuleInput, checkTarget bool) error {
	if in.RoleID != nil {
		if _, err := a.GetExistingRole(ctx, q, *in.RoleID); err != nil {
			return err
		}
	} else {
		if _, err := a.GetExistingTypeResourceAnyService(ctx, q, *in.TypeResourceID); err != nil {
			return err
		}
		if checkTarget {
			if err := a.checkRuleTarget(ctx, q, in); err != nil {
				return err
			}
		}
		if err := a.checkAccessGroupRefs(ctx, q, *in.TypeResourceID, in.Access); err != nil {
			return err
		}
		if in.ResourceFromClaim != nil {
			if _, err := a.GetExistingTypeResourceAnyService(ctx, q, in.ResourceFromClaim.LookupTypeResourceID); err != nil {
				return err
			}
		}
	}
	if in.MemberOfGroupID != nil {
		if _, err := a.GetExistingGroup(ctx, q, *in.MemberOfGroupID); err != nil {
			return err
		}
	}
	if in.RequiresGroupID != nil {
		if _, err := a.GetExistingGroup(ctx, q, *in.RequiresGroupID); err != nil {
			return err
		}
	}
	return nil
}

// fromClaimParam: SQL NULL (not JSON null) for a missing resource_from_claim, otherwise the
// check constraint "resource_id IS NULL OR resource_from_claim IS NULL" does not work.
func fromClaimParam(v *model.ResourceFromClaim) *string {
	if v == nil {
		return nil
	}
	s := engine.MustJSON(v)
	return &s
}

// whereParam is a resource_where as a jsonb parameter: SQL NULL when there are no conditions.
func whereParam(conds []model.PredicateCondition) *string {
	if len(conds) == 0 {
		return nil
	}
	s := engine.MustJSON(conds)
	return &s
}

// ruleServiceID is the service a rule belongs to: the one that owns its resource type (none for a role rule).
func ruleServiceID(ctx context.Context, q db.Querier, in *model.AssignmentRuleInput) (*uuid.UUID, error) {
	if in.TypeResourceID == nil || in.RoleID != nil {
		return nil, nil
	}
	return serviceOfType(ctx, q, *in.TypeResourceID)
}

// onMissingOf is what a denial does when a condition reads an attribute the request does not carry (see
// model.AssignmentRuleInput.OnMissingAttribute). Given explicitly, that is it. Otherwise a deny rule that was a deny
// keeps what it had, and any other deny applies it, as a new deny rule does. An allow rule skips it: it grants nothing
// on a missing attribute.
func onMissingOf(in *model.AssignmentRuleInput, old *model.AssignmentRule) string {
	switch {
	case in.Effect != model.EffectDeny:
		return model.OnMissingSkip
	case in.OnMissingAttribute != nil:
		return *in.OnMissingAttribute
	case old != nil && old.Effect == model.EffectDeny:
		return old.OnMissingAttribute
	default:
		return model.OnMissingApply
	}
}

// ruleArgs are the values of the columns an assignment rule is written with, in the order of its INSERT.
func ruleArgs(in *model.AssignmentRuleInput, onMissing string) []any {
	return []any{
		engine.ToNullUUID(in.RoleID), engine.ToNullUUID(in.TypeResourceID), in.ResourceID, fromClaimParam(in.ResourceFromClaim),
		in.Access, in.Subject, engine.MustJSON(in.Predicate), engine.ToNullUUID(in.MemberOfGroupID), *in.Enabled,
		in.Effect, in.Exception, in.ValidFrom, in.ExpiresAt, in.Name, in.Description,
		onMissing, engine.MustJSON(in.RequiresPredicate), engine.ToNullUUID(in.RequiresGroupID), whereParam(in.ResourceWhere), in.SubjectKind,
	}
}

func (a *App) CreateAssignmentRule(ctx context.Context, in *model.AssignmentRuleInput) (*model.AssignmentRule, error) {
	return a.createAssignmentRule(ctx, in, nil)
}

// createAssignmentRule creates a rule with the given id, or with a new one when id is nil: the history restores the
// rules it deleted with the ids they had.
func (a *App) createAssignmentRule(ctx context.Context, in *model.AssignmentRuleInput, id *uuid.UUID) (*model.AssignmentRule, error) {
	var r *model.AssignmentRule
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		if err := a.checkAssignmentRuleRefs(ctx, q, in, true); err != nil {
			return change{}, err
		}
		serviceID, err := ruleServiceID(ctx, q, in)
		if err != nil {
			return change{}, err
		}
		r, err = engine.ScanAssignmentRule(q.QueryRow(ctx, `
			INSERT INTO assignment_rule AS ar
				(id, role_id, type_resource_id, resource_id, resource_from_claim, access, subject, predicate, member_of_group_id,
				 enabled, effect, exception, valid_from, expires_at, name, description,
				 on_missing_attribute, requires_predicate, requires_group_id, resource_where, subject_kind)
			VALUES (COALESCE($21::uuid, gen_random_uuid()), $1, $2, $3, $4::jsonb, $5, $6, $7::jsonb, $8, $9, $10, $11, $12, $13, $14, $15,
				$16, $17::jsonb, $18, $19::jsonb, $20)
			RETURNING `+engine.AssignmentRuleColumns, append(ruleArgs(in, onMissingOf(in, nil)), id)...,
		))
		if db.IsPrimaryKeyViolation(err) {
			return change{}, idTaken(id)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "assignment_rule", entityID: r.ID, serviceID: serviceID, action: actionCreate, after: r}, nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListAssignmentRules returns the rules of every service and resource type: a rule carries its own
// type_resource_id (or role_id), so a single management page does not need nesting in the URL.
func (a *App) ListAssignmentRules(ctx context.Context) ([]model.AssignmentRuleWithContext, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT `+engine.AssignmentRuleColumns+`, t.service_id, s.name, t.name, ro.name,
		       ar.role_id IS NOT NULL
		       OR (ar.resource_id IS NULL AND ar.resource_from_claim IS NULL AND ar.resource_where IS NULL)
		       OR `+liveSynchronizerExists("t.id")+`
		FROM assignment_rule ar
		LEFT JOIN type_resource t ON t.id = ar.type_resource_id
		LEFT JOIN service s ON s.id = t.service_id
		LEFT JOIN role ro ON ro.id = ar.role_id
		WHERE ar.role_id IS NOT NULL OR (t.deleted_at IS NULL AND s.deleted_at IS NULL)
		ORDER BY s.name, t.name, ar.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.AssignmentRuleWithContext{}
	for rows.Next() {
		var c model.AssignmentRuleWithContext
		r, err := engine.ScanAssignmentRule(rows, &c.ServiceID, &c.ServiceName, &c.TypeResourceName, &c.RoleName, &c.TargetValid)
		if err != nil {
			return nil, err
		}
		c.AssignmentRule = *r
		out = append(out, c)
	}
	return out, rows.Err()
}

// AssignmentRuleOf reads a rule as it is stored. The API checks the target it has before a change or a removal of it.
func (a *App) AssignmentRuleOf(ctx context.Context, ruleID uuid.UUID) (*model.AssignmentRule, error) {
	return a.getExistingAssignmentRule(ctx, a.DB, ruleID)
}

func (a *App) getExistingAssignmentRule(ctx context.Context, q db.Querier, ruleID uuid.UUID) (*model.AssignmentRule, error) {
	r, err := engine.ScanAssignmentRule(q.QueryRow(ctx,
		`SELECT `+engine.AssignmentRuleColumns+` FROM assignment_rule ar WHERE ar.id = $1`, ruleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.AssignmentRuleNotFound, ruleID)
	}
	return r, err
}

// ruleTargetChanged: the target is re-checked only when it changes — a rule that became "dead" after
// its type lost the synchronizer must stay editable (to disable it or change its access), otherwise
// it could only be deleted.
func ruleTargetChanged(old *model.AssignmentRule, in *model.AssignmentRuleInput) bool {
	return !equalPtr(old.TypeResourceID, in.TypeResourceID) ||
		!equalPtr(old.ResourceID, in.ResourceID) ||
		!equalPtr(old.ResourceFromClaim, in.ResourceFromClaim) ||
		!sameConditions(old.ResourceWhere, in.ResourceWhere)
}

// sameConditions reports whether two lists of conditions are the same (nil and empty are both no conditions).
func sameConditions(a, b []model.PredicateCondition) bool {
	return (len(a) == 0 && len(b) == 0) || engine.MustJSON(a) == engine.MustJSON(b)
}

// RetargetsRule reports whether an update moves a rule to another target: its type or its role, the resource it names,
// the claim that finds it, or its conditions on resources. The API checks such a change with more than a point write.
func RetargetsRule(old *model.AssignmentRule, in *model.AssignmentRuleInput) bool {
	return ruleTargetChanged(old, in) || !equalPtr(old.RoleID, in.RoleID)
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (a *App) UpdateAssignmentRule(ctx context.Context, ruleID uuid.UUID, in *model.AssignmentRuleInput) (*model.AssignmentRule, error) {
	var r *model.AssignmentRule
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingAssignmentRule(ctx, q, ruleID)
		if err != nil {
			return change{}, err
		}
		if err := a.checkAssignmentRuleRefs(ctx, q, in, ruleTargetChanged(old, in)); err != nil {
			return change{}, err
		}
		serviceID, err := ruleServiceID(ctx, q, in)
		if err != nil {
			return change{}, err
		}

		r, err = engine.ScanAssignmentRule(q.QueryRow(ctx, `
			UPDATE assignment_rule AS ar SET
				role_id = $2, type_resource_id = $3, resource_id = $4, resource_from_claim = $5::jsonb, access = $6,
				subject = $7, predicate = $8::jsonb, member_of_group_id = $9, enabled = $10,
				effect = $11, exception = $12, valid_from = $13, expires_at = $14, name = $15, description = $16,
				on_missing_attribute = $17, requires_predicate = $18::jsonb, requires_group_id = $19,
				resource_where = $20::jsonb, subject_kind = $21
			WHERE ar.id = $1
			RETURNING `+engine.AssignmentRuleColumns, append([]any{ruleID}, ruleArgs(in, onMissingOf(in, old))...)...,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return change{}, apperr.NotFound(i18n.AssignmentRuleNotFound, ruleID)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "assignment_rule", entityID: ruleID, serviceID: serviceID, action: actionUpdate,
			before: old, after: r}, nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (a *App) DeleteAssignmentRule(ctx context.Context, ruleID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingAssignmentRule(ctx, q, ruleID)
		if err != nil {
			return change{}, err
		}
		if _, err := q.Exec(ctx, `DELETE FROM assignment_rule WHERE id = $1`, ruleID); err != nil {
			return change{}, err
		}
		var serviceID *uuid.UUID
		if old.TypeResourceID != nil && old.RoleID == nil {
			serviceID, err = serviceOfType(ctx, q, *old.TypeResourceID)
			if err != nil {
				return change{}, err
			}
		}
		return change{entity: "assignment_rule", entityID: ruleID, serviceID: serviceID, action: actionDelete,
			before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return ruleID, nil
}
