package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

const searchResourcePath = "/service/" + fakeServiceRef + "/access/v1/search/resource"

// resourceSearchBody is a resource search of the fake caller: the type todo, the action read, the page limit and token.
func resourceSearchBody(limit int, token, action string) string {
	return fmt.Sprintf(`{"subject":{"type":"user","id":%q},"resource":{"type":"todo"},"action":{"name":%q},"page":{"limit":%d,"token":%q}}`,
		fakeCaller, action, limit, token)
}

// searchAnswer is what the tests read of a resource search answer.
type searchAnswer struct {
	Results []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"results"`
	Page map[string]any `json:"page"`
}

func decodeSearch(t *testing.T, rec *httptest.ResponseRecorder) searchAnswer {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the search answered %d: %s", rec.Code, rec.Body)
	}
	var out searchAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the answer is not a search answer: %v: %s", err, rec.Body)
	}
	return out
}

func (a searchAnswer) ids() []string {
	out := make([]string, len(a.Results))
	for i, r := range a.Results {
		out[i] = r.ID
	}
	return out
}

func (a searchAnswer) nextToken() string {
	s, _ := a.Page["next_token"].(string)
	return s
}

// TestContract_AZ04_PagesContinueAfterTheLastKeyAndEndWithAnEmptyToken: the token of a page holds the key the page covers up to,
// so the next page starts after it and no result is repeated; the last page has the token "", and a resource search gives no
// total (it would need the whole result). The pages are those the engine answers with (engine.SearchResources, whose scan is
// tested on PostgreSQL).
func TestContract_AZ04_PagesContinueAfterTheLastKeyAndEndWithAnEmptyToken(t *testing.T) {
	fake := newFakePDP()
	fake.searchPages = []engine.ResourcePage{
		{Hits: []engine.ResourceHit{{ID: "1", Name: "one"}, {ID: "2", Name: "two"}}, Last: "2"},
		{Hits: []engine.ResourceHit{{ID: "3", Name: "three"}}, Last: "3", Done: true},
	}
	h := authzenTestAPI(fake, &config.Config{})

	first := decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(2, "", "read")))
	if got := first.ids(); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("first page = %v, want [1 2]", got)
	}
	token := first.nextToken()
	if token == "" {
		t.Fatal("a page that is not the last has a next_token")
	}
	if _, ok := first.Page["total"]; ok {
		t.Errorf("a resource search gives no total: %v", first.Page)
	}

	second := decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(2, token, "read")))
	if got := second.ids(); len(got) != 1 || got[0] != "3" {
		t.Fatalf("second page = %v, want [3]", got)
	}
	if second.nextToken() != "" {
		t.Errorf("the last page has the token \"\", got %q", second.nextToken())
	}
	if len(fake.searches) != 2 || fake.searches[0].started || !fake.searches[1].started || fake.searches[1].after != "2" {
		t.Errorf("the engine was asked %+v, want the first page from the start and the second after the key 2", fake.searches)
	}
	if fake.searches[0].limit != 2 {
		t.Errorf("the page limit reaches the engine as %d, want 2", fake.searches[0].limit)
	}
}

// TestContract_AZ04_AChangedRequestIsRefused: a token belongs to the request that gave it; if the limit, the action, the
// type or the context differs, the answer is a 400 authzen_page_invalid (as the specification recommends).
func TestContract_AZ04_AChangedRequestIsRefused(t *testing.T) {
	fake := newFakePDP()
	fake.searchPages = []engine.ResourcePage{{Hits: []engine.ResourceHit{{ID: "1"}}, Last: "1"}}
	h := authzenTestAPI(fake, &config.Config{})
	token := decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(1, "", "read"))).nextToken()
	if token == "" {
		t.Fatal("the first page has a token")
	}

	for name, body := range map[string]string{
		"another limit":  resourceSearchBody(2, token, "read"),
		"another action": resourceSearchBody(1, token, "write"),
		"another type": fmt.Sprintf(`{"subject":{"type":"user","id":%q},"resource":{"type":"document"},"action":{"name":"read"},"page":{"limit":1,"token":%q}}`,
			fakeCaller, token),
		"another context": fmt.Sprintf(`{"subject":{"type":"user","id":%q},"resource":{"type":"todo"},"action":{"name":"read"},"context":{"ip":"10.0.0.1"},"page":{"limit":1,"token":%q}}`,
			fakeCaller, token),
		"a token that is not one": resourceSearchBody(1, "not-a-token", "read"),
	} {
		rec := authzenCall(h, searchResourcePath, body)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Licetum-Error-Code") != "authzen_page_invalid" {
			t.Errorf("%s: %d %s %s, want 400 authzen_page_invalid", name, rec.Code, rec.Header().Get("Licetum-Error-Code"), rec.Body)
		}
	}
	if len(fake.searches) != 1 {
		t.Errorf("a refused page must not reach the engine: %d calls", len(fake.searches))
	}
}

// TestContract_AZ04_ThePageDigestFollowsWhatTheSearchReads: between two pages a newer min_revision applies to the page that
// carries it, and a key the API ignores is not part of the request, so both leave the token valid; the properties of the
// subject are read by the resource search, so a change of them is a changed request (400 authzen_page_invalid).
func TestContract_AZ04_ThePageDigestFollowsWhatTheSearchReads(t *testing.T) {
	fake := newFakePDP()
	fake.searchPages = []engine.ResourcePage{
		{Hits: []engine.ResourceHit{{ID: "1"}}, Last: "1"},
		{Hits: []engine.ResourceHit{{ID: "2"}}, Last: "2", Done: true},
		{Hits: []engine.ResourceHit{{ID: "2"}}, Last: "2", Done: true},
	}
	h := authzenTestAPI(fake, &config.Config{})
	body := func(token, subjectExtra, context string) string {
		return fmt.Sprintf(`{"subject":{"type":"user","id":%q%s},"resource":{"type":"todo"},"action":{"name":"read"},"context":%s,"page":{"limit":1,"token":%q}}`,
			fakeCaller, subjectExtra, context, token)
	}
	properties := `,"properties":{"department":"sales"}`
	first := decodeSearch(t, authzenCall(h, searchResourcePath, body("", properties, `{"ip":"10.0.0.1","licetum":{"min_revision":"1.1"}}`)))
	token := first.nextToken()
	if token == "" {
		t.Fatal("the first page has a token")
	}
	// A newer min_revision and an ignored nested key: the same request.
	second := authzenCall(h, searchResourcePath, body(token, properties, `{"ip":"10.0.0.1","nested":{"x":"y"},"licetum":{"min_revision":"1.2"}}`))
	if second.Code != http.StatusOK {
		t.Errorf("an ignored key must not change the request: %d %s", second.Code, second.Body)
	}
	// The properties of the subject changed: another request.
	rec := authzenCall(h, searchResourcePath, body(token, `,"properties":{"department":"hr"}`, `{"ip":"10.0.0.1"}`))
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Licetum-Error-Code") != "authzen_page_invalid" {
		t.Errorf("changed subject.properties: %d %s %s, want 400 authzen_page_invalid", rec.Code, rec.Header().Get("Licetum-Error-Code"), rec.Body)
	}
}

// TestContract_AZ13_TheEngineIsAskedForTheRequestedTypeWithTheScanBudget: the search reads only the type asked for, at most
// PDP__SEARCH_SCAN_BUDGET candidates (the default when the setting is not loaded), and a page that the budget stopped, shorter
// than its limit even when empty, still carries a next_token that continues after the last candidate read.
func TestContract_AZ13_TheEngineIsAskedForTheRequestedTypeWithTheScanBudget(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg    *config.Config
		budget int
	}{
		"the configured budget": {&config.Config{PDP: config.PDP{SearchScanBudget: 250}}, 250},
		"the default budget":    {&config.Config{}, config.DefaultSearchScanBudget},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakePDP()
			// The budget stopped the first page before it found a hit: it is empty, and the scan was cut at the key k9.
			fake.searchPages = []engine.ResourcePage{{Last: "k9"}, {Hits: []engine.ResourceHit{{ID: "ka"}}, Last: "ka", Done: true}}
			h := authzenTestAPI(fake, tc.cfg)

			first := decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(5, "", "read")))
			if len(first.Results) != 0 || first.nextToken() == "" {
				t.Fatalf("an empty page that the budget stopped still has a next_token: %v", first)
			}
			second := decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(5, first.nextToken(), "read")))
			if got := second.ids(); len(got) != 1 || got[0] != "ka" || second.nextToken() != "" {
				t.Fatalf("second page = %v token %q, want [ka] and the token \"\"", got, second.nextToken())
			}
			if len(fake.searches) != 2 {
				t.Fatalf("the engine was asked %d times, want 2", len(fake.searches))
			}
			if s := fake.searches[0]; s.typeName != "todo" || s.action != "read" || s.budget != tc.budget {
				t.Errorf("the engine was asked %+v, want the type todo, the action read and the budget %d", s, tc.budget)
			}
			if !fake.searches[1].started || fake.searches[1].after != "k9" {
				t.Errorf("the page after the budget starts after %q, want the last candidate read, k9", fake.searches[1].after)
			}
		})
	}
}

// TestContract_AZ04_AnEmptyKeyIsAKeyNotTheStart: a source may give a resource the empty string as its id. A page that ends at that
// key has a token, and the page after it starts after the empty key; the request without a token is the only one that starts
// from the first key.
func TestContract_AZ04_AnEmptyKeyIsAKeyNotTheStart(t *testing.T) {
	fake := newFakePDP()
	fake.searchPages = []engine.ResourcePage{
		{Hits: []engine.ResourceHit{{ID: ""}}, Last: ""},
		{Hits: []engine.ResourceHit{{ID: "b"}}, Last: "b", Done: true},
	}
	h := authzenTestAPI(fake, &config.Config{})
	first := decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(1, "", "read")))
	if first.nextToken() == "" {
		t.Fatal("a page that is not the last has a next_token, also when it ends at the empty key")
	}
	decodeSearch(t, authzenCall(h, searchResourcePath, resourceSearchBody(1, first.nextToken(), "read")))
	if len(fake.searches) != 2 || fake.searches[0].started || !fake.searches[1].started || fake.searches[1].after != "" {
		t.Errorf("the engine was asked %+v, want the second page to start after the empty key", fake.searches)
	}
}

// TestContract_AZ04_ATokenWithAKeyTheDatabaseCannotTakeIsRefused: the key in a token goes to PostgreSQL as text, which cannot hold
// a NUL byte, so a token with one is a 400 authzen_page_invalid, not an error of the database.
func TestContract_AZ04_ATokenWithAKeyTheDatabaseCannotTakeIsRefused(t *testing.T) {
	fake := newFakePDP()
	h := authzenTestAPI(fake, &config.Config{})
	sum := searchSum("resource", searchInput{
		Subject:  &model.AuthzenSubject{Type: "user", ID: fakeCaller},
		Resource: &model.AuthzenResource{Type: "todo"},
		Action:   &model.AuthzenAction{Name: "read"},
	}, model.RequestContext{}, 1)
	rec := authzenCall(h, searchResourcePath, resourceSearchBody(1, encodeCursor("a\x00b", sum), "read"))
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Licetum-Error-Code") != "authzen_page_invalid" {
		t.Errorf("%d %s, want 400 authzen_page_invalid", rec.Code, rec.Body)
	}
	if len(fake.searches) != 0 {
		t.Errorf("a refused page must not reach the engine: %d calls", len(fake.searches))
	}
}
