package server

// The AuthZEN handlers on a fake engine, without a database: what the handlers do with the answers of the engine, and how
// they answer. The fake answers a question the way the engine does (engine.answerCheck): a type or a resource that does not
// exist is not allowed, and the explanation says why.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/httpx"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

const (
	fakeServiceRef = "orders"
	fakeToken      = "caller-token"
	fakeCaller     = "alice@acmecorp.com"
)

var fakeServiceID = uuid.MustParse("2f4f0c1a-6f7e-4b7b-9f31-3c2d5e6a7b8c")

// The types and the resources of the fake service.
var (
	fakeTypes     = map[string]bool{"todo": true, "document": true}
	fakeResources = map[string]bool{"todo/1": true, "todo/2": true, "todo/3": true, "document/1": true}
)

// fakePDP is the pdpEngine of the handler tests. Its grants are subject, action, type and resource id ("*" the type as a
// whole). It counts the questions CheckAccess is asked, which is how the tests see what the handlers evaluated.
type fakePDP struct {
	grants map[[4]string]bool
	asked  int
}

func newFakePDP(grants ...[4]string) *fakePDP {
	f := &fakePDP{grants: map[[4]string]bool{}}
	for _, g := range grants {
		f.grants[g] = true
	}
	return f
}

func (f *fakePDP) ServiceIDByRef(_ context.Context, ref string) (uuid.UUID, error) {
	if ref != fakeServiceRef {
		return uuid.Nil, apperr.NotFound(i18n.ServiceNotFound, ref)
	}
	return fakeServiceID, nil
}

func (f *fakePDP) GetServiceForCheck(_ context.Context, serviceID uuid.UUID) (*model.Service, error) {
	return &model.Service{ID: serviceID}, nil
}

func (f *fakePDP) VerifyToken(_ context.Context, _ *model.Service, token string) (engine.Claims, error) {
	if token != fakeToken {
		return nil, apperr.InvalidToken(i18n.TokenInvalid)
	}
	return engine.Claims{"preferred_username": fakeCaller}, nil
}

func (f *fakePDP) NoteSubject(string, engine.Claims) {}

func (f *fakePDP) NewEnv(claims engine.Claims, attrs map[string]any) engine.Env {
	return engine.NewEnv(claims, attrs)
}

func (f *fakePDP) CheckAccess(_ context.Context, _ uuid.UUID, subject string, _ engine.Env, requests []model.CheckRequest, explain bool, _ engine.Consistency) ([]model.CheckResult, error) {
	out := make([]model.CheckResult, len(requests))
	for i, r := range requests {
		f.asked++
		id := model.AuthzenWholeType
		if r.ResourceID != nil {
			id = *r.ResourceID
		}
		decision, allowed := engine.DecisionNoAccess, false
		switch {
		case !fakeTypes[r.Type]:
			decision = engine.DecisionTypeNotFound
		case r.ResourceID != nil && !fakeResources[r.Type+"/"+id]:
			decision = engine.DecisionResourceNotFound
		case f.grants[[4]string{subject, r.Action, r.Type, id}]:
			decision, allowed = engine.DecisionAllowed, true
		}
		out[i] = model.CheckResult{Allowed: allowed}
		if explain {
			out[i].Explanation = &model.Explanation{Decision: decision}
		}
	}
	return out, nil
}

func (f *fakePDP) LogDecisions(context.Context, uuid.UUID, string, []model.CheckRequest, []model.CheckResult, engine.Consistency) {
}

func (f *fakePDP) GetPermission(context.Context, uuid.UUID, string, engine.Env, engine.Consistency) (engine.PermissionResult, error) {
	return engine.PermissionResult{}, nil
}

func (f *fakePDP) LoadKnownSubjectsAfter(context.Context, string, int) ([]engine.KnownSubject, error) {
	return nil, nil
}

func (f *fakePDP) RecordDelegatedCall(context.Context, uuid.UUID, engine.DelegatedCall) error {
	return nil
}

// authzenTestAPI serves the AuthZEN operations of a pdpEngine: the handlers, their errors and their documentation as the PDP
// registers them. The middleware of the server (the request id, the admission) is not part of it: see TestContract_AZ07.
func authzenTestAPI(eng pdpEngine, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	api := newAPI(mux, "Licetum PDP", nil, false)
	registerAuthzen(api, eng, cfg, []map[string][]string{{httpx.BearerScheme: {}}}, []int{http.StatusUnauthorized, http.StatusFailedDependency})
	return mux
}

// authzenCall sends an AuthZEN request with the token of the fake caller.
func authzenCall(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+fakeToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// authzenAnswer is what the tests read of an answer: the decision of a single evaluation, the evaluations of a batch, and
// the context of each.
type authzenAnswer struct {
	Decision    *bool          `json:"decision"`
	Context     map[string]any `json:"context"`
	Evaluations []struct {
		Decision bool           `json:"decision"`
		Context  map[string]any `json:"context"`
	} `json:"evaluations"`
}

func decodeAnswer(t *testing.T, rec *httptest.ResponseRecorder) authzenAnswer {
	t.Helper()
	var out authzenAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the answer is not an AuthZEN answer: %v: %s", err, rec.Body)
	}
	return out
}

// specVector is one vector of the AuthZEN examples (testdata/spec): the grants of the fake policy, the request, and what the
// answer must carry. decision is the decision of a single answer, decisions the decisions of a batch, reasons whether each
// evaluation of a batch carries the explanation, and reason whether a single answer does.
type specVector struct {
	Name      string          `json:"name"`
	Endpoint  string          `json:"endpoint"`
	Grants    [][]string      `json:"grants"`
	Request   json.RawMessage `json:"request"`
	Decision  *bool           `json:"decision"`
	Decisions []bool          `json:"decisions"`
	Reasons   []bool          `json:"reasons"`
	Reason    *bool           `json:"reason"`
}

// TestContract_AZ02_DefaultsAndSemanticsAsTheSpecification runs the examples of the specification (testdata/spec) through the
// handlers: the defaults of a batch are merged into each evaluation, the three semantics answer as the examples do, and
// context.licetum is read from the top-level context only.
func TestContract_AZ02_DefaultsAndSemanticsAsTheSpecification(t *testing.T) {
	files, err := filepath.Glob("../pdp/authzen/testdata/spec/*.json")
	if err != nil || len(files) < 10 {
		t.Fatalf("the examples of the specification: %d files, %v", len(files), err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var v specVector
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("%s: %v", v.Name, err)
			}
			grants := make([][4]string, len(v.Grants))
			for i, g := range v.Grants {
				grants[i] = [4]string{g[0], g[1], g[2], g[3]}
			}
			h := authzenTestAPI(newFakePDP(grants...), &config.Config{})
			rec := authzenCall(h, "/service/"+fakeServiceRef+"/access/v1/"+v.Endpoint, string(v.Request))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d: %s", v.Name, rec.Code, rec.Body)
			}
			got := decodeAnswer(t, rec)
			if v.Decision != nil && (got.Decision == nil || *got.Decision != *v.Decision) {
				t.Errorf("%s: decision %v, want %v", v.Name, got.Decision, *v.Decision)
			}
			if v.Decisions != nil {
				if len(got.Evaluations) != len(v.Decisions) {
					t.Fatalf("%s: %d evaluations answered, want %d: %s", v.Name, len(got.Evaluations), len(v.Decisions), rec.Body)
				}
				for i, want := range v.Decisions {
					if got.Evaluations[i].Decision != want {
						t.Errorf("%s: evaluation %d decision %v, want %v", v.Name, i, got.Evaluations[i].Decision, want)
					}
				}
			}
			for i, want := range v.Reasons {
				if _, has := got.Evaluations[i].Context["reason_admin"]; has != want {
					t.Errorf("%s: evaluation %d explained %v, want %v", v.Name, i, has, want)
				}
			}
			if v.Reason != nil {
				if _, has := got.Context["reason_admin"]; has != *v.Reason {
					t.Errorf("%s: explained %v, want %v", v.Name, has, *v.Reason)
				}
			}
		})
	}
}

// TestContract_AZ03_ShortCircuitEvaluatesNothingAfterTheStop counts the questions the engine is asked: under a short-circuiting
// semantic the batch stops at the evaluation that decides it, and nothing after it is evaluated.
func TestContract_AZ03_ShortCircuitEvaluatesNothingAfterTheStop(t *testing.T) {
	grants := [][4]string{{fakeCaller, "can_read", "todo", "1"}, {fakeCaller, "can_read", "todo", "3"}}
	item := func(id string) string { return fmt.Sprintf(`{"resource":{"type":"todo","id":%q}}`, id) }
	other := `{"subject":{"type":"user","id":"bob"},"resource":{"type":"todo","id":"1"}}`
	for _, c := range []struct {
		name      string
		semantic  string
		items     []string
		asked     int
		decisions string
	}{
		{"deny stops at the first denial", "deny_on_first_deny", []string{item("1"), item("2"), item("3"), item("1")}, 2, "TF"},
		{"permit stops at the first permit", "permit_on_first_permit", []string{item("2"), item("3"), item("1")}, 2, "FT"},
		{"execute_all asks every evaluation", "execute_all", []string{item("1"), item("2"), item("3"), item("1")}, 4, "TFTT"},
		{"an evaluation that cannot be decided stops deny", "deny_on_first_deny", []string{item("1"), other, item("3")}, 1, "TF"},
		{"a permit that comes first stops permit", "permit_on_first_permit", []string{item("1"), item("3")}, 1, "T"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := newFakePDP(grants...)
			h := authzenTestAPI(fake, &config.Config{})
			body := fmt.Sprintf(`{"subject":{"type":"user","id":%q},"action":{"name":"can_read"},"options":{"evaluations_semantic":%q},"evaluations":[%s]}`,
				fakeCaller, c.semantic, strings.Join(c.items, ","))
			rec := authzenCall(h, "/service/"+fakeServiceRef+"/access/v1/evaluations", body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if fake.asked != c.asked {
				t.Errorf("the engine was asked %d questions, want %d", fake.asked, c.asked)
			}
			got := decodeAnswer(t, rec)
			var decisions strings.Builder
			for _, e := range got.Evaluations {
				if e.Decision {
					decisions.WriteByte('T')
				} else {
					decisions.WriteByte('F')
				}
			}
			if decisions.String() != c.decisions {
				t.Errorf("decisions %s, want %s: %s", decisions.String(), c.decisions, rec.Body)
			}
		})
	}
}

// TestContract_AZ07_RequestIDIsReturnedVerbatimWhenSafe: a request id that is safe for a header comes back exactly as it was
// sent; one that is not (a space, a control character, an empty or an overlong value) is replaced by a generated one.
func TestContract_AZ07_RequestIDIsReturnedVerbatimWhenSafe(t *testing.T) {
	h := NewPDPHandler(engine.New(nil, nil, &config.Config{}))
	get := func(id string) string {
		// The public port serves no operation of its own any more: the 404 of the route table goes through the same stack.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-ID", id)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("X-Request-ID")
	}
	for _, id := range []string{"a", "req-42", "abc.DEF_123:9/x~y", "!#$%&'()*+,-./:;<=>?@[]^_`{|}~", strings.Repeat("r", 128)} {
		if got := get(id); got != id {
			t.Errorf("the id %q came back as %q", id, got)
		}
	}
	for _, id := range []string{"has space", "tab\tin", "ctl\x01", "é", strings.Repeat("r", 129)} {
		got := get(id)
		if _, err := uuid.Parse(got); err != nil {
			t.Errorf("the unsafe id %q must be replaced by a generated one, got %q", id, got)
		}
	}
	if got := get(""); got == "" {
		t.Error("a request without an id gets a generated one")
	}
}

// TestContract_AZ11_UnknownTypeOrResourceIsDecisionFalse: an evaluation of a type or of a resource that does not exist is
// decision false in a 200 (with the reason in the explanation), and a search of such a type has no results.
func TestContract_AZ11_UnknownTypeOrResourceIsDecisionFalse(t *testing.T) {
	h := authzenTestAPI(newFakePDP([4]string{fakeCaller, "can_read", "todo", "1"}), &config.Config{})
	subject := fmt.Sprintf(`"subject":{"type":"user","id":%q}`, fakeCaller)
	for _, c := range []struct{ name, resource, reason string }{
		{"unknown type", `{"type":"invoice","id":"1"}`, "type_not_found"},
		{"unknown resource", `{"type":"todo","id":"99"}`, "resource_not_found"},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := fmt.Sprintf(`{%s,"resource":%s,"action":{"name":"can_read"},"context":{"licetum":{"explain":true}}}`, subject, c.resource)
			rec := authzenCall(h, "/service/"+fakeServiceRef+"/access/v1/evaluation", body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
			}
			got := decodeAnswer(t, rec)
			if got.Decision == nil || *got.Decision {
				t.Fatalf("decision %v, want false: %s", got.Decision, rec.Body)
			}
			explanation, _ := got.Context["reason_admin"].(map[string]any)
			if explanation["decision"] != c.reason {
				t.Errorf("the explanation says %v, want %s", explanation["decision"], c.reason)
			}
		})
	}
	t.Run("in a batch", func(t *testing.T) {
		body := fmt.Sprintf(`{%s,"action":{"name":"can_read"},"evaluations":[{"resource":{"type":"invoice","id":"1"}},{"resource":{"type":"todo","id":"99"}},{"resource":{"type":"todo","id":"1"}}]}`, subject)
		rec := authzenCall(h, "/service/"+fakeServiceRef+"/access/v1/evaluations", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}
		got := decodeAnswer(t, rec)
		if len(got.Evaluations) != 3 || got.Evaluations[0].Decision || got.Evaluations[1].Decision || !got.Evaluations[2].Decision {
			t.Errorf("decisions must be false, false, true: %s", rec.Body)
		}
		for i, e := range got.Evaluations {
			if _, failed := e.Context["error"]; failed {
				t.Errorf("evaluation %d is an error, not a decision: %s", i, rec.Body)
			}
		}
	})
	t.Run("a search of an unknown type has no results", func(t *testing.T) {
		body := fmt.Sprintf(`{%s,"resource":{"type":"invoice"},"action":{"name":"can_read"}}`, subject)
		rec := authzenCall(h, "/service/"+fakeServiceRef+"/access/v1/search/resource", body)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results":[]`) {
			t.Errorf("status %d, want 200 with no results: %s", rec.Code, rec.Body)
		}
	})
}

// TestAuthzenErrorsHaveAStringBodyAndACode: an AuthZEN error has the localized message as a JSON string, its catalog key in
// the Licetum-Error-Code header, and the headers the error carries (the challenge of a 401).
func TestAuthzenErrorsHaveAStringBodyAndACode(t *testing.T) {
	h := authzenTestAPI(newFakePDP(), &config.Config{})
	const path = "/service/" + fakeServiceRef + "/access/v1/evaluation"
	valid := fmt.Sprintf(`{"subject":{"type":"user","id":%q},"resource":{"type":"todo","id":"1"},"action":{"name":"can_read"}}`, fakeCaller)

	check := func(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
		}
		var msg string
		if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil || msg == "" {
			t.Errorf("the body must be the message as a JSON string: %q", rec.Body)
		}
		if got := rec.Header().Get("Licetum-Error-Code"); got != code {
			t.Errorf("Licetum-Error-Code %q, want %q", got, code)
		}
	}

	t.Run("no credentials is a 401 with the challenge", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(valid))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		check(t, rec, http.StatusUnauthorized, "auth_header_required")
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate %q, want Bearer", got)
		}
	})
	t.Run("another person without a trusted client is a 403", func(t *testing.T) {
		body := `{"subject":{"type":"user","id":"bob"},"resource":{"type":"todo","id":"1"},"action":{"name":"can_read"}}`
		check(t, authzenCall(h, path, body), http.StatusForbidden, "authzen_subject_mismatch")
	})
	t.Run("a request that is not valid is a 400", func(t *testing.T) {
		check(t, authzenCall(h, path, `{"subject":{"type":"user","id":"a"},"resource":{"type":"todo"},"action":{"name":"r"}}`), http.StatusBadRequest, "authzen_field_required")
	})
	t.Run("a body over one MiB is a 400, not a 413", func(t *testing.T) {
		body := `{"context":{"note":"` + strings.Repeat("a", 1<<20) + `"}}`
		check(t, authzenCall(h, path, body), http.StatusBadRequest, "validation_failed")
	})
}

// TestRequestIDLogCopyIsEscapedAndBounded: the access log carries a rejected X-Request-ID escaped and at most requestIDMax
// bytes long, so that one request cannot write a second line.
func TestRequestIDLogCopyIsEscapedAndBounded(t *testing.T) {
	if got := logRequestID("a\nb\"c"); got != `a\nb\"c` {
		t.Errorf("logRequestID = %q", got)
	}
	if got := logRequestID(strings.Repeat("x", 300)); len(got) != requestIDMax {
		t.Errorf("logRequestID keeps %d bytes, want %d", len(got), requestIDMax)
	}
}
