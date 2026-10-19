package harness_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

func tokenOf(t *testing.T, user string) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"preferred_username": user, "sub": user + "-sub"}).SignedString([]byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// Branches asks for the branches of the type the person may read, as the person the token names, and carries the options of authzenreq.
func TestBranchesIsTheSearchOfTheReadableBranchesOfTheHolderOfTheToken(t *testing.T) {
	req := harness.Branches("org", tokenOf(t, "alice"), authzenreq.AtLeast(7))
	want := authzenreq.SearchResource("org", "alice", "branch", "read", authzenreq.AtLeast(7))
	got, _ := json.Marshal(req)
	wanted, _ := json.Marshal(want)
	if string(got) != string(wanted) {
		t.Fatalf("Branches = %s\nwant      %s", got, wanted)
	}
	if req.Method != http.MethodPost || req.Path != "/service/org/access/v1/search/resource?at_least_as_fresh=7" {
		t.Fatalf("Branches sends %s %s", req.Method, req.Path)
	}
}

// A request for the http.Client of a test carries the body, the token and the levers, and is one the client may send again.
func TestHTTPRequestCarriesTheBodyTheTokenAndTheLevers(t *testing.T) {
	question := authzenreq.Evaluation("org", "alice", authzenreq.On("branch", "b1"), "read")
	req, err := harness.HTTPRequest(context.Background(), "http://replica", question, "the-token", harness.Higher())
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.String() != "http://replica/service/org/access/v1/evaluation" {
		t.Fatalf("%s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer the-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := req.Header.Get(harness.ConsistencyHeader); got != string(engine.HigherConsistency) {
		t.Errorf("%s = %q", harness.ConsistencyHeader, got)
	}
	// The client sends a POST again after a connection the server closed while it was idle only when it holds this entry, which is
	// empty, so that nothing goes on the wire.
	if v, ok := req.Header["Idempotency-Key"]; !ok || len(v) != 0 {
		t.Errorf("Idempotency-Key = %v, %v: want an empty entry", v, ok)
	}
	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body["subject"] == nil || body["resource"] == nil || body["action"] == nil {
		t.Errorf("body %s: %v", raw, err)
	}

	none, err := harness.HTTPRequest(context.Background(), "http://replica", question, "")
	if err != nil {
		t.Fatal(err)
	}
	if none.Header.Get("Authorization") != "" || none.Header.Get(harness.ConsistencyHeader) != "" {
		t.Errorf("a request without token or lever has %v", none.Header)
	}
}

// What the server sees of a request built by HTTPRequest: the lever reaches the handler as the freshness of the context.
func TestTheLeverOfARequestReachesTheHandlerBehindLevers(t *testing.T) {
	var level engine.Consistency
	var set bool
	var sent http.Header
	srv := httptest.NewServer(harness.Levers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		level, set = engine.ConsistencyIn(r.Context())
		sent = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	})))
	defer srv.Close()

	req, err := harness.HTTPRequest(context.Background(), srv.URL, authzenreq.SearchResource("org", "alice", "branch", "read"), "", harness.Higher())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !set || level != engine.HigherConsistency {
		t.Fatalf("the handler saw %q (set %v), want higher", level, set)
	}
	if _, ok := sent["Idempotency-Key"]; ok {
		t.Errorf("the empty Idempotency-Key went on the wire: %v", sent)
	}
}

func TestResultDecodesTheAnswersOfASearchAndOfAnEvaluation(t *testing.T) {
	search := &harness.Result{Status: http.StatusOK, Body: []byte(`{"results":[{"type":"branch","id":"b1"},{"type":"branch","id":"b2"}],"page":{"count":2}}`)}
	if ids := search.IDs(t); len(ids) != 2 || ids[0] != "b1" || ids[1] != "b2" {
		t.Errorf("IDs = %v", ids)
	}
	if !search.Sees() {
		t.Error("two resources: Sees is false")
	}

	empty := &harness.Result{Status: http.StatusOK, Body: []byte(`{"results":[],"page":{"count":0}}`)}
	if len(empty.IDs(t)) != 0 || empty.Sees() {
		t.Errorf("no resource: IDs %v, Sees %v", empty.IDs(t), empty.Sees())
	}
	// A refusal is nobody's absence of access: Sees is false for it, and IDs would fail the test.
	refused := &harness.Result{Status: http.StatusServiceUnavailable, Body: []byte(`{"detail":"policy unavailable","extra":{"code":"policy_unavailable"}}`)}
	if refused.Sees() {
		t.Error("a 503 sees something")
	}

	if allowed := (&harness.Result{Status: http.StatusOK, Body: []byte(`{"decision":true}`)}).Decision(t); !allowed {
		t.Error("decision true read as false")
	}
	if allowed := (&harness.Result{Status: http.StatusOK, Body: []byte(`{"decision":false}`)}).Decision(t); allowed {
		t.Error("decision false read as true")
	}
}
