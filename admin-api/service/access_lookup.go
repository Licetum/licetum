package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

const accessPeopleSample = 20

// WhoHasAccess lists the rules in force that cover a resource (or a type as a whole), with the levels that reach it and
// whom each applies to — a subject, a group, a condition, everyone — and, where the registry knows them, the people.
// It is the reverse of a check: the rules are those of every subject, loaded once, and the covering is the one the
// decision uses (coversOf), so what is listed is what a decision would weigh. Rules that find their resource from a
// claim cannot be placed without a token and are listed apart.
func (a *App) WhoHasAccess(ctx context.Context, serviceID, typeResourceID uuid.UUID, resourceID *string) (*model.AccessLookup, error) {
	if _, err := a.GetExistingTypeResource(ctx, a.DB, serviceID, typeResourceID); err != nil {
		return nil, err
	}
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	typeNode, ok := g.Node(typeResourceID)
	if !ok {
		return nil, apperr.NotFound(i18n.TypeResourceNotFound, typeResourceID)
	}
	out := &model.AccessLookup{Type: typeNode.Name, Resource: resourceID, Entries: []model.AccessEntry{}, Dynamic: []model.AccessEntry{}}

	// A rule is relevant if it lies on the type or on a type above it.
	relevant := map[uuid.UUID]bool{typeResourceID: true}
	for id := range g.AncestorTypes(typeResourceID) {
		relevant[id] = true
	}
	relevantIDs := engine.Keys(relevant)
	rules, err := a.LoadRules(ctx, `
		SELECT `+engine.AssignmentRuleColumns+`
		FROM assignment_rule ar
		LEFT JOIN type_resource t ON t.id = ar.type_resource_id
		WHERE `+engine.RuleActiveSQL+`
		  AND (ar.role_id IS NOT NULL OR (ar.type_resource_id = ANY($1) AND t.deleted_at IS NULL))
		ORDER BY ar.id`, relevantIDs)
	if err != nil {
		return nil, err
	}
	if rules, err = a.ExpandRoles(ctx, rules, relevantIDs); err != nil {
		return nil, err
	}
	// The types the rules reach, those below them and those above the asked type must be current before the anchors and
	// the hierarchy of the resource are looked up: the closure is built from all of them.
	involved := map[uuid.UUID]bool{typeResourceID: true}
	for id := range relevant {
		involved[id] = true
	}
	for _, r := range rules {
		involved[*r.TypeResourceID] = true
		for id := range g.DescendantsOf(*r.TypeResourceID) {
			involved[id] = true
		}
	}
	if _, err := a.EnsureSynced(ctx, g, engine.Keys(involved), false); err != nil {
		return nil, err
	}

	var row uuid.UUID
	if resourceID != nil {
		cached, err := a.CacheLookup(ctx, g, typeResourceID, *resourceID)
		if err != nil {
			return nil, err
		}
		if cached == nil {
			return nil, apperr.NotFound(i18n.ResourceNotFound, *resourceID)
		}
		row = cached.ID
		name := cached.Name
		out.Name = &name
	}

	var static, dynamic []model.AssignmentRule
	for _, r := range rules {
		if r.ResourceFromClaim != nil {
			dynamic = append(dynamic, r)
		} else {
			static = append(static, r)
		}
	}
	insts, err := a.BuildInstances(ctx, static, engine.NewEnv(nil, nil), nil)
	if err != nil {
		return nil, err
	}
	p := &engine.Prepared{Graph: g, Insts: insts}

	var covers []engine.Cover
	if resourceID == nil {
		for i := range insts {
			if insts[i].Whole() && insts[i].AnchorType == typeResourceID {
				covers = append(covers, engine.Cover{Inst: i, Mask: engine.AllLevels})
			}
		}
	} else {
		ancestors, err := a.AncestorMasks(ctx, []uuid.UUID{row})
		if err != nil {
			return nil, err
		}
		unresolved, err := a.UnresolvedParents(ctx, []uuid.UUID{typeResourceID})
		if err != nil {
			return nil, err
		}
		covers = p.CoversOf(typeResourceID, ancestors[row], unresolved[row])
	}

	names, err := a.lookupNames(ctx, rules)
	if err != nil {
		return nil, err
	}
	var entryRules, dynamicRules []*model.AssignmentRule
	for _, reach := range reachesOf(insts, covers) {
		in := &insts[reach.inst]
		entry := entryOf(in, p.RuleRef(reach.inst), reach, names)
		entry.Via = viaOf(in, row, resourceID != nil)
		out.Entries = append(out.Entries, entry)
		entryRules = append(entryRules, in.Rule)
	}
	for i := range dynamic {
		r := &dynamic[i]
		in := &engine.Instance{Rule: r, Deny: r.Effect == model.EffectDeny, Exception: r.Exception, Levels: r.Access,
			AnchorType: *r.TypeResourceID, Note: engine.RuleOrigin(*r)}
		entry := entryOf(in, p.RefOf(in), instanceReach{known: levelsThrough(in, engine.AllLevels)}, names)
		entry.Via = "claim"
		out.Dynamic = append(out.Dynamic, entry)
		dynamicRules = append(dynamicRules, r)
	}

	if err := a.attachPeople(ctx, out, entryRules, dynamicRules); err != nil {
		return nil, err
	}
	// Denials first, then the grants; stable, so the order of the rules (by id) holds within each.
	slices.SortStableFunc(out.Entries, func(x, y model.AccessEntry) int {
		if x.Rule.Effect == y.Rule.Effect {
			return 0
		}
		if x.Rule.Effect == model.EffectDeny {
			return -1
		}
		return 1
	})
	return out, nil
}

// names of the groups and roles the rules refer to.
type lookedUpNames struct {
	groups map[uuid.UUID]string
	roles  map[uuid.UUID]string
}

func (a *App) lookupNames(ctx context.Context, rules []model.AssignmentRule) (lookedUpNames, error) {
	n := lookedUpNames{groups: map[uuid.UUID]string{}, roles: map[uuid.UUID]string{}}
	var groupIDs, roleIDs []uuid.UUID
	for _, r := range rules {
		if r.MemberOfGroupID != nil {
			groupIDs = append(groupIDs, *r.MemberOfGroupID)
		}
		if r.Subject != nil {
			if id, ok := strings.CutPrefix(*r.Subject, model.GroupSubjectPrefix); ok {
				if gid, err := uuid.Parse(id); err == nil {
					groupIDs = append(groupIDs, gid)
				}
			}
		}
		if r.RoleID != nil {
			roleIDs = append(roleIDs, *r.RoleID)
		}
	}
	read := func(sql string, ids []uuid.UUID, into map[uuid.UUID]string) error {
		if len(ids) == 0 {
			return nil
		}
		rows, err := a.DB.Query(ctx, sql, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			into[id] = name
		}
		return rows.Err()
	}
	if err := read(`SELECT id, name FROM user_group WHERE id = ANY($1)`, groupIDs, n.groups); err != nil {
		return n, err
	}
	return n, read(`SELECT id, name FROM role WHERE id = ANY($1)`, roleIDs, n.roles)
}

// instanceReach is how an instance reaches the resource: the levels its covers carry for real (known), and those a
// denial carries only through a parent that is not in the cache yet (assumed).
type instanceReach struct {
	inst           int
	known, assumed []string
	assumedCover   bool
}

// reachesOf merges the covers of each instance, in the order of their first cover. CoversOf gives a denial twice when it
// reaches the resource through a known ancestor and may also reach it, with more levels, through a missing parent: it is
// one rule on one resource, so it makes one entry.
func reachesOf(insts []engine.Instance, covers []engine.Cover) []instanceReach {
	var out []instanceReach
	at := map[int]int{}
	for _, c := range covers {
		i, ok := at[c.Inst]
		if !ok {
			i = len(out)
			at[c.Inst] = i
			out = append(out, instanceReach{inst: c.Inst})
		}
		levels := levelsThrough(&insts[c.Inst], c.Mask)
		if r := &out[i]; c.Assumed {
			r.assumed, r.assumedCover = append(r.assumed, levels...), true
		} else {
			r.known = append(r.known, levels...)
		}
	}
	return out
}

// levelsThrough lists the levels of an instance that flow along a mask: the levels of the rule the mask lets through. A
// denial of every level (*) denies what flows (denyMatches): along a mask of some levels, it lists those levels.
func levelsThrough(in *engine.Instance, mask engine.LevelSet) []string {
	if !mask.All && slices.Contains(in.Levels, model.AllLevels) {
		return mask.Sorted()
	}
	return engine.LevelsOf(in.Levels...).Intersect(mask).Sorted()
}

// entryOf describes a rule that covers (or may cover) a resource, with the levels that reach it: those that reach for
// real and those only assumed, which are also listed apart. The entry is assumed when no level it lists reaches for real.
func entryOf(in *engine.Instance, ref model.RuleRef, reach instanceReach, names lookedUpNames) model.AccessEntry {
	rule := in.Rule
	levels := engine.LevelsOf(slices.Concat(reach.known, reach.assumed)...).Sorted()
	entry := model.AccessEntry{
		Rule: ref, Name: rule.Name, ExpiresAt: rule.ExpiresAt, ValidFrom: rule.ValidFrom, Levels: levels,
		AssumedLevels: []string{}, People: []model.KnownPerson{}, Who: whoOf(rule, names),
	}
	for _, l := range engine.LevelsOf(reach.assumed...).Sorted() {
		if !slices.Contains(reach.known, l) {
			entry.AssumedLevels = append(entry.AssumedLevels, l)
		}
	}
	entry.Assumed = reach.assumedCover && len(entry.AssumedLevels) == len(entry.Levels)
	if rule.RoleID != nil {
		entry.Role = &model.NamedID{ID: *rule.RoleID, Name: names.roles[*rule.RoleID]}
	}
	return entry
}

// viaOf says how an instance reaches the resource (row is its cache row; forResource is false for a type as a whole).
func viaOf(in *engine.Instance, row uuid.UUID, forResource bool) string {
	switch {
	case !forResource || in.Whole():
		return "type"
	case len(in.Rule.ResourceWhere) > 0:
		return "attribute"
	case in.AnchorRow != nil && *in.AnchorRow == row:
		return "resource"
	}
	return "ancestor"
}

func whoOf(rule *model.AssignmentRule, names lookedUpNames) model.AccessWho {
	who := model.AccessWho{Predicate: []model.PredicateCondition{}, Guarded: len(rule.RequiresPredicate) > 0 || rule.RequiresGroupID != nil}
	group := func(id uuid.UUID) *model.NamedID { return &model.NamedID{ID: id, Name: names.groups[id]} }
	switch {
	case rule.Subject != nil:
		if id, ok := strings.CutPrefix(*rule.Subject, model.GroupSubjectPrefix); ok {
			if gid, err := uuid.Parse(id); err == nil {
				who.Kind, who.Group = "group", group(gid)
				return who
			}
		}
		who.Kind, who.Subject = "user", rule.Subject
	case rule.MemberOfGroupID != nil:
		who.Kind, who.Group = "group", group(*rule.MemberOfGroupID)
	case len(rule.Predicate) > 0:
		who.Kind, who.Predicate = "condition", rule.Predicate
	default:
		who.Kind = "everyone"
	}
	return who
}

// peopleReached tries every rule on the known subjects and says, per rule, how many it applies to and who the first
// few are. A subject is tried with the claims the registry kept for it; a condition on the request context cannot hold
// without a request, so it reaches nobody here. The registry figures say how far to trust the answer.
func (a *App) peopleReached(ctx context.Context, rules []*model.AssignmentRule) ([]peopleResult, model.AccessPeople, error) {
	results := make([]peopleResult, len(rules))
	info := model.AccessPeople{Enabled: a.Cfg.Subjects.Enabled}
	for i := range results {
		results[i].sample = []model.KnownPerson{}
	}
	// With the registry off nothing is said about people, whatever it still holds.
	if !info.Enabled {
		return results, info, nil
	}
	subjects, err := a.loadKnownSubjects(ctx, engine.EvaluationLimit+1)
	if err != nil {
		return nil, info, err
	}
	if len(subjects) > engine.EvaluationLimit {
		subjects, info.Limited = subjects[:engine.EvaluationLimit], true
	}
	info.Registry = len(subjects)
	if len(subjects) == 0 || len(rules) == 0 {
		return results, info, nil
	}
	members, err := a.LiveMemberships(ctx)
	if err != nil {
		return nil, info, err
	}
	now := time.Now()
	for _, s := range subjects {
		env := engine.Env{Claims: s.Claims, Now: now}
		identity, _ := engine.IdentityOf(members, s.Subject, env)
		for i, rule := range rules {
			if applies, _ := engine.RuleApplies(*rule, identity, env); !applies {
				continue
			}
			results[i].total++
			if len(results[i].sample) < accessPeopleSample {
				results[i].sample = append(results[i].sample, model.KnownPerson{Subject: s.Subject, Name: s.Name})
			}
		}
	}
	return results, info, nil
}

// peopleResult is how many known subjects a rule applies to, with the first few.
type peopleResult struct {
	total  int
	sample []model.KnownPerson
}

// attachPeople adds, to every entry, the known subjects the rule applies to (entryRules[i] is the rule of
// out.Entries[i] before the entries are ordered, dynamicRules[i] that of out.Dynamic[i]).
func (a *App) attachPeople(ctx context.Context, out *model.AccessLookup, entryRules, dynamicRules []*model.AssignmentRule) error {
	results, info, err := a.peopleReached(ctx, slices.Concat(entryRules, dynamicRules))
	if err != nil {
		return err
	}
	out.People = info
	for i := range out.Entries {
		out.Entries[i].PeopleTotal, out.Entries[i].People = results[i].total, results[i].sample
	}
	for i := range out.Dynamic {
		r := results[len(out.Entries)+i]
		out.Dynamic[i].PeopleTotal, out.Dynamic[i].People = r.total, r.sample
	}
	return nil
}
