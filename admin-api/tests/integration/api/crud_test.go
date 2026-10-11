//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func servicePayload(e *env, name string) map[string]any {
	return map[string]any{
		"name": name, "url": e.mock.URL + "/app", "icon": e.mock.URL + "/icon.png",
		"authorization_url": e.mock.URL + "/realm/auth", "token_url": e.mock.URL + "/realm/token",
		"jwks_url": e.mock.URL + "/jwks", "issuer": e.mock.URL + "/realm", "slug": name, "client_id": name, "client_secret": "secret",
	}
}

func requireErrorCode(t *testing.T, status int, raw []byte, wantStatus int, wantCode string) {
	t.Helper()
	var body struct {
		Extra struct {
			Code string `json:"code"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode error response: %v (%s)", err, raw)
	}
	if status != wantStatus || body.Extra.Code != wantCode {
		t.Fatalf("error = %d/%q, want %d/%q (%s)", status, body.Extra.Code, wantStatus, wantCode, raw)
	}
}

func TestServiceCRUDCoversSuccessNotFoundMalformedIDAndAuthorization(t *testing.T) {
	e := setup(t)

	// Arrange: create one valid service as the platform administrator.
	created := e.must(http.MethodPost, "/service", servicePayload(e, "orders"))
	serviceID := created["id"].(string)
	if created["name"] != "orders" || strings.TrimRight(created["url"].(string), "/") != e.mock.URL+"/app" {
		t.Fatalf("created service = %+v", created)
	}

	// Act + Assert: list returns the created resource with the caller-visible fields.
	status, raw := e.do(http.MethodGet, e.admin.URL+"/service", nil)
	if status != http.StatusOK {
		t.Fatalf("list services: status %d, body %s", status, raw)
	}
	var services []map[string]any
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range services {
		if item["id"] == serviceID {
			found = true
			if item["name"] != "orders" {
				t.Fatalf("listed service = %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("created service %q is absent from list: %s", serviceID, raw)
	}

	// Partial update changes only supplied fields and null explicitly clears the secret.
	updated := e.must(http.MethodPatch, "/service/"+serviceID, map[string]any{"name": "orders-v2", "client_secret": nil})
	if updated["name"] != "orders-v2" {
		t.Fatalf("updated service = %+v", updated)
	}

	// Authorization is checked before entity lookup: an unknown id is not a visible resource
	// and therefore returns 403, while a malformed id fails request validation with 422.
	status, raw = e.do(http.MethodDelete, e.admin.URL+"/service/"+uuid.NewString(), nil)
	requireErrorCode(t, status, raw, http.StatusForbidden, "access_denied")
	status, raw = e.do(http.MethodDelete, e.admin.URL+"/service/not-a-uuid", nil)
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "validation_failed")

	// Delete succeeds for the existing entity and the following lookup is no longer visible.
	status, raw = e.do(http.MethodDelete, e.admin.URL+"/service/"+serviceID, nil)
	if status != http.StatusOK {
		t.Fatalf("delete service: %d %s", status, raw)
	}
	status, raw = e.do(http.MethodGet, e.admin.URL+"/service", nil)
	if status != http.StatusOK || strings.Contains(string(raw), serviceID) {
		t.Fatalf("deleted service remains visible: %d %s", status, raw)
	}

	// Missing credentials are rejected before the request reaches the service layer.
	status, raw = e.do(http.MethodGet, e.admin.URL+"/service", nil, "Authorization", "")
	if status != http.StatusUnauthorized || strings.TrimSpace(string(raw)) == "" {
		t.Fatalf("unauthorized list: %d %s", status, raw)
	}
}

func TestGroupAndAssignmentRuleCRUDCoversValidationAndBlocking(t *testing.T) {
	e := setup(t)
	serviceID := e.must(http.MethodPost, "/service", servicePayload(e, "catalog"))["id"].(string)
	typeID := e.must(http.MethodPost, "/service/"+serviceID+"/type_resource", map[string]any{"name": "product"})["id"].(string)

	// Create/list/update a group and a rule attached to a concrete type.
	group := e.must(http.MethodPost, "/group", map[string]any{"name": "catalog-readers"})
	groupID := group["id"].(string)
	status, raw := e.do(http.MethodGet, e.admin.URL+"/group", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), groupID) {
		t.Fatalf("list groups: %d %s", status, raw)
	}
	e.must(http.MethodPatch, "/group/"+groupID, map[string]any{"name": "catalog-writers"})

	rule := e.must(http.MethodPost, "/assignment_rule", map[string]any{
		"type_resource_id": typeID, "access": []string{"read"}, "member_of_group_id": groupID,
	})
	ruleID := rule["id"].(string)
	updated := e.must(http.MethodPatch, "/assignment_rule/"+ruleID, map[string]any{
		"type_resource_id": typeID, "access": []string{"read", "write"}, "member_of_group_id": groupID, "enabled": false,
	})
	if updated["enabled"] != false {
		t.Fatalf("updated rule = %+v", updated)
	}

	// Invalid target combinations are rejected with a stable validation code.
	status, raw = e.do(http.MethodPost, e.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": typeID, "access": []string{"read"}, "subject": "alice", "member_of_group_id": groupID,
	})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "subject_target_exclusive")

	// A group with dependants cannot be deleted until its rule is removed.
	status, raw = e.do(http.MethodDelete, e.admin.URL+"/group/"+groupID, nil)
	if status != http.StatusConflict || !strings.Contains(string(raw), "assignment_rule") {
		t.Fatalf("delete referenced group: %d %s", status, raw)
	}
	e.must(http.MethodDelete, "/assignment_rule/"+ruleID, nil)
	e.must(http.MethodDelete, "/group/"+groupID, nil)

	// Unknown and malformed rule IDs remain distinct API errors; authorization still
	// precedes lookup for a valid-looking but unknown resource ID.
	status, raw = e.do(http.MethodDelete, e.admin.URL+"/assignment_rule/"+uuid.NewString(), nil)
	requireErrorCode(t, status, raw, http.StatusForbidden, "access_denied")
	status, raw = e.do(http.MethodDelete, e.admin.URL+"/assignment_rule/not-a-uuid", nil)
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "validation_failed")
}

func TestGroupMemberCRUDSupportsPredicateUpdateAndDelete(t *testing.T) {
	e := setup(t)
	role := e.must(http.MethodPost, "/group", map[string]any{"name": "regional-users"})["id"].(string)

	member := e.must(http.MethodPost, "/group/"+role+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "region", "op": "eq", "value": "north"}},
	})
	memberID := member["id"].(string)
	if member["enabled"] != true {
		t.Fatalf("new membership = %+v", member)
	}

	status, raw := e.do(http.MethodGet, e.admin.URL+"/group/"+role+"/member", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), memberID) {
		t.Fatalf("list memberships: %d %s", status, raw)
	}
	updated := e.must(http.MethodPatch, "/group/"+role+"/member/"+memberID, map[string]any{
		"predicate": []any{map[string]any{"attr": "region", "op": "eq", "value": "south"}}, "enabled": false,
	})
	if updated["enabled"] != false {
		t.Fatalf("updated membership = %+v", updated)
	}
	e.must(http.MethodDelete, "/group/"+role+"/member/"+memberID, nil)
	status, raw = e.do(http.MethodGet, e.admin.URL+"/group/"+role+"/member", nil)
	if status != http.StatusOK || strings.Contains(string(raw), memberID) {
		t.Fatalf("deleted membership remains visible: %d %s", status, raw)
	}
}
