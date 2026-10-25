//go:build integration

package api_test

// Integration tests of the policy tools: the scenarios with their coverage, the linter, and the export of a policy as a
// document that imports back to the same policy (a dry run of the unchanged document plans nothing), with edits and prune.
// Same Testcontainers environment as hierarchy_test.go.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

// importPolicy posts a document to the import of the org service and returns the plan.
func (w *world) importPolicy(doc model.PolicyDocument, dryRun, prune bool) model.PolicyImportPlan {
	w.t.Helper()
	target := "/service/" + w.org + "/policy/import?dry_run=" + strconv.FormatBool(dryRun) + "&prune=" + strconv.FormatBool(prune)
	status, raw := w.do("POST", w.admin.URL+target, doc)
	if status != http.StatusOK {
		w.t.Fatalf("import: %d %s", status, raw)
	}
	var plan model.PolicyImportPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		w.t.Fatal(err)
	}
	return plan
}

// exportPolicy reads the policy of the org service as a document.
func (w *world) exportPolicy() model.PolicyDocument {
	w.t.Helper()
	var doc model.PolicyDocument
	if err := json.Unmarshal(w.mustRaw("GET", "/service/"+w.org+"/policy/export"), &doc); err != nil {
		w.t.Fatal(err)
	}
	return doc
}

func TestIntegrationPolicyTestsReportTheirCoverage(t *testing.T) {
	w := newWorld(t)
	read := w.rule("branch", "", map[string]any{"subject": hierarchySubject})
	deny := w.rule("branch", "B2", map[string]any{"subject": hierarchySubject, "effect": "deny", "access": []string{"read"}})
	unused := w.rule("division", "D1", map[string]any{"subject": "nobody", "access": []string{"write"}})

	report := w.must("POST", "/service/"+w.org+"/policy/test", map[string]any{"cases": []map[string]any{
		{"name": "sales read the branches", "subject": hierarchySubject, "checks": []map[string]any{
			{"type": "branch", "resource_id": "B1", "action": "read", "expect": "allow"},
			{"type": "branch", "resource_id": "B2", "action": "read", "expect": "deny"},
			{"type": "nothing", "action": "read", "expect": "deny"},
		}},
		{"name": "a wrong expectation", "subject": hierarchySubject, "checks": []map[string]any{
			{"type": "branch", "resource_id": "B1", "action": "read", "expect": "deny"},
		}},
	}})
	if report["passed"] != float64(1) || report["failed"] != float64(1) {
		t.Fatalf("scenarios: %v", report)
	}
	cases := report["cases"].([]any)
	if cases[1].(map[string]any)["passed"] != false {
		t.Fatalf("the wrong expectation holds: %v", cases[1])
	}
	first := cases[0].(map[string]any)["checks"].([]any)
	if rules := first[1].(map[string]any)["rules"].([]any); !slices.Contains(toStrings(rules), deny) {
		t.Fatalf("the denial that decided B2 is not named: %v", rules)
	}

	// Three rules apply to the types of the service; the scenarios reach the first two, not the one for nobody.
	coverage := report["coverage"].(map[string]any)
	if coverage["rules"] != float64(3) || coverage["covered"] != float64(2) {
		t.Fatalf("coverage: %v", coverage)
	}
	if uncovered := toStrings(coverage["uncovered"].([]any)); !slices.Equal(uncovered, []string{unused}) {
		t.Fatalf("uncovered rules: %v (the rules are %s, %s, %s)", uncovered, read, deny, unused)
	}
}

// toStrings converts a JSON list of strings.
func toStrings(list []any) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

func TestIntegrationLintNamesWhatDecidesNothingOrGrantsTooMuch(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "", map[string]any{"subject": hierarchySubject, "enabled": false})
	// Everyone who signs in: an empty predicate keeps the helper from adding the subject.
	w.rule("division", "", map[string]any{"access": []string{"read"}, "predicate": []any{}})
	w.rule("branch", "B3", map[string]any{"subject": hierarchySubject, "access": []string{"read"}})
	w.rule("branch", "B3", map[string]any{"subject": hierarchySubject, "access": []string{"read"}})
	auditors := w.must("POST", "/group", map[string]any{"name": "auditors"})["id"].(string)
	w.rule("branch", "B4", map[string]any{"member_of_group_id": auditors, "access": []string{"read"}})
	w.rule("branch", "B1", map[string]any{"subject": hierarchySubject, "access": []string{"read"}, "exception": true})
	role := w.must("POST", "/role", map[string]any{"name": "unused-role"})["id"].(string)
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "access": []string{"read"}})
	// The synchronizer of branch goes: the rule on B1 names a resource no synchronizer reports any more.
	w.exec(`UPDATE synchronizer SET deleted_at = now() WHERE type_resource_id = $1`, w.typeByName["branch"])

	status, raw := w.do("GET", w.admin.URL+"/service/"+w.org+"/policy/lint", nil)
	if status != http.StatusOK {
		t.Fatalf("lint: %d %s", status, raw)
	}
	var report model.LintReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	codes := map[string]int{}
	for _, f := range report.Findings {
		codes[f.Code]++
		if f.Message == "" || f.Severity == "" {
			t.Fatalf("a finding without its message or severity: %+v", f)
		}
	}
	for _, code := range []string{"lint_rule_disabled", "lint_rule_everyone", "lint_rule_duplicate",
		"lint_group_empty", "lint_exception_nothing_to_lift", "lint_rule_target_dead", "lint_role_unused"} {
		if codes[code] == 0 {
			t.Errorf("no %s finding: %v", code, codes)
		}
	}
}

func TestIntegrationPolicyExportImportsBackToTheSamePolicy(t *testing.T) {
	w := newWorld(t)
	auditors := w.must("POST", "/group", map[string]any{"name": "auditors"})["id"].(string)
	w.must("POST", "/group/"+auditors+"/member", map[string]any{
		"predicate": []map[string]any{{"attr": "team", "op": "eq", "value": "audit"}}})
	w.must("POST", w.typePath("branch")+"/access_group", map[string]any{"name": "editor", "levels": []string{"read", "write"}})
	role := w.must("POST", "/role", map[string]any{"name": "reader"})["id"].(string)
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "access": []string{"read"}})
	w.rule("branch", "B2", map[string]any{"subject": hierarchySubject, "access": []string{"@editor"}, "name": "branch-edit"})
	w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "subject": hierarchySubject, "name": "reader-grant"})
	w.must("POST", "/assignment_rule", map[string]any{"type_resource_id": w.typeByName["branch"], "access": []string{"read"},
		"member_of_group_id": auditors, "requires_group_id": auditors, "name": "audit-guard"})

	doc := w.exportPolicy()
	if doc.Format != model.PolicyDocumentFormat || doc.Service != "org" || len(doc.Rules) != 3 ||
		len(doc.Groups) != 1 || len(doc.Roles) != 1 || len(doc.AccessGroups) != 1 {
		t.Fatalf("the document: %+v", doc)
	}

	// The document of the policy plans nothing: every entity already matches it.
	plan := w.importPolicy(doc, true, false)
	if len(plan.Operations) != 0 || plan.Counts != (model.PolicyPlanCounts{Unchanged: 6}) {
		t.Fatalf("a dry run of the exported policy: %+v", plan)
	}

	// An edit of a rule, and a new rule, plan two writes and apply them.
	for i := range doc.Rules {
		if doc.Rules[i].Name == "branch-edit" {
			doc.Rules[i].Access = []string{"read"}
		}
	}
	extra := "extra"
	doc.Rules = append(doc.Rules, model.PolicyRule{Name: extra, Type: ptr("org/branch"), ResourceID: ptr("B3"),
		Subject: ptr(hierarchySubject), Access: []string{"read"}})
	plan = w.importPolicy(doc, true, false)
	if plan.Counts != (model.PolicyPlanCounts{Created: 1, Updated: 1, Unchanged: 5}) || len(plan.Operations) != 2 {
		t.Fatalf("the plan of an edit: %+v", plan)
	}
	applied := w.importPolicy(doc, false, false)
	if len(applied.Results) != 2 {
		t.Fatalf("applied: %+v", applied)
	}
	if got := accessOf(w.permission(w.org, nil), "branch", "B2"); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("after the import, B2 has %v", got)
	}
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2", "B3")

	// Without prune, a rule the document leaves out stays; with prune, it goes.
	kept := w.exportPolicy()
	kept.Rules = slices.DeleteFunc(kept.Rules, func(r model.PolicyRule) bool { return r.Name == "reader-grant" })
	if plan := w.importPolicy(kept, true, false); plan.Counts.Deleted != 0 {
		t.Fatalf("without prune nothing is deleted: %+v", plan.Counts)
	}
	if plan := w.importPolicy(kept, true, true); plan.Counts.Deleted != 1 {
		t.Fatalf("with prune the rule goes: %+v", plan.Counts)
	}

	// A document for another service, a reference to nothing, and two rules with one name are refused.
	other := w.exportPolicy()
	other.Service = "elsewhere"
	if status, raw := w.do("POST", w.admin.URL+"/service/"+w.org+"/policy/import", other); status != http.StatusUnprocessableEntity {
		t.Fatalf("another service's document: %d %s", status, raw)
	}
	missing := w.exportPolicy()
	missing.Rules = append(missing.Rules, model.PolicyRule{Name: "lost", Type: ptr("org/nothing"), Subject: ptr("x"),
		Access: []string{"read"}})
	if status, raw := w.do("POST", w.admin.URL+"/service/"+w.org+"/policy/import?dry_run=true", missing); status != http.StatusUnprocessableEntity ||
		!strings.Contains(string(raw), "org/nothing") {
		t.Fatalf("a type that is not there: %d %s", status, raw)
	}
	twice := w.exportPolicy()
	twice.Rules = append(twice.Rules, twice.Rules[0])
	if status, raw := w.do("POST", w.admin.URL+"/service/"+w.org+"/policy/import?dry_run=true", twice); status != http.StatusUnprocessableEntity {
		t.Fatalf("two rules with one name: %d %s", status, raw)
	}
}

func ptr[T any](v T) *T { return &v }
