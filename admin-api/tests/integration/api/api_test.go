//go:build integration

package api_test

// Integration test against real PostgreSQL and Redis. Run it with:
//
// The integration package owns disposable PostgreSQL and Redis containers. Run it with
// `go test -tags integration ./tests/integration/api` while Docker is available.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/server"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/admin-api/api"
	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
	support "github.com/licetum/licetum/core/testsupport"
)

var testInfrastructure *support.Infrastructure

func TestMain(m *testing.M) {
	infrastructure, err := support.Start(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "integration infrastructure: %v\n", err)
		os.Exit(1)
	}
	testInfrastructure = infrastructure
	code := m.Run()
	if err := infrastructure.Terminate(context.Background()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "terminate integration infrastructure: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

// syncEngine is the engine of the synchronization the suite runs against: LICETUM_SYNC_ENGINE=postgres, or dbos (the default).
func syncEngine() string {
	if v := os.Getenv("LICETUM_SYNC_ENGINE"); v != "" {
		return v
	}
	return "dbos"
}

type env struct {
	t     *testing.T
	admin *httptest.Server
	check *httptest.Server
	ops   *httptest.Server // the operations listener of the same PDP: probes and metrics (server.NewOpsHandler)
	mock  *httptest.Server
	key   *rsa.PrivateKey
	app   *service.App
	pool  *pgxpool.Pool
	cfg   *config.Config
	// adminToken is sent to the admin API by default (see do).
	adminToken string
	// stopWorker stops the worker the test process runs (a test that needs the jobs to wait calls it).
	stopWorker func()
}

// startWorker runs a synchronization worker on app until the returned function is called or the test ends.
func startWorker(t *testing.T, app *engine.App) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = app.RunWorker(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

func setup(t *testing.T) *env {
	redisURI := testInfrastructure.RedisURI
	ctx := context.Background()

	// An isolated schema per run (a database of its own behind PgBouncer).
	iso := testInfrastructure.Isolate(ctx, t)

	e := &env{t: t}
	var err error
	e.key, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e.mock = newTestServer(t, e.mockHandler())
	t.Cleanup(e.mock.Close)

	// The self-service trusts the same mock IdP; everyone with preferred_username=admin is a
	// platform admin.
	m := e.mock.URL
	cfg := &config.Config{
		DB:   config.Database{URI: iso.URI, PoolSize: 5, PgBouncer: iso.PgBouncer, MigrateURI: iso.MigrateURI},
		Auth: config.Auth{StateSecret: "test-secret", StateTTL: time.Minute},
		Cors: config.Cors{AllowOrigins: []string{"http://localhost:5173"}},
		// Tests that ask for higher consistency in a loop must not be spaced out to one source call per second.
		Sync: config.Sync{FreshMinInterval: time.Millisecond, Engine: syncEngine(), DBOSSchema: "dbos_" + iso.Name},
		// The registry of known subjects is on, as by default.
		Subjects: config.Subjects{Enabled: true, RetentionDays: 90},
		Platform: config.Platform{
			Name: "Licetum", URL: m + "/app", Icon: m + "/icon.png",
			AdminGroupName: "Platform administrator", AdminClaim: "preferred_username",
			AdminClaimValues: []string{"admin"},
		},
		// Administrators sign in at the mock identity provider: its tokens for the admin API carry testAdminAudience.
		AdminAuth: config.AdminAuth{Issuer: m + "/realm", Audience: testAdminAudience, JWKSURL: m + "/jwks",
			SubjectClaim: "preferred_username"},
	}
	if err := db.RunMigrate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	rdb, err := redisx.Connect(ctx, redisURI)
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	app := service.New(engine.New(pool, rdb, cfg))
	t.Cleanup(app.Close)
	// The synchronization jobs are executed by a worker; the test process runs one next to the APIs it serves.
	e.stopWorker = startWorker(t, app.App)
	e.app = app
	e.pool = pool
	e.cfg = cfg
	e.admin = newTestServer(t, api.NewAdminHandler(app))
	e.check = newTestServer(t, server.NewPDPHandler(app.App))
	e.ops = newTestServer(t, server.NewOpsHandler(app.App))
	t.Cleanup(e.admin.Close)
	t.Cleanup(e.check.Close)
	t.Cleanup(e.ops.Close)
	e.adminToken = e.token(jwt.MapClaims{"preferred_username": "admin", "sub": "admin-sub"})
	return e
}

// externalChange tells the app about a change made to the policy by other means than the admin API (SQL in a test, or
// restore-admin in another process): the revision is bumped as every write does, and the app reads it again at once.
func (e *env) externalChange(ctx context.Context) {
	e.t.Helper()
	revision, err := db.BumpRevision(ctx, e.pool, db.ScopeRules)
	if err != nil {
		e.t.Fatal(err)
	}
	e.app.AfterWrite(ctx, revision)
}

func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test server: %v", err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server
}

var sources = map[string][]map[string]any{
	"division":  {{"id": "d1", "name": "North"}, {"id": "d2", "name": "South"}},
	"branch":    {{"id": "b1", "name": "Branch1", "parent_id": "d1"}, {"id": "b2", "name": "Branch2", "parent_id": "d1"}, {"id": "b3", "name": "Branch3", "parent_id": "d2"}},
	"planogram": {{"id": "p1", "name": "Plano1", "parent_id": "b1"}, {"id": "p3", "name": "Plano3", "parent_id": "b3"}},
}

func (e *env) mockHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		jwk := jose.JSONWebKey{Key: &e.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
	})
	mux.HandleFunc("GET /sync/{kind}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sources[r.PathValue("kind")])
	})
	// A source closed with a header: the synchronizer must send its configured headers.
	secure := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer source-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rows := sources[r.PathValue("kind")]
		if id := r.PathValue("id"); id != "" {
			for _, row := range rows {
				if row["id"] == id {
					_ = json.NewEncoder(w).Encode(row)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rows)
	}
	mux.HandleFunc("GET /secure/{kind}", secure)
	mux.HandleFunc("GET /secure/{kind}/{id}", secure)
	// The IdP token endpoint for the refresh grant.
	mux.HandleFunc("POST /realm/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("refresh_token") {
		case "good":
			_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":300,` +
				`"refresh_token":"new-refresh","refresh_expires_in":"1800","id_token":"x"}`))
		case "expired":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		}
	})
	mux.HandleFunc("GET /sync/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		for _, row := range sources[r.PathValue("kind")] {
			if row["id"] == r.PathValue("id") {
				_ = json.NewEncoder(w).Encode(row)
				return
			}
		}
		http.NotFound(w, r)
	})
	return mux
}

// testAdminAudience is the audience (ADMIN_AUTH__AUDIENCE) the admin API of the tests requires.
const testAdminAudience = "licetum-admin"

// token signs claims as the mock identity provider. A token that names no audience gets the one of the admin API, so the same
// token works there and (where the audience is not looked at) at a service; a test of the audience sets "aud" itself.
func (e *env) token(claims jwt.MapClaims) string {
	claims["iss"] = e.mock.URL + "/realm"
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	if _, ok := claims["aud"]; !ok {
		claims["aud"] = testAdminAudience
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(e.key)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) do(method, url string, body any, headers ...string) (int, []byte) {
	e.t.Helper()
	status, _, raw := e.doWithHeader(method, url, body, headers...)
	return status, raw
}

// doWithHeader is do with the headers of the answer too: the code of an AuthZEN error is in its Licetum-Error-Code header.
func (e *env) doWithHeader(method, url string, body any, headers ...string) (int, http.Header, []byte) {
	e.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Admin API requests act as the platform admin unless the caller passes its own Authorization
	// (an empty value sends none).
	if e.admin != nil && strings.HasPrefix(url, e.admin.URL) {
		req.Header.Set("Authorization", "Bearer "+e.adminToken)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
			continue
		}
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		e.t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, resp.Header, buf.Bytes()
}

// must performs an admin API request that must return 200 and returns the decoded JSON.
func (e *env) must(method, path string, body any) map[string]any {
	e.t.Helper()
	status, raw := e.do(method, e.admin.URL+path, body)
	if status != http.StatusOK {
		e.t.Fatalf("%s %s: %d %s", method, path, status, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// Huma serializes successful DELETE responses as a JSON string containing
		// the deleted ID. Keep must convenient for mutation tests that only need
		// to assert the 200 status while still accepting object responses.
		var deletedID string
		if stringErr := json.Unmarshal(raw, &deletedID); stringErr == nil {
			return map[string]any{"id": deletedID}
		}
		e.t.Fatalf("decode %s %s response: %v (%s)", method, path, err, raw)
	}
	return out
}

func expectJSON(t *testing.T, name string, got any, wantJSON string) {
	t.Helper()
	var want any
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode actual JSON: %v", err)
	}
	var gotNorm any
	if err := json.Unmarshal(gotRaw, &gotNorm); err != nil {
		t.Fatalf("normalize actual JSON: %v", err)
	}
	if !reflect.DeepEqual(gotNorm, want) {
		t.Fatalf("%s:\n got  %s\n want %s", name, gotRaw, wantJSON)
	}
}

func TestIntegrationPermissionFlow(t *testing.T) {
	e := setup(t)
	m := e.mock.URL
	svc := func(name string) map[string]any {
		return map[string]any{
			"name": name, "url": m + "/app", "icon": m + "/icon.png",
			"authorization_url": m + "/realm/auth", "token_url": m + "/realm/token",
			"jwks_url": m + "/jwks", "issuer": m + "/realm", "slug": name, "client_id": name, "client_secret": "s",
		}
	}

	filials := e.must("POST", "/service/", svc("filials"))["id"].(string)
	replan := e.must("POST", "/service", svc("replan"))["id"].(string)

	division := e.must("POST", "/service/"+filials+"/type_resource/", map[string]any{"name": "division"})["id"].(string)
	branch := e.must("POST", "/service/"+filials+"/type_resource/",
		map[string]any{"name": "branch", "type_resource_parent_id": division})["id"].(string)
	planogram := e.must("POST", "/service/"+replan+"/type_resource/",
		map[string]any{"name": "planogram", "type_resource_parent_id": branch})["id"].(string)

	for _, s := range []struct{ svc, tr, kind, probe string }{
		{filials, division, "division", "d1"}, {filials, branch, "branch", "b1"}, {replan, planogram, "planogram", "p1"},
	} {
		sync := e.must("POST", "/service/"+s.svc+"/type_resource/"+s.tr+"/resource/synchronizer",
			map[string]any{"config": map[string]any{"kind": "http", "url": m + "/sync/" + s.kind}, "probe_resource_id": s.probe})
		expectJSON(t, "synchronizer config", sync["config"], `{"kind":"http","url":"`+m+`/sync/`+s.kind+`"}`)
	}

	sales := e.must("POST", "/group/", map[string]any{"name": "sales"})["id"].(string)
	managers := e.must("POST", "/group/", map[string]any{"name": "managers"})["id"].(string)
	e.must("POST", "/group/"+sales+"/member/",
		map[string]any{"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	e.must("POST", "/group/"+managers+"/member/", map[string]any{"member_group_id": sales})

	directRule := e.must("POST", "/assignment_rule/",
		map[string]any{"type_resource_id": division, "resource_id": "d1", "access": []string{"read"}, "subject": "alice"})["id"].(string)
	e.must("POST", "/assignment_rule/", map[string]any{"type_resource_id": planogram, "access": []string{"write"},
		"predicate": []any{map[string]any{"attr": "level", "op": "in", "value": []string{"5", "6"}}}})
	e.must("POST", "/assignment_rule/", map[string]any{"type_resource_id": branch, "access": []string{"audit"},
		"member_of_group_id":  managers,
		"resource_from_claim": map[string]any{"claim": "branch_name", "lookup_type_resource_id": branch}})

	alice := e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "u1", "department": "sales", "branch_name": "Branch3"})
	bob := e.token(jwt.MapClaims{"sub": "bob", "level": 5})

	// permissions is what a person may do in a service: for each type that has resources they have a level on, those
	// resources with their levels (the searches of the AuthZEN API, one per level the test grants).
	permissions := func(ref, token string, types ...string) map[string][]listedResource {
		out := map[string][]listedResource{}
		for _, typ := range types {
			if list := e.listedLevels(ref, token, typ, "read", "write", "audit"); len(list) > 0 {
				out[typ] = list
			}
		}
		return out
	}

	// alice: d1 directly + cascade to b1,b2; through the nested role managers — b3 by name from a claim.
	expectJSON(t, "alice@filials", permissions("filials", alice, "division", "branch"), `{
		"division": [{"id":"d1","name":"North","access":["read"]}],
		"branch": [
			{"id":"b1","name":"Branch1","access":["read"]},
			{"id":"b2","name":"Branch2","access":["read"]},
			{"id":"b3","name":"Branch3","access":["audit"]}
		]}`)
	// The cascade crosses service boundaries: division(filials) -> branch -> planogram(replan).
	expectJSON(t, "alice@replan", permissions("replan", alice, "planogram"), `{"planogram": [
		{"id":"p1","name":"Plano1","access":["read"]},
		{"id":"p3","name":"Plano3","access":["audit"]}]}`)
	// bob: predicate level in [5,6] (an int claim is compared as a string) — the whole planogram type.
	expectJSON(t, "bob@replan", permissions("replan", bob, "planogram"), `{"planogram": [
		{"id":"p1","name":"Plano1","access":["write"]},
		{"id":"p3","name":"Plano3","access":["write"]}]}`)

	// Changing a rule invalidates the permission cache immediately (version in Redis).
	e.must("PATCH", "/assignment_rule/"+directRule,
		map[string]any{"type_resource_id": division, "resource_id": "d2", "access": []string{"read", "write"}, "subject": "alice"})
	expectJSON(t, "alice@filials after patch", permissions("filials", alice, "division", "branch"), `{
		"division": [{"id":"d2","name":"South","access":["read","write"]}],
		"branch": [{"id":"b3","name":"Branch3","access":["audit","read","write"]}]}`)

	// Deleting a referenced type is blocked, with the list of reasons.
	status, raw := e.do("DELETE", e.admin.URL+"/service/"+filials+"/type_resource/"+branch, nil)
	if status != http.StatusConflict || !strings.Contains(string(raw), `"blocked_by":["synchronizer","assignment_rule","type_resource"]`) {
		t.Fatalf("delete branch: %d %s", status, raw)
	}
	// Cycles in the type tree are rejected.
	status, _ = e.do("PATCH", e.admin.URL+"/service/"+filials+"/type_resource/"+division,
		map[string]any{"name": "division", "type_resource_parent_id": branch})
	if status != http.StatusBadRequest {
		t.Fatalf("cycle in the type tree: want 400, got %d", status)
	}

	// Garbage tokens get 401 and do not open the circuit breaker for valid users.
	for i := range 6 {
		if status, _ := e.permission("filials", fmt.Sprintf("garbage.%d.x", i)); status != http.StatusUnauthorized {
			t.Fatalf("garbage token: want 401, got %d", status)
		}
	}
	if status, _ := e.permission("filials", alice); status != http.StatusOK {
		t.Fatalf("a valid token must still pass after garbage tokens, got %d", status)
	}

	// Resources and coverage.
	status, raw = e.do("GET", e.admin.URL+"/service/"+replan+"/type_resource/"+planogram+"/resource?resource_id=p3", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), `"parent_id":"b3"`) {
		t.Fatalf("resource p3: %d %s", status, raw)
	}
	status, raw = e.do("GET", e.admin.URL+"/type_resource/"+branch+"/resource", nil)
	if status != http.StatusOK || strings.Count(string(raw), `"name"`) != 3 {
		t.Fatalf("list resources: %d %s", status, raw)
	}
}

func TestIntegrationOAuthLogin(t *testing.T) {
	e := setup(t)
	m := e.mock.URL
	public := e.must("POST", "/service/", map[string]any{
		"name": "public", "url": m + "/app", "icon": m + "/icon.png",
		"authorization_url": m + "/realm/auth?kc_idp_hint=x", "token_url": m + "/realm/token",
		"jwks_url": m + "/jwks", "issuer": m + "/realm", "slug": "pub", "client_id": "pub",
	})["id"].(string)

	status, _ := e.do("GET", e.admin.URL+"/service/"+public+"/oauth/login?redirect_to="+
		url.QueryEscape("http://localhost:5173.evil.com/"), nil)
	if status != http.StatusBadRequest {
		t.Fatalf("redirect_to to a foreign origin: want 400, got %d", status)
	}

	req, _ := http.NewRequest(http.MethodGet, e.admin.URL+"/service/"+public+"/oauth/login?redirect_to="+
		url.QueryEscape("http://localhost:5173/cb"), nil)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	params := loc.Query()
	if resp.StatusCode != http.StatusTemporaryRedirect || params.Get("kc_idp_hint") != "x" ||
		params.Get("code_challenge_method") != "S256" || params.Get("client_id") != "pub" ||
		!strings.HasSuffix(params.Get("redirect_uri"), "/service/"+public+"/oauth/callback") {
		t.Fatalf("login redirect: %d %s", resp.StatusCode, loc)
	}

	// A confidential client does not use PKCE; client_secret: null turns it back into a public one.
	loginChallenge := func() string {
		req, _ := http.NewRequest(http.MethodGet, e.admin.URL+"/service/"+public+"/oauth/login", nil)
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		return loc.Query().Get("code_challenge_method")
	}
	e.must("PATCH", "/service/"+public, map[string]any{"client_secret": "now-confidential"})
	if got := loginChallenge(); got != "" {
		t.Fatalf("a confidential client must not use PKCE, got %q", got)
	}
	e.must("PATCH", "/service/"+public, map[string]any{"client_secret": nil})
	if got := loginChallenge(); got != "S256" {
		t.Fatalf("client_secret: null must make the client public (PKCE), got %q", got)
	}

	status, raw := e.do("GET", e.admin.URL+"/service/"+public+"/oauth/callback?code=c&state=forged", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("callback with a forged state: %d %s", status, raw)
	}
}

func TestIntegrationSchemaValidation(t *testing.T) {
	e := setup(t)
	svcID := e.must("POST", "/service/", map[string]any{
		"name": "svc", "url": e.mock.URL + "/app", "icon": e.mock.URL + "/icon.png",
		"authorization_url": e.mock.URL + "/a", "token_url": e.mock.URL + "/t", "jwks_url": e.mock.URL + "/jwks",
		"issuer": e.mock.URL + "/realm", "slug": "c", "client_id": "c", "client_secret": "s",
	})["id"].(string)
	typeID := e.must("POST", "/service/"+svcID+"/type_resource/", map[string]any{"name": "t", "type_resource_parent_id": nil})["id"].(string)

	// The frontend sends extra fields and explicit nulls — pydantic accepted them, so must Huma.
	group := e.must("POST", "/group/", map[string]any{"name": "r1", "id": "ignored", "unknown": 1})
	if _, has := group["$schema"]; has {
		t.Fatal("the response must not contain $schema")
	}
	e.must("POST", "/assignment_rule/", map[string]any{
		"type_resource_id": typeID, "access": []string{"read"}, "resource_id": nil, "resource_from_claim": nil,
		"subject": nil, "member_of_group_id": nil, "predicate": []any{}, "subjectValue": "extra-field",
	})
	e.must("POST", "/group/"+group["id"].(string)+"/member/", map[string]any{"member_group_id": nil, "predicate": []any{}, "enabled": true})
	e.must("PATCH", "/service/"+svcID, map[string]any{"name": "svc2", "client_secret": nil})

	for _, tc := range []struct {
		name, method, path string
		body               any
		status             int
		detailHas          string
	}{
		{"rule without type_resource_id", "POST", "/assignment_rule/", map[string]any{"access": []string{"r"}}, 422, "type_resource_id"},
		{"rule with an invalid uuid", "POST", "/assignment_rule/", map[string]any{"type_resource_id": "nope", "access": []string{"r"}}, 422, "type_resource_id"},
		{"rule with an unknown op", "POST", "/assignment_rule/", map[string]any{"type_resource_id": typeID, "access": []string{"r"},
			"predicate": []any{map[string]any{"attr": "a", "op": "between", "value": "b"}}}, 422, "op"},
		{"synchronizer without probe_resource_id", "POST", "/service/" + svcID + "/type_resource/" + typeID + "/resource/synchronizer",
			map[string]any{"config": map[string]any{"kind": "http", "url": "http://x"}}, 422, "probe_resource_id"},
		{"invalid uuid in the path", "DELETE", "/group/not-a-uuid", nil, 422, "group_id"},
		{"resource without the required query", "GET", "/service/" + svcID + "/type_resource/" + typeID + "/resource", nil, 422, "resource_id"},
		{"empty service PATCH", "PATCH", "/service/" + svcID, map[string]any{}, 422, "At least one field"},
	} {
		status, raw := e.do(tc.method, e.admin.URL+tc.path, tc.body)
		var body struct {
			Detail string `json:"detail"`
			Extra  struct {
				Type string `json:"type"`
			} `json:"extra"`
		}
		_ = json.Unmarshal(raw, &body)
		if status != tc.status || !strings.Contains(body.Detail, tc.detailHas) || body.Extra.Type != "generic" {
			t.Errorf("%s: %d %s", tc.name, status, raw)
		}
	}

	// Docs and spec: the admin API serves them. The PDP's public port serves neither (its document is what `licetum openapi pdp`
	// prints, checked below), and its probes and metrics are on the operations listener.
	status, raw := e.do("GET", e.admin.URL+"/openapi.json", nil)
	var spec struct {
		OpenAPI string         `json:"openapi"`
		Paths   map[string]any `json:"paths"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &spec) != nil || spec.OpenAPI != "3.1.0" || len(spec.Paths) == 0 {
		t.Fatalf("openapi.json: %d %.200s", status, raw)
	}
	if status, _ := e.do("GET", e.admin.URL+"/docs", nil); status != http.StatusOK {
		t.Fatalf("/docs: %d", status)
	}
	for _, path := range []string{"/openapi.json", "/docs", "/healthz", "/readyz", "/metrics"} {
		if status, _ := e.do("GET", e.check.URL+path, nil); status != http.StatusNotFound {
			t.Fatalf("the PDP's public port answers %s with %d, want 404", path, status)
		}
	}
	_, pdp := server.NewPDPAPI(e.app.App)
	doc, err := server.LocalizedSpec(pdp, i18n.Default, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"authzen_evaluation"`) || !strings.Contains(string(doc), `"bearer"`) {
		t.Fatal("the PDP spec lacks authzen_evaluation or the bearer security scheme")
	}
}

// The admin API is protected by the platform's own policies (see auth.go).
func TestIntegrationPlatformPolicies(t *testing.T) {
	e := setup(t)
	m := e.mock.URL
	svc := func(name string) map[string]any {
		return map[string]any{
			"name": name, "url": m + "/app", "icon": m + "/icon.png",
			"authorization_url": m + "/realm/auth", "token_url": m + "/realm/token",
			"jwks_url": m + "/jwks", "issuer": m + "/realm", "slug": name, "client_id": name, "client_secret": "s",
		}
	}
	asUser := func(token string) []string { return []string{"Authorization", "Bearer " + token} }
	decode := func(raw []byte) map[string]any {
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}

	// /platform stays public: the UI needs it before signing in.
	status, raw := e.do("GET", e.admin.URL+"/platform", nil, "Authorization", "")
	if status != http.StatusOK {
		t.Fatalf("/platform: %d", status)
	}
	// The UI reads the platform service, the administrators' group and the policy types from it.
	platform := decode(raw)
	policyTypes, _ := platform["types"].([]any)
	if platform["service_id"] != model.PlatformServiceID.String() ||
		platform["admin_group_id"] != model.PlatformAdminGroupID.String() ||
		len(policyTypes) != len(model.PlatformTypes) {
		t.Fatalf("/platform: %s", raw)
	}

	// No or a broken token — 401.
	for _, auth := range []string{"", "Basic abc", "Bearer garbage.x.y"} {
		status, raw := e.do("GET", e.admin.URL+"/service", nil, "Authorization", auth)
		if status != http.StatusUnauthorized {
			t.Fatalf("GET /service with Authorization %q: %d %s", auth, status, raw)
		}
	}

	// The admin gets read+write on every policy as a whole.
	_, raw = e.do("GET", e.admin.URL+"/platform/me", nil)
	expectJSON(t, "admin /platform/me", decode(raw), `{"subject":"admin","types":{
		"service":{"whole_type":["read","write"],"entries":[],"denied":[]},
		"type_resource":{"whole_type":["read","write"],"entries":[],"denied":[]},
		"group":{"whole_type":["read","write"],"entries":[],"denied":[]},
		"group_member":{"whole_type":["read","write"],"entries":[],"denied":[]},
		"role":{"whole_type":["read","write"],"entries":[],"denied":[]},
		"assignment_rule":{"whole_type":["read","write"],"entries":[],"denied":[]}}}`)

	// A signed-in user without any policy: /platform/me is empty, everything else is 403.
	carol := e.token(jwt.MapClaims{"preferred_username": "carol"})
	_, raw = e.do("GET", e.admin.URL+"/platform/me", nil, asUser(carol)...)
	expectJSON(t, "carol /platform/me", decode(raw), `{"subject":"carol","types":{}}`)
	status, raw = e.do("GET", e.admin.URL+"/service", nil, append(asUser(carol), "Accept-Language", "ru")...)
	if status != http.StatusForbidden || !strings.Contains(string(raw), `"code":"access_denied"`) ||
		!strings.Contains(string(raw), "Недостаточно прав: read на «service»") {
		t.Fatalf("carol GET /service: %d %s", status, raw)
	}
	// Authorization is checked before the body: no 422 leaks the schema to a stranger.
	if status, raw := e.do("POST", e.admin.URL+"/group", map[string]any{}, asUser(carol)...); status != http.StatusForbidden {
		t.Fatalf("carol POST /group with an invalid body: %d %s", status, raw)
	}

	// A point grant on one service: carol sees and edits only it, and cannot create new ones.
	x := e.must("POST", "/service", svc("x"))["id"].(string)
	y := e.must("POST", "/service", svc("y"))["id"].(string)
	e.must("POST", "/assignment_rule", map[string]any{
		"type_resource_id": model.PlatformTypeResourceID(model.PlatformService).String(),
		"resource_id":      x, "access": []string{"read", "write"}, "subject": "carol",
	})

	status, raw = e.do("GET", e.admin.URL+"/service", nil, asUser(carol)...)
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	if status != http.StatusOK || len(list) != 1 || list[0]["id"] != x {
		t.Fatalf("carol GET /service must return only x: %d %s", status, raw)
	}
	if status, raw := e.do("PATCH", e.admin.URL+"/service/"+x, map[string]any{"name": "x2"}, asUser(carol)...); status != http.StatusOK {
		t.Fatalf("carol PATCH x: %d %s", status, raw)
	}
	if status, _ := e.do("PATCH", e.admin.URL+"/service/"+strings.ToUpper(y), map[string]any{"name": "y2"}, asUser(carol)...); status != http.StatusForbidden {
		t.Fatalf("carol PATCH y: want 403, got %d", status)
	}
	if status, _ := e.do("POST", e.admin.URL+"/service", svc("z"), asUser(carol)...); status != http.StatusForbidden {
		t.Fatalf("carol POST /service with a point grant only: want 403, got %d", status)
	}
	_, raw = e.do("GET", e.admin.URL+"/platform/me", nil, asUser(carol)...)
	expectJSON(t, "carol /platform/me with a point grant", decode(raw), `{"subject":"carol","types":{
		"service":{"whole_type":[],"entries":[{"id":"`+x+`","name":"x2","access":["read","write"]}],"denied":[]}}}`)

	// Re-running the seed never restores what an admin has revoked.
	groupRule := model.PlatformAdminRuleID(model.PlatformGroup).String()
	e.must("DELETE", "/assignment_rule/"+groupRule, nil)
	if err := db.SeedPlatform(context.Background(), e.pool, e.cfg); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.do("GET", e.admin.URL+"/group", nil); status != http.StatusForbidden {
		t.Fatalf("admin GET /group after revoking the group policy and re-seeding: want 403, got %d", status)
	}

	// Without the self-service the admin API is closed to everyone.
	if _, err := e.pool.Exec(context.Background(), `UPDATE service SET deleted_at = now() WHERE id = $1`,
		model.PlatformServiceID); err != nil {
		t.Fatal(err)
	}
	if status, raw := e.do("GET", e.admin.URL+"/service", nil); status != http.StatusServiceUnavailable ||
		!strings.Contains(string(raw), `"code":"platform_not_seeded"`) {
		t.Fatalf("GET /service without the self-service: %d %s", status, raw)
	}
	if status, _ := e.do("GET", e.admin.URL+"/platform", nil); status != http.StatusNotFound {
		t.Fatalf("/platform without the self-service: want 404, got %d", status)
	}
}
