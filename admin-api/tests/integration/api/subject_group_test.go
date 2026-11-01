//go:build integration

package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/licetum/licetum/core/engine"
)

func TestSubjectGroupIsAnOwnedReferenceAndExportsByName(t *testing.T) {
	w := newWorld(t)
	group := w.must("POST", "/group", map[string]any{"service_id": w.org, "name": "sales-subject"})["id"].(string)
	w.must("POST", "/group/"+group+"/member", map[string]any{"predicate": []map[string]any{{"attr": "department", "op": "eq", "value": "sales"}}})
	rule := w.must("POST", "/assignment_rule", map[string]any{"name": "group-subject", "subject_group_id": group, "type_resource_id": w.branch, "resource_id": "B1", "access": []string{"read"}})
	if rule["subject"] != nil || rule["subject_group_id"] != group {
		t.Fatalf("group reference: %v", rule)
	}
	expectIDs(t, w.permission(w.org, engine.Claims{"department": "sales"}), "branch", "B1")
	expectIDs(t, w.permission(w.org, engine.Claims{"department": "other"}), "branch")
	status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{"subject": "group:" + group, "type_resource_id": w.branch, "access": []string{"read"}})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "subject_group_reserved")
	w.expect(http.StatusUnprocessableEntity, "POST", "/assignment_rule", map[string]any{"subject": "alice", "subject_group_id": group, "type_resource_id": w.branch, "access": []string{"read"}})
	foreign := w.must("POST", "/group", map[string]any{"service_id": w.appSvc, "name": "sales-subject"})["id"].(string)
	w.expect(http.StatusUnprocessableEntity, "POST", "/assignment_rule", map[string]any{"subject_group_id": foreign, "type_resource_id": w.branch, "access": []string{"read"}})
	_, err := w.pool.Exec(context.Background(), `INSERT INTO assignment_rule(service_id,type_resource_id,subject_group_id,predicate,access,enabled) VALUES($1,$2,$3,'[]','{read}',true)`, w.org, w.branch, foreign)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("foreign subject group: %v", err)
	}
	doc := w.exportPolicy()
	found := false
	for _, r := range doc.Rules {
		if r.Name == "group-subject" {
			found = r.Subject == nil && r.SubjectGroup != nil && *r.SubjectGroup == "sales-subject"
		}
	}
	if !found {
		t.Fatalf("export did not name the group: %+v", doc.Rules)
	}
	if plan := w.importPolicy(doc, true, false); len(plan.Operations) != 0 {
		t.Fatalf("round trip planned changes: %+v", plan)
	}
	w.expect(http.StatusConflict, "DELETE", "/group/"+group, nil)
}
