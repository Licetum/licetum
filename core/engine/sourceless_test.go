package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// sourcelessGraph is division <- branch (service org) <- item <- part (service app), where item lost its synchronizer
// (sourceDeleted) or never had one; the other types have theirs.
func sourcelessGraph(org, app uuid.UUID, types [4]uuid.UUID, itemSync, sourceDeleted bool) *TypeGraph {
	division, branch, item, part := types[0], types[1], types[2], types[3]
	return newTypeGraph([]*TypeNode{
		{ID: division, ServiceID: org, Name: "division", Sync: true},
		{ID: branch, ServiceID: org, Name: "branch", Sync: true, Parents: []typeEdge{{Parent: division, Inherit: AllLevels}}},
		{ID: item, ServiceID: app, Name: "item", Sync: itemSync, SourceDeleted: sourceDeleted,
			Parents: []typeEdge{{Parent: branch, Inherit: AllLevels}}},
		{ID: part, ServiceID: app, Name: "part", Sync: true, Parents: []typeEdge{{Parent: item, Inherit: AllLevels}}},
	})
}

// A type without a live synchronizer has no resources, whatever rows its deleted synchronizer left in the local copy: the
// type graph says so, the climb of a claim lookup does not pass it, and a rule on a resource of it anchors nothing (a
// denial stays, absent, for the links below that name its rows).
func TestATypeWithoutASynchronizerHasNoResourcesForARule(t *testing.T) {
	org, app := uuid.New(), uuid.New()
	types := [4]uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	division, branch, item, part := types[0], types[1], types[2], types[3]
	g := sourcelessGraph(org, app, types, false, true)

	if !g.IsSync(division) || !g.IsSync(part) || g.IsSync(item) || g.IsSync(uuid.New()) {
		t.Fatal("IsSync: the types with a live synchronizer only")
	}
	if got := g.withoutResources(); !slices.Equal(got, []uuid.UUID{item}) {
		t.Fatalf("withoutResources = %v, want item", got)
	}
	// A parameter of the queries: an empty list, not NULL, when every type has a synchronizer.
	if got := sourcelessGraph(org, app, types, true, false).withoutResources(); got == nil || len(got) != 0 {
		t.Fatalf("withoutResources of a graph where every type is sync = %#v, want an empty list", got)
	}
	if got := climbTypes(g, item, branch, g.IsSync); got != nil {
		t.Fatalf("a lookup among the resources of item climbs nowhere: %v", got)
	}
	if got := climbTypes(g, part, branch, g.IsSync); slices.Contains(got, item) || !slices.Contains(got, part) || !slices.Contains(got, branch) {
		t.Fatalf("a climb from part to branch does not pass item: %v", got)
	}
	if got := climbTypes(g, part, division, func(id uuid.UUID) bool { return id != division }); got != nil {
		t.Fatalf("the rule's type without resources: %v", got)
	}

	i1 := "I1"
	rule := func(n byte, effect string, edit func(*model.AssignmentRule)) model.AssignmentRule {
		r := ruleOn("00000000-0000-0000-0000-00000000000"+string(n), item, ptr("alice"))
		r.Effect = effect
		edit(&r)
		return r
	}
	rules := []model.AssignmentRule{
		rule('1', model.EffectAllow, func(r *model.AssignmentRule) { r.ResourceID = &i1 }),
		rule('2', model.EffectDeny, func(r *model.AssignmentRule) { r.ResourceID = &i1 }),
		rule('3', model.EffectAllow, func(r *model.AssignmentRule) {
			branchID := branch
			r.TypeResourceID = &branchID
			r.ResourceFromClaim = &model.ResourceFromClaim{Claim: "item_name", LookupTypeResourceID: item, MatchBy: model.MatchByName}
		}),
		rule('4', model.EffectAllow, func(r *model.AssignmentRule) {
			r.ResourceWhere = []model.PredicateCondition{{Attr: "owner", Op: model.OpEq, Value: model.PredicateValue{Str: "alice"}}}
		}),
		rule('5', model.EffectAllow, func(*model.AssignmentRule) {}),
	}
	// No resource is looked up for any of them: an App without a database answers.
	insts, err := (&App{}).BuildInstances(context.Background(), g, rules, NewEnv(Claims{"item_name": "Item 1"}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 2 {
		t.Fatalf("got %d instances, want the absent denial and the whole-type rule: %+v", len(insts), insts)
	}
	if d := insts[0]; d.Rule.ID != rules[1].ID || !d.Deny || !d.Absent || d.AnchorRow == nil || d.AnchorExt != "I1" {
		t.Errorf("the denial on I1 is absent: %+v", d)
	}
	if w := insts[1]; w.Rule.ID != rules[4].ID || !w.Whole() || w.AnchorType != item {
		t.Errorf("the whole-type rule on item: %+v", w)
	}
}

// A question about a resource of a type without a live synchronizer is resource_not_found, explained so, while the type as
// a whole is decided by the whole-type rules on it.
func TestAQuestionAboutAResourceOfATypeWithoutASynchronizerIsNotFound(t *testing.T) {
	org, app := uuid.New(), uuid.New()
	types := [4]uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	division, branch, item, part := types[0], types[1], types[2], types[3]
	g := sourcelessGraph(org, app, types, false, true)
	whole := ruleOn("00000000-0000-0000-0000-000000000001", item, ptr("alice"))
	insts := []Instance{{Rule: &whole, Levels: []string{"read"}, AnchorType: item}}
	p := &Prepared{Graph: g, Insts: insts, engine: newEngine(g, insts, nil),
		sourced: map[uuid.UUID]bool{division: true, branch: true, part: true}}

	i1 := "I1"
	results, err := (&App{}).decideChecks(context.Background(), p, app, []model.CheckRequest{
		{Type: "item", ResourceID: &i1, Action: "read"},
		{Type: "item", Action: "read"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Allowed || r.Explanation.Decision != DecisionResourceNotFound ||
		len(r.Explanation.GrantedBy) != 0 || len(r.Explanation.Denials) != 0 {
		t.Errorf("I1: %+v %+v", r, r.Explanation)
	}
	if r := results[1]; !r.Allowed || r.Explanation.Decision != DecisionAllowed || len(r.Explanation.GrantedBy) != 1 {
		t.Errorf("item as a whole: %+v %+v", r, r.Explanation)
	}
}

// The structure of a type below one that lost its synchronizer changes, so that its closure is rebuilt without the routes
// through the rows left behind, and changes back when the synchronizer is there again. A type that never had one leaves
// the signatures as they were.
func TestTheSignatureMarksATypeAboveThatLostItsSynchronizer(t *testing.T) {
	org, app := uuid.New(), uuid.New()
	types := [4]uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	part := types[3]
	synced := sourcelessGraph(org, app, types, true, false).signature(part)
	if got := sourcelessGraph(org, app, types, false, true).signature(part); got == synced {
		t.Error("the signature of part must change when item loses its synchronizer")
	}
	if got := sourcelessGraph(org, app, types, false, false).signature(part); got != synced {
		t.Error("a type above that never had a synchronizer must not change the signature")
	}
}
