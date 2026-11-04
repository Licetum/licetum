//go:build integration

package api_test

import (
	"context"
	"net/http"
	"testing"
)

func TestResourceFromPropertyUsesTheNewWireAndStoredKeys(t *testing.T) {
	w := newWorld(t)
	target := map[string]any{"property": "branch_name", "lookup_type_resource_id": w.branch}
	body := map[string]any{"type_resource_id": w.branch, "subject": "person", "access": []string{"read"}, "resource_from_property": target}
	rule := w.must(http.MethodPost, "/assignment_rule", body)
	if rule["resource_from_property"].(map[string]any)["property"] != "branch_name" {
		t.Fatalf("new target: %v", rule)
	}
	var property string
	if err := w.pool.QueryRow(context.Background(), `SELECT resource_from_property->>'property' FROM assignment_rule WHERE id=$1`, rule["id"]).Scan(&property); err != nil || property != "branch_name" {
		t.Fatalf("stored property: %q %v", property, err)
	}
	// Refusing the retired outer field prevents an old target becoming a whole-type grant.
	delete(body, "resource_from_property")
	body["resource_from_claim"] = target
	status, raw := w.do(http.MethodPost, w.admin.URL+"/assignment_rule", body)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("retired outer field: %d %s", status, raw)
	}
	delete(body, "resource_from_claim")
	body["resource_from_property"] = map[string]any{"claim": "branch_name", "lookup_type_resource_id": w.branch}
	status, raw = w.do(http.MethodPost, w.admin.URL+"/assignment_rule", body)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("retired inner field: %d %s", status, raw)
	}
}
