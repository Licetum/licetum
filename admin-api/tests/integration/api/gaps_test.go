//go:build integration

package api_test

// Integration tests of what the policy document and the batch carry beyond the common cases: role items with conditions
// and access groups, nested groups with a subject kind, list attributes in the conditions over resources, and the conflicts
// of given ids. Same Testcontainers environment as hierarchy_test.go.

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func TestIntegrationBatchRefusesAnIdAnotherEntityHas(t *testing.T) {
	w := newWorld(t)
	taken := "00000000-0000-4000-8000-0000000000c1"
	w.must("POST", "/batch", map[string]any{"operations": []map[string]any{
		{"op": "create", "entity": "role", "id": taken, "body": map[string]any{"name": "first"}},
	}})

	status, raw := w.do("POST", w.admin.URL+"/batch", map[string]any{"operations": []map[string]any{
		{"op": "create", "entity": "role", "body": map[string]any{"name": "other"}},
		{"op": "create", "entity": "role", "id": taken, "body": map[string]any{"name": "second"}},
	}})
	if status != http.StatusConflict || !strings.Contains(string(raw), `"operation":1`) {
		t.Fatalf("a given id of another role: %d %s", status, raw)
	}
	if strings.Contains(string(w.mustRaw("GET", "/role")), `"other"`) {
		t.Fatal("the operation before the conflict was applied")
	}
}

func TestIntegrationPolicyImportRefusesARuleIdOfAnotherService(t *testing.T) {
	w := newWorld(t)
	rule := w.rule("branch", "B1", map[string]any{"subject": hierarchySubject})

	// A document of the app service that names a rule with the id of a rule of the org service.
	doc := model.PolicyDocument{Format: model.PolicyDocumentFormat, Service: "app", Rules: []model.PolicyRule{
		{Name: "rule-" + rule, Type: ptr("app/item"), Subject: ptr("someone"), Access: []string{"read"}},
	}}
	if status, raw := w.do("POST", w.admin.URL+"/service/"+w.appSvc+"/policy/import", doc); status != http.StatusConflict {
		t.Fatalf("a rule id of another service: %d %s", status, raw)
	}
	expectIDs(t, w.permission(w.org, nil), "branch", "B1")
}

func TestIntegrationRoleItemsKeepTheirConditionsAndAccessGroupsThroughTheDocument(t *testing.T) {
	w := newWorld(t)
	w.projectType()
	w.must("POST", w.typePath("project")+"/access_group", map[string]any{"name": "viewer", "levels": []string{"read"}})
	role := w.must("POST", "/role", map[string]any{"name": "project-reader"})["id"].(string)
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.typeByName["project"],
		"resource_where":   []map[string]any{{"attr": "owner", "op": "eq", "value": "alice"}},
		"access":           []string{"@viewer"},
	})
	w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "subject": hierarchySubject})
	res := w.permission(w.org, nil)
	expectIDs(t, res, "project", "P1", "P3")
	if got := accessOf(res, "project", "P1"); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("the access group inside the role: %v", got)
	}

	// A condition on the resources of a type without a synchronizer is refused for a role item as for a rule.
	plain := w.typeResource(w.org, "plain", "")
	if status, raw := w.do("POST", w.admin.URL+"/role/"+role+"/permission", map[string]any{
		"type_resource_id": plain,
		"resource_where":   []map[string]any{{"attr": "owner", "op": "eq", "value": "a"}},
		"access":           []string{"read"},
	}); status != http.StatusBadRequest {
		t.Fatalf("resource_where on a type without a synchronizer: %d %s", status, raw)
	}

	// The document carries the item with its condition and its group, and a dry run plans nothing for them.
	doc := w.exportPolicy()
	if len(doc.Roles) != 1 || len(doc.Roles[0].Items) != 1 {
		t.Fatalf("the roles in the document: %+v", doc.Roles)
	}
	item := doc.Roles[0].Items[0]
	if item.Type != "org/project" || !slices.Equal(item.Access, []string{"@viewer"}) || len(item.ResourceWhere) != 1 {
		t.Fatalf("the item in the document: %+v", item)
	}
	if len(doc.AccessGroups) != 1 || doc.AccessGroups[0].Name != "viewer" {
		t.Fatalf("the access groups in the document: %+v", doc.AccessGroups)
	}
	plan := w.importPolicy(doc, true, false)
	if len(plan.Operations) != 0 || plan.Counts != (model.PolicyPlanCounts{Unchanged: 3}) {
		t.Fatalf("a dry run of the exported role: %+v", plan)
	}
}

func TestIntegrationNestedGroupsAndSubjectKindsRoundTrip(t *testing.T) {
	w := newWorld(t)
	inner := w.must("POST", "/group", map[string]any{"name": "inner"})["id"].(string)
	w.must("POST", "/group/"+inner+"/member", map[string]any{
		"predicate": []map[string]any{{"attr": "team", "op": "eq", "value": "audit"}}})
	outer := w.must("POST", "/group", map[string]any{"name": "outer"})["id"].(string)
	w.must("POST", "/group/"+outer+"/member", map[string]any{"member_group_id": inner})
	w.must("POST", "/assignment_rule", map[string]any{"type_resource_id": w.typeByName["branch"], "resource_id": "B1",
		"access": []string{"read"}, "member_of_group_id": outer, "subject_kind": "service", "name": "outer-read"})

	doc := w.exportPolicy()
	nested := false
	for _, g := range doc.Groups {
		if g.Name == "outer" && len(g.Members) == 1 && g.Members[0].Group != nil && *g.Members[0].Group == "inner" {
			nested = true
		}
	}
	if !nested || len(doc.Groups) != 2 {
		t.Fatalf("the nested group in the document: %+v", doc.Groups)
	}
	if len(doc.Rules) != 1 || doc.Rules[0].SubjectKind == nil || *doc.Rules[0].SubjectKind != "service" ||
		doc.Rules[0].MemberOf == nil || *doc.Rules[0].MemberOf != "outer" {
		t.Fatalf("the rule in the document: %+v", doc.Rules)
	}
	plan := w.importPolicy(doc, true, false)
	if len(plan.Operations) != 0 || plan.Counts != (model.PolicyPlanCounts{Unchanged: 3}) {
		t.Fatalf("a dry run of the exported nested groups: %+v", plan)
	}
}

// Equality and membership over a list attribute: a resource matches when the list holds the value. The database narrows
// the rows by containment, which does not match a list against a scalar, so both shapes are looked up.
func TestIntegrationResourceWhereMatchesListAttributes(t *testing.T) {
	w := newWorld(t)
	w.exec(`CREATE TABLE src_tagged (id text PRIMARY KEY, name text, parent_id text, tags text[])`)
	w.exec(`INSERT INTO src_tagged VALUES ('T1', 'Tagged 1', NULL, '{gold,new}'), ('T2', 'Tagged 2', NULL, '{silver}'),
		('T3', 'Tagged 3', NULL, '{gold}')`)
	w.typeResource(w.org, "tagged", "")
	w.must("POST", w.typePath("tagged")+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id, tags FROM src_tagged WHERE id = :id",
			"list_query": "SELECT id, name, parent_id, tags FROM src_tagged",
		},
		"probe_resource_id": "T1",
	})
	tagged := w.typeByName["tagged"]
	w.must("POST", "/assignment_rule", map[string]any{"type_resource_id": tagged, "subject": hierarchySubject,
		"access": []string{"read"}, "resource_where": []map[string]any{{"attr": "tags", "op": "eq", "value": "gold"}}})
	w.must("POST", "/assignment_rule", map[string]any{"type_resource_id": tagged, "subject": hierarchySubject,
		"access": []string{"write"}, "resource_where": []map[string]any{{"attr": "tags", "op": "in", "value": []string{"silver", "new"}}}})

	res := w.permission(w.org, nil)
	expectIDs(t, res, "tagged", "T1", "T2", "T3")
	for id, want := range map[string][]string{"T1": {"read", "write"}, "T2": {"write"}, "T3": {"read"}} {
		if got := accessOf(res, "tagged", id); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", id, got, want)
		}
	}
}
