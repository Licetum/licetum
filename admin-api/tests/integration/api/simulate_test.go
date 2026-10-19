//go:build integration

package api_test

// Integration tests of the simulation of the AuthZEN API for administrators (ADR 0001, 8.5): the requests and the answers of the PDP,
// for the subject and the properties a request names, with the explanation always on and nothing recorded.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// simulate sends a request that authzenreq built for the PDP to the simulation of the service, as the holder of the headers: the
// same body at the same endpoint below /simulate, on the admin API.
func (w *world) simulate(req authzenreq.Request, headers ...string) (int, map[string]any) {
	w.t.Helper()
	path := strings.Replace(req.Path, "/service/"+w.org+"/", "/service/"+w.org+"/simulate/", 1)
	if path == req.Path {
		w.t.Fatalf("the request is not about the service %s: %s", w.org, req.Path)
	}
	status, header, raw := w.doWithHeader(req.Method, w.admin.URL+path, req.Body, headers...)
	return status, authzenAnswer(w.t, status, header, raw)
}

// reasonOf is the explanation an evaluation carries, or nil.
func reasonOf(answer map[string]any) map[string]any {
	context, _ := answer["context"].(map[string]any)
	reason, _ := context["reason_admin"].(map[string]any)
	return reason
}

func TestSimulationAnswersLikeThePDPForThePropertiesGivenAndExplains(t *testing.T) {
	w := newWorld(t)
	sales := w.must("POST", "/group", map[string]any{"name": "sales"})["id"].(string)
	w.must("POST", "/group/"+sales+"/member", map[string]any{"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	w.grant(map[string]any{"type_resource_id": w.branch, "member_of_group_id": sales, "access": []string{"write"}})
	w.rule("division", "D1", nil)
	w.rule("rrs", "R1", deny("read"))

	// The properties are the claims: with the department the group rule holds, without it it does not.
	department := authzenreq.WithProperties(map[string]any{"department": "sales"})
	status, out := w.simulate(authzenreq.Evaluation(w.org, "dave", authzenreq.On("branch", "B3"), "write", department))
	if status != http.StatusOK || out["decision"] != true {
		t.Fatalf("dave from sales writes B3: %d %v", status, out)
	}
	reason := reasonOf(out)
	if reason == nil || reason["decision"] != "allowed" || len(reason["granted_by"].([]any)) != 1 {
		t.Fatalf("the evaluation always explains itself: %v", out)
	}
	status, out = w.simulate(authzenreq.Evaluation(w.org, "dave", authzenreq.On("branch", "B3"), "write"))
	if reason := reasonOf(out); status != http.StatusOK || out["decision"] != false || reason == nil || reason["decision"] != "no_access" {
		t.Fatalf("dave without properties: %d %v", status, out)
	}

	// The same decision as the PDP gives to a PEP client for the same person, and the denial explained.
	client := w.pepClient(w.org, "evaluator", db.PEPKindAPIKey, engine.PEPEvaluate)
	key, _ := w.pepKey(client)
	for _, id := range []string{"B1", "B3"} {
		question := authzenreq.Evaluation(w.org, hierarchySubject, authzenreq.On("branch", id), "read")
		pdp, header, raw := w.askWithHeader(question, key)
		simulated, sim := w.simulate(question)
		if pdp != http.StatusOK || simulated != http.StatusOK {
			t.Fatalf("%s: the PDP says %d, the simulation %d", id, pdp, simulated)
		}
		if got := authzenAnswer(t, pdp, header, raw)["decision"]; got != sim["decision"] {
			t.Errorf("%s: the PDP says %v, the simulation %v", id, got, sim["decision"])
		}
	}
	_, denied := w.simulate(authzenreq.Evaluation(w.org, hierarchySubject, authzenreq.On("branch", "B1"), "read"))
	if reason := reasonOf(denied); denied["decision"] != false || reason == nil || reason["decision"] != "denied" || len(reason["denials"].([]any)) != 1 {
		t.Fatalf("the denial of R1 explained: %v", denied)
	}

	// A batch, with a default subject, the semantic of the options, and each answer explained. The batch is cut at the stopping
	// evaluation, as the PDP cuts it: B3 is allowed, B1 is denied and B4 is not decided.
	status, out = w.simulate(authzenreq.Evaluations(w.org, hierarchySubject, []authzenreq.Question{
		{Resource: authzenreq.On("branch", "B3"), Action: "read"},
		{Resource: authzenreq.On("branch", "B1"), Action: "read"},
		{Resource: authzenreq.On("branch", "B4"), Action: "read"},
	}, authzenreq.Semantic("deny_on_first_deny")))
	evals, _ := out["evaluations"].([]any)
	if status != http.StatusOK || len(evals) != 2 || evals[0].(map[string]any)["decision"] != true || evals[1].(map[string]any)["decision"] != false {
		t.Fatalf("evaluations: %d %v", status, out)
	}
	for i, e := range evals {
		if reasonOf(e.(map[string]any)) == nil {
			t.Errorf("the evaluation %d of a batch carries no explanation: %v", i, e)
		}
	}
}

func TestSimulationSearchesAndTheirPagingAreThePDPs(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	w.sendPeople(w.org, person("user-1", "User One", nil), person("nobody", "Nobody", nil))

	status, out := w.simulate(authzenreq.SearchResource(w.org, hierarchySubject, "branch", "read", authzenreq.Page(2, "")))
	results, _ := out["results"].([]any)
	token, _ := out["page"].(map[string]any)["next_token"].(string)
	if status != http.StatusOK || len(results) != 2 || token == "" {
		t.Fatalf("resource search, a page of two: %d %v", status, out)
	}
	status, out = w.simulate(authzenreq.SearchResource(w.org, hierarchySubject, "branch", "read", authzenreq.Page(2, token)))
	if rest, _ := out["results"].([]any); status != http.StatusOK || len(rest) != 1 || rest[0].(map[string]any)["id"] != "B3" {
		t.Fatalf("the next page: %d %v", status, out)
	}

	status, out = w.simulate(authzenreq.SearchAction(w.org, hierarchySubject, authzenreq.On("branch", "B2")))
	if actions, _ := out["results"].([]any); status != http.StatusOK || len(actions) != 1 || actions[0].(map[string]any)["name"] != "read" {
		t.Fatalf("action search: %d %v", status, out)
	}

	status, out = w.simulate(authzenreq.SearchSubject(w.org, authzenreq.On("branch", "B1"), "read"))
	if people, _ := out["results"].([]any); status != http.StatusOK || len(people) != 1 || people[0].(map[string]any)["id"] != hierarchySubject {
		t.Fatalf("subject search: %d %v", status, out)
	}
	// Nothing was recorded: a PEP client's subject search would be audited as a call on behalf of people.
	if n := w.countRows(`SELECT count(*) FROM delegated_call`); n != 0 {
		t.Errorf("the simulation wrote %d records of delegated calls", n)
	}
}

func TestSimulationIsForAdministratorsAndChecksTheShapeOfTheRequest(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	whole := model.PlatformTypeResourceID(model.PlatformService).String()
	w.must("POST", "/assignment_rule", map[string]any{"type_resource_id": whole, "resource_id": w.org, "subject": "reader", "access": []string{"read"}})
	reader := "Bearer " + w.token(jwt.MapClaims{"preferred_username": "reader"})
	stranger := "Bearer " + w.token(jwt.MapClaims{"preferred_username": "stranger"})
	question := authzenreq.Evaluation(w.org, hierarchySubject, authzenreq.On("branch", "B1"), "read")

	if status, _ := w.simulate(question, "Authorization", reader); status != http.StatusOK {
		t.Errorf("a reader of the service simulates: %d", status)
	}
	if status, _ := w.simulate(question, "Authorization", stranger); status != http.StatusForbidden {
		t.Errorf("a stranger simulates: %d", status)
	}
	if status, _ := w.simulate(question, "Authorization", ""); status != http.StatusUnauthorized {
		t.Errorf("without a token: %d", status)
	}

	resource := map[string]any{"type": "branch", "id": "B1"}
	read := map[string]any{"name": "read"}
	for name, bad := range map[string]map[string]any{
		"no action":     {"subject": map[string]any{"type": "user", "id": "x"}, "resource": resource},
		"no subject id": {"subject": map[string]any{"type": "user"}, "resource": resource, "action": read},
		"a group":       {"subject": map[string]any{"type": "user", "id": "group:1"}, "resource": resource, "action": read},
	} {
		malformed := question
		malformed.Body = bad
		if status, out := w.simulate(malformed); status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %v, want a refusal", name, status, out)
		}
	}
	status, out := w.simulate(authzenreq.Evaluation(w.org, hierarchySubject, authzenreq.On("nope", "X"), "read"))
	if reason := reasonOf(out); status != http.StatusOK || out["decision"] != false || reason == nil || !strings.Contains(reason["decision"].(string), "not_found") {
		t.Errorf("an unknown type: %d %v", status, out)
	}
}
