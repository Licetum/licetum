//go:build integration

package api_test

// Integration tests of the batch executor and of the history of the policy: a batch is atomic and names the operation
// that failed; a revert undoes one change of the log; a rollback goes back to a position of the log, restoring what
// was deleted with the ids and the items it had; the change feed waits for the next record. Same Testcontainers
// environment as hierarchy_test.go.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// lastChangeID returns the position of the newest record of the change log (0 when there is none).
func (e *env) lastChangeID() int64 {
	e.t.Helper()
	recs := e.changes("?limit=500")
	if len(recs) == 0 {
		return 0
	}
	return int64(recs[len(recs)-1]["id"].(float64))
}

// revert performs POST /changes/{id}/revert and returns the answer.
func (e *env) revert(id int64, dryRun bool) map[string]any {
	e.t.Helper()
	return e.must("POST", "/changes/"+strconv.FormatInt(id, 10)+"/revert", map[string]any{"dry_run": dryRun})
}

func TestIntegrationBatchIsAtomicAndNamesTheFailingOperation(t *testing.T) {
	w := newWorld(t)
	role := "00000000-0000-4000-8000-0000000000a1"
	status, raw := w.do("POST", w.admin.URL+"/batch", map[string]any{"operations": []map[string]any{
		{"op": "create", "entity": "role", "id": role, "body": map[string]any{"name": "batch_role"}},
		{"op": "create", "entity": "role_permission", "parent": role,
			"body": map[string]any{"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "access": []string{"read"}}},
		{"op": "create", "entity": "assignment_rule", "body": map[string]any{"access": []string{"read"}}},
	}})
	if status != http.StatusUnprocessableEntity || !strings.Contains(string(raw), `"operation":2`) {
		t.Fatalf("a batch with a bad operation: %d %s", status, raw)
	}
	if strings.Contains(string(w.mustRaw("GET", "/role")), "batch_role") {
		t.Fatal("the role of a failed batch was written")
	}

	// Each operation sees the writes of the ones before it: the item names the role created above it, and the
	// role assignment grants the role to the subject.
	res := w.must("POST", "/batch", map[string]any{"operations": []map[string]any{
		{"op": "create", "entity": "role", "id": role, "body": map[string]any{"name": "batch_role"}},
		{"op": "create", "entity": "role_permission", "parent": role,
			"body": map[string]any{"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "access": []string{"read"}}},
		{"op": "create", "entity": "assignment_rule", "body": map[string]any{"role_id": role, "subject": hierarchySubject}},
	}})
	if results := res["results"].([]any); len(results) != 3 || results[0].(map[string]any)["id"] != role {
		t.Fatalf("results: %v", res)
	}
	expectIDs(t, w.permission(w.org, nil), "branch", "B1")

	// An update and a delete name their entity; a group member needs its group.
	if status, raw := w.do("POST", w.admin.URL+"/batch", map[string]any{"operations": []map[string]any{
		{"op": "delete", "entity": "role"},
	}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("a delete without an id: %d %s", status, raw)
	}
	if status, raw := w.do("POST", w.admin.URL+"/batch", map[string]any{"operations": []map[string]any{
		{"op": "create", "entity": "group_member", "body": map[string]any{"predicate": []any{}}},
	}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("a group member without its group: %d %s", status, raw)
	}
}

func TestIntegrationRevertUndoesOneChangeOfTheLog(t *testing.T) {
	w := newWorld(t)
	rule := w.rule("branch", "B1", map[string]any{"subject": hierarchySubject})
	created := w.lastChangeID()
	expectIDs(t, w.permission(w.org, nil), "branch", "B1")

	w.must("PATCH", "/assignment_rule/"+rule, map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B1", "subject": hierarchySubject, "access": []string{"write"}})
	updated := w.lastChangeID()
	if got := accessOf(w.permission(w.org, nil), "branch", "B1"); len(got) != 1 || got[0] != "write" {
		t.Fatalf("after the update: %v", got)
	}

	// Reverting the update writes the rule back as it was; the revert is a change of its own.
	dry := w.revert(updated, true)
	if dry["dry_run"] != true || len(dry["results"].([]any)) != 0 {
		t.Fatalf("a dry run applies nothing: %v", dry)
	}
	w.revert(updated, false)
	if got := accessOf(w.permission(w.org, nil), "branch", "B1"); len(got) != 1 || got[0] != "read" {
		t.Fatalf("after reverting the update: %v", got)
	}
	if w.lastChangeID() <= updated {
		t.Fatal("the revert left no record in the log")
	}

	// Reverting the creation deletes the rule.
	w.revert(created, false)
	expectIDs(t, w.permission(w.org, nil), "branch")

	// A change to a service is not a batch entity: it cannot be reverted, and the answer says so.
	services := w.changes("?entity=service&limit=500")
	serviceChange := int64(services[0]["id"].(float64))
	if status, raw := w.do("POST", w.admin.URL+"/changes/"+strconv.FormatInt(serviceChange, 10)+"/revert",
		map[string]any{}); status != http.StatusConflict {
		t.Fatalf("revert of a service change: %d %s", status, raw)
	}
}

func TestIntegrationRollbackRestoresThePolicyAtAPosition(t *testing.T) {
	w := newWorld(t)
	keep := w.rule("branch", "B1", map[string]any{"subject": hierarchySubject})
	role := w.must("POST", "/role", map[string]any{"name": "reader"})["id"].(string)
	item := w.must("POST", "/role/"+role+"/permission", map[string]any{
		"type_resource_id": w.typeByName["branch"], "resource_id": "B2", "access": []string{"read"}})["id"].(string)
	roleRule := w.must("POST", "/assignment_rule", map[string]any{"role_id": role, "subject": hierarchySubject})["id"].(string)
	position := w.lastChangeID()
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2")

	// After the position: the rules go, the role with its item goes; a group comes.
	w.must("DELETE", "/assignment_rule/"+roleRule, nil)
	w.must("DELETE", "/assignment_rule/"+keep, nil)
	w.must("DELETE", "/role/"+role, nil)
	group := w.must("POST", "/group", map[string]any{"name": "later"})["id"].(string)
	expectIDs(t, w.permission(w.org, nil), "branch")

	// The dry run names what would be undone, newest first, and applies nothing.
	status, raw := w.do("POST", w.admin.URL+"/changes/rollback", map[string]any{"to": position, "dry_run": true})
	if status != http.StatusOK {
		t.Fatalf("dry run: %d %s", status, raw)
	}
	var plan struct {
		Operations []map[string]any `json:"operations"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil || len(plan.Operations) < 5 {
		t.Fatalf("the plan: %v %s", err, raw)
	}
	if plan.Operations[0]["entity"] != "group" || plan.Operations[0]["op"] != "delete" {
		t.Fatalf("the newest change is undone first: %v", plan.Operations[0])
	}
	expectIDs(t, w.permission(w.org, nil), "branch")

	// The rollback restores the rule, the role and its item under their own ids, and removes the group.
	w.must("POST", "/changes/rollback", map[string]any{"to": position})
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2")
	if !strings.Contains(string(w.mustRaw("GET", "/role/"+role+"/permission")), item) {
		t.Fatal("the item of the restored role is not back")
	}
	if strings.Contains(string(w.mustRaw("GET", "/group")), group) {
		t.Fatal("the group created after the position is still there")
	}

	// A rollback over a change that cannot be undone (a service created after the position) applies nothing.
	w.must("POST", "/service/", map[string]any{"name": "late", "url": w.mock.URL + "/app", "icon": w.mock.URL + "/icon.png",
		"slug": "late"})
	before := w.lastChangeID()
	w.must("DELETE", "/assignment_rule/"+keep, nil)
	w.must("DELETE", "/assignment_rule/"+roleRule, nil)
	if status, raw := w.do("POST", w.admin.URL+"/changes/rollback", map[string]any{"to": before - 2}); status != http.StatusConflict {
		t.Fatalf("a rollback over a service change: %d %s", status, raw)
	}
	expectIDs(t, w.permission(w.org, nil), "branch")
}

func TestIntegrationChangeFeedWaitsForTheNextRecord(t *testing.T) {
	w := newWorld(t)
	last := w.lastChangeID()
	started := time.Now()
	status, raw := w.do("GET", w.admin.URL+"/changes?after="+strconv.FormatInt(last, 10)+"&wait=1", nil)
	if status != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" || time.Since(started) < 900*time.Millisecond {
		t.Fatalf("a wait with nothing to read: %d %s after %v", status, raw, time.Since(started))
	}

	// A record written while a client waits is delivered to it.
	answered := make(chan []byte, 1)
	go func() {
		_, body := w.do("GET", w.admin.URL+"/changes?after="+strconv.FormatInt(last, 10)+"&wait=10", nil)
		answered <- body
	}()
	time.Sleep(300 * time.Millisecond)
	w.must("POST", "/group", map[string]any{"name": "feed"})
	select {
	case body := <-answered:
		if !strings.Contains(string(body), `"feed"`) {
			t.Fatalf("the waiting client got: %s", body)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the waiting client was not answered")
	}
}
