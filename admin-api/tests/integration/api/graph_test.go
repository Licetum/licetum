//go:build integration

package api_test

// Integration tests of the decision semantics on the resource DAG: denials and exceptions, several
// parents, per-edge inherit masks, access level implications, validity, fail-closed orphans, and the
// Check / AuthZEN / metrics endpoints. Same Testcontainers environment as hierarchy_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/core/engine"

	"github.com/golang-jwt/jwt/v5"
)

func (w *world) rule(typeName, resourceID string, extra map[string]any) string {
	w.t.Helper()
	rule := map[string]any{"type_resource_id": w.typeByName[typeName]}
	if resourceID != "" {
		rule["resource_id"] = resourceID
	}
	for k, v := range extra {
		rule[k] = v
	}
	return w.grant(rule)
}

func deny(levels ...string) map[string]any {
	return map[string]any{"effect": "deny", "access": levels}
}

func exception(levels ...string) map[string]any {
	return map[string]any{"exception": true, "access": levels}
}

func TestIntegrationDenyAndExceptions(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2", "B3")

	// A denial below a grant takes the level away from its whole subtree.
	w.rule("rrs", "R1", deny("read"))
	res := w.permission(w.org, nil)
	expectIDs(t, res, "rrs", "R2")
	expectIDs(t, res, "branch", "B3")
	expectIDs(t, res, "division", "D1")

	// An exception below the denial lifts it for what it covers only.
	w.rule("branch", "B2", exception("read"))
	res = w.permission(w.org, nil)
	expectIDs(t, res, "branch", "B2", "B3")

	// A denial below the exception wins again.
	w.rule("item", "I1", deny("read"))
	w.rule("branch", "B1", exception("read"))
	res = w.permission(w.appSvc, nil)
	expectIDs(t, res, "item")
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2", "B3")
}

func TestIntegrationDenyWholeTypeAndWildcard(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "", nil) // read on the whole type: every branch
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2", "B3", "B4")

	w.rule("branch", "B2", deny("*"))
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B3", "B4")

	// A denial on a whole type beats any point grant on it, an exception on the whole type lifts it.
	denyAll := w.rule("branch", "", deny("read"))
	if res := w.permission(w.org, nil); len(res["branch"].Resources) != 0 {
		t.Fatalf("a whole-type denial must take everything: %+v", res)
	}
	w.rule("branch", "B4", exception("read"))
	w.rule("branch", "B4", nil)
	expectIDs(t, w.permission(w.org, nil), "branch", "B4")
	w.must("DELETE", "/assignment_rule/"+denyAll, nil)
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B3", "B4")
}

func TestIntegrationNeAndNotInPredicates(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", map[string]any{"subject": nil, "predicate": []any{
		map[string]any{"attr": "department", "op": "eq", "value": "sales"},
		map[string]any{"attr": "username", "op": "ne", "value": "ivanovich"},
		map[string]any{"attr": "grade", "op": "not_in", "value": []string{"intern"}},
	}})
	got := func(claims engine.Claims) []string { return ids(w.permission(w.org, claims), "division") }
	if r := got(engine.Claims{"department": "sales", "username": "petrov", "grade": "senior"}); len(r) != 1 {
		t.Fatalf("petrov: %v", r)
	}
	for name, claims := range map[string]engine.Claims{
		"ivanovich":     {"department": "sales", "username": "ivanovich", "grade": "senior"},
		"intern":        {"department": "sales", "username": "petrov", "grade": "intern"},
		"missing claim": {"department": "sales", "grade": "senior"},
	} {
		if r := got(claims); len(r) != 0 {
			t.Errorf("%s must not match: %v", name, r)
		}
	}
}

func TestIntegrationMultipleParentsAndInherit(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.exec(`CREATE TABLE src_doc (id text PRIMARY KEY, name text)`)
	w.exec(`CREATE TABLE src_doc_parent (id text, parent_type text, parent_id text)`)
	w.exec(`INSERT INTO src_doc VALUES ('X1', 'Doc 1'), ('X2', 'Doc 2')`)
	// X1 lies under branch B1 and division D2, X2 under B3 only.
	w.exec(`INSERT INTO src_doc_parent VALUES ('X1','branch','B1'), ('X1','division','D2'), ('X2','branch','B3')`)

	inherit := func(levels ...string) any {
		if levels == nil {
			return nil
		}
		return levels
	}
	doc := w.must("POST", "/service/"+w.appSvc+"/type_resource", map[string]any{"name": "doc", "parents": []any{
		map[string]any{"type_resource_id": w.branch, "inherit": inherit()},
		map[string]any{"type_resource_id": w.division, "inherit": inherit("write")},
	}})
	if len(doc["parents"].([]any)) != 2 || doc["type_resource_parent_id"] != nil {
		t.Fatalf("doc parents: %+v", doc)
	}
	w.typeByName["doc"], w.serviceOfType["doc"] = doc["id"].(string), w.appSvc
	w.must("POST", w.typePath("doc")+"/resource/synchronizer", map[string]any{"config": map[string]any{
		"kind": "sql", "dsn": w.cfg.DB.URI,
		"query":              "SELECT id, name, NULL::text AS parent_id FROM src_doc WHERE id = :id",
		"list_query":         "SELECT id, name, NULL::text AS parent_id FROM src_doc",
		"parents_list_query": "SELECT id, parent_type, parent_id FROM src_doc_parent",
	}, "probe_resource_id": "X1"})

	// Through the branch parent everything is inherited.
	r := w.rule("branch", "B1", nil)
	expectIDs(t, w.permission(w.appSvc, nil), "doc", "X1")
	w.must("DELETE", "/assignment_rule/"+r, nil)

	// Through the division parent only "write" flows down: read on D2 does not reach X1 ...
	r = w.rule("division", "D2", nil)
	expectIDs(t, w.permission(w.appSvc, nil), "doc")
	w.must("DELETE", "/assignment_rule/"+r, nil)
	// ... write does.
	w.rule("division", "D2", map[string]any{"access": []string{"write"}})
	res := w.permission(w.appSvc, nil)
	expectIDs(t, res, "doc", "X1")
	if a := res["doc"].Resources[0].Access; len(a) != 1 || a[0] != "write" {
		t.Fatalf("access: %v", a)
	}

	// The type cannot get itself as an ancestor.
	status, raw := w.do("PATCH", w.admin.URL+w.typePath("division"), map[string]any{"name": "division", "parents": []any{
		map[string]any{"type_resource_id": w.typeByName["doc"]},
	}})
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusConflict {
		t.Fatalf("a cycle of types must be rejected: %d %s", status, raw)
	}
	_ = ctx
}

func TestIntegrationAccessImplies(t *testing.T) {
	w := newWorld(t)
	w.must("PATCH", w.typePath("branch"), map[string]any{"name": "branch",
		"parents":        []any{map[string]any{"type_resource_id": w.rrs}},
		"access_implies": map[string][]string{"write": {"read"}}})

	w.rule("branch", "B1", map[string]any{"access": []string{"write"}})
	res := w.permission(w.org, nil)
	if got := res["branch"].Resources; len(got) != 1 || !containsAll(got[0].Access, "read", "write") {
		t.Fatalf("write must include read: %+v", got)
	}
	// Denying read denies write as well, since write includes read.
	w.rule("branch", "B1", deny("read"))
	expectIDs(t, w.permission(w.org, nil), "branch")
}

func containsAll(have []string, want ...string) bool {
	for _, x := range want {
		found := false
		for _, h := range have {
			found = found || h == x
		}
		if !found {
			return false
		}
	}
	return true
}

func TestIntegrationValidity(t *testing.T) {
	w := newWorld(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	w.rule("division", "D1", map[string]any{"expires_at": past})
	w.rule("division", "D2", map[string]any{"valid_from": future})
	expectIDs(t, w.permission(w.org, nil), "division")

	w.rule("division", "D1", map[string]any{"valid_from": past, "expires_at": future})
	expectIDs(t, w.permission(w.org, nil), "division", "D1")

	status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{"type_resource_id": w.division,
		"resource_id": "D1", "subject": "u", "access": []string{"read"}, "valid_from": future, "expires_at": past})
	if status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
		t.Fatalf("expires_at before valid_from must be rejected: %d %s", status, raw)
	}
}

func TestIntegrationOrphanFailsClosed(t *testing.T) {
	w := newWorld(t)
	// B9 points to the rrs R9 that the source of rrs does not know (yet).
	w.source("branch", "B9", "Branch 9", "R9")
	w.rule("branch", "", nil)
	w.rule("rrs", "R1", deny("read"))
	res := w.permission(w.org, nil)
	// B9 may lie under R1 for all we know: no access until the hierarchy above it is known.
	expectIDs(t, res, "branch", "B3", "B4")

	w.source("rrs", "R9", "RRS 9", "D2")
	expectIDs(t, w.permission(w.org, nil), "branch", "B3", "B4", "B9")
}

// checkAccess performs POST /service/{id}/check as hierarchySubject.
func (w *world) checkAccess(serviceID string, explain bool, checks ...map[string]any) []map[string]any {
	w.t.Helper()
	status, raw := w.do("POST", w.check.URL+"/service/"+serviceID+"/check?consistency=higher",
		map[string]any{"checks": checks, "explain": explain},
		"Authorization", "Bearer "+w.token(jwt.MapClaims{"preferred_username": hierarchySubject}))
	if status != http.StatusOK {
		w.t.Fatalf("check: %d %s", status, raw)
	}
	var out struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		w.t.Fatal(err)
	}
	return out.Results
}

func TestIntegrationCheckExplainAuthzenMetrics(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	denyRule := w.rule("rrs", "R1", deny("read"))
	w.rule("branch", "B2", exception("read"))

	res := w.checkAccess(w.org, true,
		map[string]any{"type": "branch", "resource_id": "B3", "action": "read"},
		map[string]any{"type": "branch", "resource_id": "B1", "action": "read"},
		map[string]any{"type": "branch", "resource_id": "B2", "action": "read"},
		map[string]any{"type": "branch", "resource_id": "B4", "action": "read"},
		map[string]any{"type": "branch", "resource_id": "NOPE", "action": "read"},
		map[string]any{"type": "nothing", "action": "read"},
	)
	want := []struct {
		allowed  bool
		decision string
	}{{true, "allowed"}, {false, "denied"}, {true, "allowed"}, {false, "no_access"}, {false, "resource_not_found"}, {false, "type_not_found"}}
	if len(res) != len(want) {
		t.Fatalf("results: %+v", res)
	}
	for i, x := range want {
		expl, _ := res[i]["explanation"].(map[string]any)
		if res[i]["allowed"] != x.allowed || expl["decision"] != x.decision {
			t.Errorf("check %d: got %+v, want %+v", i, res[i], x)
		}
	}
	// B1 is denied by the rule on R1 and nothing lifts it.
	denials := res[1]["explanation"].(map[string]any)["denials"].([]any)
	if len(denials) != 1 || denials[0].(map[string]any)["rule"].(map[string]any)["rule_id"] != denyRule {
		t.Fatalf("denials of B1: %+v", denials)
	}
	// B2 shows the exception that lifts it.
	lifted := res[2]["explanation"].(map[string]any)["denials"].([]any)[0].(map[string]any)["lifted_by"].([]any)
	if len(lifted) != 1 {
		t.Fatalf("B2 must show what lifts the denial: %+v", res[2])
	}
	// Without explain the answer has no explanation.
	plain := w.checkAccess(w.org, false, map[string]any{"type": "branch", "resource_id": "B3", "action": "read"})
	if _, has := plain[0]["explanation"]; has || plain[0]["allowed"] != true {
		t.Fatalf("plain check: %+v", plain)
	}

	// AuthZEN.
	bearer := "Bearer " + w.token(jwt.MapClaims{"preferred_username": hierarchySubject})
	status, raw := w.do("POST", w.check.URL+"/service/"+w.org+"/access/v1/evaluation", map[string]any{
		"subject":  map[string]any{"type": "user", "id": hierarchySubject},
		"resource": map[string]any{"type": "branch", "id": "B3"},
		"action":   map[string]any{"name": "read"},
	}, "Authorization", bearer)
	if status != http.StatusOK || !strings.Contains(string(raw), `"decision":true`) {
		t.Fatalf("authzen: %d %s", status, raw)
	}
	status, raw = w.do("POST", w.check.URL+"/service/"+w.org+"/access/v1/evaluations", map[string]any{
		"subject": map[string]any{"type": "user", "id": hierarchySubject},
		"action":  map[string]any{"name": "read"},
		"evaluations": []any{
			map[string]any{"resource": map[string]any{"type": "branch", "id": "B3"}},
			map[string]any{"resource": map[string]any{"type": "branch", "id": "B1"}},
		},
	}, "Authorization", bearer)
	if status != http.StatusOK || !strings.Contains(string(raw), `[{"decision":true},{"decision":false}]`) {
		t.Fatalf("authzen batch: %d %s", status, raw)
	}
	// Another subject cannot be asked about.
	status, _ = w.do("POST", w.check.URL+"/service/"+w.org+"/access/v1/evaluation", map[string]any{
		"subject":  map[string]any{"type": "user", "id": "someone-else"},
		"resource": map[string]any{"type": "branch", "id": "B3"},
		"action":   map[string]any{"name": "read"},
	}, "Authorization", bearer)
	if status != http.StatusForbidden {
		t.Fatalf("authzen subject mismatch: %d", status)
	}

	// Metrics.
	status, raw = w.do("GET", w.check.URL+"/metrics", nil)
	for _, name := range []string{"licetum_check_decisions_total", "licetum_http_requests_total", "licetum_permission_compute_seconds"} {
		if status != http.StatusOK || !strings.Contains(string(raw), name) {
			t.Errorf("/metrics lacks %s (status %d)", name, status)
		}
	}
}

// projectWorld adds a type "project" (under branch) whose source reports an owner and a list of owners
// besides the fixed columns; they arrive as the attributes (meta) of the resources.
func (w *world) projectWorld() {
	w.t.Helper()
	w.exec(`CREATE TABLE src_project (id text PRIMARY KEY, name text, parent_id text, owner text, owners text[])`)
	w.exec(`INSERT INTO src_project VALUES
		('P1', 'Project 1', 'B1', 'alice', '{alice}'),
		('P2', 'Project 2', 'B1', 'bob', '{bob}'),
		('P3', 'Project 3', 'B3', 'alice', '{alice,dave}')`)
	project := w.must("POST", "/service/"+w.appSvc+"/type_resource", map[string]any{
		"name": "project", "parents": []any{map[string]any{"type_resource_id": w.branch}}})
	w.typeByName["project"], w.serviceOfType["project"] = project["id"].(string), w.appSvc
	w.must("POST", w.typePath("project")+"/resource/synchronizer", map[string]any{"config": map[string]any{
		"kind": "sql", "dsn": w.cfg.DB.URI,
		"query":      "SELECT id, name, parent_id, owner, owners FROM src_project WHERE id = :id",
		"list_query": "SELECT id, name, parent_id, owner, owners FROM src_project",
	}, "probe_resource_id": "P1"})
}

func TestIntegrationResourceAttributes(t *testing.T) {
	w := newWorld(t)
	w.projectWorld()

	// The resource list shows the attributes the source reported.
	status, raw := w.do("GET", w.admin.URL+w.typePath("project")+"/resource?resource_id=P3", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), `"owner":"alice"`) ||
		!strings.Contains(string(raw), `"owners":["alice","dave"]`) {
		t.Fatalf("resource P3: %d %s", status, raw)
	}

	// One rule: whoever the "username" claim names as the owner gets access to that project.
	byOwner := func(matchBy string) map[string]any {
		return map[string]any{"resource_from_claim": map[string]any{
			"claim": "username", "lookup_type_resource_id": w.typeByName["project"], "match_by": matchBy}}
	}
	rule := w.rule("project", "", byOwner("meta.owner"))
	alice, bob := engine.Claims{"username": "alice"}, engine.Claims{"username": "bob"}
	expectIDs(t, w.permission(w.appSvc, alice), "project", "P1", "P3")
	expectIDs(t, w.permission(w.appSvc, bob), "project", "P2")
	if got := w.permission(w.appSvc, engine.Claims{"username": "nobody"}); len(got) != 0 {
		t.Fatalf("an unknown owner must get nothing: %+v", got)
	}

	// A change of the owner in the source reaches the permissions after the next synchronization.
	w.exec(`UPDATE src_project SET owner = 'bob' WHERE id = 'P1'`)
	expectIDs(t, w.permission(w.appSvc, alice), "project", "P3")
	expectIDs(t, w.permission(w.appSvc, bob), "project", "P1", "P2")

	// The attribute may be a list: the claim only has to be one of its items.
	w.must("DELETE", "/assignment_rule/"+rule, nil)
	w.rule("project", "", byOwner("meta.owners"))
	expectIDs(t, w.permission(w.appSvc, engine.Claims{"username": "dave"}), "project", "P3")

	// The anchor may lie above the matched resource: the branches of the owner's projects cascade down.
	w.rule("branch", "", map[string]any{"resource_from_claim": map[string]any{
		"claim": "username", "lookup_type_resource_id": w.typeByName["project"], "match_by": "meta.owner"}})
	res := w.permission(w.org, bob)
	expectIDs(t, res, "branch", "B1")
	expectIDs(t, w.permission(w.appSvc, bob), "item", "I1")

	// An invalid attribute name is rejected.
	status, raw = w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["project"], "subject": "u", "access": []string{"read"},
		"resource_from_claim": map[string]any{"claim": "c", "lookup_type_resource_id": w.typeByName["project"], "match_by": "meta.bad name"}})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("a bad attribute name must be rejected: %d %s", status, raw)
	}
}
