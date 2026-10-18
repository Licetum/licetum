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
	if got, cut := climbTypes(g, item, branch, g.IsSync); got != nil || !cut {
		t.Fatalf("a lookup among the resources of item climbs nowhere, and its anchors are cut: %v %v", got, cut)
	}
	if got, cut := climbTypes(g, part, branch, g.IsSync); slices.Contains(got, item) || !slices.Contains(got, part) || !slices.Contains(got, branch) || !cut {
		t.Fatalf("a climb from part to branch does not pass item, and says it was cut: %v %v", got, cut)
	}
	if got, cut := climbTypes(g, part, division, func(id uuid.UUID) bool { return id != division }); got != nil || !cut {
		t.Fatalf("the rule's type without resources: %v %v", got, cut)
	}
	if got, cut := climbTypes(g, branch, part, g.IsSync); got != nil || cut {
		t.Fatalf("a rule's type below the lookup type is reached by no climb, and has no anchor to lose: %v %v", got, cut)
	}
	if got, cut := climbTypes(g, branch, division, g.IsSync); len(got) != 2 || cut {
		t.Fatalf("a climb between types that have resources loses nothing: %v %v", got, cut)
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
		// The denials that read the resources of item or climb through them cannot be anchored.
		rule('6', model.EffectDeny, func(r *model.AssignmentRule) {
			branchID := branch
			r.TypeResourceID = &branchID
			r.ResourceFromClaim = &model.ResourceFromClaim{Claim: "item_name", LookupTypeResourceID: item, MatchBy: model.MatchByName}
		}),
		rule('7', model.EffectDeny, func(r *model.AssignmentRule) {
			r.ResourceWhere = []model.PredicateCondition{{Attr: "owner", Op: model.OpEq, Value: model.PredicateValue{Str: "alice"}}}
		}),
		// A denial with no claim to look up is not applied, as for a lookup that is not cut.
		rule('8', model.EffectDeny, func(r *model.AssignmentRule) {
			branchID := branch
			r.TypeResourceID = &branchID
			r.ResourceFromClaim = &model.ResourceFromClaim{Claim: "missing", LookupTypeResourceID: item, MatchBy: model.MatchByName}
		}),
	}
	// No resource is looked up for any of them (a lookup among the resources of part would be): an App without a database answers.
	insts, err := (&App{}).BuildInstances(context.Background(), g, rules, NewEnv(Claims{"item_name": "Item 1"}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		rule   int // the index in rules
		anchor uuid.UUID
		ext    string
		whole  bool
	}
	wants := []want{
		{rule: 1, anchor: item, ext: "I1"},   // the denial on I1
		{rule: 4, anchor: item, whole: true}, // the whole-type rule on item
		{rule: 5, anchor: branch},            // the claim is looked up among the resources of item
		{rule: 6, anchor: item},              // the attributes of the resources of item
	}
	if len(insts) != len(wants) {
		t.Fatalf("got %d instances, want %d: %+v", len(insts), len(wants), insts)
	}
	for i, w := range wants {
		in := insts[i]
		if in.Rule.ID != rules[w.rule].ID || in.AnchorType != w.anchor || in.Whole() != w.whole {
			t.Errorf("instance %d: %+v, want rule %d at %v", i, in, w.rule, w.anchor)
			continue
		}
		if w.whole {
			continue
		}
		if !in.Deny || !in.Absent || in.AnchorRow == nil || *in.AnchorRow != uuid.Nil || in.AnchorExt != w.ext {
			t.Errorf("instance %d is an absent denial that covers nothing for real (ext %q): %+v", i, w.ext, in)
		}
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
