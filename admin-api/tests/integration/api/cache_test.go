//go:build integration

package api_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// An answer is gzip-compressed only for a client that accepts gzip, and says so in Vary.
func TestAnswersAreGzippedOnlyForClientsThatAcceptIt(t *testing.T) {
	e := setup(t)
	org := e.must("POST", "/service", servicePayload(e, "gzip-org"))["id"].(string)
	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})

	// Without a transparent decompression, the test sees the bytes the server sends.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	fetch := func(accept string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, e.check.URL+"/service/"+org+"/permission", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+alice)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, raw
	}

	resp, raw := fetch("gzip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("permission: %d %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("a client that accepts gzip got a plain answer: %v", resp.Header)
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("a compressed answer must say it varies by Accept-Encoding: %v", resp.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the gzip body does not open: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("the gzip body does not decompress: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatalf("the decompressed answer is not JSON: %v (%s)", err, plain)
	}

	resp, raw = fetch("")
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("gzip sent to a client that did not ask for it: %q", enc)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the plain answer is not JSON: %v (%s)", err, raw)
	}

	// gzip;q=0 refuses gzip: the answer must stay plain.
	resp, _ = fetch("gzip;q=0")
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("gzip sent to a client that refuses it with q=0: %q", enc)
	}
}

// A minimize_latency answer does not wait for a source that was synchronized before: the refresh runs in the
// background. An answer may lag the source by one refresh; once the refresh has run, the answers show what the
// source gained — the cached answers are dropped by the change of the resource data, not only by the time.
func TestMinimizeLatencyRefreshesAChangedSourceInTheBackground(t *testing.T) {
	e := setup(t)
	var mu sync.Mutex
	list := []map[string]any{{"id": "b1", "name": "Branch1"}, {"id": "b2", "name": "Branch2"}}
	var listCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /src/branch", func(w http.ResponseWriter, _ *http.Request) {
		listCalls.Add(1)
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("GET /src/branch/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "Branch"})
	})
	source := newTestServer(t, mux)

	org := e.must("POST", "/service", servicePayload(e, "refresh-org"))["id"].(string)
	branch := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	// cache_ttl 0: every synchronization asks the source itself, so a change of the source is seen by it.
	e.must("POST", "/service/"+org+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config":            map[string]any{"kind": "http", "url": source.URL + "/src/branch", "cache_ttl": 0},
		"probe_resource_id": "b1",
	})
	e.must("POST", "/assignment_rule", map[string]any{"type_resource_id": branch, "access": []string{"read"}, "subject": "alice"})

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})
	ask := func() string {
		t.Helper()
		status, raw := e.do(http.MethodGet, e.check.URL+"/service/"+org+"/permission", nil, "Authorization", "Bearer "+alice)
		if status != http.StatusOK {
			t.Fatalf("permission: %d %s", status, raw)
		}
		return string(raw)
	}

	// The first synchronization has nothing to answer from: it is done in the request.
	if got := ask(); !strings.Contains(got, `"b2"`) {
		t.Fatalf("the first answer misses the source's resources: %s", got)
	}
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("the first synchronization asks the source %d times, want 1", got)
	}

	mu.Lock()
	list = append(list, map[string]any{"id": "b3", "name": "Branch3"})
	mu.Unlock()
	// An admin write ends the throttle of the synchronization, so the next answer finds the source due.
	e.must("POST", "/service", servicePayload(e, "refresh-bump"))

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(ask(), `"b3"`) {
		if time.Now().After(deadline) {
			t.Fatalf("the background refresh never reached the answers (the source was asked %d times)", listCalls.Load())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := listCalls.Load(); got < 2 {
		t.Fatalf("the refresh did not ask the source: %d calls", got)
	}
}

// The answers of POST /check are cached per subject and question, and a change of the rules is seen at once.
func TestCheckAnswersFollowARuleChangeAtOnce(t *testing.T) {
	e := setup(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /src/branch", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "b1", "name": "Branch1"}, {"id": "b2", "name": "Branch2"}})
	})
	mux.HandleFunc("GET /src/branch/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "Branch"})
	})
	source := newTestServer(t, mux)

	org := e.must("POST", "/service", servicePayload(e, "check-org"))["id"].(string)
	branch := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+org+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config":            map[string]any{"kind": "http", "url": source.URL + "/src/branch", "cache_ttl": 300},
		"probe_resource_id": "b1",
	})
	rule := e.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": branch, "resource_id": "b1", "access": []string{"read"}, "subject": "alice",
	})["id"].(string)

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})
	question := map[string]any{"checks": []map[string]any{{"type": "branch", "resource_id": "b1", "action": "read"}}}
	allowed := func() bool {
		t.Helper()
		status, raw := e.do(http.MethodPost, e.check.URL+"/service/"+org+"/check", question, "Authorization", "Bearer "+alice)
		if status != http.StatusOK {
			t.Fatalf("check: %d %s", status, raw)
		}
		var out struct {
			Results []struct {
				Allowed bool `json:"allowed"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Results) != 1 {
			t.Fatalf("check answer: %v (%s)", err, raw)
		}
		return out.Results[0].Allowed
	}

	if !allowed() {
		t.Fatal("the rule must grant read on b1")
	}
	if !allowed() {
		t.Fatal("the same question must get the same answer")
	}
	e.must("DELETE", "/assignment_rule/"+rule, nil)
	if allowed() {
		t.Fatal("the rule is deleted, but the answer still grants access: a cached answer outlived its rule")
	}
}

// A source that answers with no resources empties its type; a source that does not answer keeps the data it had.
// The answer of the source is applied as it is: the resources come back when it lists them again.
func TestSourceAnswersDecideWhetherTheTypeIsEmptied(t *testing.T) {
	e := setup(t)
	var mu sync.Mutex
	var mode string // "list", "empty" or "fail"
	mode = "list"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /src/branch", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		current := mode
		mu.Unlock()
		switch current {
		case "fail":
			http.Error(w, "down", http.StatusInternalServerError)
		case "empty":
			_, _ = w.Write([]byte("[]"))
		default:
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "b1", "name": "Branch1"}, {"id": "b2", "name": "Branch2"}})
		}
	})
	mux.HandleFunc("GET /src/branch/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "Branch"})
	})
	source := newTestServer(t, mux)
	setMode := func(m string) {
		mu.Lock()
		mode = m
		mu.Unlock()
	}

	org := e.must("POST", "/service", servicePayload(e, "empty-org"))["id"].(string)
	branch := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+org+"/type_resource/"+branch+"/resource/synchronizer", map[string]any{
		"config":            map[string]any{"kind": "http", "url": source.URL + "/src/branch", "cache_ttl": 0},
		"probe_resource_id": "b1",
	})
	e.must("POST", "/assignment_rule", map[string]any{"type_resource_id": branch, "access": []string{"read"}, "subject": "alice"})

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})
	// consistency=higher: every answer comes from the source at once, so the test does not wait for a refresh.
	ask := func() string {
		t.Helper()
		status, raw := e.do(http.MethodGet, e.check.URL+"/service/"+org+"/permission?consistency=higher", nil, "Authorization", "Bearer "+alice)
		if status != http.StatusOK {
			t.Fatalf("permission: %d %s", status, raw)
		}
		return string(raw)
	}

	if got := ask(); !strings.Contains(got, `"b1"`) || !strings.Contains(got, `"b2"`) {
		t.Fatalf("the list answer must show both branches: %s", got)
	}

	setMode("fail")
	if got := ask(); !strings.Contains(got, `"b1"`) || !strings.Contains(got, `"b2"`) {
		t.Fatalf("a source that does not answer must keep the data: %s", got)
	}

	setMode("empty")
	if got := ask(); strings.Contains(got, `"b1"`) || strings.Contains(got, `"b2"`) {
		t.Fatalf("a source that answers with no resources must empty the type: %s", got)
	}

	setMode("list")
	if got := ask(); !strings.Contains(got, `"b1"`) {
		t.Fatalf("the resources come back when the source lists them again: %s", got)
	}
}
