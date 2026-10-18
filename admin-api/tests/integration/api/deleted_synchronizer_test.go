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

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

func TestIntegrationADeletedSynchronizerLeavesNoResourcesToDecide(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	// part (service app) below item: its source names I1 as the parent of P1.
	w.exec(`CREATE TABLE src_part (id text PRIMARY KEY, name text, parent_id text)`)
	w.source("part", "P1", "Part 1", "I1")
	w.typeResource(w.appSvc, "part", w.item)
	itemSource := func() map[string]any {
		return map[string]any{
			"config": map[string]any{
				"kind": "sql", "dsn": w.cfg.DB.URI,
				"query":      "SELECT id, name, parent_id FROM src_item WHERE id = :id",
				"list_query": "SELECT id, name, parent_id FROM src_item",
			},
			"probe_resource_id": "I1",
		}
	}
	w.must("POST", w.typePath("part")+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id FROM src_part WHERE id = :id",
			"list_query": "SELECT id, name, parent_id FROM src_part",
		},
		"probe_resource_id": "P1",
	})
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
