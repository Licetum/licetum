//go:build integration

package api_test

// Integration tests of the AuthZEN Authorization API 1.0: the evaluations and their options, the three searches with paging,
// the questions about other people that a trusted client asks, and the discovery document.

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

func (w *world) authzen(path string, body any, claims jwt.MapClaims) (int, map[string]any) {
	w.t.Helper()
	headers := []string{}
	if claims != nil {
		headers = append(headers, "Authorization", "Bearer "+w.token(claims))
	}
	status, header, raw := w.doWithHeader(http.MethodPost, w.check.URL+"/service/"+w.org+"/access/v1/"+path+"?consistency=higher", body, headers...)
	return status, authzenAnswer(w.t, status, header, raw)
}

// authzenEval is an evaluation of the action on the resource. An evaluation requires resource.id: * names the type as a whole.
func authzenEval(resourceType, resourceID, action string) map[string]any {
	return map[string]any{
		"resource": map[string]any{"type": resourceType, "id": resourceID},
		"action":   map[string]any{"name": action},
	}
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
	out := batch("", b3, other, authzenEval("nothing", "*", "read"))
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
	if _, hasTotal := page["total"]; hasTotal || page["count"] != float64(2) || page["next_token"] == "" {
		t.Fatalf("page: %v (a resource search gives no total)", page)
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

	// Subjects: only a trusted client may ask, and only the people of the directory of the service are listed.
	status, _ = w.authzen("search/subject", map[string]any{"subject": map[string]any{"type": "user"},
		"resource": map[string]any{"type": "branch", "id": "B1"}, "action": map[string]any{"name": "read"}}, self)
	if status != http.StatusForbidden {
		t.Fatalf("subject search by a client that is not trusted: %d", status)
	}
	w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{"docs-backend"}})
	w.sendPeople(w.org, person("dave", "Dave", nil))
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "dave", "access": []string{"read"}})
	trusted := jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"}
	status, out = w.authzen("search/subject", map[string]any{"subject": map[string]any{"type": "user"},
		"resource": map[string]any{"type": "branch", "id": "B1"}, "action": map[string]any{"name": "read"}}, trusted)
	// user-1 reads B1 through D1 but is not in the directory, so only dave is listed.
	found := map[string]map[string]any{}
	results, _ := out["results"].([]any)
	for _, r := range results {
		entry := r.(map[string]any)
		found[entry["id"].(string)] = entry
	}
	dave, ok := found["dave"]
	if status != http.StatusOK || !ok || len(found) != 1 || dave["type"] != "user" || dave["properties"].(map[string]any)["name"] != "Dave" {
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
	status, header, raw := w.doWithHeader("GET", w.check.URL+"/.well-known/authzen-configuration/service/"+w.org, nil)
	requireAuthzenError(t, status, header, raw, http.StatusNotFound, "authzen_metadata_disabled")

	w.cfg.PDP.PublicURL = "https://licetum.test"
	status, raw = w.do("GET", w.check.URL+"/.well-known/authzen-configuration/service/"+w.org, nil)
	if status != http.StatusOK {
		t.Fatalf("metadata: %d %s", status, raw)
	}
	doc := decodeMap(t, raw)
	// The identifier of the PDP names the service by its slug (ADR 0001, 7.2); the slug becomes fixed in P3 (ADM11). The path takes the id too.
	pdp := "https://licetum.test/service/org"
	if doc["policy_decision_point"] != pdp || doc["access_evaluation_endpoint"] != pdp+"/access/v1/evaluation" ||
		doc["search_resource_endpoint"] != pdp+"/access/v1/search/resource" {
		t.Fatalf("metadata: %v", doc)
	}
	status, header, raw = w.doWithHeader("GET", w.check.URL+"/.well-known/authzen-configuration/service/00000000-0000-0000-0000-000000000000", nil)
	requireAuthzenError(t, status, header, raw, http.StatusNotFound, "service_not_found")
}

// TestContract_DIR01_SubjectSearchListsTheAllowedEntriesOfTheDirectoryOfTheService: the people of the directory of the service, of
// the requested type, who may do the action are listed, by id, in pages that neither skip nor repeat; an empty directory gives an
// empty result, a person who is not in it is never found, and the entries of another service or type never appear.
func TestContract_DIR01_SubjectSearchListsTheAllowedEntriesOfTheDirectoryOfTheService(t *testing.T) {
	w := newWorld(t)
	client := w.pepClient(w.org, "directory-reader", db.PEPKindAPIKey, engine.PEPSearchSubject)
	key, _ := w.pepKey(client)
	appClient := w.pepClient(w.appSvc, "directory-reader", db.PEPKindAPIKey, engine.PEPSearchSubject)
	appKey, _ := w.pepKey(appClient)

	search := func(ref, credential string, opts ...authzenreq.Option) (int, []string, string) {
		w.t.Helper()
		status, raw := w.ask(authzenreq.SearchSubject(ref, authzenreq.On("branch", "B1"), "read", opts...), credential)
		if status != http.StatusOK {
			return status, nil, ""
		}
		ids, next, err := authzenreq.SubjectIDs(status, raw)
		if err != nil {
			t.Fatal(err)
		}
		return status, ids, next
	}

	// An empty directory gives an empty result, not an error, whoever has rules.
	for _, p := range []string{"p3", "p1", "p5", "p2", "p4", "ghost"} {
		w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": p, "access": []string{"read"}})
	}
	if status, ids, next := search("org", key); status != http.StatusOK || len(ids) != 0 || next != "" {
		t.Fatalf("an empty directory: %d %v %q", status, ids, next)
	}

	// The directory: five people with a rule, one without, one of another type with a rule, one with a rule in the other
	// service only. "ghost" has a rule and is not in the directory.
	w.sendPeople(w.org,
		person("p3", "Person 3", nil), person("p1", "Person 1", nil), person("p5", "", nil), person("p2", "Person 2", nil),
		person("p4", "Person 4", nil), person("outsider", "Outsider", nil),
		map[string]any{"type": "service", "id": "p9", "name": "A service with a rule"},
	)
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "p9", "access": []string{"read"}})
	w.sendPeople(w.appSvc, person("p6", "Person of app", nil))
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "p6", "access": []string{"read"}})

	// Every page, two at a time: the people with access, once, in the order of their ids.
	var got []string
	var next string
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatalf("the pages never end: %v", got)
		}
		opts := []authzenreq.Option{authzenreq.Page(2, next)}
		status, ids, token := search("org", key, opts...)
		if status != http.StatusOK {
			t.Fatalf("page %d: %d", pages, status)
		}
		got = append(got, ids...)
		if token == "" {
			break
		}
		next = token
	}
	if strings.Join(got, ",") != "p1,p2,p3,p4,p5" {
		t.Fatalf("the people with access, once, by id: %v (not the one without a rule, the one of another type, the one who is only in the rules, nor the person of the other service)", got)
	}
	// The names are the stored ones, and a person sent without a name has none.
	status, raw := w.ask(authzenreq.SearchSubject("org", authzenreq.On("branch", "B1"), "read", authzenreq.Page(5, "")), key)
	if status != http.StatusOK || !strings.Contains(string(raw), `"name":"Person 3"`) {
		t.Fatalf("the display name is missing: %d %s", status, raw)
	}
	if strings.Contains(string(raw), "Outsider") || strings.Contains(string(raw), "Person of app") {
		t.Fatalf("an entry that is not allowed, or of another service, is listed: %s", raw)
	}

	// The other service sees its own directory only.
	if status, ids, _ := search("app", appKey); status != http.StatusOK || len(ids) != 0 {
		t.Fatalf("app: %d %v (p6's rule is on a branch of org; the item type of app has none)", status, ids)
	}
	w.grant(map[string]any{"type_resource_id": w.item, "resource_id": "I1", "subject": "p6", "access": []string{"read"}})
	status, raw = w.ask(authzenreq.SearchSubject("app", authzenreq.On("item", "I1"), "read"), appKey)
	if ids, _, err := authzenreq.SubjectIDs(status, raw); err != nil || strings.Join(ids, ",") != "p6" {
		t.Fatalf("app lists %v (%v), want p6 only", ids, err)
	}

	// A token that is not the one of the page's request is refused.
	_, _, token := search("org", key, authzenreq.Page(2, ""))
	if token == "" {
		t.Fatal("a first page of two has a next token")
	}
	status, header, body := w.askWithHeader(authzenreq.SearchSubject("org", authzenreq.On("branch", "B2"), "read", authzenreq.Page(2, token)), key)
	requireAuthzenError(t, status, header, body, http.StatusBadRequest, "authzen_page_invalid")
}

// searchResourcePages pages the resource search of the type and the action for the claims until the token is empty. It returns
// the ids in the order of the pages, and the number of pages that had no result yet carried a token (the scan budget stopped
// them).
func (w *world) searchResourcePages(resourceType, action string, limit int, claims jwt.MapClaims) (ids []string, pages, emptyWithToken int) {
	w.t.Helper()
	subject := hierarchySubject
	if name, ok := claims["preferred_username"].(string); ok {
		subject = name
	}
	token := ""
	for pages < 200 {
		page := map[string]any{"limit": limit}
		if token != "" {
			page["token"] = token
		}
		status, out := w.authzen("search/resource", map[string]any{
			"subject":  map[string]any{"type": "user", "id": subject},
			"resource": map[string]any{"type": resourceType},
			"action":   map[string]any{"name": action},
			"page":     page,
		}, claims)
		if status != http.StatusOK {
			w.t.Fatalf("resource search: %d %v", status, out)
		}
		pages++
		results := out["results"].([]any)
		for _, r := range results {
			ids = append(ids, r.(map[string]any)["id"].(string))
		}
		token, _ = out["page"].(map[string]any)["next_token"].(string)
		if len(results) == 0 && token != "" {
			emptyWithToken++
		}
		if token == "" {
			return ids, pages, emptyWithToken
		}
	}
	w.t.Fatal("the resource search did not end within 200 pages")
	return nil, 0, 0
}

// TestContract_AZ04_ResourceSearchPagesInBytewiseOrder: the pages of the resource search follow the bytewise order of the ids whatever
// the collation of the database is, and no result is repeated or skipped: ids that differ in case, punctuation, digits and
// non-ASCII letters are paged by four. (An empty id is a key of the cursor too, which the unit tests of the handler check; the
// answer cannot show a resource without an id.)
func TestContract_AZ04_ResourceSearchPagesInBytewiseOrder(t *testing.T) {
	w := newWorld(t)
	var collated bool
	if err := w.pool.QueryRow(context.Background(), `SELECT 'a' < 'B'`).Scan(&collated); err == nil && !collated {
		t.Log("the default collation of this database is bytewise: here the order of the pages cannot tell COLLATE \"C\" from the default")
	}
	extra := []string{"b", "B", "a_1", "a-1", "a1", "A1", "a.1", "10", "9", "é", "É", "Z", "z"}
	for _, id := range extra {
		w.source("branch", id, "Name "+id, "R1")
	}
	w.rule("division", "D1", nil)
	self := jwt.MapClaims{"preferred_username": hierarchySubject}

	want := append([]string{"B1", "B2", "B3"}, extra...)
	sort.Strings(want)
	got, pages, _ := w.searchResourcePages("branch", "read", 4, self)
	if !slices.Equal(got, want) {
		t.Fatalf("the pages give\n%v\nwant, in bytewise order,\n%v", got, want)
	}
	if pages < 4 {
		t.Errorf("%d resources by four are at least 4 pages, got %d", len(want), pages)
	}
}

// The resource search gives what the permission map of the service gives: the resources of the type with the level, after the
// denials and the exceptions of the rules, in bytewise order (the engine decides each candidate with the rules of the caller).
func TestAuthzenResourceSearchMatchesThePermissionMap(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	w.rule("rrs", "R1", deny("read"))
	w.rule("branch", "B2", exception("read"))
	self := jwt.MapClaims{"preferred_username": hierarchySubject}

	var want []string
	for _, r := range w.permission(w.org, nil)["branch"].Resources {
		if slices.Contains(r.Access, "read") {
			want = append(want, r.ID)
		}
	}
	sort.Strings(want)
	if len(want) == 0 {
		t.Fatal("the policy lets the caller read some branch: the comparison would prove nothing")
	}
	got, _, _ := w.searchResourcePages("branch", "read", 2, self)
	if !slices.Equal(got, want) {
		t.Fatalf("search = %v, permission map = %v", got, want)
	}
}

// TestContract_AZ13_ResourceSearchStopsAtTheScanBudget: one request reads at most PDP__SEARCH_SCAN_BUDGET candidates. A page that the
// budget stops is shorter than its limit, even empty, and still carries a next_token that continues after the last candidate
// read; the pages together give the result.
func TestContract_AZ13_ResourceSearchStopsAtTheScanBudget(t *testing.T) {
	w := newWorld(t)
	w.rule("branch", "B3", nil)
	w.cfg.PDP.SearchScanBudget = 2
	self := jwt.MapClaims{"preferred_username": hierarchySubject}

	got, pages, emptyWithToken := w.searchResourcePages("branch", "read", 10, self)
	if strings.Join(got, ",") != "B3" {
		t.Fatalf("the pages give %v, want [B3]", got)
	}
	// B1 and B2 are read and refused first: the first page is empty and still has a token.
	if emptyWithToken == 0 || pages < 2 {
		t.Errorf("%d pages, %d empty ones with a token: the budget of 2 candidates must cut the scan of 4 branches", pages, emptyWithToken)
	}
}

// A caller to whom no rule gives anything gets a finished empty page: the type is not scanned for nothing. A caller with only
// denials is the same.
func TestAuthzenResourceSearchWithoutAGrantIsEmptyAndFinished(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	w.rule("branch", "B1", map[string]any{"subject": "mallory", "effect": "deny", "access": []string{"read"}})
	for _, name := range []string{"stranger", "mallory"} {
		got, pages, _ := w.searchResourcePages("branch", "read", 10, jwt.MapClaims{"preferred_username": name})
		if len(got) != 0 || pages != 1 {
			t.Errorf("%s: the pages give %v in %d pages, want one empty page with the token \"\"", name, got, pages)
		}
	}
}
