//go:build integration

package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRuleOwnerIsDerivedAndCannotBeRetargeted(t *testing.T) {
	e := setup(t)
	first, second := e.newService("rule-first"), e.newService("rule-second")
	typ := e.must("POST", "/service/"+first+"/type_resource", map[string]any{"name": "flag"})["id"].(string)
	foreign := e.must("POST", "/service/"+second+"/type_resource", map[string]any{"name": "flag"})["id"].(string)
	group := e.must("POST", "/group", map[string]any{"service_id": second, "name": "foreign"})["id"].(string)
	role := e.must("POST", "/role", map[string]any{"service_id": first, "name": "reader"})["id"].(string)
	rule := e.must("POST", "/assignment_rule", map[string]any{"type_resource_id": typ, "access": []string{"read"}, "subject": "alice"})
	if rule["service_id"] != first {
		t.Fatalf("rule owner: %v", rule)
	}
	e.expect(http.StatusUnprocessableEntity, "PATCH", "/assignment_rule/"+rule["id"].(string), map[string]any{"type_resource_id": foreign, "access": []string{"read"}, "subject": "alice"})
	for _, field := range []string{"member_of_group_id", "requires_group_id"} {
		body := map[string]any{"type_resource_id": typ, "access": []string{"read"}, field: group}
		if field == "requires_group_id" {
			body["subject"] = "alice"
		}
		e.expect(http.StatusUnprocessableEntity, "POST", "/assignment_rule", body)
	}
	e.expect(http.StatusUnprocessableEntity, "POST", "/assignment_rule", map[string]any{"role_id": role, "member_of_group_id": group})
	roleRule := e.must("POST", "/assignment_rule", map[string]any{"role_id": role, "subject": "alice"})
	if roleRule["service_id"] != first {
		t.Fatalf("role rule owner: %v", roleRule)
	}
	for name, tc := range map[string]struct {
		sql  string
		args []any
	}{
		"type":   {`INSERT INTO assignment_rule(service_id,type_resource_id,subject,predicate,access,enabled) VALUES($1,$2,'alice','[]','{read}',true)`, []any{first, foreign}},
		"role":   {`INSERT INTO assignment_rule(service_id,role_id,subject,predicate,enabled) VALUES($1,$2,'alice','[]',true)`, []any{second, role}},
		"member": {`INSERT INTO assignment_rule(service_id,type_resource_id,member_of_group_id,predicate,access,enabled) VALUES($1,$2,$3,'[]','{read}',true)`, []any{first, typ, group}},
		"guard":  {`INSERT INTO assignment_rule(service_id,type_resource_id,requires_group_id,subject,predicate,access,enabled) VALUES($1,$2,$3,'alice','[]','{read}',true)`, []any{first, typ, group}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.pool.Exec(context.Background(), tc.sql, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
				t.Fatalf("foreign reference: %v", err)
			}
		})
	}
}
