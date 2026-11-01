//go:build integration

package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

func TestLiteralSubjectTypesSeparateCachedDecisionsAndMixedBatches(t *testing.T) {
	w := newWorld(t)
	key, _ := w.pepKey(w.pepClient(w.org, "typed-evaluator", db.PEPKindAPIKey, engine.PEPEvaluate, engine.PEPSearchResource, engine.PEPSearchAction, engine.PEPSearchSubject))
	w.grant(map[string]any{"type_resource_id": w.branch, "subject": "typed-person", "subject_type": "user", "access": []string{"read"}})
	w.grant(map[string]any{"type_resource_id": w.branch, "subject": "typed-person", "subject_type": "service", "access": []string{"write"}})
	waitFor(t, "the typed rule to reach the PDP", func() bool {
		status, raw := w.ask(authzenreq.Evaluation(w.ref(w.org), "typed-person", authzenreq.On("branch", "B3"), "read"), key)
		allowed, err := authzenreq.Allowed(status, raw)
		return err == nil && allowed
	})

	// No consistency override: alternating the same ID exercises the normal answer cache.
	for round := 0; round < 3; round++ {
		for _, typ := range []string{"user", "service", "robot"} {
			for _, action := range []string{"read", "write"} {
				req := authzenreq.Evaluation(w.ref(w.org), "typed-person", authzenreq.On("branch", "B3"), action,
					authzenreq.WithSubjectType(typ), authzenreq.WithProperties(map[string]any{"type": "user", "subject_type": "user"}))
				status, header, raw := w.askWithHeader(req, key)
				out := authzenAnswer(t, status, header, raw)
				want := typ == "user" && action == "read" || typ == "service" && action == "write"
				if status != http.StatusOK || out["decision"] != want {
					t.Fatalf("round %d type %s action %s: %d %v, want %v", round, typ, action, status, out, want)
				}
				simulation := req
				simulation.Path = strings.Replace(req.Path, "/service/org/", "/service/"+w.org+"/", 1)
				status, out = w.simulate(simulation)
				if status != http.StatusOK || out["decision"] != want {
					t.Fatalf("simulation type %s action %s: %d %v, want %v", typ, action, status, out, want)
				}
			}
		}
	}
	for _, typ := range []string{"user", "service", "robot"} {
		entry := person("typed-person", "Typed person", nil)
		entry["type"] = typ
		w.sendPeople(w.org, entry)
	}
	for _, typ := range []string{"user", "service", "robot"} {
		opts := authzenreq.WithSubjectType(typ)
		status, raw := w.ask(authzenreq.SearchAction(w.ref(w.org), "typed-person", authzenreq.On("branch", "B3"), opts), key)
		actions, _, err := authzenreq.Actions(status, raw)
		want := ""
		if typ == "user" {
			want = "read"
		}
		if typ == "service" {
			want = "write"
		}
		if err != nil || strings.Join(actions, ",") != want {
			t.Fatalf("%s action search: %v %v", typ, actions, err)
		}
		for _, action := range []string{"read", "write"} {
			allowed := typ == "user" && action == "read" || typ == "service" && action == "write"
			status, raw = w.ask(authzenreq.SearchResource(w.ref(w.org), "typed-person", "branch", action, opts), key)
			ids, _, err := authzenreq.ResourceIDs(status, raw)
			if err != nil || slices.Contains(ids, "B3") != allowed {
				t.Fatalf("%s %s resource search: %v %v", typ, action, ids, err)
			}
			status, raw = w.ask(authzenreq.SearchSubject(w.ref(w.org), authzenreq.On("branch", "B3"), action, opts), key)
			ids, _, err = authzenreq.SubjectIDs(status, raw)
			if err != nil || slices.Contains(ids, "typed-person") != allowed {
				t.Fatalf("%s %s subject search: %v %v", typ, action, ids, err)
			}
		}
	}

	evaluations := []any{}
	for _, typ := range []string{"user", "service", "robot"} {
		for _, action := range []string{"read", "write"} {
			item := authzenEval("branch", "B3", action)
			item["subject"] = map[string]any{"type": typ, "id": "typed-person"}
			evaluations = append(evaluations, item)
		}
	}
	status, header, raw := w.doWithHeader(http.MethodPost, w.check.URL+"/service/org/access/v1/evaluations", map[string]any{"evaluations": evaluations}, "Authorization", "Bearer "+key)
	out := authzenAnswer(t, status, header, raw)
	if status != http.StatusOK || !equalBools(decisionsOf(t, out), true, false, false, true, false, false) {
		t.Fatalf("mixed types: %d %v", status, out)
	}
}
