//go:build integration

package api_test

// The questions a PEP client puts to the AuthZEN API about the people of its application. The person is subject.id and their
// claims are subject.properties: the claims decide, the name of the person is the preferred_username unless the caller sends
// one, a subject that looks like a group is refused, and the claims are bounded.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// propertiesWorld is the hierarchy world with two groups and a rule for each: everyone from sales reads under D1, and the
// people of the group "team", which is alice by her name, read under D2. The service has the PEP client docs-backend.
type propertiesWorld struct {
	*world
	group  string // the group of the people from sales
	client string // the API key of the PEP client docs-backend
}

func newPropertiesWorld(t *testing.T) *propertiesWorld {
	t.Helper()
	w := newWorld(t)
	group := w.must("POST", "/group", map[string]any{"name": "sales"})["id"].(string)
	w.must("POST", "/group/"+group+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	team := w.must("POST", "/group", map[string]any{"name": "team"})["id"].(string)
	w.must("POST", "/group/"+team+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "preferred_username", "op": "eq", "value": "alice"}}})
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "member_of_group_id": group, "access": []string{"read"}})
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D2", "member_of_group_id": team, "access": []string{"read"}})
	key, _ := w.pepKey(w.pepClient(w.org, "docs-backend", db.PEPKindAPIKey, engine.PEPEvaluate, engine.PEPSearchResource, engine.PEPSearchAction))
	return &propertiesWorld{world: w, group: group, client: key}
}

// reads asks, as the PEP client, whether the person may read the branch; the claims of the person are the properties.
func (w *propertiesWorld) reads(person string, properties map[string]any, branch string) bool {
	w.t.Helper()
	opts := []authzenreq.Option{}
	if properties != nil {
		opts = append(opts, authzenreq.WithProperties(properties))
	}
	status, body := w.ask(authzenreq.Evaluation(w.ref(w.org), person, authzenreq.On("branch", branch), "read", opts...), w.client)
	allowed, err := authzenreq.Allowed(status, body)
	if err != nil {
		w.t.Fatalf("%s reads %s: %v", person, branch, err)
	}
	return allowed
}

func TestAuthzenPropertiesDecideForAPerson(t *testing.T) {
	w := newPropertiesWorld(t)

	// The claims the caller sends decide.
	if !w.reads("dave", map[string]any{"department": "sales"}, "B1") {
		t.Fatal("dave from sales reads B1")
	}
	if w.reads("dave", nil, "B1") {
		t.Fatal("dave without the claim does not")
	}
	if w.reads("dave", map[string]any{"department": "hr"}, "B1") {
		t.Fatal("dave from another department does not")
	}
	// preferred_username is the person's name unless the caller sends one, so a rule that names a person by it applies.
	if !w.reads("alice", nil, "B4") {
		t.Fatal("alice is in the group team by her name and reads B4")
	}
	if w.reads("bob", nil, "B4") {
		t.Fatal("bob is not")
	}
	if !w.reads("bob", map[string]any{"preferred_username": "alice"}, "B4") {
		t.Fatal("the caller vouches for the claims, preferred_username included")
	}
}

func TestAuthzenASubjectThatLooksLikeAGroupIsRefused(t *testing.T) {
	w := newPropertiesWorld(t)
	groupSubject := "group:" + w.group

	// A subject like that would be taken for the group itself: the evaluation is refused.
	status, header, body := w.askWithHeader(authzenreq.Evaluation(w.ref(w.org), groupSubject, authzenreq.On("branch", "B1"), "read"), w.client)
	requireAuthzenError(t, status, header, body, http.StatusBadRequest, "delegation_subject_invalid")
	status, header, body = w.askWithHeader(authzenreq.SearchResource(w.ref(w.org), groupSubject, "branch", "read"), w.client)
	requireAuthzenError(t, status, header, body, http.StatusBadRequest, "delegation_subject_invalid")

	// In a batch it is a denial that says why, not a failure of the batch, and not a decision.
	status, body = w.ask(authzenreq.Evaluations(w.ref(w.org), groupSubject, []authzenreq.Question{
		{Resource: authzenreq.On("branch", "B1"), Action: "read"}}), w.client)
	var failed *authzenreq.EvaluationError
	if _, err := authzenreq.Decisions(status, body); !errors.As(err, &failed) || failed.Status != http.StatusBadRequest {
		t.Fatalf("a group as the subject of a batch: %d %s (%v)", status, body, err)
	}
}

func TestAuthzenTheClaimsOfAPersonAreBounded(t *testing.T) {
	w := newPropertiesWorld(t)
	claims := func(n int) map[string]any {
		out := map[string]any{}
		for i := range n {
			out[fmt.Sprintf("claim%03d", i)] = "x"
		}
		return out
	}
	about := func(properties map[string]any) (int, http.Header, []byte) {
		w.t.Helper()
		return w.askWithHeader(authzenreq.Evaluation(w.ref(w.org), "dave", authzenreq.On("branch", "B1"), "read", authzenreq.WithProperties(properties)), w.client)
	}

	// At most 100 claims.
	status, _, body := about(claims(100))
	if _, err := authzenreq.Allowed(status, body); err != nil {
		t.Fatalf("100 claims: %v", err)
	}
	status, header, body := about(claims(101))
	requireAuthzenError(t, status, header, body, http.StatusBadRequest, "delegation_claims_invalid")

	// At most 16 KiB of them.
	status, _, body = about(map[string]any{"note": strings.Repeat("x", 10*1024)})
	if _, err := authzenreq.Allowed(status, body); err != nil {
		t.Fatalf("10 KiB of claims: %v", err)
	}
	status, header, body = about(map[string]any{"note": strings.Repeat("x", 17*1024)})
	requireAuthzenError(t, status, header, body, http.StatusBadRequest, "delegation_claims_invalid")
}

func TestAuthzenTheResourcesOfAPersonAreSearchedByTheirProperties(t *testing.T) {
	w := newPropertiesWorld(t)
	search := func(person string, properties map[string]any) []string {
		w.t.Helper()
		opts := []authzenreq.Option{}
		if properties != nil {
			opts = append(opts, authzenreq.WithProperties(properties))
		}
		status, body := w.ask(authzenreq.SearchResource(w.ref(w.org), person, "branch", "read", opts...), w.client)
		ids, _, err := authzenreq.ResourceIDs(status, body)
		if err != nil {
			w.t.Fatalf("resources of %s: %v", person, err)
		}
		return ids
	}

	// Everything under D1 for a person from sales, nothing for one without the claim.
	if got := search("dave", map[string]any{"department": "sales"}); !slices.Equal(got, []string{"B1", "B2", "B3"}) {
		t.Fatalf("the branches of dave from sales: %v", got)
	}
	if got := search("dave", nil); len(got) != 0 {
		t.Fatalf("the branches of dave without the claim: %v", got)
	}
	// The name of the person is their preferred_username here as well: alice is in team, which reads under D2.
	if got := search("alice", nil); !slices.Equal(got, []string{"B4"}) {
		t.Fatalf("the branches of alice: %v", got)
	}
}
