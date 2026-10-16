//go:build integration

package api_test

// Integration tests of the AuthZEN Authorization API 1.0: the evaluations and their options, the three searches with paging,
// the questions about other people that a trusted client asks, and the discovery document.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func (w *world) authzen(path string, body any, claims jwt.MapClaims) (int, map[string]any) {
	w.t.Helper()
	headers := []string{}
	if claims != nil {
		headers = append(headers, "Authorization", "Bearer "+w.token(claims))
	}
	status, raw := w.do(http.MethodPost, w.check.URL+"/service/"+w.org+"/access/v1/"+path+"?consistency=higher", body, headers...)
	out := map[string]any{}
	if len(raw) > 0 {
		out = decodeMap(w.t, raw)
	}
	return status, out
}

func authzenEval(resourceType, resourceID, action string) map[string]any {
	resource := map[string]any{"type": resourceType}
	if resourceID != "" {
		resource["id"] = resourceID
	}
	return map[string]any{"resource": resource, "action": map[string]any{"name": action}}
}

func decisionsOf(t *testing.T, body map[string]any) []bool {
	t.Helper()
	var out []bool
	for _, e := range body["evaluations"].([]any) {
		out = append(out, e.(map[string]any)["decision"].(bool))
	}
	return out
}

func TestAuthzenEvaluationsSemanticsAndErrors(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	w.rule("rrs", "R1", deny("read"))
	self := jwt.MapClaims{"preferred_username": hierarchySubject}
	subject := map[string]any{"type": "user", "id": hierarchySubject}

	batch := func(semantic string, evaluations ...any) map[string]any {
		body := map[string]any{"subject": subject, "evaluations": evaluations}
		if semantic != "" {
			body["options"] = map[string]any{"evaluations_semantic": semantic}
		}
		status, out := w.authzen("evaluations", body, self)
		if status != http.StatusOK {
			t.Fatalf("evaluations: %d %v", status, out)
		}
		return out
	}
	// B3 is readable; B1 and B2 are under the denied R1.
	b3, b1 := authzenEval("branch", "B3", "read"), authzenEval("branch", "B1", "read")

	if got := decisionsOf(t, batch("", b3, b1, b3)); !equalBools(got, true, false, true) {
		t.Fatalf("execute_all is the default: %v", got)
	}
	denied := batch("deny_on_first_deny", b3, b1, b3)
	if got := decisionsOf(t, denied); !equalBools(got, true, false) {
		t.Fatalf("deny_on_first_deny stops at the first denial: %v", got)
	}
	reason := denied["evaluations"].([]any)[1].(map[string]any)["context"].(map[string]any)
	if reason["reason"] != "deny_on_first_deny" {
		t.Fatalf("the stopping denial says why: %v", reason)
	}
	if got := decisionsOf(t, batch("permit_on_first_permit", b1, b3, b3)); !equalBools(got, false, true) {
		t.Fatalf("permit_on_first_permit stops at the first permit: %v", got)
	}

	// An evaluation that names somebody else is a denial with its error, not a failure of the batch.
	other := map[string]any{"subject": map[string]any{"type": "user", "id": "someone-else"}, "resource": b3["resource"], "action": b3["action"]}
	out := batch("", b3, other, authzenEval("nothing", "", "read"))
	items := out["evaluations"].([]any)
	if len(items) != 3 || items[0].(map[string]any)["decision"] != true {
		t.Fatalf("batch with a bad evaluation: %v", out)
	}
	errInfo := items[1].(map[string]any)["context"].(map[string]any)["error"].(map[string]any)
	if items[1].(map[string]any)["decision"] != false || errInfo["status"] != float64(http.StatusForbidden) {
		t.Fatalf("a subject the caller may not ask about: %v", items[1])
	}
	if items[2].(map[string]any)["decision"] != false {
		t.Fatalf("an unknown type is a denial: %v", items[2])
	}

	// No evaluations: the top-level fields are one evaluation, and the answer is its decision.
	status, single := w.authzen("evaluations", map[string]any{"subject": subject, "resource": b3["resource"], "action": b3["action"]}, self)
	if status != http.StatusOK || single["decision"] != true || single["evaluations"] != nil {
		t.Fatalf("evaluations without evaluations: %d %v", status, single)
	}

	// What is not valid is a 400 (the specification has no 422), what is not allowed a 403, no token a 401.
	status, _ = w.authzen("evaluation", map[string]any{"subject": subject, "resource": map[string]any{"id": "B3"}, "action": b3["action"]}, self)
	if status != http.StatusBadRequest {
		t.Fatalf("a resource without a type: %d", status)
	}
	status, _ = w.authzen("evaluation", map[string]any{"subject": subject, "action": b3["action"]}, self)
	if status != http.StatusBadRequest {
		t.Fatalf("no resource: %d", status)
	}
	status, _ = w.authzen("evaluations", map[string]any{"subject": subject, "options": map[string]any{"evaluations_semantic": "first"},
		"evaluations": []any{b3}}, self)
	if status != http.StatusBadRequest {
		t.Fatalf("an unknown semantic: %d", status)
	}
	status, _ = w.authzen("evaluation", map[string]any{"subject": map[string]any{"type": "user", "id": "someone-else"},
		"resource": b3["resource"], "action": b3["action"]}, self)
	if status != http.StatusForbidden {
		t.Fatalf("another subject for a client that is not trusted: %d", status)
	}
	if status, _ = w.authzen("evaluation", map[string]any{"subject": subject, "resource": b3["resource"], "action": b3["action"]}, nil); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", status)
	}
}

func equalBools(got []bool, want ...bool) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAuthzenSearches(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	w.rule("branch", "B2", map[string]any{"access": []string{"write"}})
	self := jwt.MapClaims{"preferred_username": hierarchySubject}
	subject := map[string]any{"type": "user", "id": hierarchySubject}

	// Resources: B1, B2 and B3 are readable; the page of two names the next.
	search := func(path string, body map[string]any) map[string]any {
		status, out := w.authzen("search/"+path, body, self)
		if status != http.StatusOK {
			t.Fatalf("search %s: %d %v", path, status, out)
		}
		return out
	}
	resourceIDs := func(out map[string]any) []string {
		var got []string
		for _, r := range out["results"].([]any) {
			got = append(got, r.(map[string]any)["id"].(string))
		}
		return got
	}
	request := func(page map[string]any) map[string]any {
		body := map[string]any{"subject": subject, "resource": map[string]any{"type": "branch"}, "action": map[string]any{"name": "read"}}
		if page != nil {
			body["page"] = page
		}
		return body
	}
	first := search("resource", request(map[string]any{"limit": 2}))
	if got := resourceIDs(first); strings.Join(got, ",") != "B1,B2" {
		t.Fatalf("first page: %v", first)
	}
	page := first["page"].(map[string]any)
	if page["total"] != float64(3) || page["count"] != float64(2) || page["next_token"] == "" {
		t.Fatalf("page: %v", page)
	}
	if first["results"].([]any)[0].(map[string]any)["properties"].(map[string]any)["name"] != "Branch 1" {
		t.Fatalf("the name of the resource: %v", first)
	}
	second := search("resource", request(map[string]any{"limit": 2, "token": page["next_token"]}))
	if got := resourceIDs(second); strings.Join(got, ",") != "B3" || second["page"].(map[string]any)["next_token"] != "" {
		t.Fatalf("last page: %v", second)
	}
	// The token belongs to its request.
	status, out := w.authzen("search/resource", request(map[string]any{"limit": 1, "token": page["next_token"]}), self)
	requireErrorCode(t, status, []byte(mustJSON(out)), http.StatusBadRequest, "authzen_page_invalid")
	if got := search("resource", map[string]any{"subject": subject, "resource": map[string]any{"type": "branch"}, "action": map[string]any{"name": "write"}}); strings.Join(resourceIDs(got), ",") != "B2" {
		t.Fatalf("write is granted on B2 only: %v", got)
	}

	// Actions on a resource.
	actions := func(resource map[string]any) []string {
		var got []string
		for _, a := range search("action", map[string]any{"subject": subject, "resource": resource})["results"].([]any) {
			got = append(got, a.(map[string]any)["name"].(string))
		}
		return got
	}
	if got := actions(map[string]any{"type": "branch", "id": "B2"}); strings.Join(got, ",") != "read,write" {
		t.Fatalf("actions on B2: %v", got)
	}
	if got := actions(map[string]any{"type": "branch", "id": "B1"}); strings.Join(got, ",") != "read" {
		t.Fatalf("actions on B1: %v", got)
	}
	if got := actions(map[string]any{"type": "branch", "id": "B4"}); len(got) != 0 {
		t.Fatalf("actions on B4: %v", got)
	}

	// Subjects: only a trusted client may ask, and only the known people are listed.
	status, _ = w.authzen("search/subject", map[string]any{"subject": map[string]any{"type": "user"},
		"resource": map[string]any{"type": "branch", "id": "B1"}, "action": map[string]any{"name": "read"}}, self)
	if status != http.StatusForbidden {
		t.Fatalf("subject search by a client that is not trusted: %d", status)
	}
	w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{"docs-backend"}})
	if s, _ := w.env.permission(w.org, w.token(jwt.MapClaims{"preferred_username": "dave", "name": "Dave", "sub": "dave"})); s != http.StatusOK {
		t.Fatalf("permission as dave: %d", s)
	}
	waitFor(t, "dave in the registry", func() bool {
		s, raw := w.do("GET", w.admin.URL+"/subject?search=dave", nil)
		return s == http.StatusOK && decodeMap(t, raw)["total"] == float64(1)
	})
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "dave", "access": []string{"read"}})
	trusted := jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"}
	status, out = w.authzen("search/subject", map[string]any{"subject": map[string]any{"type": "user"},
		"resource": map[string]any{"type": "branch", "id": "B1"}, "action": map[string]any{"name": "read"}}, trusted)
	// The holder of the first tokens may be listed too (user-1 reads B1 through D1, and was seen); nobody else is.
	found := map[string]map[string]any{}
	results, _ := out["results"].([]any)
	for _, r := range results {
		entry := r.(map[string]any)
		found[entry["id"].(string)] = entry
	}
	dave, ok := found["dave"]
	if status != http.StatusOK || !ok || len(found) > 2 || dave["type"] != "user" || dave["properties"].(map[string]any)["name"] != "Dave" {
		t.Fatalf("subject search: %d %v", status, out)
	}
	if _, stranger := found["svc-docs-backend"]; stranger {
		t.Fatalf("the client itself has no access: %v", out)
	}
}

func TestAuthzenTrustedClientAsksAboutAPerson(t *testing.T) {
	w := newWorld(t)
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "subject": "dave", "access": []string{"read"}})
	w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{"docs-backend"}})
	trusted := jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"}
	dave := map[string]any{"type": "user", "id": "dave"}

	status, out := w.authzen("evaluation", map[string]any{"subject": dave, "resource": map[string]any{"type": "branch", "id": "B1"},
		"action": map[string]any{"name": "read"}}, trusted)
	if status != http.StatusOK || out["decision"] != true {
		t.Fatalf("a trusted client asks about dave: %d %v", status, out)
	}
	status, out = w.authzen("search/resource", map[string]any{"subject": dave, "resource": map[string]any{"type": "branch"},
		"action": map[string]any{"name": "read"}}, trusted)
	if status != http.StatusOK || len(out["results"].([]any)) != 3 {
		t.Fatalf("resources of dave: %d %v", status, out)
	}
	// A client that is not trusted may not.
	status, _ = w.authzen("evaluation", map[string]any{"subject": dave, "resource": map[string]any{"type": "branch", "id": "B1"},
		"action": map[string]any{"name": "read"}}, jwt.MapClaims{"azp": "stranger", "sub": "svc"})
	if status != http.StatusForbidden {
		t.Fatalf("a stranger asks about dave: %d", status)
	}

	// The calls are audited like the calls on behalf of a person.
	calls := w.must("GET", "/service/"+w.org+"/delegated_calls", nil)["items"].([]any)
	if len(calls) != 2 {
		t.Fatalf("two calls were answered: %v", calls)
	}
}

func TestAuthzenMetadata(t *testing.T) {
	w := newWorld(t)
	status, raw := w.do("GET", w.check.URL+"/.well-known/authzen-configuration/service/"+w.org, nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "authzen_metadata_disabled")

	w.cfg.HTTP.PublicURL = "https://licetum.test"
	status, raw = w.do("GET", w.check.URL+"/.well-known/authzen-configuration/service/"+w.org, nil)
	if status != http.StatusOK {
		t.Fatalf("metadata: %d %s", status, raw)
	}
	doc := decodeMap(t, raw)
	pdp := "https://licetum.test/service/" + w.org
	if doc["policy_decision_point"] != pdp || doc["access_evaluation_endpoint"] != pdp+"/access/v1/evaluation" ||
		doc["search_resource_endpoint"] != pdp+"/access/v1/search/resource" {
		t.Fatalf("metadata: %v", doc)
	}
	status, raw = w.do("GET", w.check.URL+"/.well-known/authzen-configuration/service/00000000-0000-0000-0000-000000000000", nil)
	requireErrorCode(t, status, raw, http.StatusNotFound, "service_not_found")
}

func TestAuthzenSubjectSearchPagesWithoutSkippingOrRepeating(t *testing.T) {
	w := newWorld(t)
	w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{"docs-backend"}})
	people := []string{"p1", "p2", "p3", "p4", "p5"}
	for _, p := range append([]string{"outsider"}, people...) {
		if s, _ := w.env.permission(w.org, w.token(jwt.MapClaims{"preferred_username": p, "sub": p})); s != http.StatusOK {
			t.Fatalf("permission as %s: %d", p, s)
		}
		if p != "outsider" {
			w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": p, "access": []string{"read"}})
		}
	}
	// The registry is written in batches (once a second); write what is waiting now.
	w.app.FlushSubjects(context.Background())
	waitFor(t, "the people in the registry", func() bool {
		s, raw := w.do("GET", w.admin.URL+"/subject", nil)
		return s == http.StatusOK && decodeMap(t, raw)["total"].(float64) >= 6
	})

	trusted := jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"}
	request := func(token any) map[string]any {
		page := map[string]any{"limit": 2}
		if token != nil {
			page["token"] = token
		}
		return map[string]any{"subject": map[string]any{"type": "user"}, "resource": map[string]any{"type": "branch", "id": "B1"},
			"action": map[string]any{"name": "read"}, "page": page}
	}
	var got []string
	var token any
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatalf("the pages never end: %v", got)
		}
		status, out := w.authzen("search/subject", request(token), trusted)
		if status != http.StatusOK {
			t.Fatalf("page %d: %d %v", pages, status, out)
		}
		page := out["page"].(map[string]any)
		if _, known := page["total"]; known {
			t.Fatalf("the subject search does not know the total: %v", page)
		}
		for _, r := range out["results"].([]any) {
			got = append(got, r.(map[string]any)["id"].(string))
		}
		if page["next_token"] == "" {
			break
		}
		token = page["next_token"]
	}
	if strings.Join(got, ",") != strings.Join(people, ",") {
		t.Fatalf("every person with access once, in the order of their identifiers: %v", got)
	}

	// The caller is a subject like any other, a trusted client included: rules may grant a service account access by its claims.
	waitFor(t, "the trusted client in the registry", func() bool {
		return w.must("GET", "/subject?search=svc-docs-backend", nil)["total"] == float64(1)
	})
	if slices.Contains(got, "svc-docs-backend") {
		t.Fatalf("the client has no access to B1: %v", got)
	}
}
