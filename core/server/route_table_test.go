package server

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata")

// TestContract_AZ10_ThePublicPortServesExactlyTheAuthzenRoutes compares the route table of the PDP's public port, one route per
// line ("METHOD path", sorted), with testdata/pdp_routes.golden, and holds every route to be one of AuthZEN: an evaluation or a
// search of a service, or the discovery document. The routes are the operations of the Huma API that NewPDPAPI registers: an
// http.ServeMux does not list its patterns, and the public handler adds nothing but the JSON 404 for every other path. A route
// added or removed on purpose is recorded with go test -update; a route that is not AuthZEN fails whatever the golden says.
func TestContract_AZ10_ThePublicPortServesExactlyTheAuthzenRoutes(t *testing.T) {
	_, api := NewPDPAPI(engine.New(nil, &config.Config{}))
	var routes []string
	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet:     item.Get,
			http.MethodPut:     item.Put,
			http.MethodPost:    item.Post,
			http.MethodDelete:  item.Delete,
			http.MethodOptions: item.Options,
			http.MethodHead:    item.Head,
			http.MethodPatch:   item.Patch,
			http.MethodTrace:   item.Trace,
		} {
			if op != nil {
				routes = append(routes, method+" "+path)
			}
		}
	}
	sort.Strings(routes)
	for _, r := range routes {
		if !strings.Contains(r, "/access/v1/") && !strings.HasPrefix(r, "GET /.well-known/authzen-configuration/") {
			t.Errorf("%s is not a route of AuthZEN", r)
		}
	}
	got := strings.Join(routes, "\n") + "\n"

	file := filepath.Join("testdata", "pdp_routes.golden")
	if *update {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs (go test -update rewrites it):\ngot:\n%s\nwant:\n%s", file, got, want)
	}
}

// The probes, the metrics, the docs and the component schemas of the document are not on the PDP's public port: the probes and
// the metrics are on the ops listener, the rest is not served at all.
func TestPDPPublicPortServesNoOperationsEndpoint(t *testing.T) {
	h := NewPDPHandler(engine.New(nil, &config.Config{}))
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/openapi.json", "/openapi.yaml", "/docs", "/schemas/Error.json", "/schemas/Error"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s on the public port: %d, want 404", path, rec.Code)
		}
	}
}

// The ops listener answers the probes and the metrics, and nothing else of the PDP. Without a database the readiness probe
// says 503; liveness does not depend on anything, and a draining process is not ready.
func TestOpsHandlerServesTheProbesAndTheMetrics(t *testing.T) {
	app := engine.New(nil, &config.Config{})
	h := NewOpsHandler(app)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/healthz"); rec.Code != http.StatusOK {
		t.Errorf("GET /healthz: %d, want 200", rec.Code)
	}
	if rec := get("/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz without a database: %d, want 503", rec.Code)
	}
	rec := get("/metrics")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "licetum_http_inflight") {
		t.Errorf("GET /metrics: %d, want 200 with licetum_http_inflight", rec.Code)
	}
	for _, path := range []string{"/service/orders/permission", "/openapi.json", "/schemas/Error.json"} {
		if rec := get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s on the ops listener: %d, want 404", path, rec.Code)
		}
	}
	app.SetDraining()
	if rec := get("/readyz"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "draining") {
		t.Errorf("GET /readyz while draining: %d %s, want 503 with the reason draining", rec.Code, rec.Body)
	}
	if rec := get("/healthz"); rec.Code != http.StatusOK {
		t.Errorf("GET /healthz while draining: %d, want 200", rec.Code)
	}
}
