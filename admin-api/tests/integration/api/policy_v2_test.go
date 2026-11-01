//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func TestPolicyV1IsRejectedBeforeItsOldFieldsInEveryLanguage(t *testing.T) {
	w := newWorld(t)
	legacy := map[string]any{"format": "licetum.policy/v1", "service": "org", "rules": []any{map[string]any{
		"name": "legacy", "type": "org/branch", "subject": "alice", "subject_kind": "user", "access": []string{"read"},
		"resource_from_claim": map[string]any{"claim": "department", "lookup_type": "org/branch"},
		"predicate":           []any{map[string]any{"attr": "department", "op": "eq", "value": "sales", "source": "claim"}},
	}}}
	for lang, hint := range map[string]string{"en": "not imported", "ru": "не импортируются", "zh": "不导入"} {
		status, raw := w.do(http.MethodPost, w.admin.URL+"/service/"+w.org+"/policy/import?dry_run=true", legacy, "Accept-Language", lang)
		requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "policy_v1_not_imported")
		if !strings.Contains(string(raw), "licetum.policy/v1") || !strings.Contains(string(raw), hint) {
			t.Fatalf("%s version hint: %s", lang, raw)
		}
	}
}

func TestPolicyV2ExportsOwnedTypedPropertyRulesAndScenariosUseProperties(t *testing.T) {
	w := newWorld(t)
	group := w.must(http.MethodPost, "/group", map[string]any{"service_id": w.org, "name": "sales"})["id"].(string)
	w.must(http.MethodPost, "/group/"+group+"/member", map[string]any{"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	w.grant(map[string]any{"name": "sales-write", "type_resource_id": w.branch, "subject_group_id": group, "access": []string{"write"}})
	w.grant(map[string]any{"name": "typed-property", "type_resource_id": w.branch, "subject": "case-person", "subject_type": "service", "access": []string{"read"},
		"resource_from_property": map[string]any{"property": "branch_name", "lookup_type_resource_id": w.branch},
		"requires_predicate":     []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	scenario := func(name, typ, department, expect string) map[string]any {
		return map[string]any{
			"name": name, "subject": "case-person", "subject_type": typ, "properties": map[string]any{"department": department, "branch_name": "Branch 3"},
			"checks": []any{map[string]any{"type": "branch", "resource_id": "B3", "action": "read", "expect": expect}},
		}
	}
	cases := []any{scenario("service sales", "service", "sales", "allow"), scenario("same ID user", "user", "sales", "deny"), scenario("wrong properties", "service", "legal", "deny")}
	report := w.must(http.MethodPost, "/service/"+w.org+"/policy/test", map[string]any{"cases": cases})
	if report["passed"] != float64(3) || report["failed"] != float64(0) {
		t.Fatalf("typed property scenarios: %v", report)
	}
	saved := w.must(http.MethodPost, "/service/"+w.org+"/policy/scenarios", cases[0])
	stored := saved["case"].(map[string]any)
	if stored["subject_type"] != "service" || stored["properties"].(map[string]any)["department"] != "sales" {
		t.Fatalf("stored scenario: %v", stored)
	}
	if _, legacy := stored["claims"]; legacy {
		t.Fatal("scenario exported the old properties field")
	}
	raw := w.mustRaw(http.MethodGet, "/service/"+w.org+"/policy/export")
	var doc model.PolicyDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != "licetum.policy/v2" {
		t.Fatalf("export version: %s", doc.Format)
	}
	var typed, grouped bool
	for _, r := range doc.Rules {
		if r.Name == "typed-property" {
			typed = r.SubjectType != nil && *r.SubjectType == "service" && r.ResourceFromProperty != nil && r.ResourceFromProperty.Property == "branch_name"
		}
		if r.Name == "sales-write" {
			grouped = r.SubjectGroup != nil && *r.SubjectGroup == "sales"
		}
	}
	if !typed || !grouped {
		t.Fatalf("v2 lost typed/property/group fields: %+v", doc.Rules)
	}
	status, raw := w.do(http.MethodPost, w.admin.URL+"/service/"+w.org+"/policy/import?dry_run=true", doc)
	if status != http.StatusOK {
		t.Fatalf("v2 import: %d %s", status, raw)
	}
	var plan model.PolicyImportPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 0 {
		t.Fatalf("v2 round trip is not idempotent: %+v", plan)
	}
}
