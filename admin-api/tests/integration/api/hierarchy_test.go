//go:build integration

package api_test

// Integration tests of the permission computation over the resource hierarchy (resource_cache with
// the closure table), the type modes (sync / not_sync) and the synchronizer secrets. Infrastructure
// is provided by the package Testcontainers harness in api_test.go.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/engine"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
)

const hierarchySubject = "user-1"

// world is division <- rrs <- branch (service org) and item <- branch (service app), every type with
// an SQL synchronizer reading a src_* table of the test schema.
type world struct {
	*env
	org, appSvc                 string
	division, rrs, branch, item string
	syncs                       map[string]string // type name -> synchronizer id
	typeByName                  map[string]string
	serviceOfType               map[string]string
}

func newWorld(t *testing.T) *world {
	e := setup(t)
	ctx := context.Background()
	for _, table := range []string{"division", "rrs", "branch", "item"} {
		if _, err := e.pool.Exec(ctx, `CREATE TABLE src_`+table+` (id text PRIMARY KEY, name text, parent_id text)`); err != nil {
			t.Fatal(err)
		}
	}
	w := &world{env: e, syncs: map[string]string{}, typeByName: map[string]string{}, serviceOfType: map[string]string{}}
	w.source("division", "D1", "Div 1", "", "D2", "Div 2", "")
	w.source("rrs", "R1", "RRS 1", "D1", "R2", "RRS 2", "D1", "R3", "RRS 3", "D2")
	w.source("branch", "B1", "Branch 1", "R1", "B2", "Branch 2", "R1", "B3", "Branch 3", "R2", "B4", "Branch 4", "R3")
	w.source("item", "I1", "Item 1", "B1", "I4", "Item 4", "B4")

	m := e.mock.URL
	svc := func(name string) string {
		return e.must("POST", "/service", map[string]any{
			"name": name, "url": m + "/app", "icon": m + "/icon.png",
			"authorization_url": m + "/realm/auth", "token_url": m + "/realm/token",
			"jwks_url": m + "/jwks", "issuer": m + "/realm", "slug": name, "client_id": name, "client_secret": "s",
		})["id"].(string)
	}
	w.org, w.appSvc = svc("org"), svc("app")
	w.division = w.typeResource(w.org, "division", "")
	w.rrs = w.typeResource(w.org, "rrs", w.division)
	w.branch = w.typeResource(w.org, "branch", w.rrs)
	w.item = w.typeResource(w.appSvc, "item", w.branch)

	for name, probe := range map[string]string{"division": "D1", "rrs": "R1", "branch": "B1", "item": "I1"} {
		w.syncs[name] = e.must("POST", w.typePath(name)+"/resource/synchronizer", map[string]any{
			"config": map[string]any{
				"kind": "sql", "dsn": e.cfg.DB.URI,
				"query":      "SELECT id, name, parent_id FROM src_" + name + " WHERE id = :id",
				"list_query": "SELECT id, name, parent_id FROM src_" + name,
			},
			"probe_resource_id": probe,
		})["id"].(string)
	}
	return w
}

func (w *world) typeResource(serviceID, name, parent string) string {
	body := map[string]any{"name": name}
	if parent != "" {
		body["type_resource_parent_id"] = parent
	}
	id := w.must("POST", "/service/"+serviceID+"/type_resource", body)["id"].(string)
	w.typeByName[name] = id
	w.serviceOfType[name] = serviceID
	return id
}

func (w *world) typePath(name string) string {
	return "/service/" + w.serviceOfType[name] + "/type_resource/" + w.typeByName[name]
}

// source inserts (id, name, parent_id) triples into src_<table>; an empty parent is NULL.
func (w *world) source(table string, values ...string) {
	w.t.Helper()
	for i := 0; i+2 < len(values); i += 3 {
		var parent *string
		if values[i+2] != "" {
			parent = &values[i+2]
		}
		if _, err := w.pool.Exec(context.Background(), `INSERT INTO src_`+table+` VALUES ($1, $2, $3)`,
			values[i], values[i+1], parent); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.pool.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) grant(rule map[string]any) string {
	w.t.Helper()
	if _, ok := rule["access"]; !ok {
		rule["access"] = []string{"read"}
	}
	if _, ok := rule["subject"]; !ok && rule["member_of_group_id"] == nil && rule["predicate"] == nil {
		rule["subject"] = hierarchySubject
	}
	return w.must("POST", "/assignment_rule", rule)["id"].(string)
}

// permission computes the access of hierarchySubject with higher consistency: neither the result
// cache nor the source throttle is used (a source change does not bump the cache version by itself).
func (w *world) permission(serviceID string, claims engine.Claims) engine.PermissionResult {
	w.t.Helper()
	if claims == nil {
		claims = engine.Claims{}
	}
	out, err := w.app.GetPermission(context.Background(), uuid.MustParse(serviceID), hierarchySubject,
		engine.NewEnv(claims, nil), engine.HigherConsistency)
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func ids(result engine.PermissionResult, typeName string) []string {
	var out []string
	for _, r := range result[typeName].Resources {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	return out
}

func expectIDs(t *testing.T, result engine.PermissionResult, typeName string, want ...string) {
	t.Helper()
	if got := ids(result, typeName); !slices.Equal(got, want) {
		t.Fatalf("%s: got %v, want %v (full result %+v)", typeName, got, want, result)
	}
}

func TestIntegrationHierarchyCascade(t *testing.T) {
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1"})

	org := w.permission(w.org, nil)
	expectIDs(t, org, "division", "D1")
	expectIDs(t, org, "rrs", "R1", "R2")
	expectIDs(t, org, "branch", "B1", "B2", "B3")
	if org["branch"].Type != model.TypeModeSync {
		t.Fatalf("branch must be sync: %+v", org["branch"])
	}

	// The cascade crosses services: resources of another service under the branches.
	app := w.permission(w.appSvc, nil)
	expectIDs(t, app, "item", "I1")
	if r := app["item"].Resources[0]; r.Name != "Item 1" || !slices.Equal(r.Access, []string{"read"}) {
		t.Fatalf("item entry: %+v", r)
	}
	// The hash of every synchronized source is kept, so an unchanged source is not written again.
	for _, name := range []string{"division", "rrs", "branch", "item"} {
		var n int
		err := w.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM sync_state WHERE type_resource_id = $1 AND data_hash <> '' AND applied_at IS NOT NULL`,
			w.typeByName[name]).Scan(&n)
		if err != nil || n != 1 {
			t.Fatalf("no sync hash for %s: %d %v", name, n, err)
		}
	}
}

func TestIntegrationHierarchyResourceFromClaim(t *testing.T) {
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.rrs, "resource_from_claim": map[string]any{
		"claim": "department", "lookup_type_resource_id": w.branch, "match_by": "name",
	}})

	// branch B3 -> its rrs R2 -> everything under R2
	result := w.permission(w.org, engine.Claims{"department": "Branch 3"})
	expectIDs(t, result, "rrs", "R2")
	expectIDs(t, result, "branch", "B3")

	if nothing := w.permission(w.org, engine.Claims{"department": "no such branch"}); len(nothing) != 0 {
		t.Fatalf("an unknown claim value must grant nothing: %+v", nothing)
	}
}

func TestIntegrationHierarchyWholeTypeAndMerge(t *testing.T) {
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.rrs, "access": []string{"read", "write"}})
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "access": []string{"audit"}})

	result := w.permission(w.org, nil)
	expectIDs(t, result, "rrs", "R1", "R2", "R3")
	expectIDs(t, result, "branch", "B1", "B2", "B3", "B4")
	if _, ok := result["division"]; ok {
		t.Fatal("a grant on rrs must not reach its ancestor division")
	}
	for _, r := range result["branch"].Resources {
		want := []string{"read", "write"}
		if r.ID == "B1" {
			want = []string{"audit", "read", "write"}
		}
		if !slices.Equal(r.Access, want) {
			t.Fatalf("branch %s access %v, want %v", r.ID, r.Access, want)
		}
	}
}

func TestIntegrationHierarchySourceChanges(t *testing.T) {
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1"})
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2", "B3")

	// B3 moved under an rrs of another division, B2 disappeared, B5 appeared under R1.
	w.exec(`UPDATE src_branch SET parent_id = 'R3' WHERE id = 'B3'`)
	w.exec(`DELETE FROM src_branch WHERE id = 'B2'`)
	w.source("branch", "B5", "Branch 5", "R1")

	after := w.permission(w.org, nil)
	expectIDs(t, after, "branch", "B1", "B5")
	expectIDs(t, after, "rrs", "R1", "R2")

	// The resource that disappeared is switched off, not deleted, and is no longer resolved.
	var active bool
	if err := w.pool.QueryRow(context.Background(),
		`SELECT active FROM resource_cache WHERE external_id = 'B2'`).Scan(&active); err != nil || active {
		t.Fatalf("B2 must stay in the cache inactive: active=%v err=%v", active, err)
	}
	if status, _ := w.do("GET", w.admin.URL+w.typePath("branch")+"/resource?resource_id=B2", nil); status != http.StatusNotFound {
		t.Fatalf("a switched-off resource must not be found: %d", status)
	}
	if status, _ := w.do("GET", w.admin.URL+w.typePath("branch")+"/resource?resource_id=B1", nil); status != http.StatusOK {
		t.Fatalf("B1 must be found: %d", status)
	}
	_, raw := w.do("GET", w.admin.URL+"/type_resource/"+w.branch+"/resource", nil)
	var listed []model.Resource
	_ = json.Unmarshal(raw, &listed)
	if len(listed) != 4 || listed[0].ID != "B1" {
		t.Fatalf("the resource list must hold the 4 active branches sorted by name: %s", raw)
	}

	// A source that answers with no rows is telling the truth: its branches are gone from the hierarchy.
	w.exec(`DELETE FROM src_branch`)
	expectIDs(t, w.permission(w.org, nil), "branch")
}

func TestIntegrationHierarchyTypeTreeChanges(t *testing.T) {
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1"})
	expectIDs(t, w.permission(w.org, nil), "branch", "B1", "B2", "B3")

	// The sources did not change, only the type tree did: branch now hangs right under division
	// while its parent_id still points at rrs ids — the paths must be rebuilt anyway.
	w.exec(`DELETE FROM type_resource_parent WHERE type_resource_id = $1`, w.branch)
	w.exec(`INSERT INTO type_resource_parent (type_resource_id, parent_id) VALUES ($1, $2)`, w.branch, w.division)
	if got := ids(w.permission(w.org, nil), "branch"); len(got) != 0 {
		t.Fatalf("after re-parenting no branch is under D1, got %v", got)
	}
}

func TestIntegrationHierarchyDeletedMiddleType(t *testing.T) {
	w := newWorld(t)
	w.exec(`UPDATE type_resource SET deleted_at = now() WHERE id = $1`, w.rrs)
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1"})

	expectIDs(t, w.permission(w.org, nil), "branch", "B1")
	var withoutPath int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM resource_cache c WHERE c.active AND c.type_resource_id = $1
		   AND NOT EXISTS (SELECT 1 FROM resource_ancestor a WHERE a.descendant_id = c.id)`,
		w.branch).Scan(&withoutPath); err != nil || withoutPath != 0 {
		t.Fatalf("branches under a deleted type must still be in the closure: %d %v", withoutPath, err)
	}
}

func TestIntegrationTypeModes(t *testing.T) {
	w := newWorld(t)
	// A permission without resources for a role matched by the position claim: the type has no
	// synchronizer, so it is returned as not_sync.
	capability := w.typeResource(w.appSvc, "advanced_mode", "")
	role := w.must("POST", "/group", map[string]any{"name": "advanced-users"})["id"].(string)
	w.must("POST", "/group/"+role+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "position", "op": "in", "value": []string{"manager"}}},
	})
	w.grant(map[string]any{"type_resource_id": capability, "member_of_group_id": role, "access": []string{"write", "read"}})
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1"})

	manager := w.permission(w.appSvc, engine.Claims{"position": "manager"})
	expectIDs(t, manager, "item", "I1")
	expectJSON(t, "advanced_mode", manager["advanced_mode"], `{"type":"not_sync","access":["read","write"]}`)

	// Without an applicable rule a not_sync type is not in the response at all.
	cashier := w.permission(w.appSvc, engine.Claims{"position": "cashier"})
	if _, ok := cashier["advanced_mode"]; ok || len(cashier) != 1 {
		t.Fatalf("cashier must only see item: %+v", cashier)
	}

	// The check API returns the typed shape. The result cache is keyed by subject, not by claims, so
	// reset it after the cashier computation above.
	if _, err := redisx.BumpVersion(context.Background(), w.app.Redis); err != nil {
		t.Fatal(err)
	}
	token := w.token(jwt.MapClaims{"preferred_username": hierarchySubject, "position": "manager"})
	status, perm := w.env.permission(w.appSvc, token)
	if status != http.StatusOK {
		t.Fatalf("check API: %d %v", status, perm)
	}
	expectJSON(t, "check API", perm, `{
		"item": {"type":"sync","resources":[{"id":"I1","name":"Item 1","access":["read"]}]},
		"advanced_mode": {"type":"not_sync","access":["read","write"]}}`)

	// A specific resource can only be targeted on a type with a synchronizer.
	status, raw := w.do("POST", w.admin.URL+"/assignment_rule", map[string]any{
		"type_resource_id": capability, "resource_id": "X1", "access": []string{"read"}, "subject": hierarchySubject,
	})
	if status != http.StatusBadRequest || !strings.Contains(string(raw), `"code":"assignment_rule_needs_synchronizer"`) {
		t.Fatalf("a point rule on a type without a synchronizer: %d %s", status, raw)
	}
	pointRule := w.grant(map[string]any{"type_resource_id": w.item, "resource_id": "I4"})

	// A whole-type grant on item: sync with every resource; after its synchronizer is deleted the type
	// turns into not_sync and the point rule stops taking effect.
	w.grant(map[string]any{"type_resource_id": w.item})
	expectIDs(t, w.permission(w.appSvc, nil), "item", "I1", "I4")
	w.must("DELETE", w.typePath("item")+"/resource/synchronizer/"+w.syncs["item"], nil)
	expectJSON(t, "item without a synchronizer", w.permission(w.appSvc, nil)["item"], `{"type":"not_sync","access":["read"]}`)

	_, raw = w.do("GET", w.admin.URL+"/assignment_rule", nil)
	var rules []model.AssignmentRuleWithContext
	_ = json.Unmarshal(raw, &rules)
	for _, r := range rules {
		if want := r.ID.String() != pointRule; r.TargetValid != want {
			t.Fatalf("rule %s (resource %v): target_valid=%v, want %v", r.ID, r.ResourceID, r.TargetValid, want)
		}
	}
	// The dead rule can still be edited as long as its target stays the same…
	w.must("PATCH", "/assignment_rule/"+pointRule, map[string]any{
		"type_resource_id": w.item, "resource_id": "I4", "access": []string{"read", "write"}, "subject": hierarchySubject, "enabled": false,
	})
	// …but not re-targeted to another resource of the type without a synchronizer.
	if status, raw := w.do("PATCH", w.admin.URL+"/assignment_rule/"+pointRule, map[string]any{
		"type_resource_id": w.item, "resource_id": "I1", "access": []string{"read"}, "subject": hierarchySubject,
	}); status != http.StatusBadRequest {
		t.Fatalf("re-targeting a dead rule: %d %s", status, raw)
	}
}

func TestIntegrationTypeResourceNameUnique(t *testing.T) {
	w := newWorld(t)
	status, raw := w.do("POST", w.admin.URL+"/service/"+w.org+"/type_resource", map[string]any{"name": "branch"})
	if status != http.StatusConflict || !strings.Contains(string(raw), `"code":"type_resource_name_taken"`) {
		t.Fatalf("a duplicate name: %d %s", status, raw)
	}
	if status, raw := w.do("PATCH", w.admin.URL+w.typePath("division"), map[string]any{"name": "rrs"}); status != http.StatusConflict {
		t.Fatalf("renaming to a taken name: %d %s", status, raw)
	}
	// The same name in another service, or keeping one's own name, is fine.
	w.must("POST", "/service/"+w.appSvc+"/type_resource", map[string]any{"name": "branch"})
	w.must("PATCH", w.typePath("division"), map[string]any{"name": "division"})
}

func TestIntegrationSynchronizerSecrets(t *testing.T) {
	e := setup(t)
	m := e.mock.URL
	svcID := e.must("POST", "/service", map[string]any{
		"name": "svc", "url": m + "/app", "icon": m + "/icon.png",
		"authorization_url": m + "/realm/auth", "token_url": m + "/realm/token", "jwks_url": m + "/jwks",
		"issuer": m + "/realm", "slug": "c", "client_id": "c", "client_secret": "s",
	})["id"].(string)
	typeID := e.must("POST", "/service/"+svcID+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	path := "/service/" + svcID + "/type_resource/" + typeID + "/resource/synchronizer"

	// Without the header the closed source does not answer, so the probe fails.
	if status, _ := e.do("POST", e.admin.URL+path, map[string]any{
		"config": map[string]any{"kind": "http", "url": m + "/secure/branch"}, "probe_resource_id": "b1",
	}); status != http.StatusFailedDependency {
		t.Fatalf("a closed source without headers: want 424, got %d", status)
	}
	if status, _ := e.do("POST", e.admin.URL+path, map[string]any{
		"config":            map[string]any{"kind": "http", "url": m + "/secure/branch", "headers": map[string]string{"Bad Name": "x"}},
		"probe_resource_id": "b1",
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("an invalid header name: want 422, got %d", status)
	}

	created := e.must("POST", path, map[string]any{
		"config": map[string]any{"kind": "http", "url": m + "/secure/branch",
			"headers": map[string]string{"Authorization": "Bearer source-secret"}},
		"probe_resource_id": "b1",
	})
	expectJSON(t, "created config", created["config"],
		`{"kind":"http","url":"`+m+`/secure/branch","headers":{"Authorization":"********"}}`)
	syncID := created["id"].(string)

	_, raw := e.do("GET", e.admin.URL+path, nil)
	if strings.Contains(string(raw), "source-secret") {
		t.Fatalf("the list leaks the header value: %s", raw)
	}
	// Platform synchronizers point at our own DB: their DSN password is masked the same way.
	_, raw = e.do("GET", e.admin.URL+"/service/"+model.PlatformServiceID.String()+"/type_resource/"+
		model.PlatformTypeResourceID(model.PlatformService).String()+"/resource/synchronizer", nil)
	if !strings.Contains(string(raw), `"dsn"`) {
		t.Fatalf("platform synchronizers: %s", raw)
	}

	// Sending the mask back keeps the stored secret: the probe still passes.
	e.must("PATCH", path+"/"+syncID, map[string]any{"config": created["config"], "probe_resource_id": "b2"})
	var stored string
	if err := e.pool.QueryRow(context.Background(), `SELECT config->'headers'->>'Authorization' FROM synchronizer WHERE id = $1`,
		syncID).Scan(&stored); err != nil || stored != "Bearer source-secret" {
		t.Fatalf("the stored header after a masked update: %q %v", stored, err)
	}
	// A new value replaces it.
	if status, _ := e.do("PATCH", e.admin.URL+path+"/"+syncID, map[string]any{
		"config":            map[string]any{"kind": "http", "url": m + "/secure/branch", "headers": map[string]string{"Authorization": "Bearer wrong"}},
		"probe_resource_id": "b1",
	}); status != http.StatusFailedDependency {
		t.Fatalf("a wrong header value must fail the probe: %d", status)
	}

	// The resource list goes through the headers too.
	_, raw = e.do("GET", e.admin.URL+"/type_resource/"+typeID+"/resource", nil)
	if strings.Count(string(raw), `"id"`) != 3 {
		t.Fatalf("resources of the closed source: %s", raw)
	}
}

func TestIntegrationOAuthRefresh(t *testing.T) {
	e := setup(t)
	platform := model.PlatformServiceID.String()
	refresh := func(token string) (int, []byte, http.Header) {
		req, _ := http.NewRequest(http.MethodPost, e.admin.URL+"/service/"+platform+"/oauth/refresh",
			strings.NewReader(`{"refresh_token":"`+token+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw, resp.Header
	}

	status, raw, _ := refresh("good")
	var tokens map[string]any
	_ = json.Unmarshal(raw, &tokens)
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %s", status, raw)
	}
	expectJSON(t, "refreshed tokens", tokens, `{"access_token":"new-access","token_type":"Bearer","expires_in":300,
		"refresh_token":"new-refresh","refresh_expires_in":1800}`)

	status, raw, headers := refresh("expired")
	if status != http.StatusUnauthorized || !strings.Contains(string(raw), `"code":"refresh_token_invalid"`) ||
		headers.Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
		t.Fatalf("an expired refresh token: %d %s %v", status, raw, headers)
	}
	// invalid_client (e.g. a changed client secret) is not a reason to log everybody out.
	if status, raw, _ := refresh("other"); status != http.StatusFailedDependency {
		t.Fatalf("invalid_client: want 424, got %d %s", status, raw)
	}

	// 401 from the admin API carries the Bearer challenge.
	req, _ := http.NewRequest(http.MethodGet, e.admin.URL+"/service", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("admin API without a token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}
