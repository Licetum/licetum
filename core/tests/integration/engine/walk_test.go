//go:build integration

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

// The anchors of a resource_from_claim rule are found by walking the resource links up from the matched resources
// (findAncestorsBatch, through BuildInstances). The fixture is a hierarchy of five levels with two side hierarchies that
// lie on no route to the targets: division > region > branch > project > document, a project is also in a zone, and a
// document also has a tag (tagset > tag). Some documents have two projects, and some projects are switched off.

// walkTypes are the resource types and their parents, parents first.
var walkTypes = []struct {
	name    string
	parents []string
}{
	{"division", nil},
	{"region", []string{"division"}},
	{"branch", []string{"region"}},
	{"zone", nil},
	{"project", []string{"branch", "zone"}},
	{"tagset", nil},
	{"tag", []string{"tagset"}},
	{"document", []string{"project", "tag"}},
}

// walkResource is a resource of the fixture, with what the expected answers are computed from.
type walkResource struct {
	id      uuid.UUID
	typ     string
	ext     string
	name    string
	owner   string
	active  bool
	parents []*walkResource
}

type walkFixture struct {
	app   *engine.App
	graph *engine.TypeGraph
	types map[string]uuid.UUID
	all   []*walkResource
}

// walkScenario is one resource_from_claim rule: the resources of type lookup matching value, and their ancestors of
// type target.
type walkScenario struct {
	name           string
	lookup, target string
	matchBy, value string
}

var walkScenarios = []walkScenario{
	// The owner pattern: the rule's type is the lookup type, the matched documents are the anchors.
	{"owner pattern", "document", "document", "meta.owner", "u1"},
	{"project owner pattern", "project", "project", "meta.owner", "u2"},
	{"document to project", "document", "project", "meta.owner", "u2"},
	{"document to branch", "document", "branch", "meta.owner", "u3"},
	{"document to division", "document", "division", "meta.owner", "u4"},
	{"one document to region by name", "document", "region", model.MatchByName, "document 1234"},
	{"one project to branch by id", "project", "branch", model.MatchByID, "project-00042"},
	// The target lies below the lookup type: nothing can be found.
	{"target below", "project", "document", "meta.owner", "u1"},
}

func newWalkFixture(tb testing.TB) *walkFixture {
	tb.Helper()
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = infrastructure.Terminate(context.Background()) })
	pool, err := db.Connect(ctx, config.Database{URI: infrastructure.DBURI, PoolSize: 4})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pool.Close)
	if err := db.MigrateUpTo(ctx, pool, db.LatestMigration()); err != nil {
		tb.Fatal(err)
	}

	fx := &walkFixture{app: engine.New(pool, nil, &config.Config{}), types: map[string]uuid.UUID{}}
	service := uuid.New()
	mustExec(tb, pool, `INSERT INTO service (id, name, url, icon, slug) VALUES ($1, 'walk', '', '', 'walk')`, service)
	for _, t := range walkTypes {
		id := uuid.New()
		fx.types[t.name] = id
		mustExec(tb, pool, `INSERT INTO type_resource (id, service_id, name) VALUES ($1, $2, $3)`, id, service, t.name)
		for _, p := range t.parents {
			mustExec(tb, pool, `INSERT INTO type_resource_parent (type_resource_id, parent_id) VALUES ($1, $2)`, id, fx.types[p])
		}
	}

	add := func(typ string, n, owners int, parents func(i int) []*walkResource) []*walkResource {
		out := make([]*walkResource, n)
		for i := range n {
			r := &walkResource{
				id: uuid.New(), typ: typ, ext: fmt.Sprintf("%s-%05d", typ, i), name: fmt.Sprintf("%s %d", typ, i), active: true,
			}
			if owners > 0 {
				r.owner = fmt.Sprintf("u%d", i%owners)
			}
			if parents != nil {
				r.parents = parents(i)
			}
			out[i] = r
		}
		fx.all = append(fx.all, out...)
		return out
	}
	divisions := add("division", 4, 0, nil)
	regions := add("region", 16, 0, func(i int) []*walkResource { return []*walkResource{divisions[i/4]} })
	branches := add("branch", 80, 0, func(i int) []*walkResource { return []*walkResource{regions[i/5]} })
	zones := add("zone", 20, 0, nil)
	projects := add("project", 400, 4, func(i int) []*walkResource { return []*walkResource{branches[i/5], zones[i%20]} })
	for i := 50; i < len(projects); i += 97 {
		projects[i].active = false
	}
	tagsets := add("tagset", 10, 0, nil)
	tags := add("tag", 100, 0, func(i int) []*walkResource { return []*walkResource{tagsets[i/10]} })
	add("document", 4000, 10, func(i int) []*walkResource {
		out := []*walkResource{projects[i/10], tags[i%100]}
		if i%7 == 0 {
			out = append(out, projects[(i/10+1)%len(projects)])
		}
		return out
	})

	var ids, typeIDs []uuid.UUID
	var exts, names, metas []string
	var actives []bool
	var childIDs, parentTypes, parentIDs []uuid.UUID
	var parentExts []string
	for _, r := range fx.all {
		meta := map[string]string{}
		if r.owner != "" {
			meta["owner"] = r.owner
		}
		raw, err := json.Marshal(meta)
		if err != nil {
			tb.Fatal(err)
		}
		ids, typeIDs, exts, names = append(ids, r.id), append(typeIDs, fx.types[r.typ]), append(exts, r.ext), append(names, r.name)
		metas, actives = append(metas, string(raw)), append(actives, r.active)
		for _, p := range r.parents {
			childIDs, parentTypes, parentExts = append(childIDs, r.id), append(parentTypes, fx.types[p.typ]), append(parentExts, p.ext)
			parentIDs = append(parentIDs, p.id)
		}
	}
	mustExec(tb, pool, `
		INSERT INTO resource_cache (id, type_resource_id, external_id, name, meta, active)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::varchar[], $4::varchar[], $5::jsonb[], $6::boolean[])`,
		ids, typeIDs, exts, names, metas, actives)
	// A link to a switched-off parent stays resolved, as a synchronization leaves it.
	mustExec(tb, pool, `
		INSERT INTO resource_edge (child_id, parent_type_id, parent_external_id, parent_id)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::varchar[], $4::uuid[])`,
		childIDs, parentTypes, parentExts, parentIDs)
	mustExec(tb, pool, `ANALYZE`)

	if fx.graph, err = fx.app.LoadTypeGraph(ctx); err != nil {
		tb.Fatal(err)
	}
	return fx
}

func mustExec(tb testing.TB, pool *pgxpool.Pool, sql string, args ...any) {
	tb.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		tb.Fatal(err)
	}
}

// rules turns the scenarios into rules, each reading the claim named after its index, and the claims of the caller.
func (fx *walkFixture) rules(scenarios []walkScenario) ([]model.AssignmentRule, engine.Env) {
	rules := make([]model.AssignmentRule, len(scenarios))
	claims := engine.Claims{}
	for i, s := range scenarios {
		target := fx.types[s.target]
		claim := fmt.Sprintf("c%d", i)
		claims[claim] = s.value
		rules[i] = model.AssignmentRule{
			ID: uuid.New(), TypeResourceID: &target, Access: []string{"read"}, Enabled: true, Effect: model.EffectAllow,
			ResourceFromClaim: &model.ResourceFromClaim{Claim: claim, LookupTypeResourceID: fx.types[s.lookup], MatchBy: s.matchBy},
		}
	}
	return rules, engine.NewEnv(claims, nil)
}

// anchors returns the external ids of the anchors found for each rule.
func (fx *walkFixture) anchors(tb testing.TB, rules []model.AssignmentRule, env engine.Env) map[uuid.UUID][]string {
	tb.Helper()
	insts, err := fx.app.BuildInstances(context.Background(), fx.graph, rules, env, nil)
	if err != nil {
		tb.Fatal(err)
	}
	out := map[uuid.UUID][]string{}
	for _, in := range insts {
		out[in.Rule.ID] = append(out[in.Rule.ID], in.AnchorExt)
	}
	return out
}

// expected is what the walk has to find: the resources of the target type at or above the matched ones, through every
// link to an active parent, whatever its type.
func (fx *walkFixture) expected(s walkScenario) []string {
	var stack []*walkResource
	for _, r := range fx.all {
		if r.typ != s.lookup || !r.active {
			continue
		}
		var match bool
		switch s.matchBy {
		case model.MatchByName:
			match = r.name == s.value
		case model.MatchByID:
			match = r.ext == s.value
		case model.MatchByMetaPrefix + "owner":
			match = r.owner == s.value
		}
		if match {
			stack = append(stack, r)
		}
	}
	seen := map[*walkResource]bool{}
	var out []string
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[r] {
			continue
		}
		seen[r] = true
		if r.typ == s.target {
			out = append(out, r.ext)
		}
		for _, p := range r.parents {
			if p.active {
				stack = append(stack, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

// The walk finds every anchor a climb to the roots finds: stopping at the target type, leaving the links that lead
// away from it and not climbing at all when the lookup type is the target change nothing in the answer.
func TestFromClaimAnchorsOnADeepHierarchy(t *testing.T) {
	fx := newWalkFixture(t)
	rules, env := fx.rules(walkScenarios)
	got := fx.anchors(t, rules, env)
	for i, s := range walkScenarios {
		want := fx.expected(s)
		if s.target != "document" && len(want) == 0 {
			t.Fatalf("%s: the fixture gives no anchor", s.name)
		}
		have := got[rules[i].ID]
		slices.Sort(have)
		if !slices.Equal(have, want) {
			t.Errorf("%s: %d anchors %v, want %d %v", s.name, len(have), first(have), len(want), first(want))
		}
	}
}

func first(s []string) []string { return s[:min(len(s), 5)] }

// BenchmarkFromClaimWalk measures the lookups of the scenarios one by one, and all of them as one request (one query per
// kind of match).
func BenchmarkFromClaimWalk(b *testing.B) {
	fx := newWalkFixture(b)
	run := func(name string, scenarios []walkScenario) {
		b.Run(name, func(b *testing.B) {
			rules, env := fx.rules(scenarios)
			for b.Loop() {
				if _, err := fx.app.BuildInstances(context.Background(), fx.graph, rules, env, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	for _, s := range walkScenarios {
		run(s.name, []walkScenario{s})
	}
	run("all in one request", walkScenarios)
}
