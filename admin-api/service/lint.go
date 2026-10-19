package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// Lint reads the policy of a service for mistakes and leftovers: rules that decide nothing, rules that grant too much,
// and entities nothing uses. It reads the policy as it is written and the data as it is now. Every finding names what
// it is about, and its message is in the language of the request.
func (a *App) Lint(ctx context.Context, serviceID uuid.UUID) (*model.LintReport, error) {
	rules, err := a.serviceRulesAll(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	lint := &linter{lang: i18n.FromContext(ctx), report: &model.LintReport{Findings: []model.LintFinding{}}}

	var typeIDs, groupIDs []uuid.UUID
	hasDeny := false
	for _, r := range rules {
		if r.TypeResourceID != nil {
			typeIDs = append(typeIDs, *r.TypeResourceID)
		}
		groupIDs = append(groupIDs, groupsOfRule(r)...)
		hasDeny = hasDeny || r.Effect == model.EffectDeny
	}
	synced, err := a.TypesWithSynchronizer(ctx, typeIDs)
	if err != nil {
		return nil, err
	}
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	withMembers, err := a.groupsWithMembers(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	matching, err := a.ResourcesMatching(ctx, rules, func(id uuid.UUID) bool { return synced[id] })
	if err != nil {
		return nil, err
	}

	now := time.Now()
	signatures := map[string]bool{}
	for i, r := range rules {
		id := r.ID
		label := ruleLabel(r)
		switch {
		case !r.Enabled:
			lint.add(model.LintInfo, i18n.LintRuleDisabled, &id, nil, nil, label)
			continue
		case (r.ValidFrom != nil && r.ValidFrom.After(now)) || (r.ExpiresAt != nil && !r.ExpiresAt.After(now)):
			lint.add(model.LintWarning, i18n.LintRuleNotInForce, &id, nil, nil, label)
			continue
		}
		// A rule on a resource of a type without a synchronizer decides nothing now, but it is still read for the rest:
		// a duplicate or a guard on an empty group is worth knowing when the synchronizer comes back.
		if r.RoleID == nil && hasResourceTarget(r) && !synced[*r.TypeResourceID] {
			lint.add(model.LintError, i18n.LintRuleTargetDead, &id, nil, nil, label)
		} else if r.RoleID == nil && r.ResourceFromClaim != nil && g.LookupCut(r.ResourceFromClaim.LookupTypeResourceID, *r.TypeResourceID) {
			// The type is there, but the claim is looked up in a type without resources, or the climb passes one: the
			// rule finds nothing there (a denial keeps an absent anchor, see engine.TypeGraph.LookupCut).
			lint.add(model.LintError, i18n.LintRuleLookupDead, &id, nil, nil, label)
		}

		if r.Effect != model.EffectDeny && everyoneRule(r) {
			lint.add(model.LintWarning, i18n.LintRuleEveryone, &id, nil, nil, label)
		}
		if r.Effect == model.EffectDeny && r.OnMissingAttribute == model.OnMissingApply && claimCondition(r.Predicate, r.RequiresPredicate) {
			lint.add(model.LintInfo, i18n.LintDenyMissingClaim, &id, nil, nil, label)
		}
		for _, g := range groupsOfRule(r) {
			if !withMembers[g] {
				gid := g
				lint.add(model.LintWarning, i18n.LintGroupEmpty, &id, nil, &gid, label)
				break
			}
		}
		if r.Exception && !hasDeny {
			lint.add(model.LintInfo, i18n.LintExceptionNothingToLift, &id, nil, nil, label)
		}
		if len(r.ResourceWhere) > 0 && len(matching[i]) == 0 {
			lint.add(model.LintInfo, i18n.LintResourceWhereEmpty, &id, nil, nil, label)
		}
		if sig := ruleSignature(r); signatures[sig] {
			lint.add(model.LintWarning, i18n.LintRuleDuplicate, &id, nil, nil, label)
		} else {
			signatures[sig] = true
		}
	}

	unusedRoles, err := a.unusedRoles(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, role := range unusedRoles {
		rid := role.ID
		lint.add(model.LintInfo, i18n.LintRoleUnused, nil, &rid, nil, role.Name)
	}
	unusedGroups, err := a.unusedAccessGroups(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, name := range unusedGroups {
		lint.add(model.LintInfo, i18n.LintAccessGroupUnused, nil, nil, nil, name)
	}
	return lint.report, nil
}

// linter collects the findings of one Lint call.
type linter struct {
	lang   i18n.Lang
	report *model.LintReport
}

// add records a finding: its message is the catalog text of the key, filled with the label of what it is about.
func (l *linter) add(severity string, key i18n.Key, rule, role, group *uuid.UUID, label string) {
	l.report.Findings = append(l.report.Findings, model.LintFinding{
		Severity: severity, Code: string(key), Message: i18n.T(l.lang, key, label),
		RuleID: rule, RoleID: role, GroupID: group,
	})
}

// ruleLabel names a rule in a message: its name, or its id when it has none.
func ruleLabel(r model.AssignmentRule) string {
	if r.Name != nil && *r.Name != "" {
		return *r.Name
	}
	return r.ID.String()
}

// hasResourceTarget reports whether a rule names resources (and so needs a synchronizer to take effect).
func hasResourceTarget(r model.AssignmentRule) bool {
	return r.ResourceID != nil || r.ResourceFromClaim != nil || len(r.ResourceWhere) > 0
}

// everyoneRule reports whether a rule covers every subject: no subject, group or claim condition narrows it, and no guard
// does either.
func everyoneRule(r model.AssignmentRule) bool {
	return r.Subject == nil && r.MemberOfGroupID == nil && len(r.Predicate) == 0 &&
		len(r.RequiresPredicate) == 0 && r.RequiresGroupID == nil
}

// groupsOfRule lists the groups a rule refers to: the group it covers, the group it is guarded by, and a group subject.
func groupsOfRule(r model.AssignmentRule) []uuid.UUID {
	var out []uuid.UUID
	if r.MemberOfGroupID != nil {
		out = append(out, *r.MemberOfGroupID)
	}
	if r.RequiresGroupID != nil {
		out = append(out, *r.RequiresGroupID)
	}
	if r.Subject != nil && strings.HasPrefix(*r.Subject, model.GroupSubjectPrefix) {
		if id, err := uuid.Parse(strings.TrimPrefix(*r.Subject, model.GroupSubjectPrefix)); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// claimCondition reports whether a condition reads a claim of the token (the default source).
func claimCondition(lists ...[]model.PredicateCondition) bool {
	for _, conds := range lists {
		for _, c := range conds {
			if c.Source == "" || c.Source == model.SourceClaim {
				return true
			}
		}
	}
	return false
}

// ruleSignature identifies what a rule does, whatever its name, description, validity or state: two rules with the same
// signature grant and deny the same things to the same subjects.
func ruleSignature(r model.AssignmentRule) string {
	raw, _ := json.Marshal(struct {
		Type, Role, Resource, Subject, Member, RequiresGroup any
		Claim                                                any
		Where, Access, Predicate, Requires                   any
		Effect, Exception, OnMissing                         any
	}{
		Type: r.TypeResourceID, Role: r.RoleID, Resource: r.ResourceID, Subject: r.Subject,
		Member: r.MemberOfGroupID, RequiresGroup: r.RequiresGroupID, Claim: r.ResourceFromClaim,
		Where: r.ResourceWhere, Access: r.Access, Predicate: r.Predicate, Requires: r.RequiresPredicate,
		Effect: r.Effect, Exception: r.Exception, OnMissing: r.OnMissingAttribute,
	})
	return string(raw)
}

// groupsWithMembers returns which of the groups have at least one membership.
func (a *App) groupsWithMembers(ctx context.Context, groupIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := a.DB.Query(ctx, `SELECT DISTINCT group_id FROM group_member WHERE group_id = ANY($1)`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// unusedRoles returns the roles with items on the resource types of a service that no rule grants.
func (a *App) unusedRoles(ctx context.Context, serviceID uuid.UUID) ([]model.Role, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT DISTINCT ro.id, ro.name FROM role ro
		JOIN role_permission rp ON rp.role_id = ro.id
		JOIN type_resource t ON t.id = rp.type_resource_id
		WHERE t.service_id = $1 AND t.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM assignment_rule ar WHERE ar.role_id = ro.id)
		ORDER BY ro.name`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Role
	for rows.Next() {
		var r model.Role
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// unusedAccessGroups returns the names of the access groups of a service's types that no rule or role item names.
func (a *App) unusedAccessGroups(ctx context.Context, serviceID uuid.UUID) ([]string, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT g.name FROM access_group g
		JOIN type_resource t ON t.id = g.type_resource_id
		WHERE t.service_id = $1 AND t.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM assignment_rule ar
		                  WHERE ar.type_resource_id = g.type_resource_id AND ('@' || g.name) = ANY(ar.access))
		  AND NOT EXISTS (SELECT 1 FROM role_permission rp
		                  WHERE rp.type_resource_id = g.type_resource_id AND ('@' || g.name) = ANY(rp.access))
		ORDER BY g.name`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
