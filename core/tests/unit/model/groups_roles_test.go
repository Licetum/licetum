package model_test

import (
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

const someTypeID = "294e1950-4746-541b-9ea3-b073b60fd6e9"

func TestAssignmentRuleGrantsEitherARoleOrAnInlinePermission(t *testing.T) {
	const roleID = "0d7e2a52-7c52-4c1e-9a3f-1f2b3c4d5e6f"
	if _, err := decodeAndValidate[model.AssignmentRuleInput](t, `{"role_id":"`+roleID+`","subject":"alice"}`); err != nil {
		t.Fatalf("a role rule needs no inline permission: %v", err)
	}
	mixed := map[string]string{
		"role and type":          `{"role_id":"` + roleID + `","type_resource_id":"` + someTypeID + `"}`,
		"role and access":        `{"role_id":"` + roleID + `","access":["read"]}`,
		"role and resource":      `{"role_id":"` + roleID + `","resource_id":"x"}`,
		"neither role nor type":  `{"access":["read"],"subject":"alice"}`,
		"role with two subjects": `{"role_id":"` + roleID + `","subject":"a","predicate":[{"attr":"a","op":"eq","value":"b"}]}`,
	}
	for name, raw := range mixed {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeAndValidate[model.AssignmentRuleInput](t, raw); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRolePermissionItemTakesAnAccessLevelAndOneTarget(t *testing.T) {
	ok := []string{
		`{"type_resource_id":"` + someTypeID + `","access":["read","write"]}`,
		`{"type_resource_id":"` + someTypeID + `","resource_id":"b1","access":["read"]}`,
	}
	for _, raw := range ok {
		if _, err := decodeAndValidate[model.RolePermissionInput](t, raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	invalid := map[string]string{
		"no access":   `{"type_resource_id":"` + someTypeID + `","access":[]}`,
		"wildcard":    `{"type_resource_id":"` + someTypeID + `","access":["*"]}`,
		"empty level": `{"type_resource_id":"` + someTypeID + `","access":[""]}`,
		"two targets": `{"type_resource_id":"` + someTypeID + `","resource_id":"b1","resource_from_claim":{"claim":"c","lookup_type_resource_id":"` + someTypeID + `"},"access":["read"]}`,
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeAndValidate[model.RolePermissionInput](t, raw); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestServiceSlugFormat(t *testing.T) {
	for _, slug := range []string{"replan", "my-service_2", "1c"} {
		if !model.ValidSlug(slug) {
			t.Errorf("%q must be a valid slug", slug)
		}
	}
	for _, slug := range []string{"", "Replan", "-replan", "re plan", "re.plan", strings.Repeat("a", 65), "00000000-0000-0000-0000-000000000000"} {
		if model.ValidSlug(slug) {
			t.Errorf("%q must not be a valid slug", slug)
		}
	}
}

func TestSynchronizerCacheTTLIsBounded(t *testing.T) {
	const dsn = `"dsn":"postgresql://reader:secret@db.example:5432/app"`
	for _, ttl := range []string{"0", "30", "86400"} {
		raw := `{"config":{"kind":"sql",` + dsn + `,"query":"SELECT id, name, parent_id FROM t WHERE id = :id",` +
			`"list_query":"SELECT id, name, parent_id FROM t","cache_ttl":` + ttl + `}}`
		if _, err := decodeAndValidate[model.SynchronizerInput](t, raw); err != nil {
			t.Errorf("cache_ttl %s: %v", ttl, err)
		}
	}
	for _, ttl := range []string{"-1", "86401"} {
		raw := `{"config":{"kind":"sql",` + dsn + `,"query":"SELECT id, name, parent_id FROM t WHERE id = :id",` +
			`"list_query":"SELECT id, name, parent_id FROM t","cache_ttl":` + ttl + `}}`
		if _, err := decodeAndValidate[model.SynchronizerInput](t, raw); err == nil {
			t.Errorf("cache_ttl %s must be rejected", ttl)
		}
	}
}
