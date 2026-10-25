//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// call is e.do for a test that reads the response headers too.
func (e *env) call(method, url string, body any, headers ...string) (int, http.Header, []byte) {
	e.t.Helper()
	return e.doWithHeader(method, url, body, headers...)
}

// newService registers a client service whose URLs point at the mock IdP.
func (e *env) newService(name string) string {
	e.t.Helper()
	m := e.mock.URL
	return e.must("POST", "/service/", map[string]any{
		"name": name, "url": m + "/app", "icon": m + "/icon.png",
		"slug": name,
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

	// a read that asks for the revision sees the write: alice may read the flag, a type without resources, as a whole
	rev, err := strconv.ParseInt(revision, 10, 64)
	if err != nil {
		t.Fatalf("the revision of a write is a number: %q", revision)
	}
	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "u1"})
	status, raw = e.ask(authzenreq.Evaluation("pilot", "alice", authzenreq.Whole("flag"), "read", authzenreq.AtLeast(rev)), alice)
	if allowed, err := authzenreq.Allowed(status, raw); err != nil || !allowed {
		t.Fatalf("evaluation at the revision: %d %s", status, raw)
	}
	status, raw = e.ask(authzenreq.SearchAction("pilot", "alice", authzenreq.Whole("flag"), authzenreq.AtLeast(rev)), alice)
	levels, _, err := authzenreq.Actions(status, raw)
	if err != nil {
		t.Fatalf("actions at the revision: %v", err)
	}
	expectJSON(t, "alice sees the write", levels, `["read"]`)
	if status, ids := e.search("pilot", alice, "flag", "read", authzenreq.AtLeast(rev)); status != http.StatusOK || len(ids) != 0 {
		t.Fatalf("a type without resources lists none: %d %v", status, ids)
	}

	// a revision the cache has not reached is answered from the data, not refused
	status, raw = e.ask(authzenreq.Evaluation("pilot", "alice", authzenreq.Whole("flag"), "read", authzenreq.AtLeast(999999999)), alice)
	if status != http.StatusOK {
		t.Fatalf("a future revision: %d %s", status, raw)
	}

	// a read does not return a revision
	status, header, _ = e.call(http.MethodGet, e.admin.URL+"/assignment_rule/", nil)
	if status != http.StatusOK || header.Get("X-Authz-Revision") != "" {
		t.Fatalf("a read carries no revision: %d %q", status, header.Get("X-Authz-Revision"))
	}
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
	req := authzenreq.Evaluations("pilot2", "alice", []authzenreq.Question{
		{Resource: authzenreq.Whole("flag"), Action: "read"}, {Resource: authzenreq.Whole("flag"), Action: "write"}})
	status, raw := e.ask(req, alice, "X-Request-ID", "rid-7")
	if _, err := authzenreq.Decisions(status, raw); err != nil {
		t.Fatalf("evaluations: %v", err)
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
