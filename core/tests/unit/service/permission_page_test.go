package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

// syncResources is a sync type holding the resources ids, each with the given access levels.
func syncResources(access []string, ids ...string) model.TypePermission {
	tp := model.TypePermission{Type: model.TypeModeSync}
	for _, id := range ids {
		tp.Resources = append(tp.Resources, model.ResourceAccess{ID: id, Name: "name-" + id, Access: access})
	}
	return tp
}

func resourceIDs(tp model.TypePermission) []string {
	out := []string{}
	for _, r := range tp.Resources {
		out = append(out, r.ID)
	}
	return out
}

func TestPagingFollowsTheIDOrder(t *testing.T) {
	result := engine.PermissionResult{"branch": syncResources([]string{"read"}, "b3", "b1", "b5", "b2", "b4")}

	page, err := result.Filter(engine.PermissionFilter{Type: "branch", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceIDs(page["branch"]); strings.Join(got, ",") != "b1,b2" || page["branch"].NextAfter != "b2" {
		t.Fatalf("first page: %v, next %q", got, page["branch"].NextAfter)
	}

	page, err = result.Filter(engine.PermissionFilter{Type: "branch", Limit: 2, After: "b2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceIDs(page["branch"]); strings.Join(got, ",") != "b3,b4" || page["branch"].NextAfter != "b4" {
		t.Fatalf("second page: %v, next %q", got, page["branch"].NextAfter)
	}

	page, err = result.Filter(engine.PermissionFilter{Type: "branch", Limit: 2, After: "b4"})
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceIDs(page["branch"]); strings.Join(got, ",") != "b5" || page["branch"].NextAfter != "" {
		t.Fatalf("last page must name no next page: %v, next %q", got, page["branch"].NextAfter)
	}
}

func TestPagingNeedsAType(t *testing.T) {
	result := engine.PermissionResult{"branch": syncResources([]string{"read"}, "b1")}
	if _, err := result.Filter(engine.PermissionFilter{Limit: 2}); err == nil {
		t.Fatal("a limit without a type must be refused")
	}
	if _, err := result.Filter(engine.PermissionFilter{After: "b1"}); err == nil {
		t.Fatal("an after without a type must be refused")
	}
}

func TestAccessFilterKeepsTheMatchingResources(t *testing.T) {
	result := engine.PermissionResult{
		"branch": {Type: model.TypeModeSync, Resources: []model.ResourceAccess{
			{ID: "b1", Access: []string{"read"}},
			{ID: "b2", Access: []string{"read", "write"}},
		}},
		"feature": {Type: model.TypeModeNotSync, Access: []string{"read"}},
	}

	out, err := result.Filter(engine.PermissionFilter{Access: "write"})
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceIDs(out["branch"]); strings.Join(got, ",") != "b2" {
		t.Fatalf("only the resources with write: %v", got)
	}
	if _, ok := out["feature"]; ok {
		t.Fatal("a type as a whole without write must not be in the answer")
	}
}

func TestNotSyncTypeFilteredByTypeName(t *testing.T) {
	result := engine.PermissionResult{
		"feature": {Type: model.TypeModeNotSync, Access: []string{"read"}},
		"branch":  syncResources([]string{"read"}, "b1"),
	}
	out, err := result.Filter(engine.PermissionFilter{Type: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out["feature"].Type != model.TypeModeNotSync {
		t.Fatalf("only the named type: %v", out)
	}
}

func TestNextAfterIsSerialized(t *testing.T) {
	result := engine.PermissionResult{"branch": syncResources([]string{"read"}, "b1", "b2")}
	page, err := result.Filter(engine.PermissionFilter{Type: "branch", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"next_after":"b1"`) {
		t.Fatalf("a page that is not the last carries next_after: %s", raw)
	}

	last, err := result.Filter(engine.PermissionFilter{Type: "branch", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(last)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "next_after") {
		t.Fatalf("the last page carries no next_after: %s", raw)
	}
}
