//go:build integration

package api_test

// Integration tests of the checks on the targets of rules and role items. A point write on an entity does not reach a
// platform policy: it cannot put one into a role through a batch, nor move a rule to another target. And a deny on a
// group whose membership reads an attribute the request does not carry applies, as any other deny does. Same
// Testcontainers environment as hierarchy_test.go.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/model"
)

func TestIntegrationPointWriterCannotPutAPlatformPolicyIntoARoleThroughABatch(t *testing.T) {
	w := newWorld(t)
	role := w.must("POST", "/role", map[string]any{"name": "carol-role"})["id"].(string)
	// Carol may write this one role, and nothing of the platform as a whole.
	w.grant(map[string]any{"type_resource_id": model.PlatformTypeResourceID(model.PlatformRole).String(),
		"resource_id": role, "subject": "carol", "access": []string{"write"}})
	carol := "Bearer " + w.token(jwt.MapClaims{"preferred_username": "carol"})
	group := model.PlatformTypeResourceID(model.PlatformGroup).String()

	status, raw := w.do("POST", w.admin.URL+"/batch", map[string]any{"operations": []map[string]any{
		{"op": "create", "entity": "role_permission", "parent": role,
			"body": map[string]any{"type_resource_id": group, "access": []string{"read"}}},
	}}, "Authorization", carol)
	if status != http.StatusForbidden {
		t.Fatalf("a point writer of a role put a platform policy into it through a batch: %d %s", status, raw)
	}
	if strings.Contains(string(w.mustRaw("GET", "/role/"+role+"/permission")), group) {
		t.Fatal("the refused item was written")
	}
}

func TestIntegrationPointWriterChangesWhatARuleAppliesToButNotItsTarget(t *testing.T) {
	w := newWorld(t)
	rule := w.rule("branch", "B1", map[string]any{"subject": "dave", "access": []string{"read"}})
	w.grant(map[string]any{"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule).String(),
		"resource_id": rule, "subject": "carol", "access": []string{"write"}})
	carol := "Bearer " + w.token(jwt.MapClaims{"preferred_username": "carol"})
	role := w.must("POST", "/role", map[string]any{"name": "any-role"})["id"].(string)
	url := w.admin.URL + "/assignment_rule/" + rule

	// Who the rule applies to and its levels are its own to change: the target stays B1.
	if status, raw := w.do("PATCH", url, map[string]any{"type_resource_id": w.typeByName["branch"], "resource_id": "B1",
		"subject": "erin", "access": []string{"read"}}, "Authorization", carol); status != http.StatusOK {
		t.Fatalf("a point writer changes the subject of its rule: %d %s", status, raw)
	}
	// Moving it to another target needs the rules policy as a whole, as creating a rule does.
	moves := map[string]map[string]any{
		"another type":      {"type_resource_id": w.typeByName["rrs"], "subject": "erin", "access": []string{"read"}},
		"a platform policy": {"type_resource_id": model.PlatformTypeResourceID(model.PlatformGroup).String(), "subject": "erin", "access": []string{"read"}},
		"a role":            {"role_id": role, "subject": "erin"},
	}
	for name, body := range moves {
		if status, raw := w.do("PATCH", url, body, "Authorization", carol); status != http.StatusForbidden {
			t.Errorf("a point writer moves its rule to %s: %d %s", name, status, raw)
		}
	}
}

func TestIntegrationDenyOfAGroupWhoseMembershipReadsAMissingAttributeApplies(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "", map[string]any{"subject": hierarchySubject})
	sales := w.must("POST", "/group", map[string]any{"name": "sales-team"})["id"].(string)
	w.must("POST", "/group/"+sales+"/member", map[string]any{
		"predicate": []map[string]any{{"attr": "department", "op": "eq", "value": "sales"}}})
	// A new deny applies on a missing attribute: without the department claim the membership is unknown, so B1 stays
	// denied. Known not to be a member, the subject gets B1 back.
	w.rule("branch", "B1", map[string]any{"effect": "deny", "access": []string{"read"}, "member_of_group_id": sales})

	claims := jwt.MapClaims{"preferred_username": hierarchySubject}
	expectIDs(t, w.permissionWith(w.org, claims, nil), "branch", "B2", "B3", "B4")
	claims["department"] = "sales"
	expectIDs(t, w.permissionWith(w.org, claims, nil), "branch", "B2", "B3", "B4")
	claims["department"] = "hr"
	expectIDs(t, w.permissionWith(w.org, claims, nil), "branch", "B1", "B2", "B3", "B4")
}

// An allow turned into a deny keeps the default of a new deny, not the skip of the allow: the denial applies on an
// attribute the request does not carry (fail closed).
func TestIntegrationAnAllowRuleTurnedIntoADenyAppliesOnAMissingAttribute(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "", map[string]any{"subject": hierarchySubject})
	cidr := []map[string]any{{"attr": "ip", "source": "context", "op": "in_cidr", "value": "10.99.0.0/16"}}
	// The allow grants B2 only from the network: without the address it grants nothing.
	rule := w.rule("branch", "B2", map[string]any{"access": []string{"read"}, "predicate": cidr})
	w.must("PATCH", "/assignment_rule/"+rule, map[string]any{"type_resource_id": w.typeByName["branch"],
		"resource_id": "B2", "access": []string{"read"}, "predicate": cidr, "effect": "deny"})

	claims := jwt.MapClaims{"preferred_username": hierarchySubject}
	expectIDs(t, w.permissionWith(w.org, claims, nil), "branch", "B1", "B3", "B4")
	expectIDs(t, w.permissionWith(w.org, claims, map[string]any{"ip": "10.1.1.1"}), "branch", "B1", "B2", "B3", "B4")
}

// A rule that grants a role with an item on the platform grants that item: a point write on the rule does not reach to
// changing its subject, which would give the platform policy to whoever the subject becomes.
func TestIntegrationPointWriterCannotGiveItselfARoleWithAPlatformItem(t *testing.T) {
	w := newWorld(t)
	role := w.must("POST", "/role", map[string]any{"name": "platform-reader"})["id"].(string)
	w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": model.PlatformTypeResourceID(model.PlatformGroup).String(), "access": []string{"read"}})
	rule := w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "subject": "dave"})["id"].(string)
	w.grant(map[string]any{"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule).String(),
		"resource_id": rule, "subject": "carol", "access": []string{"write"}})
	carol := "Bearer " + w.token(jwt.MapClaims{"preferred_username": "carol"})

	if status, raw := w.do("PATCH", w.admin.URL+"/assignment_rule/"+rule,
		map[string]any{"role_id": role, "subject": "carol"}, "Authorization", carol); status != http.StatusForbidden {
		t.Fatalf("a point writer gave itself a role with a platform item: %d %s", status, raw)
	}
}
