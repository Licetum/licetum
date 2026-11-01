//go:build integration

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func TestGroupsAndRolesHaveImmutableServiceOwners(t *testing.T) {
	e := setup(t)
	first, second := e.newService("owner-first"), e.newService("owner-second")
	for _, kind := range []string{"group", "role"} {
		path := "/" + kind
		e.expect(http.StatusUnprocessableEntity, http.MethodPost, path, map[string]any{"name": "missing-owner"})
		a := e.must(http.MethodPost, path, map[string]any{"name": "same-name", "service_id": first})
		b := e.must(http.MethodPost, path, map[string]any{"name": "same-name", "service_id": second})
		if a["service_id"] != first || b["service_id"] != second {
			t.Fatalf("owners: %v %v", a, b)
		}
		e.expect(http.StatusConflict, http.MethodPost, path, map[string]any{"name": "same-name", "service_id": first})
		e.expect(http.StatusUnprocessableEntity, http.MethodPatch, path+"/"+a["id"].(string), map[string]any{"name": "renamed", "service_id": second})
		renamed := e.must(http.MethodPatch, path+"/"+a["id"].(string), map[string]any{"name": "renamed"})
		if renamed["service_id"] != first {
			t.Fatal("renaming changed owner")
		}
		status, raw := e.do(http.MethodGet, e.admin.URL+path+"?service_id="+first, nil)
		if status != http.StatusOK || !strings.Contains(string(raw), a["id"].(string)) || strings.Contains(string(raw), b["id"].(string)) {
			t.Fatalf("service filter %d %s", status, raw)
		}
	}
	group := e.must(http.MethodPost, "/group", map[string]any{"name": "parent", "service_id": first})["id"].(string)
	other := e.must(http.MethodPost, "/group", map[string]any{"name": "child", "service_id": second})["id"].(string)
	e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/group/"+group+"/member", map[string]any{"member_group_id": other})
	role := e.must(http.MethodPost, "/role", map[string]any{"name": "owner-role", "service_id": first})["id"].(string)
	typ := e.must(http.MethodPost, "/service/"+second+"/type_resource", map[string]any{"name": "foreign"})["id"].(string)
	e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/role/"+role+"/permission", map[string]any{"type_resource_id": typ, "access": []string{"read"}})
}

func TestPolicyImportKeepsNamesInsideTheService(t *testing.T) {
	e := setup(t)
	first, second := e.newService("import-first"), e.newService("import-second")
	otherGroup := e.must("POST", "/group", map[string]any{"service_id": second, "name": "same"})["id"].(string)
	e.must("POST", "/group/"+otherGroup+"/member", map[string]any{"predicate": []map[string]any{{"attr": "department", "op": "eq", "value": "other"}}})
	e.must("POST", "/group", map[string]any{"service_id": first, "name": "same"})
	otherRole := e.must("POST", "/role", map[string]any{"service_id": second, "name": "same"})["id"].(string)
	e.must("POST", "/role", map[string]any{"service_id": first, "name": "same"})
	otherType := e.must("POST", "/service/"+second+"/type_resource", map[string]any{"name": "doc"})["id"].(string)
	e.must("POST", "/service/"+first+"/type_resource", map[string]any{"name": "doc"})
	e.must("POST", "/role/"+otherRole+"/permission", map[string]any{"type_resource_id": otherType, "access": []string{"write"}})
	snapshot := func() string {
		var image string
		if err := e.pool.QueryRow(context.Background(), `SELECT jsonb_build_object('members',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM group_member m WHERE group_id=$1),'items',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM role_permission p WHERE role_id=$2))::text`, otherGroup, otherRole).Scan(&image); err != nil {
			t.Fatal(err)
		}
		return image
	}
	before := snapshot()
	doc := model.PolicyDocument{Format: model.PolicyDocumentFormat, Service: "import-first",
		Groups: []model.PolicyGroup{{Name: "same", Members: []model.PolicyGroupMember{{Predicate: []model.PredicateCondition{{Attr: "department", Op: model.OpEq, Value: model.PredicateValue{Str: "local"}}}}}}},
		Roles:  []model.PolicyRole{{Name: "same", Items: []model.PolicyRoleItem{{Type: "import-first/doc", Access: []string{"read"}}}}},
	}
	e.must("POST", "/service/"+first+"/policy/import?dry_run=false", doc)
	if after := snapshot(); after != before {
		t.Fatalf("import rewrote another service: before %s after %s", before, after)
	}
}
