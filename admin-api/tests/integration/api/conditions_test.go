//go:build integration

package api_test

// Integration tests of the rule language of stage 3 through the HTTP API: request attributes (context) and the
// answers that depend on them, the missing-attribute semantics of deny rules, the clock conditions, guards
// (requires_predicate, requires_group_id), resource_where over the attributes the synchronizers report, access
// groups and their references, and subject_kind. Same Testcontainers environment as hierarchy_test.go.

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/licetum/licetum/core/engine"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// checkWith asks the AuthZEN evaluations of the checks as the subject of claims, with the request attributes context, and
// returns each answer in the form the check API gave before them: {"allowed", "explanation"}, the explanation being there
// when it was asked for. A check is {"type", "resource_id", "action"}; without resource_id it is about the type as a whole.
// consistency is minimize_latency, the default, or higher. An evaluation that could not be decided fails the test: it is
// not a denial.
func (w *world) checkWith(serviceID string, claims jwt.MapClaims, context map[string]any, explain bool, consistency string,
	checks ...map[string]any) []map[string]any {
	w.t.Helper()
	questions := make([]authzenreq.Question, len(checks))
	for i, c := range checks {
		resourceType, _ := c["type"].(string)
		id, _ := c["resource_id"].(string)
		action, _ := c["action"].(string)
		questions[i] = authzenreq.Question{Resource: authzenreq.Resource{Type: resourceType, ID: id}, Action: action}
	}
	var opts []authzenreq.Option
	if context != nil {
		opts = append(opts, authzenreq.WithContext(context))
	}
	if explain {
		opts = append(opts, authzenreq.Explain())
	}
	switch consistency {
	case string(engine.HigherConsistency):
		opts = append(opts, authzenreq.Fresh())
	case string(engine.MinimizeLatency):
	default:
		w.t.Fatalf("consistency %q", consistency)
	}
	token := w.token(claims)
	status, body := w.ask(authzenreq.Evaluations(w.ref(serviceID), authzenreq.SubjectOf(token), questions, opts...), token)
	decisions, err := authzenreq.Decisions(status, body)
	if err != nil {
		w.t.Fatalf("evaluations: %v", err)
	}
	out := make([]map[string]any, len(decisions))
	for i, d := range decisions {
		out[i] = map[string]any{"allowed": d.Allowed}
		if reason, ok := d.Context["reason_admin"]; ok {
			out[i]["explanation"] = reason
		}
	}
	return out
}

// accessOf returns the access levels of one resource in a permission result.
func accessOf(result engine.PermissionResult, typeName, resourceID string) []string {
	for _, r := range result[typeName].Resources {
		if r.ID == resourceID {
			return r.Access
		}
	}
	return nil
}

// mustRaw performs an admin request that must succeed and returns its body as it is.
func (w *world) mustRaw(method, path string) []byte {
	w.t.Helper()
	status, raw := w.do(method, w.admin.URL+path, nil)
	if status != http.StatusOK {
		w.t.Fatalf("%s %s: %d %s", method, path, status, raw)
	}
	return raw
}

// projectType adds the type project: a synchronizer reads owner and tier as the attributes of its resources.
func (w *world) projectType() string {
	w.t.Helper()
	w.exec(`CREATE TABLE src_project (id text PRIMARY KEY, name text, parent_id text, owner text, tier text)`)
	w.exec(`INSERT INTO src_project VALUES ('P1', 'Project 1', NULL, 'alice', 'gold'),
		('P2', 'Project 2', NULL, 'bob', 'silver'), ('P3', 'Project 3', NULL, 'alice', 'silver')`)
	id := w.typeResource(w.org, "project", "")
	w.must("POST", w.typePath("project")+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id, owner, tier FROM src_project WHERE id = :id",
			"list_query": "SELECT id, name, parent_id, owner, tier FROM src_project",
		},
		"probe_resource_id": "P1",
	})
	return id
}

func TestIntegrationRequestAttributesDecideAndAreCachedByValue(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "", map[string]any{"predicate": []map[string]any{
		{"attr": "preferred_username", "op": "eq", "value": hierarchySubject},
		{"attr": "ip", "source": "context", "op": "in_cidr", "value": "10.0.0.0/8"},
	}})
	claims := jwt.MapClaims{"preferred_username": hierarchySubject}
	inside := map[string]any{"ip": "10.1.2.3"}
	outside := map[string]any{"ip": "192.168.0.1"}

	w.expectVisible(w.org, claims, inside, "branch", "B1", "B2", "B3", "B4")
	w.expectVisible(w.org, claims, outside, "branch")
	w.expectVisible(w.org, claims, nil, "branch")

	// The answers are cached per value of the attribute: alternating requests never see each other's answer.
	check := map[string]any{"type": "branch", "resource_id": "B1", "action": "read"}
	for _, step := range []struct {
		ctx     map[string]any
		allowed bool
	}{{inside, true}, {outside, false}, {inside, true}, {outside, false}} {
		res := w.checkWith(w.org, claims, step.ctx, false, "minimize_latency", check)
		if res[0]["allowed"] != step.allowed {
			t.Fatalf("context %v: got %v, want %v", step.ctx, res[0]["allowed"], step.allowed)
		}
	}

	// The explanation says the rule read the request, and the AuthZEN evaluation takes the attributes from its context.
	res := w.checkWith(w.org, claims, inside, true, "higher", check)
	if granted := res[0]["explanation"].(map[string]any)["granted_by"].([]any); len(granted) != 1 {
		t.Fatalf("granted_by: %v", granted)
	}
	status, raw := w.ask(authzenreq.Evaluation(w.ref(w.org), hierarchySubject, authzenreq.On("branch", "B2"), "read",
		authzenreq.WithContext(map[string]any{"ip": "10.9.9.9"})), w.token(claims))
	if allowed, err := authzenreq.Allowed(status, raw); err != nil || !allowed {
		t.Fatalf("authzen with an address inside the network: %d %s", status, raw)
	}

	// The admin simulation takes the same attributes without a token.
	sim := w.must("POST", "/service/"+w.org+"/simulate/check", map[string]any{
		"subject": hierarchySubject, "claims": map[string]any{"preferred_username": hierarchySubject},
		"context": outside, "checks": []map[string]any{check},
	})
	if results := sim["results"].([]any); results[0].(map[string]any)["allowed"] != false {
		t.Fatalf("simulation with an address outside the network: %v", sim)
	}

	// A context name with a space is ignored by an AuthZEN request (AZ06), as if the attribute were absent: the rule has no
	// address to read, so the evaluation is answered 200 and denied. An address that is not a network is refused.
	status, raw = w.ask(authzenreq.Evaluation(w.ref(w.org), hierarchySubject, authzenreq.On("branch", "B1"), "read",
		authzenreq.WithContext(map[string]any{"bad name": 1})), w.token(claims))
	if allowed, err := authzenreq.Allowed(status, raw); err != nil || allowed {
		t.Fatalf("context name with a space is ignored: want 200 and a denial, got %d %s (%v)", status, raw, err)
	}
	status, raw = w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["branch"], "access": []string{"read"}, "subject": "u",
		"predicate": []map[string]any{{"attr": "ip", "source": "context", "op": "in_cidr", "value": "10.0.0.0"}},
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("in_cidr with a bare address: %d %s", status, raw)
	}
}

func TestIntegrationDenyWithAMissingAttributeAppliesUnlessTheRuleSkipsIt(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "", map[string]any{"subject": hierarchySubject})
	cidr := []map[string]any{{"attr": "ip", "source": "context", "op": "in_cidr", "value": "10.99.0.0/16"}}

	// A new deny rule applies on a missing attribute: without the address, B2 is denied (fail closed).
	denyB2 := w.rule("branch", "B2", map[string]any{"effect": "deny", "access": []string{"read"}, "predicate": cidr})
	w.expectVisible(w.org, jwt.MapClaims{"preferred_username": hierarchySubject}, nil, "branch", "B1", "B3", "B4")
	res := w.checkWith(w.org, jwt.MapClaims{"preferred_username": hierarchySubject}, nil, true, "higher",
		map[string]any{"type": "branch", "resource_id": "B2", "action": "read"})
	denials := res[0]["explanation"].(map[string]any)["denials"].([]any)
	if len(denials) != 1 || denials[0].(map[string]any)["missing_attribute"] != true ||
		denials[0].(map[string]any)["rule"].(map[string]any)["rule_id"] != denyB2 {
		t.Fatalf("the denial on a missing attribute is not explained: %+v", res)
	}
	// With an address outside the denied network, the condition does not hold, and B2 is accessible again.
	w.expectVisible(w.org, jwt.MapClaims{"preferred_username": hierarchySubject},
		map[string]any{"ip": "10.1.1.1"}, "branch", "B1", "B2", "B3", "B4")

	// A deny rule that skips a missing attribute leaves B3 alone when the address is not known.
	w.rule("branch", "B3", map[string]any{"effect": "deny", "access": []string{"read"}, "predicate": cidr,
		"on_missing_attribute": "skip"})
	w.expectVisible(w.org, jwt.MapClaims{"preferred_username": hierarchySubject}, nil, "branch", "B1", "B3", "B4")
	w.expectVisible(w.org, jwt.MapClaims{"preferred_username": hierarchySubject},
		map[string]any{"ip": "10.99.4.4"}, "branch", "B1", "B4")

	// An allow rule grants nothing on a missing attribute, and may not be told to apply it.
	status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["branch"], "access": []string{"read"}, "subject": "u",
		"predicate": cidr, "on_missing_attribute": "apply",
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("apply on an allow rule: %d %s", status, raw)
	}
}

func TestIntegrationClockConditionsReadTheHourOfTheDecision(t *testing.T) {
	w := newWorld(t)
	me := []map[string]any{{"attr": "preferred_username", "op": "eq", "value": hierarchySubject}}
	w.rule("division", "D1", map[string]any{"predicate": append(slices.Clone(me),
		map[string]any{"attr": "hour", "source": "now", "op": "ge", "value": "0"},
		map[string]any{"attr": "weekday", "source": "now", "op": "in", "value": []string{"1", "2", "3", "4", "5", "6", "7"}})})
	w.rule("division", "D2", map[string]any{"predicate": append(slices.Clone(me),
		map[string]any{"attr": "hour", "source": "now", "op": "lt", "value": "0"})})
	w.expectVisible(w.org, jwt.MapClaims{"preferred_username": hierarchySubject}, nil, "division", "D1")

	// Only hour and weekday are read from the clock, and they are compared as numbers or days.
	status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["division"], "resource_id": "D1", "access": []string{"read"}, "subject": "u",
		"predicate": []map[string]any{{"attr": "minute", "source": "now", "op": "eq", "value": "1"}},
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("minute of the clock: %d %s", status, raw)
	}
}

func TestIntegrationGuardsNarrowARuleAndBlockTheGroupTheyUse(t *testing.T) {
	w := newWorld(t)
	auditors := w.must("POST", "/group", map[string]any{"name": "auditors"})["id"].(string)
	w.must("POST", "/group/"+auditors+"/member", map[string]any{
		"predicate": []map[string]any{{"attr": "team", "op": "eq", "value": "audit"}}})
	w.rule("branch", "", map[string]any{
		"predicate":          []map[string]any{{"attr": "preferred_username", "op": "eq", "value": hierarchySubject}},
		"requires_group_id":  auditors,
		"requires_predicate": []map[string]any{{"attr": "env", "op": "eq", "value": "prod"}},
	})

	both := jwt.MapClaims{"preferred_username": hierarchySubject, "team": "audit", "env": "prod"}
	noTeam := jwt.MapClaims{"preferred_username": hierarchySubject, "env": "prod"}
	noEnv := jwt.MapClaims{"preferred_username": hierarchySubject, "team": "audit"}
	w.expectVisible(w.org, both, nil, "branch", "B1", "B2", "B3", "B4")
	w.expectVisible(w.org, noTeam, nil, "branch")
	w.expectVisible(w.org, noEnv, nil, "branch")

	// The group the rule is guarded by cannot be deleted while the rule names it.
	status, raw := w.do("DELETE", w.admin.URL+"/group/"+auditors, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete of a guard group: %d %s", status, raw)
	}
	var blocked struct {
		Extra struct {
			BlockedBy []string `json:"blocked_by"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(raw, &blocked); err != nil || !slices.Contains(blocked.Extra.BlockedBy, "assignment_rule") {
		t.Fatalf("blocked_by does not name the rule: %s", raw)
	}
}

func TestIntegrationResourceWhereCoversTheResourcesWhoseAttributesMatch(t *testing.T) {
	w := newWorld(t)
	w.projectType()
	w.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["project"], "subject": hierarchySubject, "access": []string{"read"},
		"resource_where": []map[string]any{{"attr": "owner", "op": "eq", "value": "alice"}},
	})
	expectIDs(t, w.permission(w.org, nil), "project", "P1", "P3")

	// A list attribute counts through its elements, and an attribute a resource lacks covers nothing.
	w.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["project"], "subject": hierarchySubject, "access": []string{"write"},
		"resource_where": []map[string]any{{"attr": "tier", "op": "in", "value": []string{"gold"}}},
	})
	res := w.permission(w.org, nil)
	if got := accessOf(res, "project", "P1"); !slices.Equal(got, []string{"read", "write"}) {
		t.Fatalf("P1 access: %v", got)
	}
	if got := accessOf(res, "project", "P3"); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("P3 access: %v", got)
	}
	w.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["project"], "subject": hierarchySubject, "access": []string{"read"},
		"resource_where": []map[string]any{{"attr": "team", "op": "eq", "value": "x"}},
	})
	expectIDs(t, w.permission(w.org, nil), "project", "P1", "P3")

	// Its conditions read the resources, not the request: a source, a resource id and a reserved attribute are refused,
	// and so is a type without a synchronizer (no resources to match).
	bad := []map[string]any{
		{"type_resource_id": w.typeByName["project"], "subject": "u", "access": []string{"read"},
			"resource_where": []map[string]any{{"attr": "owner", "source": "claim", "op": "eq", "value": "a"}}},
		{"type_resource_id": w.typeByName["project"], "subject": "u", "access": []string{"read"}, "resource_id": "P1",
			"resource_where": []map[string]any{{"attr": "owner", "op": "eq", "value": "a"}}},
		{"type_resource_id": w.typeByName["project"], "subject": "u", "access": []string{"read"},
			"resource_where": []map[string]any{{"attr": "id", "op": "eq", "value": "a"}}},
	}
	for _, body := range bad {
		if status, raw := w.do("POST", w.admin.URL+"/assignment_rule", body); status != http.StatusUnprocessableEntity {
			t.Fatalf("resource_where %v: %d %s", body, status, raw)
		}
	}
	plain := w.typeResource(w.org, "plain", "")
	if status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": plain, "subject": "u", "access": []string{"read"},
		"resource_where": []map[string]any{{"attr": "owner", "op": "eq", "value": "a"}},
	}); status != http.StatusBadRequest {
		t.Fatalf("resource_where on a type without a synchronizer: %d %s", status, raw)
	}
}

func TestIntegrationAccessGroupsNameLevelsAndProtectTheirReferences(t *testing.T) {
	w := newWorld(t)
	base := w.typePath("branch") + "/access_group"

	editor := w.must("POST", base, map[string]any{"name": "editor", "levels": []string{"read", "write"}})["id"].(string)
	rule := w.rule("branch", "B1", map[string]any{"subject": hierarchySubject, "access": []string{"@editor"}})
	if got := accessOf(w.permission(w.org, nil), "branch", "B1"); !slices.Equal(got, []string{"read", "write"}) {
		t.Fatalf("the group's levels: %v", got)
	}

	// Its levels can change, and the rule follows them; its name cannot change while a rule names it.
	w.must("PATCH", base+"/"+editor, map[string]any{"name": "editor", "levels": []string{"read"}})
	if got := accessOf(w.permission(w.org, nil), "branch", "B1"); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("the changed levels: %v", got)
	}
	if status, raw := w.do("PATCH", w.admin.URL+base+"/"+editor, map[string]any{"name": "reviewer", "levels": []string{"read"}}); status != http.StatusConflict {
		t.Fatalf("rename of a used group: %d %s", status, raw)
	}
	if status, raw := w.do("DELETE", w.admin.URL+base+"/"+editor, nil); status != http.StatusConflict {
		t.Fatalf("delete of a used group: %d %s", status, raw)
	}

	// A reference to a group the type does not have is not found; the group's own levels are checked.
	if status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B2", "subject": "u", "access": []string{"@ghost"},
	}); status != http.StatusNotFound {
		t.Fatalf("reference to a missing group: %d %s", status, raw)
	}
	for _, levels := range [][]string{{"@"}, {"*"}, {"read", ""}} {
		if status, raw := w.do("POST", w.admin.URL+base, map[string]any{"name": "bad", "levels": levels}); status != http.StatusUnprocessableEntity {
			t.Fatalf("levels %v: %d %s", levels, status, raw)
		}
	}
	if status, raw := w.do("POST", w.admin.URL+base, map[string]any{"name": "has space", "levels": []string{"read"}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("group name with a space: %d %s", status, raw)
	}

	var listed []model.AccessGroup
	if err := json.Unmarshal(w.mustRaw("GET", base), &listed); err != nil || len(listed) != 1 || listed[0].Name != "editor" {
		t.Fatalf("list: %v %+v", err, listed)
	}

	// Once nothing names it, the group can go.
	w.must("DELETE", "/assignment_rule/"+rule, nil)
	if status, raw := w.do("DELETE", w.admin.URL+base+"/"+editor, nil); status != http.StatusOK {
		t.Fatalf("delete of an unused group: %d %s", status, raw)
	}
}

func TestIntegrationSubjectKindIsStoredAndChecked(t *testing.T) {
	w := newWorld(t)
	rule := w.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "subject": "svc-reporter",
		"access": []string{"read"}, "subject_kind": "service",
	})
	if rule["subject_kind"] != "service" {
		t.Fatalf("subject_kind: %v", rule["subject_kind"])
	}
	if status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "subject": "x",
		"access": []string{"read"}, "subject_kind": "robot",
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown subject_kind: %d %s", status, raw)
	}
}
