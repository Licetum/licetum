//go:build integration

package api_test

// The resources of a type whose synchronizer was deleted. Its rows stay in the local copy (a synchronizer created again
// finds them), but the type is not_sync and has no resources: every path that decides about a resource must say so, and a
// type below it, whose resources name those rows as parents, must get nothing through them. The decisions themselves are
// in the tables dec06_synchronizer_deleted.yaml and dec06_below_a_deleted_synchronizer.yaml; this test goes through the
// paths: the check on the snapshot and on the query path, the AuthZEN evaluations and searches, the admin simulation, the
// reverse lookup, the coverage question, the resource itself and the grant preview.

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

// partBelowItem adds the type part (service app) below item: its source names I1 as the parent of P1.
func (w *world) partBelowItem() {
	w.t.Helper()
	w.exec(`CREATE TABLE src_part (id text PRIMARY KEY, name text, parent_id text)`)
	w.source("part", "P1", "Part 1", "I1")
	w.typeResource(w.appSvc, "part", w.item)
	w.must("POST", w.typePath("part")+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id FROM src_part WHERE id = :id",
			"list_query": "SELECT id, name, parent_id FROM src_part",
		},
		"probe_resource_id": "P1",
	})
}

// itemSource is the body that creates the synchronizer of item again.
func (w *world) itemSource() map[string]any {
	return map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id FROM src_item WHERE id = :id",
			"list_query": "SELECT id, name, parent_id FROM src_item",
		},
		"probe_resource_id": "I1",
	}
}

func TestIntegrationADeletedSynchronizerLeavesNoResourcesToDecide(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.partBelowItem()
	itemSource := w.itemSource
	// Read on D1, which reaches I1 and, through it, P1; and a point rule on I4, which dies with the synchronizer of item.
	w.rule("division", "D1", nil)
	w.rule("item", "I4", nil)

	questions := []map[string]any{
		{"type": "item", "resource_id": "I1", "action": "read"},
		{"type": "item", "resource_id": "I4", "action": "read"},
		{"type": "part", "resource_id": "P1", "action": "read"},
	}
	asked := make([]model.CheckRequest, len(questions))
	for i, q := range questions {
		id := q["resource_id"].(string)
		asked[i] = model.CheckRequest{Type: q["type"].(string), ResourceID: &id, Action: "read"}
	}
	decisions := func(results []map[string]any) []string {
		var out []string
		for _, r := range results {
			out = append(out, r["explanation"].(map[string]any)["decision"].(string))
		}
		return out
	}
	// The snapshot path: the engine asked with the default consistency, which decides from the policy in memory (the
	// HTTP checks of the test ask with consistency=higher, which reads the policy with queries).
	snapshotDecisions := func() []string {
		t.Helper()
		results, err := w.app.CheckAccess(ctx, uuid.MustParse(w.appSvc), hierarchySubject,
			engine.NewEnv(engine.Claims{"preferred_username": hierarchySubject}, nil), asked, true, engine.MinimizeLatency)
		if err != nil {
			t.Fatal(err)
		}
		if !w.app.PolicySnapshotLoaded() {
			t.Fatal("the check did not decide from a policy snapshot")
		}
		var out []string
		for _, r := range results {
			out = append(out, r.Explanation.Decision)
		}
		return out
	}
	self := "Bearer " + w.token(jwt.MapClaims{"preferred_username": hierarchySubject})
	subject := map[string]any{"type": "user", "id": hierarchySubject}
	authzen := func(path string, body map[string]any) map[string]any {
		t.Helper()
		body["subject"] = subject
		status, raw := w.do(http.MethodPost, w.check.URL+"/service/"+w.appSvc+"/access/v1/"+path+"?consistency=higher", body,
			"Authorization", self)
		if status != http.StatusOK {
			t.Fatalf("authzen %s: %d %s", path, status, raw)
		}
		return decodeMap(t, raw)
	}
	actionsOn := func(id string) []string {
		got := []string{}
		for _, a := range authzen("search/action", map[string]any{"resource": map[string]any{"type": "item", "id": id}})["results"].([]any) {
			got = append(got, a.(map[string]any)["name"].(string))
		}
		return got
	}
	covered := func(typeName, id string) bool {
		t.Helper()
		status, raw := w.do(http.MethodGet, w.admin.URL+w.typePath(typeName)+"/resource/covered?resource_id="+id+"&granted_resource_id=D1", nil)
		var out bool
		if status != http.StatusOK || json.Unmarshal(raw, &out) != nil {
			t.Fatalf("covered %s: %d %s", id, status, raw)
		}
		return out
	}
	previewOfD1 := func() map[string]float64 {
		t.Helper()
		res := w.must("POST", "/assignment_rule/preview", map[string]any{
			"type_resource_id": w.division, "resource_id": "D1", "subject": "alice", "access": []string{"read"}})
		types := map[string]float64{}
		for _, x := range res["types"].([]any) {
			entry := x.(map[string]any)
			types[entry["type"].(string)] = entry["count"].(float64)
		}
		return types
	}

	// While item has its synchronizer, D1 reaches I1 and P1, and the point rule grants I4.
	allowed := []string{engine.DecisionAllowed, engine.DecisionAllowed, engine.DecisionAllowed}
	if got := decisions(w.checkAccess(w.appSvc, true, questions...)); !slices.Equal(got, allowed) {
		t.Fatalf("before: %v", got)
	}
	if got := actionsOn("I1"); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("before: actions on I1 %v", got)
	}
	if !covered("item", "I1") || !covered("part", "P1") {
		t.Fatal("before: D1 covers I1 and P1")
	}
	if types := previewOfD1(); types["item"] != 1 || types["part"] != 1 {
		t.Fatalf("before: the preview of a rule on D1 %v", types)
	}

	w.must(http.MethodDelete, w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)

	// The rows of item are still in the local copy: the test is about them.
	if n := w.countRows(`SELECT count(*) FROM resource_cache WHERE type_resource_id = $1 AND active`, w.item); n != 2 {
		t.Fatalf("the rows of item: %d, want 2", n)
	}
	gone := []string{engine.DecisionResourceNotFound, engine.DecisionResourceNotFound, engine.DecisionNoAccess}
	results := w.checkAccess(w.appSvc, true, questions...)
	if got := decisions(results); !slices.Equal(got, gone) {
		t.Fatalf("the query path: %v, want %v (%s)", got, gone, mustJSON(results))
	}
	for _, r := range results {
		if r["allowed"] != false || len(r["explanation"].(map[string]any)["granted_by"].([]any)) != 0 {
			t.Fatalf("an answer about what is not there grants nothing: %s", mustJSON(r))
		}
	}
	if got := snapshotDecisions(); !slices.Equal(got, gone) {
		t.Fatalf("the snapshot path: %v, want %v", got, gone)
	}
	if got := w.permission(w.appSvc, nil); len(got) != 0 {
		t.Fatalf("the permission lists nothing of item and part: %+v", got)
	}

	// AuthZEN: the evaluations, the actions on a resource and the resources of the type.
	if d := authzen("evaluation", authzenEval("item", "I1", "read"))["decision"]; d != false {
		t.Fatalf("evaluation of I1: %v", d)
	}
	batch := authzen("evaluations", map[string]any{"evaluations": []any{
		authzenEval("item", "I1", "read"), authzenEval("item", "I4", "read"), authzenEval("part", "P1", "read")}})
	if got := decisionsOf(t, batch); !equalBools(got, false, false, false) {
		t.Fatalf("evaluations: %v", got)
	}
	if got := actionsOn("I1"); len(got) != 0 {
		t.Fatalf("actions on I1: %v", got)
	}
	if got := authzen("search/resource", map[string]any{"resource": map[string]any{"type": "item"},
		"action": map[string]any{"name": "read"}})["results"].([]any); len(got) != 0 {
		t.Fatalf("resources of item: %v", got)
	}

	// The admin simulation.
	sim := w.must("POST", "/service/"+w.appSvc+"/simulate/check", map[string]any{
		"subject": hierarchySubject, "claims": map[string]any{"preferred_username": hierarchySubject},
		"checks": questions, "explain": true,
	})
	var simulated []map[string]any
	for _, r := range sim["results"].([]any) {
		simulated = append(simulated, r.(map[string]any))
	}
	if got := decisions(simulated); !slices.Equal(got, gone) {
		t.Fatalf("the simulation: %v", got)
	}

	// The reverse lookup, the resource itself, the coverage question and the preview of a rule on D1.
	status, raw := w.do(http.MethodGet, w.admin.URL+w.typePath("item")+"/access?resource_id=I1", nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "resource_not_found")
	status, raw = w.do(http.MethodGet, w.admin.URL+w.typePath("item")+"/resource?resource_id=I1", nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "resource_not_found")
	if covered("item", "I1") || covered("part", "P1") {
		t.Fatal("D1 covers neither I1, which is not there, nor P1 through it")
	}
	if types := previewOfD1(); types["item"] != 0 || types["part"] != 0 || types["branch"] != 3 {
		t.Fatalf("the preview of a rule on D1 counts no resource of item nor of part: %v", types)
	}

	// A synchronizer created again finds the rows: the resources are back, and so is the route to P1.
	w.must("POST", w.typePath("item")+"/resource/synchronizer", itemSource())
	if got := decisions(w.checkAccess(w.appSvc, true, questions...)); !slices.Equal(got, allowed) {
		t.Fatalf("with a synchronizer again, the query path: %v", got)
	}
	if got := snapshotDecisions(); !slices.Equal(got, allowed) {
		t.Fatalf("with a synchronizer again, the snapshot path: %v", got)
	}
	if !covered("item", "I1") || !covered("part", "P1") {
		t.Fatal("with a synchronizer again, D1 covers I1 and P1")
	}
}

// A synchronizer deleted (or created again) cuts (or restores) the routes through the rows of its type in the closure of
// the types below it in the same write, so that the decision about a type below does not wait for that type to be
// synchronized: whatever its source or the worker do, P1 of part, which names I1 of item as its parent, gets nothing from
// D1 while item has no synchronizer, and gets it again when item has one.
func TestIntegrationADeletedSynchronizerCutsTheRoutesBelowItWhateverTheLowerSourceDoes(t *testing.T) {
	for _, variant := range []string{"the source of part is down", "no worker runs"} {
		t.Run(variant, func(t *testing.T) {
			w := newWorld(t)
			ctx := context.Background()
			w.partBelowItem()
			w.rule("division", "D1", nil)

			id := "P1"
			asked := []model.CheckRequest{{Type: "part", ResourceID: &id, Action: "read"}}
			question := map[string]any{"type": "part", "resource_id": "P1", "action": "read"}
			// The query path (consistency=higher through the API) and the snapshot path.
			decisions := func() (queries, snapshot string) {
				t.Helper()
				queries = w.checkAccess(w.appSvc, true, question)[0]["explanation"].(map[string]any)["decision"].(string)
				results, err := w.app.CheckAccess(ctx, uuid.MustParse(w.appSvc), hierarchySubject,
					engine.NewEnv(engine.Claims{"preferred_username": hierarchySubject}, nil), asked, true, engine.MinimizeLatency)
				if err != nil {
					t.Fatal(err)
				}
				return queries, results[0].Explanation.Decision
			}
			// The ancestors P1 has in the closure, by external id.
			ancestors := func() []string {
				t.Helper()
				rows, err := w.pool.Query(ctx, `
					SELECT a.external_id FROM resource_ancestor ra
					JOIN resource_cache a ON a.id = ra.ancestor_id
					JOIN resource_cache d ON d.id = ra.descendant_id
					WHERE d.type_resource_id = $1 AND d.external_id = 'P1' ORDER BY 1`, w.typeByName["part"])
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var out []string
				for rows.Next() {
					var ext string
					if err := rows.Scan(&ext); err != nil {
						t.Fatal(err)
					}
					out = append(out, ext)
				}
				return out
			}
			dataRevision := func() int64 {
				t.Helper()
				var rev int64
				if err := w.pool.QueryRow(ctx, `SELECT revision FROM revision_state WHERE scope = 'data:' || $1`, w.appSvc).Scan(&rev); err != nil {
					t.Fatal(err)
				}
				return rev
			}

			if q, s := decisions(); q != engine.DecisionAllowed || s != engine.DecisionAllowed {
				t.Fatalf("while item has its synchronizer: %s, %s", q, s)
			}
			if got, want := ancestors(), []string{"B1", "D1", "I1", "P1", "R1"}; !slices.Equal(got, want) {
				t.Fatalf("the ancestors of P1 %v, want %v", got, want)
			}

			// From here on part is not synchronized again.
			if variant == "no worker runs" {
				w.stopWorker()
			} else {
				w.exec(`ALTER TABLE src_part RENAME TO src_part_off`)
			}

			before := dataRevision()
			w.must(http.MethodDelete, w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
			// The write cut the routes through the rows of item: nothing was synchronized since.
			if got := ancestors(); !slices.Equal(got, []string{"P1"}) {
				t.Fatalf("after the synchronizer of item was deleted, the ancestors of P1 %v, want P1 alone", got)
			}
			if got := dataRevision(); got <= before {
				t.Fatalf("the data revision of the service of part did not move: %d, was %d", got, before)
			}
			if q, s := decisions(); q != engine.DecisionNoAccess || s != engine.DecisionNoAccess {
				t.Fatalf("without a synchronizer of item, P1 gets nothing from D1: %s on the query path, %s on the snapshot path", q, s)
			}

			// And the routes come back with the synchronizer, before anything is synchronized.
			before = dataRevision()
			w.must("POST", w.typePath("item")+"/resource/synchronizer", w.itemSource())
			if got, want := ancestors(), []string{"B1", "D1", "I1", "P1", "R1"}; !slices.Equal(got, want) {
				t.Fatalf("after the synchronizer of item was created again, the ancestors of P1 %v, want %v", got, want)
			}
			if got := dataRevision(); got <= before {
				t.Fatalf("the data revision of the service of part did not move: %d, was %d", got, before)
			}
			if q, s := decisions(); q != engine.DecisionAllowed || s != engine.DecisionAllowed {
				t.Fatalf("with a synchronizer of item again: %s, %s", q, s)
			}
		})
	}
}

// A synchronization of part that read the type graph before the synchronizer of item was deleted, and applies its result
// after, writes the closure of the rows it changed under the modes of the types as the transaction sees them: P2, which it
// brings in with I1 as its parent, gets no route through the rows of item.
func TestIntegrationASynchronizationThatStartedBeforeTheSynchronizerWasDeletedDoesNotRestoreTheRoute(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.partBelowItem()
	w.rule("division", "D1", nil)
	w.checkAccess(w.appSvc, true, map[string]any{"type": "part", "resource_id": "P1", "action": "read"}) // everything synchronized

	part := uuid.MustParse(w.typeByName["part"])
	w.stopWorker()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	w.worker(nil, engine.SyncHooks{AfterFetch: func(ctx context.Context, ids []uuid.UUID) {
		if !slices.Contains(ids, part) {
			return // only the job of part is held
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}})

	// P2 appears in the source of part; the job that brings it in has read the type graph, and waits after the fetch.
	w.source("part", "P2", "Part 2", "I1")
	if _, err := w.app.Coordinator().Ensure(ctx, []uuid.UUID{part}, engine.SyncOptions{Fresh: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the job of part never fetched its source")
	}

	w.must(http.MethodDelete, w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
	close(release)

	deadline := time.Now().Add(10 * time.Second)
	for w.countRows(`SELECT count(*) FROM resource_cache WHERE type_resource_id = $1 AND external_id = 'P2'`, part) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the job of part never applied P2")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, p := range []string{"P1", "P2"} {
		if n := w.countRows(`
			SELECT count(*) FROM resource_ancestor ra
			JOIN resource_cache d ON d.id = ra.descendant_id
			WHERE d.type_resource_id = $1 AND d.external_id = $2 AND ra.ancestor_id <> ra.descendant_id`, part, p); n != 0 {
			t.Fatalf("%s has %d ancestors in the closure, though the synchronizer of item was deleted before the job applied", p, n)
		}
	}
}

// A resource_from_claim that looks a resource up in a type without a synchronizer, or climbs through one, finds nothing:
// the rule list and the items of a role say its target does not take effect (target_valid) and the lint reports it, until
// the synchronizer is there again. A claim looked up among the resources of a type that has them is not touched.
func TestIntegrationARuleThatLooksInATypeWithoutASynchronizerIsMarkedDead(t *testing.T) {
	w := newWorld(t)
	w.partBelowItem()
	claim := func(name, lookup string) map[string]any {
		return map[string]any{"resource_from_claim": map[string]any{
			"claim": name, "lookup_type_resource_id": w.typeByName[lookup], "match_by": "name"}}
	}
	withEffect := func(m map[string]any) map[string]any { m["effect"] = "deny"; return m }
	inItem := w.rule("branch", "", withEffect(claim("item_name", "item")))      // looks in item
	throughItem := w.rule("branch", "", withEffect(claim("part_name", "part"))) // climbs from part through item to branch
	inBranch := w.rule("branch", "", withEffect(claim("branch_name", "branch")))
	role := w.must("POST", "/role", map[string]any{"name": "by-item"})["id"].(string)
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.typeByName["branch"], "access": []string{"read"}, "resource_from_claim": claim("item_name", "item")["resource_from_claim"]})

	valid := func() (rules map[string]bool, items []bool) {
		t.Helper()
		var list []model.AssignmentRuleWithContext
		if err := json.Unmarshal(w.mustRaw("GET", "/assignment_rule"), &list); err != nil {
			t.Fatal(err)
		}
		rules = map[string]bool{}
		for _, r := range list {
			rules[r.ID.String()] = r.TargetValid
		}
		var perms []model.RolePermissionWithContext
		if err := json.Unmarshal(w.mustRaw("GET", "/role/"+role+"/permission"), &perms); err != nil {
			t.Fatal(err)
		}
		for _, p := range perms {
			items = append(items, p.TargetValid)
		}
		return rules, items
	}
	lintedDead := func() map[string]bool {
		t.Helper()
		var report model.LintReport
		if err := json.Unmarshal(w.mustRaw("GET", "/service/"+w.org+"/policy/lint"), &report); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, f := range report.Findings {
			if f.Code == "lint_rule_lookup_dead" && f.RuleID != nil {
				if f.Severity != model.LintError || f.Message == "" {
					t.Fatalf("the finding: %+v", f)
				}
				out[f.RuleID.String()] = true
			}
		}
		return out
	}
	expect := func(when string, in, through, branch, item bool) {
		t.Helper()
		rules, items := valid()
		if rules[inItem] != !in || rules[throughItem] != !through || rules[inBranch] != !branch || len(items) != 1 || items[0] != !item {
			t.Fatalf("%s: target_valid %v, items %v; want the lookup in item dead=%v, through item dead=%v, in branch dead=%v, role item dead=%v",
				when, rules, items, in, through, branch, item)
		}
		dead := lintedDead()
		if dead[inItem] != in || dead[throughItem] != through || dead[inBranch] != branch {
			t.Fatalf("%s: lint findings %v", when, dead)
		}
	}

	expect("while item has its synchronizer", false, false, false, false)
	w.must(http.MethodDelete, w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
	expect("without a synchronizer of item", true, true, false, true)
	w.must("POST", w.typePath("item")+"/resource/synchronizer", w.itemSource())
	expect("with a synchronizer of item again", false, false, false, false)
}

// The denials that read the resources of a type without a synchronizer, or the rows that type left, keep an anchor that is no
// resource (the tables dec06_denials_that_lose_their_type.yaml decide it): P1 of part names I1 of item as its parent, so a
// denial by claim whose anchor is an item covers P1 once item has no resources, on the query path and on the snapshot path,
// in the permission list and in the explanation. The same denial does not cover P3, which has no parent.
func TestIntegrationADenialThatLosesItsTypeStaysInForceOnBothPaths(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.partBelowItem()
	w.source("part", "P3", "Part 3", "")
	w.rule("part", "", nil)
	denial := w.rule("item", "", map[string]any{"effect": "deny", "access": []string{"*"}, "resource_from_claim": map[string]any{
		"claim": "part_name", "lookup_type_resource_id": w.typeByName["part"], "match_by": "name"}})

	claims := engine.Claims{"preferred_username": hierarchySubject, "part_name": "Part 1"}
	asked := func(ids ...string) []model.CheckRequest {
		var out []model.CheckRequest
		for _, id := range ids {
			out = append(out, model.CheckRequest{Type: "part", ResourceID: &id, Action: "read"})
		}
		return out
	}
	decide := func(consistency engine.Consistency) []model.CheckResult {
		t.Helper()
		results, err := w.app.CheckAccess(ctx, uuid.MustParse(w.appSvc), hierarchySubject, engine.NewEnv(claims, nil),
			asked("P1", "P3"), true, consistency)
		if err != nil {
			t.Fatal(err)
		}
		return results
	}
	// verify checks the answers about P1 and P3: P3 has no parent, so the denial never covers it; P1 is denied, by the item the
	// claim finds while item has resources, and as an assumed denial anchored at no resource while it has none.
	verify := func(when string, results []model.CheckResult, p1 string, assumed bool) {
		t.Helper()
		if results[0].Explanation.Decision != p1 || results[1].Explanation.Decision != engine.DecisionAllowed {
			t.Fatalf("%s: P1 %s (want %s), P3 %s (want allowed)", when, results[0].Explanation.Decision, p1, results[1].Explanation.Decision)
		}
		if p1 != engine.DecisionDenied {
			return
		}
		d := results[0].Explanation.Denials
		if len(d) != 1 || d[0].Rule.RuleID.String() != denial || d[0].UnresolvedParent != assumed || (d[0].Rule.Anchor.ResourceID == nil) != assumed {
			t.Fatalf("%s: the denial of P1: %+v", when, d)
		}
	}

	verify("while item has its synchronizer", decide(engine.HigherConsistency), engine.DecisionDenied, false)
	w.must(http.MethodDelete, w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
	verify("the query path", decide(engine.HigherConsistency), engine.DecisionDenied, true)
	verify("the snapshot path", decide(engine.MinimizeLatency), engine.DecisionDenied, true)
	if !w.app.PolicySnapshotLoaded() {
		t.Fatal("the check did not decide from a policy snapshot")
	}
	list, err := w.app.GetPermission(ctx, uuid.MustParse(w.appSvc), hierarchySubject, engine.NewEnv(claims, nil), engine.HigherConsistency)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(list, "part"); !slices.Equal(got, []string{"P3"}) {
		t.Fatalf("the permission list names %v, want P3 alone", got)
	}
	// A request without the claim is not under the denial.
	claims = engine.Claims{"preferred_username": hierarchySubject}
	verify("without the claim", decide(engine.HigherConsistency), engine.DecisionAllowed, false)
	claims = engine.Claims{"preferred_username": hierarchySubject, "part_name": "Part 1"}

	w.must("POST", w.typePath("item")+"/resource/synchronizer", w.itemSource())
	verify("with a synchronizer of item again", decide(engine.HigherConsistency), engine.DecisionDenied, false)
}

// A synchronization that is writing the component of the type holds the lock the change waits for: the change is refused with
// a 503 that says to retry (nothing is written), and goes through once the lock is free.
func TestIntegrationChangingASynchronizerWaitsForTheSynchronizationOfItsComponentAndAnswersBusy(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.partBelowItem()
	w.rule("division", "D1", nil)
	w.checkAccess(w.appSvc, true, map[string]any{"type": "part", "resource_id": "P1", "action": "read"}) // synchronized
	w.app.Hooks.LockTimeout = 300 * time.Millisecond

	graph, err := w.app.LoadTypeGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := engine.ComponentKeys(graph, []uuid.UUID{uuid.MustParse(w.item)})
	holder, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = holder.Rollback(ctx)
		}
	}
	defer release()
	for _, key := range keys {
		if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
			t.Fatal(err)
		}
	}

	status, raw := w.do(http.MethodDelete, w.admin.URL+w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
	requireErrorCode(t, status, raw, http.StatusServiceUnavailable, "synchronizer_change_busy")
	if n := w.countRows(`SELECT count(*) FROM synchronizer WHERE type_resource_id = $1 AND deleted_at IS NULL`, w.item); n != 1 {
		t.Fatalf("the synchronizer of item was changed by a refused request: %d live", n)
	}

	release()
	w.must(http.MethodDelete, w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
	if n := w.countRows(`SELECT count(*) FROM synchronizer WHERE type_resource_id = $1 AND deleted_at IS NULL`, w.item); n != 0 {
		t.Fatalf("the synchronizer of item is still live after the retry: %d", n)
	}
}
