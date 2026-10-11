//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// Every request gets an id (the caller's own X-Request-ID when it is sane) that comes back in the response and
// is written to the access log together with the operation and the caller — the caller only once its token is
// verified, so a forged token never shows up.
func TestAccessLogCarriesRequestIDOperationAndCaller(t *testing.T) {
	e := setup(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	get := func(headers ...string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, e.admin.URL+"/service", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+e.adminToken)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if got := get("X-Request-ID", "req-42").Header.Get("X-Request-ID"); got != "req-42" {
		t.Fatalf("the caller's request id is not kept: %q", got)
	}
	if got := get("X-Request-ID", "not valid!").Header.Get("X-Request-ID"); got == "" || strings.Contains(got, "!") {
		t.Fatalf("an insane request id must be replaced: %q", got)
	}
	if got := get().Header.Get("X-Request-ID"); len(got) != 36 {
		t.Fatalf("a request without an id gets a generated one: %q", got)
	}

	var line map[string]any
	found := false
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(raw), &entry) == nil && entry["msg"] == "request" && entry["request_id"] == "req-42" {
			line, found = entry, true
		}
	}
	if !found {
		t.Fatalf("no access log line for req-42 in:\n%s", logs.String())
	}
	if line["operation"] != "list_services" || line["user"] != "admin" || line["status"] != float64(http.StatusOK) {
		t.Fatalf("access log line: %v", line)
	}

	// A forged token is refused and never named in the log.
	forged := e.token(jwt.MapClaims{"preferred_username": "mallory"})
	forged = forged[:len(forged)-4] + "AAAA"
	logs.Reset()
	req, _ := http.NewRequest(http.MethodGet, e.admin.URL+"/service", nil)
	req.Header.Set("Authorization", "Bearer "+forged)
	req.Header.Set("X-Request-ID", "req-43")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || strings.Contains(logs.String(), "mallory") {
		t.Fatalf("forged token: %d, log %s", resp.StatusCode, logs.String())
	}
}
