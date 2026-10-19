package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

func TestPageOfAndCursor(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	key := func(s string) string { return s }

	page, next := pageOf(items, key, "", 2, "sum")
	if strings.Join(page, "") != "ab" || next == "" {
		t.Fatalf("first page: %v %q", page, next)
	}
	after, err := decodeCursor(next, "sum")
	if err != nil || after != "b" {
		t.Fatalf("cursor: %q %v", after, err)
	}
	page, next = pageOf(items, key, after, 2, "sum")
	if strings.Join(page, "") != "cd" || next == "" {
		t.Fatalf("second page: %v %q", page, next)
	}
	after, _ = decodeCursor(next, "sum")
	page, next = pageOf(items, key, after, 2, "sum")
	if strings.Join(page, "") != "e" || next != "" {
		t.Fatalf("last page must have no token: %v %q", page, next)
	}
	// A page that ends exactly at the end of the results has no token either.
	if page, next = pageOf(items, key, "c", 2, "sum"); strings.Join(page, "") != "de" || next != "" {
		t.Fatalf("exact end: %v %q", page, next)
	}
	// A key that is not among the items starts the page at the next greater one.
	if page, _ = pageOf(items, key, "bb", 10, "sum"); strings.Join(page, "") != "cde" {
		t.Fatalf("between keys: %v", page)
	}
	if page, next = pageOf(nil, key, "", 10, "sum"); len(page) != 0 || next != "" {
		t.Fatalf("empty: %v %q", page, next)
	}
}

func TestDecodeCursorRefusesAChangedRequest(t *testing.T) {
	token := encodeCursor("x", "sum-1")
	if _, err := decodeCursor(token, "sum-2"); err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("a token of another request must be a 400: %v", err)
	}
	for _, bad := range []string{"!!!", "e30", "bm90LWpzb24"} {
		if _, err := decodeCursor(bad, "sum-1"); err == nil || err.Status != http.StatusBadRequest {
			t.Errorf("%q must be a 400: %v", bad, err)
		}
	}
	if after, err := decodeCursor("", "sum-1"); err != nil || after != "" {
		t.Errorf("no token is the first page: %q %v", after, err)
	}
}

func searchRequest() model.AuthzenSearchRequest {
	return model.AuthzenSearchRequest{
		Subject:  &model.AuthzenSubject{Type: "user", ID: "alice"},
		Resource: &model.AuthzenResource{Type: "branch", ID: "b1"},
		Action:   &model.AuthzenAction{Name: "read"},
	}
}

func TestParseSearchRequiredFields(t *testing.T) {
	type mutate func(*model.AuthzenSearchRequest)
	cases := []struct {
		name string
		kind string
		edit mutate
		want string // the field named in the error; empty — accepted
	}{
		{"all there", "resource", func(*model.AuthzenSearchRequest) {}, ""},
		{"no subject", "resource", func(r *model.AuthzenSearchRequest) { r.Subject = nil }, "subject"},
		{"no resource", "resource", func(r *model.AuthzenSearchRequest) { r.Resource = nil }, "resource"},
		{"no subject id", "resource", func(r *model.AuthzenSearchRequest) { r.Subject.ID = "" }, "subject.id"},
		{"subject search needs no subject id", "subject", func(r *model.AuthzenSearchRequest) { r.Subject.ID = "" }, ""},
		{"no resource type", "subject", func(r *model.AuthzenSearchRequest) { r.Resource.Type = "" }, "resource.type"},
		{"no action", "resource", func(r *model.AuthzenSearchRequest) { r.Action = nil }, "action.name"},
		{"no action for subjects", "subject", func(r *model.AuthzenSearchRequest) { r.Action = nil }, "action.name"},
		{"action search has no action", "action", func(r *model.AuthzenSearchRequest) { r.Action = nil }, ""},
		{"bad context name", "resource", func(r *model.AuthzenSearchRequest) { r.Context = map[string]any{"a b": 1} }, "context"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := searchRequest()
			c.edit(&in)
			s, err := parseSearch(c.kind, in)
			if c.want == "" {
				if err != nil || s == nil {
					t.Fatalf("must be accepted: %v", err)
				}
				return
			}
			if err == nil || err.Status != http.StatusBadRequest {
				t.Fatalf("must be a 400, got %v", err)
			}
			if c.want != "context" && !strings.Contains(err.Localize(i18n.EN).Detail, c.want) {
				t.Fatalf("the error must name %q: %q", c.want, err.Localize(i18n.EN).Detail)
			}
		})
	}
}

func TestSearchSumIgnoresTheEntitySearchedFor(t *testing.T) {
	a, b := searchRequest(), searchRequest()
	b.Resource.ID = "other"
	if searchSum("resource", a, 10) != searchSum("resource", b, 10) {
		t.Error("the resource search ignores resource.id")
	}
	if searchSum("action", a, 10) == searchSum("action", b, 10) {
		t.Error("the action search is about resource.id")
	}
	b = searchRequest()
	b.Subject.ID = "bob"
	if searchSum("subject", a, 10) != searchSum("subject", b, 10) {
		t.Error("the subject search ignores subject.id")
	}
	if searchSum("resource", a, 10) == searchSum("resource", b, 10) {
		t.Error("the resource search is about the subject")
	}
	if searchSum("resource", a, 10) == searchSum("resource", a, 20) {
		t.Error("the limit is part of the request")
	}
	b = searchRequest()
	b.Context = map[string]any{"ip": "10.0.0.1"}
	if searchSum("resource", a, 10) == searchSum("resource", b, 10) {
		t.Error("the context is part of the request")
	}
}

func TestPageLimit(t *testing.T) {
	ten, huge, zero, negative := 10, 100000, 0, -1
	for _, c := range []struct {
		page *model.AuthzenPageRequest
		want int
	}{
		{nil, authzenPageMax},
		{&model.AuthzenPageRequest{}, authzenPageMax},
		{&model.AuthzenPageRequest{Limit: &ten}, 10},
		{&model.AuthzenPageRequest{Limit: &huge}, authzenPageMax},
		{&model.AuthzenPageRequest{Limit: &zero}, authzenPageMax},
		{&model.AuthzenPageRequest{Limit: &negative}, authzenPageMax},
	} {
		if got := pageLimit(c.page); got != c.want {
			t.Errorf("pageLimit(%+v) = %d, want %d", c.page, got, c.want)
		}
	}
}

func evals(decisions ...bool) []evaluation {
	out := make([]evaluation, len(decisions))
	for i, d := range decisions {
		out[i].resp = model.AuthzenEvaluationResponse{Decision: d}
	}
	return out
}

func decisions(rs []model.AuthzenEvaluationResponse) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Decision {
			b.WriteByte('T')
		} else {
			b.WriteByte('F')
		}
	}
	return b.String()
}

func TestApplySemantic(t *testing.T) {
	all := evals(true, false, true, false)
	if got := decisions(applySemantic(model.AuthzenExecuteAll, all, i18n.EN)); got != "TFTF" {
		t.Errorf("execute_all: %s", got)
	}
	out := applySemantic(model.AuthzenDenyOnFirstDeny, all, i18n.EN)
	if decisions(out) != "TF" {
		t.Errorf("deny_on_first_deny: %s", decisions(out))
	}
	if out[1].Context["reason"] != model.AuthzenDenyOnFirstDeny || out[1].Context["code"] != "200" {
		t.Errorf("the stopping denial must say why: %+v", out[1].Context)
	}
	if out[0].Context != nil {
		t.Errorf("the permits before it are as they were: %+v", out[0].Context)
	}
	if got := decisions(applySemantic(model.AuthzenDenyOnFirstDeny, evals(true, true), i18n.EN)); got != "TT" {
		t.Errorf("deny_on_first_deny without a denial answers all: %s", got)
	}
	if got := decisions(applySemantic(model.AuthzenPermitOnFirstPermit, evals(false, false, true, true), i18n.EN)); got != "FFT" {
		t.Errorf("permit_on_first_permit: %s", got)
	}
	if got := decisions(applySemantic(model.AuthzenPermitOnFirstPermit, evals(false, false), i18n.EN)); got != "FF" {
		t.Errorf("permit_on_first_permit without a permit answers all: %s", got)
	}
}

func TestApplySemanticErrorsAreDenials(t *testing.T) {
	list := evals(true, false)
	list[1].err = apperr.Forbidden(i18n.AuthzenSubjectMismatch)
	list[1].resp = model.AuthzenEvaluationResponse{}

	out := applySemantic(model.AuthzenExecuteAll, list, i18n.EN)
	errInfo, _ := out[1].Context["error"].(map[string]any)
	if out[1].Decision || errInfo["status"] != http.StatusForbidden || !strings.Contains(errInfo["message"].(string), "subject.id") {
		t.Fatalf("an evaluation that could not be decided is a denial with its error: %+v", out[1])
	}
	// The message is in the language of the request.
	ru := applySemantic(model.AuthzenExecuteAll, list, i18n.RU)
	if msg := ru[1].Context["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "Bearer") && !strings.Contains(msg, "bearer") {
		t.Errorf("the message must be localized: %q", msg)
	}
	// An error stops deny_on_first_deny, and keeps its error next to the reason.
	stopped := applySemantic(model.AuthzenDenyOnFirstDeny, list, i18n.EN)
	if len(stopped) != 2 || stopped[1].Context["error"] == nil || stopped[1].Context["reason"] != model.AuthzenDenyOnFirstDeny {
		t.Errorf("deny_on_first_deny: %+v", stopped)
	}
	// An error is not a permit.
	withError := evals(false, true)
	withError[0].err = apperr.BadRequest(i18n.AuthzenIncomplete)
	if got := decisions(applySemantic(model.AuthzenPermitOnFirstPermit, withError, i18n.EN)); got != "FT" {
		t.Errorf("permit_on_first_permit: %s", got)
	}
	// The shared error is not changed by the language of a request.
	if list[1].err.Detail != apperr.Forbidden(i18n.AuthzenSubjectMismatch).Detail {
		t.Error("the error must not be localized in place")
	}
}

func TestAuthzenAttributesDropsExplain(t *testing.T) {
	attrs := authzenAttributes(map[string]any{"explain": true, "ip": "10.0.0.1"})
	if len(attrs) != 1 || attrs["ip"] != "10.0.0.1" {
		t.Errorf("%+v", attrs)
	}
	if attrs := authzenAttributes(nil); attrs == nil || len(attrs) != 0 {
		t.Errorf("no context is an empty set of attributes, not nil: %#v", attrs)
	}
}

func TestBadRequestKeepsOtherStatuses(t *testing.T) {
	if got := badRequest(apperr.Unprocessable(i18n.ContextInvalid)); got.Status != http.StatusBadRequest {
		t.Errorf("422 -> %d", got.Status)
	}
	if got := badRequest(apperr.Forbidden(i18n.AuthzenSubjectMismatch)); got.Status != http.StatusForbidden {
		t.Errorf("403 -> %d", got.Status)
	}
}

func TestAuthzenMetadata(t *testing.T) {
	m := authzenMetadata("https://licetum.example.com/", "orders")
	if m.PolicyDecisionPoint != "https://licetum.example.com/service/orders" {
		t.Errorf("pdp: %q", m.PolicyDecisionPoint)
	}
	for endpoint, suffix := range map[string]string{
		m.AccessEvaluationEndpoint:  "/access/v1/evaluation",
		m.AccessEvaluationsEndpoint: "/access/v1/evaluations",
		m.SearchSubjectEndpoint:     "/access/v1/search/subject",
		m.SearchActionEndpoint:      "/access/v1/search/action",
		m.SearchResourceEndpoint:    "/access/v1/search/resource",
	} {
		if endpoint != m.PolicyDecisionPoint+suffix {
			t.Errorf("endpoint %q, want the pdp + %q", endpoint, suffix)
		}
	}
	if m := authzenMetadata("https://x", "a b/c"); m.PolicyDecisionPoint != "https://x/service/a%20b%2Fc" {
		t.Errorf("the reference must be escaped: %q", m.PolicyDecisionPoint)
	}
}

// ---------- over HTTP, without a database: what fails before the service is looked up ----------

func post(t *testing.T, h http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAuthzenInvalidRequestsAreBadRequests(t *testing.T) {
	h := NewPDPHandler(engine.New(nil, nil, &config.Config{}))
	const base = "/service/orders/access/v1/"
	for name, c := range map[string]struct{ path, body string }{
		"subject without type":   {"evaluation", `{"subject":{"id":"a"},"resource":{"type":"t"},"action":{"name":"r"}}`},
		"resource without type":  {"evaluation", `{"subject":{"type":"user","id":"a"},"resource":{"id":"1"},"action":{"name":"r"}}`},
		"action without name":    {"evaluation", `{"subject":{"type":"user","id":"a"},"resource":{"type":"t"},"action":{}}`},
		"subject id of a number": {"evaluation", `{"subject":{"type":"user","id":5},"resource":{"type":"t"},"action":{"name":"r"}}`},
		"unknown semantic":       {"evaluations", `{"options":{"evaluations_semantic":"first"},"evaluations":[{}]}`},
		"too many evaluations":   {"evaluations", `{"evaluations":[` + strings.Repeat(`{},`, 100) + `{}]}`},
		"page limit negative":    {"search/resource", `{"page":{"limit":-1}}`},
		"search subject of text": {"search/action", `{"subject":"alice"}`},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := post(t, h, base+c.path, c.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %v", status, body)
			}
			if extra, _ := body["extra"].(map[string]any); extra["code"] != "validation_failed" {
				t.Fatalf("the usual error shape: %v", body)
			}
		})
	}
}

func TestOtherOperationsKeepTheirValidationStatus(t *testing.T) {
	h := NewPDPHandler(engine.New(nil, nil, &config.Config{}))
	status, body := post(t, h, "/service/orders/check", `{"checks":[]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("the check API keeps its 422: %d %v", status, body)
	}
}

func TestAuthzenMetadataNeedsAPublicURL(t *testing.T) {
	h := NewPDPHandler(engine.New(nil, nil, &config.Config{}))
	req := httptest.NewRequest(http.MethodGet, "/.well-known/authzen-configuration/service/orders", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "authzen_metadata_disabled") {
		t.Fatalf("without HTTP__PUBLIC_URL there is no document: %d %s", rec.Code, rec.Body)
	}
}
