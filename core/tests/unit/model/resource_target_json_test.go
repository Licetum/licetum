package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func TestRetiredResourceTargetsCannotBecomeWholeTypeGrants(t *testing.T) {
	for _, target := range []any{&model.AssignmentRuleInput{}, &model.RolePermissionInput{}, &model.PolicyRule{}, &model.PolicyRoleItem{}} {
		err := json.Unmarshal([]byte(`{"resource_from_claim":null}`), target)
		if err == nil || !strings.Contains(err.Error(), "resource_from_property") {
			t.Fatalf("%T: expected replacement hint, got %v", target, err)
		}
	}
	var target model.PolicyResourceFromProperty
	if err := json.Unmarshal([]byte(`{"claim":"department","lookup_type":"org/project"}`), &target); err == nil || !strings.Contains(err.Error(), "property") {
		t.Fatalf("retired document property: %v", err)
	}
}
