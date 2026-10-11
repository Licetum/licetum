//go:build integration

package api_test

// Integration tests of the backend enablers of the interface: the optional address of a service, the one source of a
// type, the refresh of a type, the state of a resource, the test of a source, the protection of the last admin access,
// the stored scenarios, the registry of known subjects, the reverse lookup of access and the preview of a rule.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
)

func decodeList(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode list: %v (%s)", err, raw)
	}
	return out
}

func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode object: %v (%s)", err, raw)
	}
	return out
}

func TestServiceAddressIsOptionalAndNeverRequested(t *testing.T) {
	e := setup(t)
	m := e.mock.URL
	base := func(name string) map[string]any {
		return map[string]any{
			"name": name, "slug": name, "authorization_url": m + "/realm/auth", "token_url": m + "/realm/token",
			"jwks_url": m + "/jwks", "issuer": m + "/realm", "client_id": name,
		}
	}

	// No address and no icon, and a public client.
	created := e.must("POST", "/service", base("bare"))
	if created["url"] != nil || created["icon"] != nil {
		t.Fatalf("the address and the icon are optional: %+v", created)
	}
	if created["has_client_secret"] != false {
		t.Fatalf("a client without a secret is public: %+v", created)
	}

	// An address that nothing answers is accepted: it is only shown, never requested.
	dead := base("dead")
	dead["url"], dead["icon"], dead["client_secret"] = "http://127.0.0.1:1/app", "http://127.0.0.1:1/icon.png", "s"
	got := e.must("POST", "/service", dead)
	if got["has_client_secret"] != true || strings.TrimRight(got["url"].(string), "/") != "http://127.0.0.1:1/app" {
		t.Fatalf("service with a dead address: %+v", got)
	}

	// An empty string removes the address; the secret is never returned, only whether it is stored.
	updated := e.must("PATCH", "/service/"+got["id"].(string), map[string]any{"url": "", "icon": ""})
	if updated["url"] != nil || updated["icon"] != nil || updated["has_client_secret"] != true {
		t.Fatalf("after removing the address: %+v", updated)
	}
	cleared := e.must("PATCH", "/service/"+got["id"].(string), map[string]any{"client_secret": nil})
	if cleared["has_client_secret"] != false {
		t.Fatalf("after removing the secret: %+v", cleared)
	}
}

func TestResourceTypeHasOneSource(t *testing.T) {
	w := newWorld(t)
	status, raw := w.do("POST", w.admin.URL+w.typePath("division")+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id FROM src_division WHERE id = :id",
			"list_query": "SELECT id, name, parent_id FROM src_division",
		},
		"probe_resource_id": "D1",
	})
	requireErrorCode(t, status, raw, http.StatusConflict, "synchronizer_exists")

	// After the source is deleted, another can be added.
	w.must("DELETE", w.typePath("division")+"/resource/synchronizer/"+w.syncs["division"], nil)
	w.must("POST", w.typePath("division")+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "sql", "dsn": w.cfg.DB.URI,
			"query":      "SELECT id, name, parent_id FROM src_division WHERE id = :id",
			"list_query": "SELECT id, name, parent_id FROM src_division",
		},
		"probe_resource_id": "D1",
	})
}

func TestRefreshResourcesAsksTheSourceNow(t *testing.T) {
	w := newWorld(t)
	list := func(query string) []map[string]any {
		status, raw := w.do("GET", w.admin.URL+"/type_resource/"+w.branch+"/resource"+query, nil)
		if status != http.StatusOK {
			t.Fatalf("list resources: %d %s", status, raw)
		}
		return decodeList(t, raw)
	}
	if n := len(list("")); n != 4 {
		t.Fatalf("branches at the start: %d", n)
	}

	// A new row in the source is not read within the throttle...
	w.source("branch", "B5", "Branch 5", "R1")
	if n := len(list("")); n != 4 {
		t.Fatalf("the throttle should hold the old state, got %d", n)
	}
	// ...but a refresh asks the source now.
	refreshed := w.must("POST", "/type_resource/"+w.branch+"/resource/refresh", nil)
	if refreshed["resources"] != float64(5) || refreshed["inactive"] != float64(0) {
		t.Fatalf("refresh: %+v", refreshed)
	}
	if n := len(list("")); n != 5 {
		t.Fatalf("branches after the refresh: %d", n)
	}

	// A source that does not answer is an error, and the data stays.
	w.exec(`ALTER TABLE src_item RENAME TO src_item_away`)
	status, raw := w.do("POST", w.admin.URL+"/type_resource/"+w.item+"/resource/refresh", nil)
	requireErrorCode(t, status, raw, http.StatusFailedDependency, "synchronizer_no_answer")
	w.exec(`ALTER TABLE src_item_away RENAME TO src_item`)

	// A type without a source cannot be refreshed.
	feature := w.typeResource(w.org, "feature", "")
	status, raw = w.do("POST", w.admin.URL+"/type_resource/"+feature+"/resource/refresh", nil)
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "synchronizer_no_source")
}

func TestResourceStateIsReported(t *testing.T) {
	w := newWorld(t)
	// The type is read once, so that B4 is known; then B9 appears with a parent that is in no source, and B4 leaves.
	w.must("POST", "/type_resource/"+w.branch+"/resource/refresh", nil)
	w.source("branch", "B9", "Branch 9", "R9")
	w.exec(`DELETE FROM src_branch WHERE id = 'B4'`)
	w.must("POST", "/type_resource/"+w.branch+"/resource/refresh", nil)

	byID := func(query string) map[string]map[string]any {
		status, raw := w.do("GET", w.admin.URL+"/type_resource/"+w.branch+"/resource"+query, nil)
		if status != http.StatusOK {
			t.Fatalf("list resources: %d %s", status, raw)
		}
		out := map[string]map[string]any{}
		for _, r := range decodeList(t, raw) {
			out[r["id"].(string)] = r
		}
		return out
	}
	active := byID("")
	if _, gone := active["B4"]; gone {
		t.Fatalf("a resource gone from its source is not listed by default: %v", active)
	}
	if active["B1"]["active"] != true {
		t.Fatalf("B1 should be active: %+v", active["B1"])
	}
	parents := active["B9"]["parents"].([]any)
	if len(parents) != 1 || parents[0].(map[string]any)["resolved"] != false {
		t.Fatalf("B9's parent link should be unresolved: %+v", active["B9"])
	}
	if p := active["B1"]["parents"].([]any)[0].(map[string]any); p["resolved"] != true {
		t.Fatalf("B1's parent link should be resolved: %+v", p)
	}

	all := byID("?include_inactive=true")
	if b4, ok := all["B4"]; !ok || b4["active"] != false {
		t.Fatalf("include_inactive should return B4 switched off: %+v", all["B4"])
	}
}

func TestSourceTestReportsWhatTheSourceGives(t *testing.T) {
	w := newWorld(t)
	w.source("branch", "B9", "Branch 9", "R9") // the parent is nowhere
	path := w.typePath("branch") + "/resource/synchronizer/test"
	good := map[string]any{"config": map[string]any{
		"kind": "sql", "dsn": w.cfg.DB.URI,
		"query": "SELECT id, name, parent_id FROM src_branch WHERE id = :id",
		// B1 twice, and an attribute.
		"list_query": "SELECT id, name, parent_id, 'north' AS region FROM src_branch UNION ALL " +
			"SELECT id, name, parent_id, 'north' AS region FROM src_branch WHERE id = 'B1'",
	}}
	res := w.must("POST", path, good)
	if res["total"] != float64(6) || res["duplicate_count"] != float64(1) || res["orphan_count"] != float64(1) {
		t.Fatalf("source test: %+v", res)
	}
	if d := res["duplicates"].([]any); len(d) != 1 || d[0] != "B1" {
		t.Fatalf("duplicates: %v", d)
	}
	if o := res["orphans"].([]any)[0].(map[string]any); o["id"] != "B9" || o["parent_id"] != "R9" || o["parent_type"] != "rrs" {
		t.Fatalf("orphans: %v", o)
	}
	attrs := res["attributes"].([]any)
	if len(attrs) != 1 || attrs[0].(map[string]any)["name"] != "region" || attrs[0].(map[string]any)["count"] != float64(5) {
		t.Fatalf("attributes: %v", attrs)
	}
	if len(res["sample"].([]any)) != 5 {
		t.Fatalf("sample: %v", res["sample"])
	}
	// Nothing was saved: the type still has its one source, unchanged.
	status, raw := w.do("GET", w.admin.URL+w.typePath("branch")+"/resource/synchronizer", nil)
	if status != http.StatusOK || strings.Count(string(raw), `"list_query"`) != 1 || strings.Contains(string(raw), "UNION ALL") {
		t.Fatalf("the test must not change the sources: %d %s", status, raw)
	}

	// A source that does not answer, with a password in its address: the answer must not repeat it.
	bad := map[string]any{"config": map[string]any{
		"kind": "sql", "dsn": "postgres://user:sekret-pw-123@127.0.0.1:1/none",
		"query": "SELECT 1", "list_query": "SELECT 1",
	}}
	status, raw = w.do("POST", w.admin.URL+path, bad)
	requireErrorCode(t, status, raw, http.StatusFailedDependency, "source_test_failed")
	if strings.Contains(string(raw), "sekret-pw-123") {
		t.Fatalf("the answer repeats the password: %s", raw)
	}

	// The saved source's secrets can be left masked.
	masked := map[string]any{"synchronizer_id": w.syncs["branch"], "config": map[string]any{
		"kind": "sql", "dsn": strings.Replace(w.cfg.DB.URI, ":"+dsnPassword(w.cfg.DB.URI)+"@", ":********@", 1),
		"query": "SELECT id, name, parent_id FROM src_branch WHERE id = :id", "list_query": "SELECT id, name, parent_id FROM src_branch",
	}}
	if got := w.must("POST", path, masked); got["total"] != float64(5) {
		t.Fatalf("test of a masked source: %+v", got)
	}
}

func dsnPassword(dsn string) string {
	rest := dsn[strings.Index(dsn, "://")+3:]
	userinfo, _, _ := strings.Cut(rest, "@")
	_, password, _ := strings.Cut(userinfo, ":")
	return password
}

func TestTheLastAdminAccessIsProtected(t *testing.T) {
	e := setup(t)
	adminRule := model.PlatformAdminRuleID(model.PlatformAssignmentRule).String()

	// The only way to manage the rules cannot be removed, switched off or narrowed.
	status, raw := e.do("DELETE", e.admin.URL+"/assignment_rule/"+adminRule, nil)
	requireErrorCode(t, status, raw, http.StatusConflict, "last_admin_access")
	status, raw = e.do("PATCH", e.admin.URL+"/assignment_rule/"+adminRule, map[string]any{
		"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule).String(),
		"access":           []string{"read"}, "subject": "group:" + model.PlatformAdminGroupID.String(),
	})
	requireErrorCode(t, status, raw, http.StatusConflict, "last_admin_access")

	// Emptying the admin group is the same thing.
	status, raw = e.do("DELETE", e.admin.URL+"/group/"+model.PlatformAdminGroupID.String()+"/member/"+model.PlatformAdminGroupMemberID.String(), nil)
	requireErrorCode(t, status, raw, http.StatusConflict, "last_admin_access")

	// With another administrator in place the first can go.
	backup := e.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule).String(),
		"access":           []string{"read", "write"}, "subject": "backup",
	})["id"].(string)
	e.must("DELETE", "/assignment_rule/"+adminRule, nil)

	// And now the backup is the last one. A batch that replaces it is fine; one that only removes it is not.
	status, raw = e.do("DELETE", e.admin.URL+"/assignment_rule/"+backup, nil, "Authorization", "Bearer "+
		e.token(jwt.MapClaims{"preferred_username": "backup", "sub": "backup"}))
	requireErrorCode(t, status, raw, http.StatusConflict, "last_admin_access")
}

func TestRestoreAdminAccessPutsTheAdminsBack(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	typeID := model.PlatformTypeResourceID(model.PlatformAssignmentRule)

	// Locked out behind the API's back: the rules are off, and a denial covers the policy.
	if _, err := e.pool.Exec(ctx, `UPDATE assignment_rule SET enabled = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO assignment_rule (type_resource_id, access, subject, predicate, enabled, effect)
	        VALUES ($1, ARRAY['*'], 'admin', '[]'::jsonb, true, 'deny')`, typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := redisx.BumpVersion(ctx, e.app.Redis); err != nil {
		t.Fatal(err)
	}
	status, _ := e.do("GET", e.admin.URL+"/assignment_rule", nil)
	if status != http.StatusForbidden {
		t.Fatalf("locked out: want 403, got %d", status)
	}

	report, err := db.RestoreAdminAccess(ctx, e.pool, e.cfg, []string{"rescuer"})
	if err != nil {
		t.Fatal(err)
	}
	if report.DisabledDenials != 1 || report.Members != 2 {
		t.Fatalf("report: %+v", report)
	}
	if _, err := redisx.BumpVersion(ctx, e.app.Redis); err != nil {
		t.Fatal(err)
	}
	if status, raw := e.do("GET", e.admin.URL+"/assignment_rule", nil); status != http.StatusOK {
		t.Fatalf("the admin is back: %d %s", status, raw)
	}
	rescuer := e.token(jwt.MapClaims{"preferred_username": "rescuer", "sub": "rescuer"})
	if status, raw := e.do("GET", e.admin.URL+"/assignment_rule", nil, "Authorization", "Bearer "+rescuer); status != http.StatusOK {
		t.Fatalf("the named user is an administrator: %d %s", status, raw)
	}
	// Running it again changes nothing.
	again, err := db.RestoreAdminAccess(ctx, e.pool, e.cfg, []string{"rescuer"})
	if err != nil || again.DisabledDenials != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
}

func TestPolicyScenariosAreStoredWithTheService(t *testing.T) {
	e := setup(t)
	svc := e.newService("scen")
	other := e.newService("other")
	body := func(name string) map[string]any {
		return map[string]any{
			"name": name, "subject": "alice",
			"checks": []any{map[string]any{"type": "doc", "action": "read", "expect": "allow"}},
		}
	}
	first := e.must("POST", "/service/"+svc+"/policy/scenarios", body("alice reads"))
	id := first["id"].(string)
	if first["case"].(map[string]any)["name"] != "alice reads" {
		t.Fatalf("created: %+v", first)
	}
	status, raw := e.do("POST", e.admin.URL+"/service/"+svc+"/policy/scenarios", body("alice reads"))
	requireErrorCode(t, status, raw, http.StatusConflict, "policy_scenario_name_taken")

	e.must("PUT", "/service/"+svc+"/policy/scenarios/"+id, body("alice reads docs"))
	status, raw = e.do("GET", e.admin.URL+"/service/"+svc+"/policy/scenarios", nil)
	list := decodeList(t, raw)
	if status != http.StatusOK || len(list) != 1 || list[0]["case"].(map[string]any)["name"] != "alice reads docs" {
		t.Fatalf("list: %d %s", status, raw)
	}
	// Another service does not see it and cannot change it.
	if _, raw := e.do("GET", e.admin.URL+"/service/"+other+"/policy/scenarios", nil); strings.Contains(string(raw), "alice") {
		t.Fatalf("scenarios leaked to another service: %s", raw)
	}
	status, raw = e.do("DELETE", e.admin.URL+"/service/"+other+"/policy/scenarios/"+id, nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "policy_scenario_not_found")
	e.must("DELETE", "/service/"+svc+"/policy/scenarios/"+id, nil)
}

// waitFor polls until the condition holds (the registry is written in the background).
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// salesWorld is the hierarchy world with a group of everyone from sales, and a person of it seen by the check API.
func salesWorld(t *testing.T) (w *world, group string) {
	w = newWorld(t)
	group = w.must("POST", "/group", map[string]any{"name": "sales"})["id"].(string)
	w.must("POST", "/group/"+group+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	dave := w.token(jwt.MapClaims{"preferred_username": "dave", "name": "Dave Sales", "sub": "dave", "department": "sales", "secret_claim": "x"})
	if status, _ := w.env.permission(w.org, dave); status != http.StatusOK {
		t.Fatalf("permission as dave: %d", status)
	}
	waitFor(t, "dave in the registry", func() bool {
		status, raw := w.do("GET", w.admin.URL+"/subject?search=dave", nil)
		return status == http.StatusOK && decodeMap(t, raw)["total"] == float64(1)
	})
	return w, group
}

func TestKnownSubjectsAreNotedAndForgotten(t *testing.T) {
	w, group := salesWorld(t)

	detail := w.must("GET", "/subject/dave", nil)
	if detail["name"] != "Dave Sales" {
		t.Fatalf("name: %+v", detail)
	}
	claims := detail["claims"].(map[string]any)
	if claims["department"] != "sales" {
		t.Fatalf("the claim the policy reads is kept: %+v", claims)
	}
	if _, kept := claims["secret_claim"]; kept {
		t.Fatalf("a claim no rule reads must not be kept: %+v", claims)
	}
	groups := detail["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["id"] != group {
		t.Fatalf("groups of dave: %+v", groups)
	}

	list := w.must("GET", "/subject", nil)
	if list["enabled"] != true || list["total"].(float64) < 1 {
		t.Fatalf("list: %+v", list)
	}

	w.must("DELETE", "/subject/dave", nil)
	status, raw := w.do("GET", w.admin.URL+"/subject/dave", nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "subject_not_found")

	// Somebody without the right to manage memberships does not see the people.
	nobody := w.token(jwt.MapClaims{"preferred_username": "nobody", "sub": "nobody"})
	if status, _ := w.do("GET", w.admin.URL+"/subject", nil, "Authorization", "Bearer "+nobody); status != http.StatusForbidden {
		t.Fatalf("the registry is for administrators: %d", status)
	}
}

func TestWhoHasAccessListsTheRulesAndThePeople(t *testing.T) {
	w, group := salesWorld(t)
	alice := w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "subject": "alice", "access": []string{"read"}})
	everyone := w.grant(map[string]any{"type_resource_id": w.branch, "member_of_group_id": group, "access": []string{"write"}})
	denial := w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B2", "subject": "mallory",
		"access": []string{"read"}, "effect": "deny"})
	// Not about B2: a rule for another division.
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D2", "subject": "zed", "access": []string{"read"}})

	look := func(query string) map[string]any {
		status, raw := w.do("GET", w.admin.URL+w.typePath("branch")+"/access"+query, nil)
		if status != http.StatusOK {
			t.Fatalf("who has access%s: %d %s", query, status, raw)
		}
		return decodeMap(t, raw)
	}

	res := look("?resource_id=B2")
	entries := res["entries"].([]any)
	byRule := map[string]map[string]any{}
	var order []string
	for _, x := range entries {
		entry := x.(map[string]any)
		id := entry["rule"].(map[string]any)["rule_id"].(string)
		byRule[id] = entry
		order = append(order, id)
	}
	if len(entries) != 3 || byRule[alice] == nil || byRule[everyone] == nil || byRule[denial] == nil {
		t.Fatalf("the rules covering B2: %s", mustJSON(res))
	}
	if order[0] != denial {
		t.Fatalf("denials come first: %v", order)
	}
	if byRule[alice]["via"] != "ancestor" || byRule[everyone]["via"] != "type" || byRule[denial]["via"] != "resource" {
		t.Fatalf("how the rules reach B2: alice=%v group=%v deny=%v", byRule[alice]["via"], byRule[everyone]["via"], byRule[denial]["via"])
	}
	who := byRule[everyone]["who"].(map[string]any)
	if who["kind"] != "group" || who["group"].(map[string]any)["name"] != "sales" {
		t.Fatalf("who the group rule applies to: %+v", who)
	}
	// dave is from sales, so the group rule reaches him; the registry knows him.
	if byRule[everyone]["people_total"] != float64(1) || byRule[everyone]["people"].([]any)[0].(map[string]any)["subject"] != "dave" {
		t.Fatalf("people of the group rule: %+v", byRule[everyone])
	}
	if res["people"].(map[string]any)["registry"].(float64) < 1 {
		t.Fatalf("registry size: %+v", res["people"])
	}

	// The type as a whole: only the rules anchored on the type itself.
	whole := look("")
	if got := whole["entries"].([]any); len(got) != 1 || got[0].(map[string]any)["rule"].(map[string]any)["rule_id"] != everyone {
		t.Fatalf("rules on the whole type: %s", mustJSON(whole))
	}

	status, raw := w.do("GET", w.admin.URL+w.typePath("branch")+"/access?resource_id=NOPE", nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "resource_not_found")

	// A rule that finds its resource by a claim is listed apart.
	dynamic := w.grant(map[string]any{"type_resource_id": w.branch, "subject": "walker", "access": []string{"read"},
		"resource_from_claim": map[string]any{"claim": "branch_name", "lookup_type_resource_id": w.branch}})
	res = look("?resource_id=B1")
	dyn := res["dynamic"].([]any)
	if len(dyn) != 1 || dyn[0].(map[string]any)["rule"].(map[string]any)["rule_id"] != dynamic {
		t.Fatalf("dynamic rules: %s", mustJSON(res))
	}
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestPreviewGrantSaysWhatARuleWouldDo(t *testing.T) {
	w, group := salesWorld(t)
	preview := func(rule map[string]any) map[string]any {
		return w.must("POST", "/assignment_rule/preview", rule)
	}

	// Nothing is created.
	before := len(decodeList(t, mustBody(w.do("GET", w.admin.URL+"/assignment_rule", nil))))

	// A rule on division D1 covers D1 and everything under it, in every service: 1 + 2 rrs + 3 branches + 1 item.
	res := preview(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "subject": "alice", "access": []string{"read"}})
	if res["resources"] != float64(7) {
		t.Fatalf("D1 covers 7 resources: %s", mustJSON(res))
	}
	types := map[string]float64{}
	for _, x := range res["types"].([]any) {
		entry := x.(map[string]any)
		types[entry["type"].(string)] = entry["count"].(float64)
	}
	if types["division"] != 1 || types["rrs"] != 2 || types["branch"] != 3 || types["item"] != 1 {
		t.Fatalf("by type: %v", types)
	}

	// A denial for the same person inside it is reported, and so is a rule that already gives the same.
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B2", "subject": "alice", "access": []string{"read"}, "effect": "deny"})
	existing := w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "subject": "alice", "access": []string{"read", "write"}})
	res = preview(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "subject": "alice", "access": []string{"read"}})
	conflicts := res["conflicts"].([]any)
	if len(conflicts) != 1 || conflicts[0].(map[string]any)["position"] != "inside" || conflicts[0].(map[string]any)["kind"] != "deny" {
		t.Fatalf("conflicts: %s", mustJSON(res))
	}
	if dup := res["already_granted"].([]any); len(dup) != 1 || dup[0].(map[string]any)["rule_id"] != existing {
		t.Fatalf("already granted: %s", mustJSON(res))
	}
	// A denial for somebody else is no conflict.
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "bob", "access": []string{"read"}, "effect": "deny"})
	if res = preview(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "subject": "alice", "access": []string{"read"}}); len(res["conflicts"].([]any)) != 1 {
		t.Fatalf("a denial of another person is no conflict: %s", mustJSON(res))
	}

	// A group rule on a whole type: every resource of it, and the known people of the group.
	res = preview(map[string]any{"type_resource_id": w.branch, "member_of_group_id": group, "access": []string{"read"}})
	if res["resources"] != float64(6) { // 4 branches + 2 items under them
		t.Fatalf("whole type branch: %s", mustJSON(res))
	}
	people := res["people"].(map[string]any)
	if people["total"] != float64(1) || people["sample"].([]any)[0].(map[string]any)["subject"] != "dave" {
		t.Fatalf("people: %+v", people)
	}

	// A permission without resources has nothing to count.
	feature := w.typeResource(w.org, "feature", "")
	res = preview(map[string]any{"type_resource_id": feature, "subject": "alice", "access": []string{"use"}})
	if res["capability"] != true || res["resources"] != float64(0) {
		t.Fatalf("capability: %s", mustJSON(res))
	}

	// A rule that finds its resource by a claim cannot be counted.
	res = preview(map[string]any{"type_resource_id": w.branch, "subject": "walker", "access": []string{"read"},
		"resource_from_claim": map[string]any{"claim": "branch_name", "lookup_type_resource_id": w.branch}})
	if res["dynamic"] != true {
		t.Fatalf("dynamic: %s", mustJSON(res))
	}

	// A bad body is refused as a create would.
	status, _ := w.do("POST", w.admin.URL+"/assignment_rule/preview", map[string]any{"type_resource_id": uuidNil(), "access": []string{"read"}, "subject": "x"})
	if status < 400 {
		t.Fatalf("a rule on no type should be refused, got %d", status)
	}

	after := len(decodeList(t, mustBody(w.do("GET", w.admin.URL+"/assignment_rule", nil))))
	if after != before+3 { // the three grants of the test, none from the previews
		t.Fatalf("previews must not create rules: %d -> %d", before, after)
	}
}

func mustBody(status int, raw []byte) []byte {
	if status != http.StatusOK {
		panic(fmt.Sprintf("status %d: %s", status, raw))
	}
	return raw
}

func uuidNil() string { return "00000000-0000-0000-0000-000000000000" }

func TestNothingIsSaidAboutPeopleWhileTheRegistryIsOff(t *testing.T) {
	w, group := salesWorld(t) // dave is in the registry, which is on while the world is built
	w.cfg.Subjects.Enabled = false
	w.grant(map[string]any{"type_resource_id": w.branch, "member_of_group_id": group, "access": []string{"read"}})

	res := w.must("POST", "/assignment_rule/preview", map[string]any{
		"type_resource_id": w.branch, "member_of_group_id": group, "access": []string{"read"}})
	people := res["people"].(map[string]any)
	if people["enabled"] != false || people["registry"] != float64(0) || people["total"] != float64(0) {
		t.Fatalf("preview people with the registry off: %+v", people)
	}

	status, raw := w.do("GET", w.admin.URL+w.typePath("branch")+"/access?resource_id=B1", nil)
	if status != http.StatusOK {
		t.Fatalf("who has access: %d %s", status, raw)
	}
	look := decodeMap(t, raw)
	if look["people"].(map[string]any)["enabled"] != false {
		t.Fatalf("people with the registry off: %s", raw)
	}
	for _, e := range look["entries"].([]any) {
		if e.(map[string]any)["people_total"] != float64(0) {
			t.Fatalf("an entry names people with the registry off: %s", raw)
		}
	}
	if list := w.must("GET", "/subject", nil); list["enabled"] != false {
		t.Fatalf("the list says the registry is on: %+v", list)
	}
}
