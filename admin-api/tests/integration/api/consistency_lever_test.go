//go:build integration

package api_test

// The freshness of a request is what its caller asks for with ?consistency=higher, or what the process that serves it sets
// in the context of the request (engine.WithConsistency; a test harness does it with harness.Levers, from the header
// X-Test-Consistency). The two are one lever: these tests drive every route of the check API that reads the freshness
// (core/server: ConsistencyParam.valueIn) both ways, and compare what the engine does.

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/server"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// leverWorld is the hierarchy world with a second check API in front of the same engine, behind harness.Levers: the same
// processes, caches and counters answer through either address.
type leverWorld struct {
	*world
	leverURL string
	alice    string // the token of hierarchySubject, who reads everything under D1
	client   string // the token of a client application the service trusts
}

func newLeverWorld(t *testing.T) *leverWorld {
	t.Helper()
	w := newWorld(t)
	lever := newTestServer(t, harness.Levers(server.NewPDPHandler(w.app.App)))
	t.Cleanup(lever.Close)
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1"})
	w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{"docs-backend"}})
	return &leverWorld{
		world:    w,
		leverURL: lever.URL,
		alice:    w.token(jwt.MapClaims{"preferred_username": hierarchySubject}),
		client:   w.token(jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"}),
	}
}

// send sends the request to the check API at base, with the token and the headers.
func (w *leverWorld) send(base string, r authzenreq.Request, token string, headers ...string) (int, []byte) {
	w.t.Helper()
	return w.do(r.Method, base+r.Path, r.Body, append([]string{"Authorization", "Bearer " + token}, headers...)...)
}

// bypasses are the answers that skipped the result caches so far, as the check API counts them: the permissions, the checks and the
// pages of the resource search.
func (w *leverWorld) bypasses() (permission, check, search float64) {
	w.t.Helper()
	status, raw := w.do(http.MethodGet, w.ops.URL+"/metrics", nil)
	if status != http.StatusOK {
		w.t.Fatalf("metrics: %d", status)
	}
	value := func(name string) float64 { return max(metricValue(string(raw), name+`{result="bypass"}`), 0) }
	return value("licetum_permission_cache_total"), value("licetum_check_cache_total"), value("licetum_search_cache_total")
}

// bypassed counts answers that skipped the result cache of the permissions, of the checks and of the resource search.
type bypassed struct{ permission, check, search float64 }

// leverRoute is a route of the check API that reads the freshness of the request, with the request that asks it a question.
type leverRoute struct {
	name  string
	token string
	// build makes the request; with fresh it asks for consistency=higher in the query.
	build func(fresh bool) authzenreq.Request
}

func (w *leverWorld) routes() []leverRoute {
	service := "/service/" + w.org
	// A route of the previous releases, or one the builders do not make: the query is added by hand.
	raw := func(method, path string, body any) func(bool) authzenreq.Request {
		return func(fresh bool) authzenreq.Request {
			target := path
			if fresh {
				target += "?consistency=higher"
			}
			return authzenreq.Request{Method: method, Path: target, Body: body}
		}
	}
	authzen := func(build func(...authzenreq.Option) authzenreq.Request) func(bool) authzenreq.Request {
		return func(fresh bool) authzenreq.Request {
			if fresh {
				return build(authzenreq.Fresh())
			}
			return build()
		}
	}
	checks := []map[string]any{{"type": "branch", "resource_id": "B1", "action": "read"}}
	alice := authzenreq.SubjectOf(w.alice)
	b1 := authzenreq.On("branch", "B1")
	oneEvaluation := map[string]any{
		"subject":  map[string]any{"type": authzenreq.SubjectType, "id": alice},
		"resource": map[string]any{"type": "branch", "id": "B1"},
		"action":   map[string]any{"name": "read"},
	}
	return []leverRoute{
		// legacy:2c the route of the previous release stays until P2-23 removes it.
		{"permission", w.alice, raw(http.MethodGet, service+"/permission", nil)},
		// legacy:2c the route of the previous release stays until P2-23 removes it.
		{"check", w.alice, raw(http.MethodPost, service+"/check", map[string]any{"checks": checks})},
		// legacy:2c the route of the previous release stays until P2-23 removes it.
		{"on_behalf check", w.client, raw(http.MethodPost, service+"/on_behalf/check", map[string]any{"subject": "dave", "checks": checks})},
		// legacy:2c the route of the previous release stays until P2-23 removes it.
		{"on_behalf permission", w.client, raw(http.MethodPost, service+"/on_behalf/permission", map[string]any{"subject": "dave", "type": "branch", "access": "read"})},
		{"evaluation", w.alice, authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.Evaluation(w.org, alice, b1, "read", o...)
		})},
		// The batch endpoint without evaluations is one evaluation, a code path of its own.
		{"evaluations of one", w.alice, raw(http.MethodPost, service+"/access/v1/evaluations", oneEvaluation)},
		{"evaluations", w.alice, authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.Evaluations(w.org, alice, []authzenreq.Question{{Resource: b1, Action: "read"}, {Resource: authzenreq.On("branch", "B3"), Action: "write"}}, o...)
		})},
		{"search resource", w.alice, authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchResource(w.org, alice, "branch", "read", o...)
		})},
		{"search action of a resource", w.alice, authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchAction(w.org, alice, b1, o...)
		})},
		{"search action of a type", w.alice, authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchAction(w.org, alice, authzenreq.Whole("branch"), o...)
		})},
		{"search subject", w.client, authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchSubject(w.org, b1, "read", o...)
		})},
	}
}

// The legacy check route answers 422 for a context name the rules cannot read (core/server/check.go documents it). The
// AuthZEN evaluation ignores such a name instead (AZ06; conditions_test.go asserts that). The legal name in the same body
// is answered, so the refusal is the name's and not the body's.
func TestLegacyCheckAnswers422ForAContextNameWithASpace(t *testing.T) {
	w := newLeverWorld(t)
	checks := []map[string]any{{"type": "branch", "resource_id": "B1", "action": "read"}}
	path := "/service/" + w.org + "/check"

	// legacy:2c the route of the previous release stays until P2-23 removes it.
	status, body := w.send(w.check.URL, authzenreq.Request{Method: http.MethodPost, Path: path,
		Body: map[string]any{"checks": checks, "context": map[string]any{"ip": "10.0.0.7"}}}, w.alice)
	if status != http.StatusOK {
		t.Fatalf("the legacy check with a legal context name: %d %s", status, body)
	}
	// legacy:2c the route of the previous release stays until P2-23 removes it.
	status, body = w.send(w.check.URL, authzenreq.Request{Method: http.MethodPost, Path: path,
		Body: map[string]any{"checks": checks, "context": map[string]any{"bad name": 1}}}, w.alice)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("the legacy check with a context name with a space: want 422, got %d %s", status, body)
	}
}

// A request that carries the lever, with no query, does what the same request does with ?consistency=higher: it skips the
// result cache, as many times as the route looks it up. A request without either does not, and the lever of the context
// wins over the query.
func TestTheConsistencyLeverOfTheContextActsAsTheQueryParameter(t *testing.T) {
	w := newLeverWorld(t)
	ctx := context.Background()
	// Dave is a known subject, so that the subject search has somebody to decide.
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "dave", "access": []string{"read"}})
	if s, _ := w.env.permission(w.org, w.token(jwt.MapClaims{"preferred_username": "dave", "name": "Dave", "sub": "dave"})); s != http.StatusOK {
		t.Fatalf("permission as dave: %d", s)
	}

	for _, r := range w.routes() {
		t.Run(r.name, func(t *testing.T) {
			// take sends the request and returns how many answers it took from the data and not from the caches.
			take := func(base string, fresh bool, headers ...string) bypassed {
				t.Helper()
				permission, check, search := w.bypasses()
				status, body := w.send(base, r.build(fresh), r.token, headers...)
				if status != http.StatusOK {
					t.Fatalf("%s: %d %s", r.name, status, body)
				}
				afterPermission, afterCheck, afterSearch := w.bypasses()
				return bypassed{afterPermission - permission, afterCheck - check, afterSearch - search}
			}

			// The first request synchronizes what it needs and has the people it meets written to the registry.
			take(w.check.URL, false)
			w.app.FlushSubjects(ctx)

			if got := take(w.check.URL, false); got != (bypassed{}) {
				t.Errorf("a request that asks for nothing skipped the caches: %+v", got)
			}
			byQuery := take(w.check.URL, true)
			if byQuery == (bypassed{}) {
				t.Fatalf("?consistency=higher did not skip the caches")
			}
			if got := take(w.leverURL, false, harness.ConsistencyHeader, "higher"); got != byQuery {
				t.Errorf("the lever of the context skipped %+v, the query parameter %+v", got, byQuery)
			}
			if got := take(w.leverURL, true, harness.ConsistencyHeader, "higher"); got != byQuery {
				t.Errorf("both together skipped %+v, the query parameter alone %+v", got, byQuery)
			}
			if got := take(w.leverURL, true, harness.ConsistencyHeader, "minimize_latency"); got != (bypassed{}) {
				t.Errorf("the lever minimize_latency must win over ?consistency=higher: %+v", got)
			}
		})
	}
}

// A request with the lever asks the sources again, as ?consistency=higher does: a resource added to the source since the
// last synchronization is there at once, where an ordinary request still answers from the data it has (a source is asked
// again once its throttle has run out, 30 seconds, and the throttle of this test is far from it).
func TestTheConsistencyLeverOfTheContextRereadsTheSources(t *testing.T) {
	w := newLeverWorld(t)
	alice := authzenreq.SubjectOf(w.alice)
	allowed := func(base, id string, opts []authzenreq.Option, headers ...string) bool {
		t.Helper()
		status, body := w.send(base, authzenreq.Evaluation(w.org, alice, authzenreq.On("branch", id), "read", opts...), w.alice, headers...)
		ok, err := authzenreq.Allowed(status, body)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	listed := func(base string, opts []authzenreq.Option, headers ...string) []string {
		t.Helper()
		status, body := w.send(base, authzenreq.SearchResource(w.org, alice, "branch", "read", opts...), w.alice, headers...)
		ids, _, err := authzenreq.ResourceIDs(status, body)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}

	// The first synchronization of the types.
	if got := listed(w.check.URL, nil); !slices.Equal(got, []string{"B1", "B2", "B3"}) {
		t.Fatalf("the branches under D1: %v", got)
	}

	for _, c := range []struct {
		name, id string
		opts     []authzenreq.Option
		base     string
		headers  []string
	}{
		{"the query parameter", "B5", []authzenreq.Option{authzenreq.Fresh()}, w.check.URL, nil},
		{"the lever of the context", "B6", nil, w.leverURL, []string{harness.ConsistencyHeader, "higher"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w.source("branch", c.id, "Branch "+c.id, "R1")
			if allowed(w.check.URL, c.id, nil) {
				t.Fatalf("%s: an ordinary request saw a resource that was added to the source a moment ago", c.id)
			}
			if !allowed(c.base, c.id, c.opts, c.headers...) {
				t.Errorf("%s: the evaluation did not ask the source again", c.id)
			}
			if got := listed(c.base, c.opts, c.headers...); !slices.Contains(got, c.id) {
				t.Errorf("%s: the search did not ask the source again: %v", c.id, got)
			}
		})
	}
}
