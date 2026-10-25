//go:build integration

package api_test

// F1-11: the policy snapshot must not change an answer. The same policy, generated at random, is asked of two engines on
// one database: one reads the policy from memory (the snapshot, the default), the other reads it with queries for every
// answer (SNAPSHOT__MAX_BYTES off, the code of before the fast read path). Their answers to the permission and to the
// checks, with explanations, must be identical: for a policy with validity windows, claim-based rules, roles, nested
// groups, conditions on the request and on the clock, denials and exceptions; again after the policy was changed; and
// again after some of its validity windows have passed without any change of the policy at all.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

type policyGenerator struct {
	w      *world
	rng    *rand.Rand
	groups []string
	roles  []string
	rules  []string
	bodies map[string]map[string]any // what each rule was created with: an update takes the whole body
}

var (
	diffResources = map[string][]string{
		"division": {"D1", "D2"}, "rrs": {"R1", "R2", "R3"}, "branch": {"B1", "B2", "B3", "B4"},
		"item": {"I1", "I4"}, "project": {"P1", "P2", "P3"},
	}
	diffTypes  = []string{"division", "rrs", "branch", "item", "project"}
	diffLevels = []string{"read", "write"}
)

func (g *policyGenerator) pick(options ...string) string { return options[g.rng.Intn(len(options))] }
func (g *policyGenerator) chance(percent int) bool       { return g.rng.Intn(100) < percent }

func (g *policyGenerator) level() []string {
	switch g.rng.Intn(4) {
	case 0:
		return []string{"read"}
	case 1:
		return []string{"write"}
	}
	return []string{"read", "write"}
}

// target returns the fields of a permission: the type and what it covers.
func (g *policyGenerator) target(forRole bool) map[string]any {
	typeName := g.pick(diffTypes...)
	out := map[string]any{"type_resource_id": g.w.typeByName[typeName]}
	switch g.rng.Intn(10) {
	case 0, 1, 2: // the whole type
	case 3:
		out["resource_from_claim"] = map[string]any{"claim": "branch_name", "lookup_type_resource_id": g.w.typeByName["branch"], "match_by": "name"}
		if typeName == "project" || typeName == "item" {
			out["type_resource_id"] = g.w.typeByName["branch"]
		}
	case 4:
		out["type_resource_id"] = g.w.typeByName["project"]
		out["resource_where"] = []any{map[string]any{"attr": "owner", "op": g.pick("eq", "ne"), "value": g.pick("alice", "bob")}}
	default:
		out["resource_id"] = g.pick(diffResources[typeName]...)
	}
	out["access"] = g.level()
	return out
}

func (g *policyGenerator) validity(rule map[string]any) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	switch n := g.rng.Intn(100); {
	case n < 8:
		rule["expires_at"] = past
	case n < 14:
		rule["valid_from"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	case n < 20:
		rule["valid_from"] = past
		rule["expires_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	case n < 28: // ends during the test, with no change of the policy
		rule["expires_at"] = time.Now().Add(4 * time.Second).UTC().Format(time.RFC3339)
	case n < 33: // starts during the test
		rule["valid_from"] = time.Now().Add(4 * time.Second).UTC().Format(time.RFC3339)
	}
}

func (g *policyGenerator) conditions() []any {
	switch g.rng.Intn(6) {
	case 0:
		return []any{map[string]any{"attr": "department", "op": "eq", "value": g.pick("sales", "eng")}}
	case 1:
		return []any{map[string]any{"attr": "region", "op": "in", "value": []any{"eu", "us"}}}
	case 2:
		return []any{map[string]any{"attr": "preferred_username", "op": "regex", "value": "^user-[0-2]$"}}
	case 3:
		return []any{map[string]any{"attr": "department", "op": "ne", "value": "sales"}}
	case 4:
		return []any{
			map[string]any{"attr": "region", "op": "not_in", "value": []any{"asia"}},
			map[string]any{"attr": "hour", "source": "now", "op": "ge", "value": "0"},
		}
	}
	return []any{map[string]any{"attr": "ip", "source": "context", "op": "in_cidr", "value": []any{"10.0.0.0/8"}}}
}

func (g *policyGenerator) rule() string {
	rule := g.target(false)
	if len(g.roles) > 0 && g.chance(15) {
		rule = map[string]any{"role_id": g.roles[g.rng.Intn(len(g.roles))]}
	}
	// who
	switch g.rng.Intn(10) {
	case 0, 1, 2, 3:
		rule["subject"] = fmt.Sprintf("user-%d", g.rng.Intn(6))
	case 4, 5:
		rule["subject"] = "group:" + g.groups[g.rng.Intn(len(g.groups))]
	case 6, 7:
		rule["member_of_group_id"] = g.groups[g.rng.Intn(len(g.groups))]
	default:
		rule["predicate"] = g.conditions()
	}
	// effect
	switch n := g.rng.Intn(100); {
	case n < 22:
		rule["effect"] = "deny"
		if g.chance(30) {
			rule["access"] = []string{"*"}
		}
		if g.chance(40) {
			rule["on_missing_attribute"] = g.pick("apply", "skip")
		}
	case n < 32:
		rule["exception"] = true
	}
	if rule["effect"] != "deny" && g.chance(10) {
		rule["on_missing_attribute"] = "skip"
	}
	if g.chance(10) {
		rule["requires_predicate"] = g.conditions()
	}
	if g.chance(8) {
		rule["requires_group_id"] = g.groups[g.rng.Intn(len(g.groups))]
	}
	if g.chance(8) {
		rule["enabled"] = false
	}
	g.validity(rule)
	if _, ok := rule["role_id"]; ok {
		delete(rule, "access")
	}
	id := g.w.must("POST", "/assignment_rule", rule)["id"].(string)
	g.rules = append(g.rules, id)
	g.bodies[id] = rule
	return id
}

func (g *policyGenerator) setup() {
	w := g.w
	for i, member := range []map[string]any{
		{"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}},
		{"predicate": []any{map[string]any{"attr": "region", "op": "in", "value": []any{"eu", "us"}}}},
		{"predicate": []any{map[string]any{"attr": "preferred_username", "op": "regex", "value": "^user-[3-5]$"}}},
	} {
		id := w.must("POST", "/group", map[string]any{"name": fmt.Sprintf("g%d", i)})["id"].(string)
		g.groups = append(g.groups, id)
		w.must("POST", "/group/"+id+"/member", member)
	}
	// g3 is nested: whoever is in g0 or g1 is in g3.
	nested := w.must("POST", "/group", map[string]any{"name": "g3"})["id"].(string)
	g.groups = append(g.groups, nested)
	w.must("POST", "/group/"+nested+"/member", map[string]any{"member_group_id": g.groups[0]})
	w.must("POST", "/group/"+nested+"/member", map[string]any{"member_group_id": g.groups[1]})
	// a membership with a window that ends during the test, and one that starts during it
	w.must("POST", "/group/"+g.groups[2]+"/member", map[string]any{
		"predicate":  []any{map[string]any{"attr": "department", "op": "eq", "value": "eng"}},
		"expires_at": time.Now().Add(4 * time.Second).UTC().Format(time.RFC3339)})
	w.must("POST", "/group/"+g.groups[0]+"/member", map[string]any{
		"predicate":  []any{map[string]any{"attr": "region", "op": "eq", "value": "asia"}},
		"valid_from": time.Now().Add(4 * time.Second).UTC().Format(time.RFC3339)})

	for i := range 3 {
		role := w.must("POST", "/role", map[string]any{"name": fmt.Sprintf("role-%d", i)})["id"].(string)
		g.roles = append(g.roles, role)
		for range 2 + g.rng.Intn(2) {
			item := g.target(true)
			w.must("POST", "/role/"+role+"/permission", item)
		}
	}
	// access groups of a type, and a rule that names one
	w.must("POST", w.typePath("branch")+"/access_group", map[string]any{"name": "editor", "levels": []string{"read", "write"}})
	w.rule("branch", "", map[string]any{"subject": "user-0", "access": []string{"@editor"}})
}

type diffCase struct {
	subject string
	claims  engine.Claims
	context model.RequestContext
}

func diffCases(rng *rand.Rand, n int) []diffCase {
	var out []diffCase
	for range n {
		subject := fmt.Sprintf("user-%d", rng.Intn(7))
		claims := engine.Claims{"preferred_username": subject}
		if rng.Intn(4) != 0 {
			claims["department"] = []string{"sales", "eng", "ops"}[rng.Intn(3)]
		}
		if rng.Intn(4) != 0 {
			claims["region"] = []string{"eu", "us", "asia"}[rng.Intn(3)]
		}
		if rng.Intn(3) != 0 {
			claims["branch_name"] = fmt.Sprintf("Branch %d", 1+rng.Intn(4))
		}
		var ctx model.RequestContext
		switch rng.Intn(3) {
		case 0:
			ctx = model.RequestContext{"ip": "10.1.2.3"}
		case 1:
			ctx = model.RequestContext{"ip": "8.8.8.8"}
		}
		out = append(out, diffCase{subject: subject, claims: claims, context: ctx})
	}
	return out
}

// diffChecks asks about every resource and level of the types of a service, and about each type as a whole.
func diffChecks(types ...string) []model.CheckRequest {
	var out []model.CheckRequest
	for _, typeName := range types {
		for _, level := range diffLevels {
			out = append(out, model.CheckRequest{Type: typeName, Action: level})
			for _, id := range diffResources[typeName] {
				out = append(out, model.CheckRequest{Type: typeName, ResourceID: &id, Action: level})
			}
		}
	}
	return out
}

// firstDifference shows where two texts part.
func firstDifference(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	from := max(0, i-200)
	return fmt.Sprintf("at %d:\n  snapshot: …%s\n  queries:  …%s", i, a[from:min(len(a), i+300)], b[from:min(len(b), i+300)])
}

// wantChecks0 asks the query path again for the explained answers of a case, to count their kinds.
func wantChecks0(t *testing.T, a *engine.App, id uuid.UUID, c diffCase, env engine.Env, checks []model.CheckRequest) []model.CheckResult {
	t.Helper()
	out, err := a.CheckAccess(context.Background(), id, c.subject, env, checks, true, engine.MinimizeLatency)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestF1_11_TheSnapshotPathAnswersLikeTheQueryPath(t *testing.T) {
	w := newWorld(t)
	w.projectWorld()
	ctx := context.Background()

	// The engine of the query path: its own pool, the snapshot off.
	legacyCfg := *w.cfg
	legacyCfg.Snapshot.MaxBytes = -1
	pool, err := db.Connect(ctx, legacyCfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	legacy := engine.New(pool, &legacyCfg)
	t.Cleanup(legacy.Close)
	fast := w.app.App

	seed := time.Now().UnixNano()
	if v, err := strconv.ParseInt(os.Getenv("LICETUM_DIFF_SEED"), 10, 64); err == nil {
		seed = v
	}
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	g := &policyGenerator{w: w, rng: rng, bodies: map[string]map[string]any{}}
	g.setup()
	for range 70 {
		g.rule()
	}

	// What the comparison saw, so that it cannot pass by comparing empty answers.
	var nonEmpty, allowed, denied, noAccess int
	orgChecks, appChecks := diffChecks("division", "rrs", "branch"), diffChecks("item", "project")
	services := map[string][]model.CheckRequest{w.org: orgChecks, w.appSvc: appChecks}
	compare := func(label string, cases []diffCase) {
		t.Helper()
		for _, c := range cases {
			env := engine.NewEnv(c.claims, c.context)
			for service, checks := range services {
				id := uuid.MustParse(service)
				want, err := legacy.GetPermission(ctx, id, c.subject, env, engine.MinimizeLatency)
				if err != nil {
					t.Fatalf("%s: query path: %v", label, err)
				}
				got, err := fast.GetPermission(ctx, id, c.subject, env, engine.MinimizeLatency)
				if err != nil {
					t.Fatalf("%s: snapshot path: %v", label, err)
				}
				if len(want) > 0 {
					nonEmpty++
				}
				for _, r := range wantChecks0(t, legacy, id, c, env, checks) {
					switch {
					case r.Allowed:
						allowed++
					case r.Explanation != nil && r.Explanation.Decision == engine.DecisionDenied:
						denied++
					default:
						noAccess++
					}
				}
				if jsonOf(t, got) != jsonOf(t, want) {
					t.Fatalf("%s: permission of %s in %s differs (claims %v, context %v) %s",
						label, c.subject, service, c.claims, c.context, firstDifference(jsonOf(t, got), jsonOf(t, want)))
				}
				wantChecks, err := legacy.CheckAccess(ctx, id, c.subject, env, checks, true, engine.MinimizeLatency)
				if err != nil {
					t.Fatalf("%s: query path check: %v", label, err)
				}
				gotChecks, err := fast.CheckAccess(ctx, id, c.subject, env, checks, true, engine.MinimizeLatency)
				if err != nil {
					t.Fatalf("%s: snapshot path check: %v", label, err)
				}
				if jsonOf(t, gotChecks) != jsonOf(t, wantChecks) {
					t.Fatalf("%s: checks of %s in %s differ (claims %v, context %v) %s",
						label, c.subject, service, c.claims, c.context, firstDifference(jsonOf(t, gotChecks), jsonOf(t, wantChecks)))
				}
			}
		}
		if !fast.PolicySnapshotLoaded() {
			t.Fatalf("%s: the snapshot path did not use a snapshot", label)
		}
		if legacy.PolicySnapshotLoaded() {
			t.Fatalf("%s: the query path has a snapshot", label)
		}
	}

	cases := diffCases(rng, 120)
	defer func() {
		t.Logf("compared answers: %d non-empty permissions, %d allowed, %d denied, %d no access", nonEmpty, allowed, denied, noAccess)
		if nonEmpty < 20 || allowed < 100 || denied < 20 || noAccess < 100 {
			t.Errorf("the policy was too poor to prove anything: %d non-empty permissions, %d allowed, %d denied, %d no access",
				nonEmpty, allowed, denied, noAccess)
		}
	}()
	// Both engines read the resources from the same source, each on its own schedule: bring both to the same state first,
	// so that what is compared is the policy and not the moment of a synchronization.
	warm := func() {
		t.Helper()
		for _, c := range cases {
			env := engine.NewEnv(c.claims, c.context)
			for service, checks := range services {
				id := uuid.MustParse(service)
				for _, a := range []*engine.App{legacy, fast} {
					if _, err := a.GetPermission(ctx, id, c.subject, env, engine.HigherConsistency); err != nil {
						t.Fatal(err)
					}
					if _, err := a.CheckAccess(ctx, id, c.subject, env, checks, false, engine.HigherConsistency); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		time.Sleep(300 * time.Millisecond) // the refreshes the requests started in the background
	}
	warm()
	compare("first", cases)

	// The policy changes: rules go, rules are switched, new ones come; both paths follow.
	for _, id := range g.rules[:8] {
		w.must("DELETE", "/assignment_rule/"+id, nil)
	}
	for _, id := range g.rules[8:14] {
		body := map[string]any{}
		for k, v := range g.bodies[id] {
			body[k] = v
		}
		body["enabled"] = !(body["enabled"] == nil || body["enabled"] == true)
		w.must("PATCH", "/assignment_rule/"+id, body)
	}
	for range 15 {
		g.rule()
	}
	// A group is renamed and a role gets another item; a type gets a level hierarchy.
	w.must("PATCH", "/group/"+g.groups[3], map[string]any{"name": "g3-renamed"})
	w.must("POST", "/role/"+g.roles[0]+"/permission", map[string]any{"type_resource_id": w.typeByName["rrs"], "access": []string{"read"}})
	warm()
	compare("after changes", cases)

	// Time passes: the rules and memberships with a window that ends or starts now change the answers, and nothing in the
	// policy changed. Both paths take the clock, not a revision, into account.
	time.Sleep(6 * time.Second)
	warm()
	compare("after the windows", cases)

	// And the policy read for a fresh answer (consistency=higher) is the one of the queries.
	env := engine.NewEnv(cases[0].claims, cases[0].context)
	a, err := fast.GetPermission(ctx, uuid.MustParse(w.org), cases[0].subject, env, engine.HigherConsistency)
	if err != nil {
		t.Fatal(err)
	}
	b, err := legacy.GetPermission(ctx, uuid.MustParse(w.org), cases[0].subject, env, engine.HigherConsistency)
	if err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, a) != jsonOf(t, b) {
		t.Fatalf("higher consistency differs:\n%s\n%s", jsonOf(t, a), jsonOf(t, b))
	}
}
