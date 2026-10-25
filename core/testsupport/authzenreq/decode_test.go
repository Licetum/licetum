package authzenreq_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The answers below are made of the response types the server marshals (core/model/authzen.go), and of the answers of the
// specification.

func TestDecisionsOfASingleEvaluation(t *testing.T) {
	for _, want := range []bool{true, false} {
		body := marshal(t, model.AuthzenEvaluationResponse{Decision: want})
		got, err := authzenreq.Allowed(http.StatusOK, body)
		if err != nil || got != want {
			t.Errorf("%s: %v %v", body, got, err)
		}
	}
}

func TestDecisionsCarryTheExplanation(t *testing.T) {
	granting := model.RuleRef{RuleID: uuid.MustParse("6f1c1d52-33a5-4d0e-9d0e-0a3a8a0d7c11"), Effect: "allow", Access: []string{"read"}, AppliesTo: "alice"}
	denying := model.RuleRef{RuleID: uuid.MustParse("6f1c1d52-33a5-4d0e-9d0e-0a3a8a0d7c12"), Effect: "deny", Access: []string{"read"}, AppliesTo: "group"}
	explanation := model.Explanation{
		Decision:  "denied",
		GrantedBy: []model.RuleRef{granting},
		Denials:   []model.DenialRef{{Rule: denying, Level: "read", LiftedBy: []model.RuleRef{}}},
	}
	body := marshal(t, model.AuthzenEvaluationResponse{Decision: false, Context: map[string]any{"reason_admin": explanation}})
	ds, err := authzenreq.Decisions(http.StatusOK, body)
	if err != nil || len(ds) != 1 {
		t.Fatalf("%v %v", ds, err)
	}
	if ds[0].Allowed || ds[0].Explanation == nil || !reflect.DeepEqual(*ds[0].Explanation, explanation) {
		t.Errorf("the explanation is lost: %+v", ds[0])
	}
	if _, ok := ds[0].Context["reason_admin"]; !ok {
		t.Errorf("the context is lost: %+v", ds[0].Context)
	}
}

func TestDecisionsOfABatchKeepTheOrder(t *testing.T) {
	yes, no := true, false
	body := marshal(t, model.AuthzenEvaluationsResponse{Evaluations: []model.AuthzenEvaluationResponse{
		{Decision: yes}, {Decision: no}, {Decision: yes},
	}})
	ds, err := authzenreq.Decisions(http.StatusOK, body)
	if err != nil {
		t.Fatal(err)
	}
	var got []bool
	for _, d := range ds {
		got = append(got, d.Allowed)
	}
	if !reflect.DeepEqual(got, []bool{true, false, true}) {
		t.Errorf("got %v", got)
	}
	if _, err := authzenreq.Allowed(http.StatusOK, body); err == nil {
		t.Error("Allowed is the decision of one evaluation, not of three")
	}
}

// A request of the /evaluations endpoint without evaluations is one evaluation, and the answer is the decision itself.
func TestDecisionsOfABatchEndpointWithoutEvaluations(t *testing.T) {
	yes := true
	ds, err := authzenreq.Decisions(http.StatusOK, marshal(t, model.AuthzenEvaluationsResponse{Decision: &yes}))
	if err != nil || len(ds) != 1 || !ds[0].Allowed {
		t.Fatalf("%v %v", ds, err)
	}
}

// The batch of the specification's example: a denial may carry a reason in its context, and that is not an error.
func TestDecisionsOfTheSpecificationExample(t *testing.T) {
	body := []byte(`{"evaluations": [
		{"decision": true},
		{"decision": false, "context": {"reason": "resource not found"}},
		{"decision": false, "context": {"code": "200", "reason": "deny_on_first_deny"}}
	]}`)
	ds, err := authzenreq.Decisions(http.StatusOK, body)
	if err != nil || len(ds) != 3 {
		t.Fatalf("%v %v", ds, err)
	}
	if !ds[0].Allowed || ds[1].Allowed || ds[2].Allowed {
		t.Errorf("%+v", ds)
	}
	if ds[0].Context != nil || ds[1].Context["reason"] != "resource not found" || ds[2].Context["reason"] != "deny_on_first_deny" || ds[2].Context["code"] != "200" {
		t.Errorf("the context of the answers is lost: %+v", ds)
	}
}

// The hazard that this package exists for: an evaluation of a batch that failed is decision false with context.error.
func TestAnEvaluationThatCouldNotBeDecidedIsNotADenial(t *testing.T) {
	body := marshal(t, model.AuthzenEvaluationsResponse{Evaluations: []model.AuthzenEvaluationResponse{
		{Decision: true},
		{Decision: false, Context: map[string]any{"error": map[string]any{"status": http.StatusForbidden, "message": "subject mismatch"}}},
		{Decision: false},
	}})
	ds, err := authzenreq.Decisions(http.StatusOK, body)
	if ds != nil {
		t.Errorf("no decisions must come out of an answer with a failure: %+v", ds)
	}
	var ee *authzenreq.EvaluationError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v", err)
	}
	if ee.Index != 1 || ee.Status != http.StatusForbidden || ee.Message != "subject mismatch" {
		t.Errorf("%+v", ee)
	}
	if !strings.Contains(err.Error(), "subject mismatch") {
		t.Error(err)
	}
	// The same for the helper of one evaluation, which never says false for a failure.
	single := marshal(t, model.AuthzenEvaluationResponse{Context: map[string]any{"error": map[string]any{"status": 400, "message": "x"}}})
	if allowed, err := authzenreq.Allowed(http.StatusOK, single); allowed || !errors.As(err, &ee) {
		t.Errorf("%v %v", allowed, err)
	}
}

func TestAnAnswerThatIsNotAnswerIsAnError(t *testing.T) {
	apiError := []byte(`{"detail":"The token is invalid","extra":{"type":"generic","code":"invalid_token"}}`)
	for name, c := range map[string]struct {
		status int
		body   []byte
		want   string
	}{
		"an error body":               {http.StatusUnauthorized, apiError, "The token is invalid"},
		"an error that is no JSON":    {http.StatusBadGateway, []byte("<html>bad gateway</html>"), "bad gateway"},
		"no decision":                 {http.StatusOK, []byte(`{}`), "no decision"},
		"no decision in a batch":      {http.StatusOK, []byte(`{"evaluations":[{"decision":true},{"context":{}}]}`), "evaluation 1 has no decision"},
		"not JSON":                    {http.StatusOK, []byte("nope"), "not JSON"},
		"a context that is no object": {http.StatusOK, []byte(`{"decision":true,"context":"x"}`), "context"},
	} {
		t.Run(name, func(t *testing.T) {
			ds, err := authzenreq.Decisions(c.status, c.body)
			if err == nil || ds != nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, %v; want an error with %q", ds, err, c.want)
			}
		})
	}
	// Whatever the body is, a status that is not 200 is a StatusError, so that a test can say what it expected.
	_, err := authzenreq.Decisions(http.StatusUnauthorized, apiError)
	var se *authzenreq.StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnauthorized || se.Detail != "The token is invalid" {
		t.Errorf("%v", err)
	}
	// A body of a success that says false must not be read when the status is an error.
	if _, err := authzenreq.Allowed(http.StatusForbidden, marshal(t, model.AuthzenEvaluationResponse{Decision: false})); !errors.As(err, &se) {
		t.Errorf("%v", err)
	}
}

func TestStatusErrorKeepsLongBodiesShort(t *testing.T) {
	_, err := authzenreq.Decisions(http.StatusInternalServerError, []byte(strings.Repeat("x", 5000)))
	if err == nil || len(err.Error()) > 300 {
		t.Fatalf("%d characters", len(err.Error()))
	}
}

func resource(id, name string) model.AuthzenResource {
	return model.AuthzenResource{Type: "branch", ID: id, Properties: map[string]any{"name": name}}
}

func TestResourceIDsAndTheNextPage(t *testing.T) {
	total := 3
	body := marshal(t, model.AuthzenResourceSearchResponse{
		Page:    &model.AuthzenPageResponse{NextToken: "tok", Count: 2, Total: &total},
		Results: []model.AuthzenResource{resource("b1", "One"), resource("b2", "Two")},
	})
	ids, next, err := authzenreq.ResourceIDs(http.StatusOK, body)
	if err != nil || !reflect.DeepEqual(ids, []string{"b1", "b2"}) || next != "tok" {
		t.Fatalf("%v %q %v", ids, next, err)
	}

	last := marshal(t, model.AuthzenResourceSearchResponse{Page: &model.AuthzenPageResponse{Count: 1}, Results: []model.AuthzenResource{resource("b3", "Three")}})
	if ids, next, err = authzenreq.ResourceIDs(http.StatusOK, last); err != nil || !reflect.DeepEqual(ids, []string{"b3"}) || next != "" {
		t.Fatalf("%v %q %v", ids, next, err)
	}

	empty := marshal(t, model.AuthzenResourceSearchResponse{Page: &model.AuthzenPageResponse{}, Results: []model.AuthzenResource{}})
	if ids, next, err = authzenreq.ResourceIDs(http.StatusOK, empty); err != nil || len(ids) != 0 || next != "" {
		t.Fatalf("%v %q %v", ids, next, err)
	}
	// The specification's answer has no page when everything fits.
	if ids, _, err = authzenreq.ResourceIDs(http.StatusOK, []byte(`{"results":[{"type":"account","id":"123"}]}`)); err != nil || !reflect.DeepEqual(ids, []string{"123"}) {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestResourceIDsRefuseAnErrorStatus(t *testing.T) {
	_, _, err := authzenreq.ResourceIDs(http.StatusForbidden, []byte(`{"detail":"no"}`))
	var se *authzenreq.StatusError
	if !errors.As(err, &se) || se.Status != http.StatusForbidden {
		t.Errorf("%v", err)
	}
}

func TestActionsAndSubjectIDs(t *testing.T) {
	actions := marshal(t, model.AuthzenActionSearchResponse{
		Page:    &model.AuthzenPageResponse{NextToken: "more", Count: 2},
		Results: []model.AuthzenAction{{Name: "read"}, {Name: "write"}},
	})
	names, next, err := authzenreq.Actions(http.StatusOK, actions)
	if err != nil || !reflect.DeepEqual(names, []string{"read", "write"}) || next != "more" {
		t.Fatalf("%v %q %v", names, next, err)
	}

	subjects := marshal(t, model.AuthzenSubjectSearchResponse{
		Page:    &model.AuthzenPageResponse{Count: 2},
		Results: []model.AuthzenSubject{{Type: "user", ID: "alice", Properties: map[string]any{"name": "Alice"}}, {Type: "user", ID: "bob"}},
	})
	ids, next, err := authzenreq.SubjectIDs(http.StatusOK, subjects)
	if err != nil || !reflect.DeepEqual(ids, []string{"alice", "bob"}) || next != "" {
		t.Fatalf("%v %q %v", ids, next, err)
	}

	// An empty directory gives an empty result.
	empty := marshal(t, model.AuthzenSubjectSearchResponse{Results: []model.AuthzenSubject{}})
	if ids, _, err = authzenreq.SubjectIDs(http.StatusOK, empty); err != nil || len(ids) != 0 {
		t.Fatalf("%v %v", ids, err)
	}
}
