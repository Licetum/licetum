//go:build integration

package api_test

// The freshness of a request is set by the process that serves it (engine.WithConsistency; a test harness does it with
// harness.Levers, from the header X-Test-Consistency that authzenreq.Fresh sets) and by nobody else: the PDP takes no query
// parameter for it. These tests drive every route of the AuthZEN API that reads the freshness, and compare what the engine does,
// and check that the parameters of the previous release (?consistency=, ?at_least_as_fresh=) do nothing.

import (
	"net/http"
	"slices"
	"testing"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// leverWorld is the hierarchy world, with read on D1 for hierarchySubject.
type leverWorld struct {
	*world
	alice string // the token of hierarchySubject, who reads everything under D1
}

func newLeverWorld(t *testing.T) *leverWorld {
	t.Helper()
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1"})
	return &leverWorld{world: w, alice: w.token(map[string]any{"preferred_username": hierarchySubject})}
}

// bypasses are the answers that skipped the result caches so far, as the PDP counts them: the permissions, the checks and the
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

// leverRoute is a route of the PDP that reads the freshness of the request, with the request that asks it a question.
type leverRoute struct {
	name string
	// build makes the request; with fresh it carries the lever.
	build func(fresh bool) authzenreq.Request
}

func (w *leverWorld) routes() []leverRoute {
	authzen := func(build func(...authzenreq.Option) authzenreq.Request) func(bool) authzenreq.Request {
		return func(fresh bool) authzenreq.Request {
			if fresh {
				return build(authzenreq.Fresh())
			}
			return build()
		}
	}
	alice := authzenreq.SubjectOf(w.alice)
	b1 := authzenreq.On("branch", "B1")
	return []leverRoute{
		{"evaluation", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.Evaluation(w.ref(w.org), alice, b1, "read", o...)
		})},
		// The batch endpoint without evaluations is one evaluation, a code path of its own.
		{"evaluations of one", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			one := authzenreq.Evaluation(w.ref(w.org), alice, b1, "read", o...)
			batch := authzenreq.Evaluations(w.ref(w.org), alice, nil, o...)
			batch.Body = one.Body
			return batch
		})},
		{"evaluations", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.Evaluations(w.ref(w.org), alice, []authzenreq.Question{{Resource: b1, Action: "read"}, {Resource: authzenreq.On("branch", "B3"), Action: "write"}}, o...)
		})},
		{"search resource", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchResource(w.ref(w.org), alice, "branch", "read", o...)
		})},
		{"search action of a resource", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchAction(w.ref(w.org), alice, b1, o...)
		})},
		{"search action of a type", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchAction(w.ref(w.org), alice, authzenreq.Whole("branch"), o...)
		})},
		{"search subject", authzen(func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchSubject(w.ref(w.org), b1, "read", o...)
		})},
	}
}

// A request that carries the lever skips the result cache, as many times as the route looks it up. A request without it does
// not, and neither does one that carries the public parameters of the previous release: nothing a caller sends sets the lever.
func TestTheConsistencyLeverSkipsTheCachesAndTheOldQueryParametersDoNothing(t *testing.T) {
	w := newLeverWorld(t)
	// Dave is in the directory of the service, so that the subject search has somebody to decide.
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "dave", "access": []string{"read"}})
	w.sendPeople(w.org, person("dave", "Dave", nil))

	for _, r := range w.routes() {
		t.Run(r.name, func(t *testing.T) {
			// take sends the request and returns how many answers it took from the data and not from the caches.
			take := func(req authzenreq.Request) bypassed {
				t.Helper()
				permission, check, search := w.bypasses()
				status, body := w.ask(req, w.alice)
				if status != http.StatusOK {
					t.Fatalf("%s: %d %s", r.name, status, body)
				}
				afterPermission, afterCheck, afterSearch := w.bypasses()
				return bypassed{afterPermission - permission, afterCheck - check, afterSearch - search}
			}

			// The first request synchronizes what it needs.
			take(r.build(false))

			if got := take(r.build(false)); got != (bypassed{}) {
				t.Errorf("a request that asks for nothing skipped the caches: %+v", got)
			}
			if take(r.build(true)) == (bypassed{}) {
				t.Fatalf("the lever did not skip the caches")
			}
			old := r.build(false)
			for _, query := range []string{"?consistency=higher", "?at_least_as_fresh=999999999", "?consistency=higher&at_least_as_fresh=9"} {
				old.Path = r.build(false).Path + query
				if got := take(old); got != (bypassed{}) {
					t.Errorf("%s skipped the caches: %+v", query, got)
				}
			}
		})
	}
}

// A request with the lever asks the sources again: a resource added to the source since the last synchronization is there at
// once, where an ordinary request still answers from the data it has (a source is asked again once its throttle has run out,
// 30 seconds, and the throttle of this test is far from it).
func TestTheConsistencyLeverRereadsTheSources(t *testing.T) {
	w := newLeverWorld(t)
	alice := authzenreq.SubjectOf(w.alice)
	allowed := func(id string, opts ...authzenreq.Option) bool {
		t.Helper()
		status, body := w.ask(authzenreq.Evaluation(w.ref(w.org), alice, authzenreq.On("branch", id), "read", opts...), w.alice)
		ok, err := authzenreq.Allowed(status, body)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	listed := func(opts ...authzenreq.Option) []string {
		t.Helper()
		status, body := w.ask(authzenreq.SearchResource(w.ref(w.org), alice, "branch", "read", opts...), w.alice)
		ids, _, err := authzenreq.ResourceIDs(status, body)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}

	// The first synchronization of the types.
	if got := listed(); !slices.Equal(got, []string{"B1", "B2", "B3"}) {
		t.Fatalf("the branches under D1: %v", got)
	}

	w.source("branch", "B5", "Branch B5", "R1")
	if allowed("B5") {
		t.Fatal("an ordinary request saw a resource that was added to the source a moment ago")
	}
	if !allowed("B5", authzenreq.Fresh()) {
		t.Error("the evaluation did not ask the source again")
	}
	if got := listed(authzenreq.Fresh()); !slices.Contains(got, "B5") {
		t.Errorf("the search did not ask the source again: %v", got)
	}
}
