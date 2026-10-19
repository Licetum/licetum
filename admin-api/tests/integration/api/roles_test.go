//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/licetum/licetum/core/engine"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// A role is a set of permissions: its items reach a subject through the assignment rule that grants the role.
func TestRolePermissionsReachSubjectsThroughAssignments(t *testing.T) {
	w := newWorld(t)
	sales := w.must("POST", "/group", map[string]any{"name": "sales"})["id"].(string)
	w.must("POST", "/group/"+sales+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}},
	})
	role := w.must("POST", "/role", map[string]any{"name": "catalog-readers"})["id"].(string)
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.branch, "resource_id": "B1", "access": []string{"read"},
	})
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.item, "access": []string{"read"},
	})
	salesClaim := []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}
	grant := w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "predicate": salesClaim})
	if grant["type_resource_id"] != nil || grant["access"] != nil {
		t.Fatalf("a role rule must not carry an inline permission: %v", grant)
	}

	inSales := engine.Claims{"department": "sales"}
	expectIDs(t, w.permission(w.org, inSales), "branch", "B1")
	expectIDs(t, w.permission(w.appSvc, inSales), "item", "I1", "I4")
	expectIDs(t, w.permission(w.org, nil), "branch")
	expectIDs(t, w.permission(w.appSvc, nil), "item")

	// A deny rule that grants the same role takes all of its items away — deny wins.
	w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "effect": "deny", "predicate": salesClaim})
	expectIDs(t, w.permission(w.org, inSales), "branch")
	expectIDs(t, w.permission(w.appSvc, inSales), "item")
}

func TestRoleValidationAndBlocking(t *testing.T) {
	w := newWorld(t)
	feature := w.typeResource(w.org, "feature", "") // no synchronizer: a permission on it is for the whole type
	role := w.must("POST", "/role", map[string]any{"name": "editors"})["id"].(string)
	items := "/role/" + role + "/permission"

	status, raw := w.do(http.MethodPost, w.admin.URL+items, map[string]any{
		"type_resource_id": w.branch, "access": []string{"*"},
	})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "access_level_invalid")

	status, raw = w.do(http.MethodPost, w.admin.URL+items, map[string]any{
		"type_resource_id": w.branch, "resource_id": "B1", "resource_from_claim": map[string]any{
			"claim": "branch", "lookup_type_resource_id": w.branch,
		}, "access": []string{"read"},
	})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "resource_target_exclusive")

	status, raw = w.do(http.MethodPost, w.admin.URL+items, map[string]any{
		"type_resource_id": feature, "resource_id": "x", "access": []string{"read"},
	})
	requireErrorCode(t, status, raw, http.StatusBadRequest, "assignment_rule_needs_synchronizer")

	w.must("POST", items, map[string]any{"type_resource_id": feature, "access": []string{"use"}})
	item := w.must("POST", items, map[string]any{"type_resource_id": w.branch, "resource_id": "B2", "access": []string{"read"}})
	w.must("POST", items, map[string]any{"type_resource_id": w.item, "access": []string{"read"}})

	status, raw = w.do(http.MethodPost, w.admin.URL+"/assignment_rule", map[string]any{
		"role_id": role, "type_resource_id": w.branch, "access": []string{"read"}, "subject": hierarchySubject,
	})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "role_assignment_inline_exclusive")

	status, raw = w.do(http.MethodPost, w.admin.URL+"/assignment_rule", map[string]any{
		"access": []string{"read"}, "subject": hierarchySubject,
	})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "assignment_rule_target_required")

	rule := w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "subject": hierarchySubject})["id"].(string)

	// The listing shows the items with the names of their types.
	status, raw = w.do(http.MethodGet, w.admin.URL+items, nil)
	var listed []map[string]any
	if status != http.StatusOK || json.Unmarshal(raw, &listed) != nil || len(listed) != 3 {
		t.Fatalf("list items: %d %s", status, raw)
	}
	names := map[string]bool{}
	for _, it := range listed {
		names[it["type_resource_name"].(string)] = true
	}
	if !names["branch"] || !names["item"] || !names["feature"] {
		t.Fatalf("items without their type names: %s", raw)
	}

	// A role that rules still grant cannot be deleted, and a type that items point at cannot be deleted.
	status, raw = w.do(http.MethodDelete, w.admin.URL+"/role/"+role, nil)
	requireErrorCode(t, status, raw, http.StatusConflict, "role_definition_delete_blocked")
	if !strings.Contains(string(raw), "assignment_rule") {
		t.Fatalf("the blocking rule is not named: %s", raw)
	}
	status, raw = w.do(http.MethodDelete, w.admin.URL+w.typePath("feature"), nil)
	if status != http.StatusConflict || !strings.Contains(string(raw), "role_permission") {
		t.Fatalf("delete a type used by a role item: %d %s", status, raw)
	}

	w.must("PATCH", items+"/"+item["id"].(string), map[string]any{"type_resource_id": w.branch, "resource_id": "B3", "access": []string{"write"}})
	if status, raw := w.do(http.MethodDelete, w.admin.URL+"/assignment_rule/"+rule, nil); status != http.StatusOK {
		t.Fatalf("delete the grant: %d %s", status, raw)
	}
	w.must("DELETE", "/role/"+role, nil)
	if status, raw := w.do(http.MethodDelete, w.admin.URL+w.typePath("feature"), nil); status != http.StatusOK {
		t.Fatalf("the type is free once its role is gone: %d %s", status, raw)
	}
}

// The service slug is the second identifier of a service in the check API.
func TestServiceSlugAddressesTheServiceInTheCheckAPI(t *testing.T) {
	e := setup(t)
	orders := e.must("POST", "/service", servicePayload(e, "orders"))["id"].(string)

	for _, slug := range []string{"Orders", "-orders", "orders app", strings.Repeat("a", 65), "00000000-0000-0000-0000-000000000000"} {
		payload := servicePayload(e, "other")
		payload["slug"] = slug
		if status, raw := e.do(http.MethodPost, e.admin.URL+"/service", payload); status != http.StatusUnprocessableEntity {
			t.Errorf("slug %q: %d %s", slug, status, raw)
		}
	}
	payload := servicePayload(e, "other")
	payload["slug"] = "orders"
	status, raw := e.do(http.MethodPost, e.admin.URL+"/service", payload)
	requireErrorCode(t, status, raw, http.StatusConflict, "service_slug_taken")

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})
	if status, raw := e.permission("orders", alice); status != http.StatusOK {
		t.Fatalf("the slug addresses the service: %d %v", status, raw)
	}
	if status, _ := e.permission(orders, alice); status != http.StatusOK {
		t.Fatalf("the id still addresses the service: %d", status)
	}

	e.must("PATCH", "/service/"+orders, map[string]any{"slug": "orders-v2"})
	status, header, body := e.permissionRaw("orders", alice)
	requireAuthzenError(t, status, header, body, http.StatusNotFound, "service_not_found")
	if status, _ := e.permission("orders-v2", alice); status != http.StatusOK {
		t.Fatalf("the new slug: %d", status)
	}
}

// cache_ttl keeps the list of a source in Redis: a synchronization that runs again (an admin write ends the
// throttle) reads the cached list, while consistency=higher asks the source every time.
func TestSynchronizerCacheTTLKeepsTheSourceListInRedis(t *testing.T) {
	e := setup(t)
	var listCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /src/branch", func(w http.ResponseWriter, _ *http.Request) {
		listCalls.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "b1", "name": "Branch1"}, {"id": "b2", "name": "Branch2"}})
	})
	mux.HandleFunc("GET /src/branch/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "Branch"})
	})
	source := newTestServer(t, mux)

	org := e.must("POST", "/service", servicePayload(e, "cache-org"))["id"].(string)
	branch := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+org+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config":            map[string]any{"kind": "http", "url": source.URL + "/src/branch", "cache_ttl": 300},
		"probe_resource_id": "b1",
	})
	e.must("POST", "/assignment_rule", map[string]any{"type_resource_id": branch, "access": []string{"read"}, "subject": "alice"})

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})
	ask := func(opts ...authzenreq.Option) {
		t.Helper()
		status, ids := e.search("cache-org", alice, "branch", "read", opts...)
		if status != http.StatusOK || !slices.Contains(ids, "b2") {
			t.Fatalf("search: %d %v", status, ids)
		}
	}

	ask()
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("the first synchronization asks the source %d times, want 1", got)
	}
	// Another admin write ends the throttle of the synchronization: it runs again, but the list is in Redis.
	e.must("POST", "/service", servicePayload(e, "unrelated"))
	ask()
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("a synchronization within cache_ttl asked the source again: %d calls", got)
	}
	// consistency=higher never answers from the cached list.
	ask(authzenreq.Fresh())
	if got := listCalls.Load(); got != 2 {
		t.Fatalf("consistency=higher must ask the source: %d calls, want 2", got)
	}
}

// An item on a platform policy grants access to the platform itself: it needs write on that policy as a
// whole, like creating a rule on it. A point write on the role — which is all a role editor has — is not enough.
func TestRoleItemOnAPlatformPolicyNeedsWriteOnThePolicy(t *testing.T) {
	w := newWorld(t)
	ops := w.must("POST", "/role", map[string]any{"name": "ops"})["id"].(string)
	carol := w.token(jwt.MapClaims{"preferred_username": "carol"})
	w.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": model.PlatformTypeResourceID(model.PlatformRole).String(), "resource_id": ops,
		"access": []string{"read", "write"}, "subject": "carol",
	})
	platformRules := model.PlatformTypeResourceID(model.PlatformAssignmentRule).String()
	items := "/role/" + ops + "/permission"

	status, raw := w.do(http.MethodPost, w.admin.URL+items, map[string]any{
		"type_resource_id": platformRules, "access": []string{"write"},
	}, asUser(carol)...)
	requireErrorCode(t, status, raw, http.StatusForbidden, "access_denied")

	// A type of the business is still within a role editor's reach.
	if status, raw := w.do(http.MethodPost, w.admin.URL+items, map[string]any{
		"type_resource_id": w.branch, "resource_id": "B1", "access": []string{"read"},
	}, asUser(carol)...); status != http.StatusOK {
		t.Fatalf("a role editor adds an item on a business type: %d %s", status, raw)
	}

	// An admin's item on the policy cannot be changed or removed by the role editor either.
	item := w.must("POST", items, map[string]any{"type_resource_id": platformRules, "access": []string{"read"}})["id"].(string)
	if status, raw := w.do(http.MethodDelete, w.admin.URL+items+"/"+item, nil, asUser(carol)...); status != http.StatusForbidden {
		t.Fatalf("a role editor removes an item on the policy: %d %s", status, raw)
	}
	if status, raw := w.do(http.MethodPatch, w.admin.URL+items+"/"+item, map[string]any{
		"type_resource_id": w.branch, "resource_id": "B2", "access": []string{"read"},
	}, asUser(carol)...); status != http.StatusForbidden {
		t.Fatalf("a role editor moves an item off the policy: %d %s", status, raw)
	}
}

// A type that a claim-based target uses as its lookup type cannot be deleted while the target exists.
func TestLookupTypeOfAClaimTargetCannotBeDeleted(t *testing.T) {
	w := newWorld(t)
	region := w.typeResource(w.org, "region", "")
	rule := w.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": w.branch, "access": []string{"read"}, "subject": hierarchySubject,
		"resource_from_claim": map[string]any{"claim": "region", "lookup_type_resource_id": region, "match_by": "name"},
	})["id"].(string)

	status, raw := w.do(http.MethodDelete, w.admin.URL+w.typePath("region"), nil)
	if status != http.StatusConflict || !strings.Contains(string(raw), "assignment_rule") {
		t.Fatalf("delete the lookup type of a rule: %d %s", status, raw)
	}
	if status, raw := w.do(http.MethodDelete, w.admin.URL+"/assignment_rule/"+rule, nil); status != http.StatusOK {
		t.Fatalf("delete the rule: %d %s", status, raw)
	}
	if status, raw := w.do(http.MethodDelete, w.admin.URL+w.typePath("region"), nil); status != http.StatusOK {
		t.Fatalf("the lookup type is free once the rule is gone: %d %s", status, raw)
	}
}

// asUser sends the request with a user's bearer token instead of the platform admin's.
func asUser(token string) []string {
	return []string{"Authorization", "Bearer " + token}
}
