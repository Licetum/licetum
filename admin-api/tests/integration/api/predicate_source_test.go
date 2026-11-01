//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestPredicateSourceUsesSubjectAndRefusesTheOldWireName(t *testing.T) {
	w := newWorld(t)
	condition := func(source string) map[string]any {
		return map[string]any{"attr": "department", "op": "eq", "value": "sales", "source": source}
	}
	for _, field := range []string{"predicate", "requires_predicate", "resource_where"} {
		body := map[string]any{"type_resource_id": w.branch, "access": []string{"read"}, field: []any{condition("claim")}}
		if field != "predicate" {
			body["subject"] = "person"
		}
		status, raw := w.do(http.MethodPost, w.admin.URL+"/assignment_rule", body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "subject") {
			t.Fatalf("%s old source: %d %s", field, status, raw)
		}
	}
	body := map[string]any{"type_resource_id": w.branch, "access": []string{"read"}, "predicate": []any{condition("subject")}, "requires_predicate": []any{map[string]any{"attr": "active", "op": "eq", "value": "yes"}}}
	rule := w.must(http.MethodPost, "/assignment_rule", body)
	for _, field := range []string{"predicate", "requires_predicate"} {
		if got := rule[field].([]any)[0].(map[string]any)["source"]; got != "subject" {
			t.Fatalf("%s default is not canonical: %v", field, rule)
		}
	}
}
