//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func TestLastAdministratorLiteralSubjectType(t *testing.T) {
	e := setup(t)
	path := "/assignment_rule/" + model.PlatformAdminRuleID(model.PlatformAssignmentRule).String()
	body := map[string]any{
		"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule),
		"subject":          "admin",
		"access":           []string{"read", "write"},
	}
	// A user-typed literal can replace the seeded group and remain the last administrator.
	body["subject_type"] = "user"
	e.must(http.MethodPatch, path, body)

	body["subject_type"] = "service"
	status, raw := e.do(http.MethodPatch, e.admin.URL+path, body)
	if status != http.StatusConflict || !strings.Contains(string(raw), "last_admin_access") {
		t.Fatalf("non-user literal removed the last administrator: %d %s", status, raw)
	}
	// The rejected transaction must leave the administrator able to manage rules.
	if status, raw := e.do(http.MethodGet, e.admin.URL+path, nil); status != http.StatusOK {
		t.Fatalf("the rejected edit locked the administrator out: %d %s", status, raw)
	}
	// Omitting the type also remains valid: NULL matches every subject type.
	delete(body, "subject_type")
	e.must(http.MethodPatch, path, body)
}
