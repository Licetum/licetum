package service

import (
	"context"
	"slices"
	"strings"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

const grantPreviewSample = 10

// ruleFromInput is the rule a body would create, without an id: what the engine reads of it.
func ruleFromInput(in *model.AssignmentRuleInput) model.AssignmentRule {
	enabled := in.Enabled == nil || *in.Enabled
	return model.AssignmentRule{
		RoleID: in.RoleID, TypeResourceID: in.TypeResourceID, ResourceID: in.ResourceID,
		ResourceFromClaim: in.ResourceFromClaim, Access: in.Access, Subject: in.Subject, Predicate: in.Predicate,
		MemberOfGroupID: in.MemberOfGroupID, Enabled: enabled, Effect: in.Effect, Exception: in.Exception,
		ValidFrom: in.ValidFrom, ExpiresAt: in.ExpiresAt, Name: in.Name, Description: in.Description,
		OnMissingAttribute: onMissingOf(in, nil), RequiresPredicate: in.RequiresPredicate,
		RequiresGroupID: in.RequiresGroupID, ResourceWhere: in.ResourceWhere, SubjectKind: in.SubjectKind,
	}
}

// PreviewGrant says what a rule would do if it were saved, and saves nothing: how many resources it covers through the
// hierarchy, which known people it reaches, and which denials and exceptions in force meet it (a denial above it
// cancels what it grants, one inside it carves a part out). The body is that of POST /assignment_rule and is checked the
// same way.
func (a *App) PreviewGrant(ctx context.Context, in *model.AssignmentRuleInput) (*model.GrantPreview, error) {
	if err := a.checkAssignmentRuleRefs(ctx, a.DB, in, true); err != nil {
		return nil, err
	}
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}

	rule := ruleFromInput(in)
	// The types the rule reaches: its own, or those of the items of its role.
	var roleTypes []uuid.UUID
	if in.RoleID != nil {
		rows, err := a.DB.Query(ctx, `
			SELECT DISTINCT rp.type_resource_id FROM role_permission rp
			JOIN type_resource t ON t.id = rp.type_resource_id AND t.deleted_at IS NULL
			WHERE rp.role_id = $1`, *in.RoleID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			roleTypes = append(roleTypes, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		roleTypes = []uuid.UUID{*in.TypeResourceID}
	}
	rules, err := a.ExpandRoles(ctx, []model.AssignmentRule{rule}, roleTypes)
	if err != nil {
		return nil, err
	}

	names, err := a.lookupNames(ctx, []model.AssignmentRule{rule})
	if err != nil {
		return nil, err
	}
	out := &model.GrantPreview{
		Types: []model.GrantPreviewType{}, Sample: []string{}, Who: whoOf(&rule, names),
		Conflicts: []model.GrantConflict{}, AlreadyGranted: []model.RuleRef{},
		People: model.GrantPeople{Sample: []model.KnownPerson{}},
	}

	// The types whose data is read: the ones the rule reaches and those below them.
	involved := map[uuid.UUID]bool{}
	for _, r := range rules {
		involved[*r.TypeResourceID] = true
		for id := range g.DescendantsOf(*r.TypeResourceID) {
			involved[id] = true
		}
		// The types above, so that the ancestors of an anchor are known (for the denials above it).
		for id := range g.AncestorTypes(*r.TypeResourceID) {
			involved[id] = true
		}
	}
	if _, err := a.EnsureSynced(ctx, g, engine.Keys(involved), false); err != nil {
		return nil, err
	}
	synced, err := a.TypesWithSynchronizer(ctx, engine.Keys(involved))
	if err != nil {
		return nil, err
	}

	for _, r := range rules {
		if r.ResourceFromClaim != nil {
			out.Dynamic = true
		}
	}
	insts, err := a.BuildInstances(ctx, rules, engine.NewEnv(nil, nil), nil)
	if err != nil {
		return nil, err
	}

	// What the rule covers: the whole types (the type itself and the types below it), and the nodes under its anchors.
	wholeTypes := map[uuid.UUID]bool{}
	var anchors []uuid.UUID
	for i := range insts {
		if insts[i].Whole() {
			wholeTypes[insts[i].AnchorType] = true
			for id := range g.DescendantsOf(insts[i].AnchorType) {
				wholeTypes[id] = true
			}
		} else {
			anchors = append(anchors, *insts[i].AnchorRow)
		}
	}
	var syncedWhole []uuid.UUID
	for id := range wholeTypes {
		if synced[id] {
			syncedWhole = append(syncedWhole, id)
		}
	}
	counts, err := a.ActiveCounts(ctx, syncedWhole)
	if err != nil {
		return nil, err
	}
	perType := map[uuid.UUID]int{}
	for id, n := range counts {
		perType[id] = n
	}
	var pointResultTypes []uuid.UUID
	for id := range involved {
		if synced[id] {
			pointResultTypes = append(pointResultTypes, id)
		}
	}
	expanded, err := a.ExpandAnchors(ctx, anchors, pointResultTypes)
	if err != nil {
		return nil, err
	}
	covered := map[uuid.UUID]engine.CoveredNode{}
	for _, nodes := range expanded {
		for _, n := range nodes {
			if !wholeTypes[n.Type] {
				covered[n.ID] = n
			}
		}
	}
	for _, n := range covered {
		perType[n.Type]++
	}
	for id, n := range perType {
		out.Resources += n
		if node, ok := g.Node(id); ok && n > 0 {
			out.Types = append(out.Types, model.GrantPreviewType{Type: node.Name, Service: node.ServiceName, Count: n})
		}
	}
	slices.SortFunc(out.Types, func(x, y model.GrantPreviewType) int {
		if x.Count != y.Count {
			return y.Count - x.Count
		}
		return strings.Compare(x.Service+"/"+x.Type, y.Service+"/"+y.Type)
	})
	out.Capability = out.Resources == 0 && !out.Dynamic && len(involved) > 0 && !slices.ContainsFunc(engine.Keys(involved), func(id uuid.UUID) bool { return synced[id] })

	if len(syncedWhole) > 0 {
		rows, err := a.DB.Query(ctx, `
			SELECT name FROM resource_cache WHERE type_resource_id = ANY($1) AND active ORDER BY name LIMIT $2`,
			syncedWhole, grantPreviewSample)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			out.Sample = append(out.Sample, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if len(out.Sample) < grantPreviewSample {
		var rest []string
		for _, n := range covered {
			rest = append(rest, n.Name)
		}
		slices.Sort(rest)
		out.Sample = append(out.Sample, rest[:min(len(rest), grantPreviewSample-len(out.Sample))]...)
	}

	people, info, err := a.peopleReached(ctx, []*model.AssignmentRule{&rule})
	if err != nil {
		return nil, err
	}
	out.People = model.GrantPeople{
		Enabled: info.Enabled, Total: people[0].total, Sample: people[0].sample, Registry: info.Registry, Limited: info.Limited,
	}

	if err := a.grantMeets(ctx, g, &rule, insts, wholeTypes, covered, anchors, names, out); err != nil {
		return nil, err
	}
	return out, nil
}

// grantMeets finds the rules in force that meet a previewed rule: denials and exceptions above or inside what it
// covers, and allow rules that already give the same people the same levels on the same target.
func (a *App) grantMeets(ctx context.Context, g *engine.TypeGraph, rule *model.AssignmentRule, own []engine.Instance, wholeTypes map[uuid.UUID]bool, covered map[uuid.UUID]engine.CoveredNode, anchors []uuid.UUID, names lookedUpNames, out *model.GrantPreview) error {
	if len(own) == 0 {
		return nil
	}
	typeSet := map[uuid.UUID]bool{}
	for i := range own {
		typeSet[own[i].AnchorType] = true
		for id := range g.AncestorTypes(own[i].AnchorType) {
			typeSet[id] = true
		}
		for id := range g.DescendantsOf(own[i].AnchorType) {
			typeSet[id] = true
		}
	}
	typeIDs := engine.Keys(typeSet)
	existing, err := a.LoadRules(ctx, `
		SELECT `+engine.AssignmentRuleColumns+`
		FROM assignment_rule ar
		LEFT JOIN type_resource t ON t.id = ar.type_resource_id
		WHERE `+engine.RuleActiveSQL+`
		  AND (ar.role_id IS NOT NULL OR (ar.type_resource_id = ANY($1) AND t.deleted_at IS NULL))
		ORDER BY ar.id`, typeIDs)
	if err != nil {
		return err
	}
	if existing, err = a.ExpandRoles(ctx, existing, typeIDs); err != nil {
		return err
	}
	var static []model.AssignmentRule
	for _, r := range existing {
		if r.ResourceFromClaim == nil {
			static = append(static, r)
		}
	}
	insts, err := a.BuildInstances(ctx, static, engine.NewEnv(nil, nil), nil)
	if err != nil {
		return err
	}
	p := &engine.Prepared{Graph: g, Insts: insts}

	// Where the previewed anchors sit in the hierarchy: their ancestors, for the rules above them.
	ancestors, err := a.AncestorMasks(ctx, anchors)
	if err != nil {
		return err
	}
	aboveAnchor := func(x *engine.Instance) bool {
		for _, anchor := range anchors {
			if _, ok := ancestors[anchor][*x.AnchorRow]; ok {
				return true
			}
		}
		return false
	}
	reachesOwn := func(x *engine.Instance) (above, inside bool) {
		for i := range own {
			switch {
			case own[i].Whole() && x.Whole():
				if x.AnchorType == own[i].AnchorType {
					above = true
				} else if _, ok := g.DescendantsOf(x.AnchorType)[own[i].AnchorType]; ok {
					above = true
				} else if _, ok := g.DescendantsOf(own[i].AnchorType)[x.AnchorType]; ok {
					inside = true
				}
			case own[i].Whole():
				// x is anchored on a resource: inside when its type is covered by the whole-type rule.
				if x.AnchorType == own[i].AnchorType || wholeTypes[x.AnchorType] {
					inside = true
				}
			case x.Whole():
				if x.AnchorType == own[i].AnchorType {
					above = true
				} else if _, ok := g.DescendantsOf(x.AnchorType)[own[i].AnchorType]; ok {
					above = true
				}
			}
		}
		if !x.Whole() {
			if _, ok := covered[*x.AnchorRow]; ok {
				inside = true
			}
			if aboveAnchor(x) {
				above = true
			}
		}
		return above, inside
	}

	plainAllow := rule.Effect != model.EffectDeny && !rule.Exception && rule.RoleID == nil
	for i := range insts {
		x := &insts[i]
		if !mayReachSame(rule, x.Rule) {
			continue
		}
		switch {
		case x.Deny || x.Exception:
			above, inside := reachesOwn(x)
			if !above && !inside {
				continue
			}
			kind := "deny"
			if x.Exception {
				kind = "exception"
			}
			for _, position := range []struct {
				name string
				on   bool
			}{{"above", above}, {"inside", inside && !above}} {
				if position.on {
					out.Conflicts = append(out.Conflicts, model.GrantConflict{
						Position: position.name, Kind: kind, Rule: p.RuleRef(i), Name: x.Rule.Name, Who: whoOf(x.Rule, names),
					})
				}
			}
		case plainAllow && sameTarget(rule, x.Rule) && sameWho(rule, x.Rule) &&
			engine.LevelsOf(rule.Access...).SubsetOf(engine.LevelsOf(x.Levels...)):
			out.AlreadyGranted = append(out.AlreadyGranted, p.RuleRef(i))
		}
	}
	return nil
}

// whoKey names whom a rule applies to: a subject, a group, a set of conditions, or everyone.
func whoKey(r *model.AssignmentRule) (kind, key string) {
	switch {
	case r.Subject != nil:
		if id, ok := strings.CutPrefix(*r.Subject, model.GroupSubjectPrefix); ok {
			return "group", id
		}
		return "user", *r.Subject
	case r.MemberOfGroupID != nil:
		return "group", r.MemberOfGroupID.String()
	case len(r.Predicate) > 0:
		return "condition", engine.MustJSON(r.Predicate)
	}
	return "everyone", ""
}

// mayReachSame is false only when two rules certainly apply to different people: two different named subjects. A group
// may contain a subject, two groups may share a member, and conditions may hold for anybody.
func mayReachSame(a, b *model.AssignmentRule) bool {
	ka, va := whoKey(a)
	kb, vb := whoKey(b)
	if ka == "user" && kb == "user" {
		return va == vb
	}
	return true
}

func sameWho(a, b *model.AssignmentRule) bool {
	ka, va := whoKey(a)
	kb, vb := whoKey(b)
	return ka == kb && va == vb && len(a.RequiresPredicate) == 0 && len(b.RequiresPredicate) == 0 &&
		a.RequiresGroupID == nil && b.RequiresGroupID == nil
}

// sameTarget reports whether two rules are anchored on the same type and resource (or both on the whole type).
func sameTarget(a, b *model.AssignmentRule) bool {
	if a.TypeResourceID == nil || b.TypeResourceID == nil || *a.TypeResourceID != *b.TypeResourceID {
		return false
	}
	if (a.ResourceID == nil) != (b.ResourceID == nil) || (a.ResourceID != nil && *a.ResourceID != *b.ResourceID) {
		return false
	}
	return len(a.ResourceWhere) == 0 && len(b.ResourceWhere) == 0 && a.ResourceFromClaim == nil && b.ResourceFromClaim == nil
}
