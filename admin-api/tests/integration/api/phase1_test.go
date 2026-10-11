//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/logx"
)

// call is e.do for a test that reads the response headers too.
func (e *env) call(method, url string, body any, headers ...string) (int, http.Header, []byte) {
	e.t.Helper()
	var reader io.Reader = bytes.NewReader(nil)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.admin != nil && strings.HasPrefix(url, e.admin.URL) {
		req.Header.Set("Authorization", "Bearer "+e.adminToken)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, resp.Header, raw
}

// newService registers a client service whose URLs point at the mock IdP.
func (e *env) newService(name string) string {
	e.t.Helper()
	m := e.mock.URL
	return e.must("POST", "/service/", map[string]any{
		"name": name, "url": m + "/app", "icon": m + "/icon.png",
		"authorization_url": m + "/realm/auth", "token_url": m + "/realm/token",
		"jwks_url": m + "/jwks", "issuer": m + "/realm", "slug": name, "client_id": name, "client_secret": "s",
	})["id"].(string)
}

// changes reads the change log (the query string starts with "?" or is empty).
func (e *env) changes(query string) []map[string]any {
	e.t.Helper()
	status, raw := e.do(http.MethodGet, e.admin.URL+"/changes"+query, nil)
	if status != http.StatusOK {
		e.t.Fatalf("list changes: %d %s", status, raw)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("decode changes: %v (%s)", err, raw)
	}
	return out
}

// lockedBuffer collects the log lines of a test: the server may still log in the background.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRuleNameAndDescriptionAreStored(t *testing.T) {
	e := setup(t)
	svc := e.newService("features")
	flag := e.must("POST", "/service/"+svc+"/type_resource/", map[string]any{"name": "flag"})["id"].(string)

	rule := e.must("POST", "/assignment_rule/", map[string]any{
		"type_resource_id": flag, "access": []string{"read"}, "subject": "alice",
		"name": "  Alice reads the flag  ", "description": "for the pilot",
	})
	if rule["name"] != "Alice reads the flag" || rule["description"] != "for the pilot" {
		t.Fatalf("the name is trimmed and kept: %v", rule)
	}

	// an empty name and a null description are stored as null
	updated := e.must("PATCH", "/assignment_rule/"+rule["id"].(string), map[string]any{
		"type_resource_id": flag, "access": []string{"read"}, "subject": "alice", "name": "", "description": nil,
	})
	if updated["name"] != nil || updated["description"] != nil {
		t.Fatalf("an empty name and a null description are null: %v", updated)
	}

	status, raw := e.do(http.MethodGet, e.admin.URL+"/assignment_rule/", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), `"subject":"alice"`) {
		t.Fatalf("list rules: %d %s", status, raw)
	}
}

func TestChangeLogRecordsEveryWriteWithItsActorAndMasksSecrets(t *testing.T) {
	e := setup(t)
	group := e.must("POST", "/group/", map[string]any{"name": "auditors"})["id"].(string)
	e.must("PATCH", "/group/"+group, map[string]any{"name": "auditors-2"})
	e.must("DELETE", "/group/"+group, nil)

	svc := e.newService("ledger")
	branch := e.must("POST", "/service/"+svc+"/type_resource/", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+svc+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config": map[string]any{
			"kind": "http", "url": e.mock.URL + "/sync/branch",
			"headers": map[string]string{"Authorization": "Bearer topsecret"},
		},
		"probe_resource_id": "b1",
	})

	changes := e.changes("")
	seen := map[string]bool{}
	var update map[string]any
	for _, c := range changes {
		if c["actor"] != "admin" {
			t.Fatalf("every change names the caller: %v", c)
		}
		key := c["entity"].(string) + "/" + c["action"].(string)
		seen[key] = true
		if key == "group/update" {
			update = c
		}
	}
	for _, want := range []string{"group/create", "group/update", "group/delete", "synchronizer/create", "type_resource/create"} {
		if !seen[want] {
			t.Fatalf("no change-log record for %s: %v", want, changes)
		}
	}
	before := update["before"].(map[string]any)
	after := update["after"].(map[string]any)
	if before["name"] != "auditors" || after["name"] != "auditors-2" {
		t.Fatalf("an update keeps the entity before and after: %v", update)
	}

	raw, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "topsecret") {
		t.Fatalf("a secret must not reach the change log: %s", raw)
	}
	if !strings.Contains(string(raw), "********") {
		t.Fatalf("the change log keeps the secret masked: %s", raw)
	}

	// the filters narrow the list
	if got := e.changes("?entity=group"); len(got) != 3 {
		t.Fatalf("filter by entity: %d records", len(got))
	}
	if got := e.changes("?after=" + jsonNumber(changes[1]["id"])); len(got) != len(changes)-2 {
		t.Fatalf("records after a position: %d of %d", len(got), len(changes))
	}
}

// jsonNumber renders a JSON number decoded as float64 back to its integer text.
func jsonNumber(v any) string {
	f, _ := v.(float64)
	raw, _ := json.Marshal(int64(f))
	return string(raw)
}

func TestWritesReturnARevisionThatReadsCanRequire(t *testing.T) {
	e := setup(t)
	svc := e.newService("pilot")
	flag := e.must("POST", "/service/"+svc+"/type_resource/", map[string]any{"name": "flag"})["id"].(string)

	status, header, raw := e.call(http.MethodPost, e.admin.URL+"/assignment_rule/", map[string]any{
		"type_resource_id": flag, "access": []string{"read"}, "subject": "alice",
	})
	if status != http.StatusOK {
		t.Fatalf("create rule: %d %s", status, raw)
	}
	revision := header.Get("X-Authz-Revision")
	if revision == "" {
		t.Fatal("a write returns the revision it produced")
	}

	// a read that asks for the revision sees the write
	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "u1"})
	status, _, raw = e.call(http.MethodGet, e.check.URL+"/service/"+svc+"/permission?at_least_as_fresh="+revision, nil,
		"Authorization", "Bearer "+alice)
	if status != http.StatusOK {
		t.Fatalf("permission at the revision: %d %s", status, raw)
	}
	var perm map[string]any
	if err := json.Unmarshal(raw, &perm); err != nil {
		t.Fatal(err)
	}
	expectJSON(t, "alice sees the write", perm, `{"flag":{"type":"not_sync","access":["read"]}}`)

	// a revision the cache has not reached is answered from the data, not refused
	status, _, raw = e.call(http.MethodGet, e.check.URL+"/service/"+svc+"/permission?at_least_as_fresh=999999999", nil,
		"Authorization", "Bearer "+alice)
	if status != http.StatusOK {
		t.Fatalf("a future revision: %d %s", status, raw)
	}

	// a read does not return a revision
	status, header, _ = e.call(http.MethodGet, e.admin.URL+"/assignment_rule/", nil)
	if status != http.StatusOK || header.Get("X-Authz-Revision") != "" {
		t.Fatalf("a read carries no revision: %d %q", status, header.Get("X-Authz-Revision"))
	}
}

func TestSimulationAnswersForClaimsWithoutAToken(t *testing.T) {
	e := setup(t)
	svc := e.newService("ledger2")
	branch := e.must("POST", "/service/"+svc+"/type_resource/", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+svc+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config": map[string]any{"kind": "http", "url": e.mock.URL + "/sync/branch"}, "probe_resource_id": "b1",
	})
	e.must("POST", "/assignment_rule/", map[string]any{
		"type_resource_id": branch, "access": []string{"read"},
		"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}},
	})

	check := func(claims map[string]any) map[string]any {
		t.Helper()
		status, raw := e.do(http.MethodPost, e.admin.URL+"/service/"+svc+"/simulate/check", map[string]any{
			"subject": "dave", "claims": claims, "explain": true,
			"checks": []map[string]any{{"type": "branch", "resource_id": "b1", "action": "read"}},
		})
		if status != http.StatusOK {
			t.Fatalf("simulate check: %d %s", status, raw)
		}
		var out struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out.Results[0]
	}

	sales := check(map[string]any{"department": "sales"})
	if sales["allowed"] != true || sales["explanation"] == nil {
		t.Fatalf("a sales claim is allowed, with the explanation: %v", sales)
	}
	others := check(map[string]any{"department": "hr"})
	if others["allowed"] != false {
		t.Fatalf("another department is denied: %v", others)
	}

	status, raw := e.do(http.MethodPost, e.admin.URL+"/service/"+svc+"/simulate/permission", map[string]any{
		"subject": "dave", "claims": map[string]any{"department": "sales"}, "type": "branch", "limit": 2,
	})
	if status != http.StatusOK {
		t.Fatalf("simulate permission: %d %s", status, raw)
	}
	var page map[string]map[string]any
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if n := len(page["branch"]["resources"].([]any)); n != 2 || page["branch"]["next_after"] != "b2" {
		t.Fatalf("the first page of simulated permissions: %v", page)
	}

	// a stranger may not simulate anybody
	stranger := e.token(jwt.MapClaims{"preferred_username": "carol", "sub": "c1"})
	status, raw = e.do(http.MethodPost, e.admin.URL+"/service/"+svc+"/simulate/check", map[string]any{
		"subject": "dave", "checks": []map[string]any{{"type": "branch", "action": "read"}},
	}, "Authorization", "Bearer "+stranger)
	if status != http.StatusForbidden {
		t.Fatalf("simulation needs the rights on the service: %d %s", status, raw)
	}
}

func TestPermissionPagesWalkTheResourcesInIDOrder(t *testing.T) {
	e := setup(t)
	svc := e.newService("ledger3")
	branch := e.must("POST", "/service/"+svc+"/type_resource/", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+svc+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config": map[string]any{"kind": "http", "url": e.mock.URL + "/sync/branch"}, "probe_resource_id": "b1",
	})
	e.must("POST", "/assignment_rule/", map[string]any{"type_resource_id": branch, "access": []string{"read"}, "subject": "alice"})
	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "u1"})

	page := func(query string) map[string]any {
		t.Helper()
		status, _, raw := e.call(http.MethodGet, e.check.URL+"/service/"+svc+"/permission"+query, nil,
			"Authorization", "Bearer "+alice)
		if status != http.StatusOK {
			t.Fatalf("permission %s: %d %s", query, status, raw)
		}
		var out map[string]map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out["branch"]
	}
	ids := func(p map[string]any) string {
		var out []string
		for _, r := range p["resources"].([]any) {
			out = append(out, r.(map[string]any)["id"].(string))
		}
		return strings.Join(out, ",")
	}

	first := page("?type=branch&limit=2")
	if ids(first) != "b1,b2" || first["next_after"] != "b2" {
		t.Fatalf("first page: %v", first)
	}
	second := page("?type=branch&limit=2&after=b2")
	if ids(second) != "b3" || second["next_after"] != nil {
		t.Fatalf("the last page names no next page: %v", second)
	}

	status, _, raw := e.call(http.MethodGet, e.check.URL+"/service/"+svc+"/permission?limit=2", nil,
		"Authorization", "Bearer "+alice)
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "permission_page_needs_type")
}

func TestLogSettingsAreReadWrittenAndAppliedToTheLogger(t *testing.T) {
	e := setup(t)

	var defaults map[string]any
	status, raw := e.do(http.MethodGet, e.admin.URL+"/settings/logs", nil)
	if status != http.StatusOK {
		t.Fatalf("read log settings: %d %s", status, raw)
	}
	if err := json.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults["format"] != "json" || defaults["subject"] != "hash" || defaults["sample_allowed"] != 0.1 {
		t.Fatalf("the defaults: %v", defaults)
	}

	// a field name the logger cannot use is refused with a stable code
	status, raw = e.do(http.MethodPut, e.admin.URL+"/settings/logs", map[string]any{
		"format": "logfmt", "level": "info", "subject": "plain", "sample_allowed": 1,
		"fields": []map[string]any{{"name": "bad name"}},
		"events": map[string]bool{"access": true, "decision": true, "sync": true},
	})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "log_setting_invalid")

	// the logger of this process takes the new settings at once
	var out lockedBuffer
	sw, err := logx.New(&out, logx.DefaultOptions(slog.LevelInfo))
	if err != nil {
		t.Fatal(err)
	}
	e.app.Logs = sw
	status, raw = e.do(http.MethodPut, e.admin.URL+"/settings/logs", map[string]any{
		"format": "logfmt", "level": "info", "subject": "plain", "sample_allowed": 1,
		"fields": []map[string]any{{"name": "subject", "as": "user"}},
		"events": map[string]bool{"access": true, "decision": true, "sync": true},
	})
	if status != http.StatusOK {
		t.Fatalf("replace log settings: %d %s", status, raw)
	}
	slog.New(sw).Info("request", "subject", "alice", "status", 200)
	line := out.String()
	if !strings.Contains(line, "msg=request") || !strings.Contains(line, "user=alice") || strings.Contains(line, "subject=") {
		t.Fatalf("the new settings are applied: %q", line)
	}

	// the replacement is a change of the log, and the settings read back as they were stored
	if got := e.changes("?entity=log_settings"); len(got) != 1 || got[0]["actor"] != "admin" {
		t.Fatalf("a settings change is recorded: %v", got)
	}
	status, raw = e.do(http.MethodGet, e.admin.URL+"/settings/logs", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), `"format":"logfmt"`) {
		t.Fatalf("read back: %d %s", status, raw)
	}

	// reading and replacing the settings is for the platform administrators
	carol := e.token(jwt.MapClaims{"preferred_username": "carol", "sub": "c1"})
	status, raw = e.do(http.MethodGet, e.admin.URL+"/settings/logs", nil, "Authorization", "Bearer "+carol)
	if status != http.StatusForbidden {
		t.Fatalf("log settings are for the administrators: %d %s", status, raw)
	}
}

func TestDecisionEventsCarryTheRequestIDAndSampleAllowedAnswers(t *testing.T) {
	e := setup(t)
	svc := e.newService("pilot2")
	flag := e.must("POST", "/service/"+svc+"/type_resource/", map[string]any{"name": "flag"})["id"].(string)
	e.must("POST", "/assignment_rule/", map[string]any{"type_resource_id": flag, "access": []string{"read"}, "subject": "alice"})

	// the process logs into a buffer for this test: allowed answers sampled out, denials always written
	var out lockedBuffer
	opts := logx.DefaultOptions(slog.LevelInfo)
	opts.SampleAllowed = 0
	sw, err := logx.New(&out, opts)
	if err != nil {
		t.Fatal(err)
	}
	e.app.Logs = sw
	previous := slog.Default()
	slog.SetDefault(slog.New(sw))
	t.Cleanup(func() { slog.SetDefault(previous) })

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "u1"})
	status, _, raw := e.call(http.MethodPost, e.check.URL+"/service/"+svc+"/check", map[string]any{
		"checks": []map[string]any{{"type": "flag", "action": "read"}, {"type": "flag", "action": "write"}},
	}, "Authorization", "Bearer "+alice, "X-Request-ID", "rid-7")
	if status != http.StatusOK {
		t.Fatalf("check: %d %s", status, raw)
	}

	var decisions []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.Contains(line, `"msg":"decision"`) {
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("decision line: %v", err)
			}
			decisions = append(decisions, rec)
		}
	}
	if len(decisions) != 1 {
		t.Fatalf("only the denial is written (allowed answers are sampled out): %v", decisions)
	}
	if decisions[0]["request_id"] != "rid-7" || decisions[0]["allowed"] != false || decisions[0]["action"] != "write" {
		t.Fatalf("the denial names its request: %v", decisions[0])
	}
	if decisions[0]["subject"] == "alice" {
		t.Fatal("the subject is hashed by default")
	}
}
