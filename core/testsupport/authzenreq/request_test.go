package authzenreq_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata")

// built is a request as the tests of the callers see it.
type built struct {
	name string
	req  authzenreq.Request
}

func cases() []built {
	qs := []authzenreq.Question{
		{Resource: authzenreq.On("branch", "b1"), Action: "read"},
		{Resource: authzenreq.On("branch", "b2"), Action: "write"},
		{Resource: authzenreq.Whole("flag"), Action: "enabled"},
	}
	props := map[string]any{"preferred_username": "bob", "department": "sales", "groups": []string{"a", "b"}}
	return []built{
		{"evaluation", authzenreq.Evaluation("orders", "alice", authzenreq.On("branch", "b1"), "read")},
		{"evaluation_whole", authzenreq.Evaluation("orders", "alice", authzenreq.Whole("flag"), "enabled")},
		{"evaluation_explain", authzenreq.Evaluation("orders", "alice", authzenreq.On("branch", "b1"), "read", authzenreq.Explain())},
		{"evaluation_fresh", authzenreq.Evaluation("orders", "alice", authzenreq.On("branch", "b1"), "read",
			authzenreq.Fresh(), authzenreq.AtLeast(12), authzenreq.WithContext(map[string]any{"ip": "10.0.0.7"}))},
		{"evaluation_properties", authzenreq.Evaluation("orders", "bob", authzenreq.On("branch", "b1"), "read",
			authzenreq.WithProperties(props), authzenreq.Explain(), authzenreq.WithContext(map[string]any{"role": "auditor", "explain": false}))},
		{"evaluations", authzenreq.Evaluations("orders", "alice", qs)},
		{"evaluations_semantic", authzenreq.Evaluations("orders", "alice", qs,
			authzenreq.Semantic(model.AuthzenDenyOnFirstDeny), authzenreq.Explain(), authzenreq.AtLeast(3))},
		{"search_resource", authzenreq.SearchResource("orders", "alice", "branch", "read")},
		{"search_resource_page", authzenreq.SearchResource("orders", "alice", "branch", "read",
			authzenreq.Page(20, ""), authzenreq.WithContext(map[string]any{"ip": "10.0.0.7"}))},
		{"search_resource_next_page", authzenreq.SearchResource("orders", "alice", "branch", "read", authzenreq.Page(20, "eyJhIjoiYjIifQ"))},
		{"search_resource_properties", authzenreq.SearchResource("orders", "bob", "branch", "read", authzenreq.WithProperties(props), authzenreq.Fresh())},
		{"search_action", authzenreq.SearchAction("orders", "alice", authzenreq.On("branch", "b1"))},
		{"search_action_whole", authzenreq.SearchAction("orders", "alice", authzenreq.Whole("flag"), authzenreq.AtLeast(9))},
		{"search_subject", authzenreq.SearchSubject("orders", authzenreq.On("branch", "b1"), "read")},
		{"search_subject_page", authzenreq.SearchSubject("orders", authzenreq.Whole("branch"), "read", authzenreq.Page(50, "tok"), authzenreq.Fresh())},
	}
}

// goldenOf is the request as a file shows it.
func goldenOf(t *testing.T, r authzenreq.Request) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(struct {
		Method string      `json:"method"`
		Path   string      `json:"path"`
		Header http.Header `json:"header,omitempty"`
		Body   any         `json:"body"`
	}{r.Method, r.Path, r.Header, r.Body}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestRequestsMatchTheirGoldenFiles(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			got := goldenOf(t, c.req)
			file := filepath.Join("testdata", c.name+".json")
			if *update {
				if err := os.WriteFile(file, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs (go test -update rewrites it):\ngot:\n%s\nwant:\n%s", file, got, want)
			}
		})
	}
}

// endpointOf is the endpoint of a request below /service/orders/access/v1/: "evaluation", "search/resource", ...
func endpointOf(t *testing.T, r authzenreq.Request) string {
	t.Helper()
	u, err := url.Parse(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, ok := strings.CutPrefix(u.Path, "/service/orders/access/v1/")
	if !ok {
		t.Fatalf("not the path of the AuthZEN API of the service orders: %s", u.Path)
	}
	return endpoint
}

// strictly decodes the JSON of v into out, refusing a member that out has no field for.
func strictly(t *testing.T, v any, out any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		t.Fatalf("%v does not have the shape of core/model/authzen.go: %v\n%s", out, err, raw)
	}
}

// The oracle of the shapes is core/model/authzen.go: every member a builder writes is a field of the request type of its
// endpoint, and what the builder was given comes back out of the type.
func TestBodiesHaveTheShapeOfTheModel(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			endpoint := endpointOf(t, c.req)
			switch endpoint {
			case "evaluation":
				var m model.AuthzenEvaluationRequest
				strictly(t, c.req.Body, &m)
				if m.Subject == nil || m.Subject.Type != authzenreq.SubjectType || m.Subject.ID == "" || m.Resource == nil || m.Resource.Type == "" || m.Action == nil || m.Action.Name == "" {
					t.Errorf("an evaluation must be complete: %+v", m)
				}
				if m.Resource != nil && m.Resource.ID == "" {
					t.Errorf("an evaluation requires resource.id, * for the type as a whole: %+v", m.Resource)
				}
			case "evaluations":
				var m model.AuthzenEvaluationsRequest
				strictly(t, c.req.Body, &m)
				if m.Subject == nil || m.Subject.ID == "" || len(m.Evaluations) == 0 {
					t.Errorf("a batch must have a subject and evaluations: %+v", m)
				}
				for i, e := range m.Evaluations {
					if e.Resource == nil || e.Action == nil || e.Subject != nil || e.Context != nil {
						t.Errorf("evaluation %d must carry the resource and the action, and nothing the batch already says: %+v", i, e)
					}
					if e.Resource != nil && e.Resource.ID == "" {
						t.Errorf("evaluation %d requires resource.id, * for the type as a whole: %+v", i, e.Resource)
					}
				}
				if m.Options != nil && m.Options.EvaluationsSemantic == "" {
					t.Errorf("empty options: %+v", m.Options)
				}
			default:
				// Each search has its own request type; the subject and the resource are what the shape checks read.
				var subject *model.AuthzenSubject
				var resource *model.AuthzenResource
				switch endpoint {
				case "search/resource":
					var m model.AuthzenResourceSearchRequest
					strictly(t, c.req.Body, &m)
					subject, resource = m.Subject, m.Resource
				case "search/action":
					var m model.AuthzenActionSearchRequest
					strictly(t, c.req.Body, &m)
					subject, resource = m.Subject, m.Resource
				default:
					var m model.AuthzenSubjectSearchRequest
					strictly(t, c.req.Body, &m)
					subject, resource = m.Subject, m.Resource
				}
				if subject == nil || subject.Type != authzenreq.SubjectType || resource == nil || resource.Type == "" {
					t.Errorf("a search needs the subject type and the resource type: %+v %+v", subject, resource)
				}
				if endpoint == "search/resource" && resource.ID != "" {
					t.Errorf("the resource search ignores resource.id: %+v", resource)
				}
				if endpoint == "search/subject" && subject.ID != "" {
					t.Errorf("the subject search is about nobody in particular: %+v", subject)
				}
			}
		})
	}
}

// The committed schema of the PDP is the oracle of the routes; the PDP has no query parameter on them.
func TestRoutesExistInTheSchemaAndHaveNoQuery(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "pdp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `yaml:"name"`
				In     string `yaml:"in"`
				Schema struct {
					Enum []string `yaml:"enum"`
					Type string   `yaml:"type"`
				} `yaml:"schema"`
			} `yaml:"parameters"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			u, err := url.Parse(c.req.Path)
			if err != nil {
				t.Fatal(err)
			}
			op, ok := doc.Paths["/service/{ref}/access/v1/"+endpointOf(t, c.req)][strings.ToLower(c.req.Method)]
			if !ok {
				t.Fatalf("%s %s is no operation of core/schema/pdp.yaml", c.req.Method, u.Path)
			}
			if len(op.Parameters) != 1 || op.Parameters[0].In != "path" || op.Parameters[0].Name != "ref" {
				t.Errorf("the operation takes the service in the path and nothing else: %+v", op.Parameters)
			}
			if u.RawQuery != "" {
				t.Errorf("the PDP has no query parameter: %s", u.RawQuery)
			}
		})
	}
}

// The examples of the AuthZEN Authorization API 1.0 (access evaluation, evaluations and the three searches), reduced to the
// members Licetum reads: the builders write exactly them.
func TestBuildersWriteTheExamplesOfTheSpecification(t *testing.T) {
	const alice = "alice@acmecorp.com"
	for _, c := range []struct {
		file string
		req  authzenreq.Request
	}{
		{"spec_evaluation.json", authzenreq.Evaluation("orders", alice, authzenreq.On("account", "123"), "can_read",
			authzenreq.WithContext(map[string]any{"time": "1985-10-26T01:22-07:00"}))},
		{"spec_evaluations.json", authzenreq.Evaluations("orders", alice, []authzenreq.Question{
			{Resource: authzenreq.On("document", "1"), Action: "can_read"},
			{Resource: authzenreq.On("document", "2"), Action: "can_read"},
		}, authzenreq.WithContext(map[string]any{"time": "2024-05-31T15:22-07:00"}))},
		{"spec_evaluations_semantic.json", authzenreq.Evaluations("orders", alice, []authzenreq.Question{
			{Resource: authzenreq.On("document", "1"), Action: "can_read"},
			{Resource: authzenreq.On("document", "2"), Action: "can_edit"},
		}, authzenreq.Semantic(model.AuthzenPermitOnFirstPermit))},
		{"spec_search_subject.json", authzenreq.SearchSubject("orders", authzenreq.On("account", "123"), "can_read")},
		{"spec_search_resource.json", authzenreq.SearchResource("orders", alice, "account", "can_read")},
		{"spec_search_action.json", authzenreq.SearchAction("orders", alice, authzenreq.On("account", "123"),
			authzenreq.WithContext(map[string]any{"time": "2024-10-26T01:22-07:00"}))},
	} {
		t.Run(c.file, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatal(err)
			}
			var wantBody any
			if err := json.Unmarshal(want, &wantBody); err != nil {
				t.Fatal(err)
			}
			if got := normalized(t, c.req.Body); got != normalized(t, wantBody) {
				t.Errorf("got  %s\nwant %s", got, normalized(t, wantBody))
			}
		})
	}
}

func normalized(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var round any
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(round)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestFreshnessTravelsInTheBodyAndInATestHeader(t *testing.T) {
	ev := func(opts ...authzenreq.Option) authzenreq.Request {
		return authzenreq.Evaluation("orders", "alice", authzenreq.On("branch", "b1"), "read", opts...)
	}
	const path = "/service/orders/access/v1/evaluation"
	for _, c := range []struct {
		name     string
		opts     []authzenreq.Option
		revision string
		fresh    bool
	}{
		{"nothing", nil, "", false},
		{"fresh", []authzenreq.Option{authzenreq.Fresh()}, "", true},
		{"a revision", []authzenreq.Option{authzenreq.AtLeast(41)}, "41", false},
		{"revision zero", []authzenreq.Option{authzenreq.AtLeast(0)}, "", false},
		{"a negative revision", []authzenreq.Option{authzenreq.AtLeast(-5)}, "", false},
		{"both", []authzenreq.Option{authzenreq.Fresh(), authzenreq.AtLeast(7)}, "7", true},
	} {
		r := ev(c.opts...)
		if r.Path != path {
			t.Errorf("%s: the path is %s, and it has no query", c.name, r.Path)
		}
		body, _ := r.Body.(map[string]any)
		attrs, _ := body["context"].(map[string]any)
		settings, _ := attrs["licetum"].(map[string]any)
		if got, _ := settings["min_revision"].(string); got != c.revision {
			t.Errorf("%s: min_revision %q, want %q", c.name, got, c.revision)
		}
		if got := r.Header.Get(authzenreq.ConsistencyHeader) == "higher"; got != c.fresh {
			t.Errorf("%s: the lever header is %v, want %v", c.name, r.Header, c.fresh)
		}
	}
}

func TestTheServiceIsOneSegmentOfThePath(t *testing.T) {
	id := "6f1c1d52-33a5-5d0e-9d0e-0a3a8a0d7c11"
	if got := authzenreq.SearchAction(id, "alice", authzenreq.Whole("flag")).Path; got != "/service/"+id+"/access/v1/search/action" {
		t.Errorf("the id of a service: %s", got)
	}
	if got := authzenreq.SearchAction("a/b?c", "alice", authzenreq.Whole("flag")).Path; got != "/service/a%2Fb%3Fc/access/v1/search/action" {
		t.Errorf("a reference is escaped, not interpreted: %s", got)
	}
}

func TestOptionsAreCopiedAndMerged(t *testing.T) {
	attrs := map[string]any{"ip": "10.0.0.7"}
	claims := map[string]any{"preferred_username": "bob"}
	r := authzenreq.Evaluation("orders", "bob", authzenreq.On("branch", "b1"), "read",
		authzenreq.WithContext(attrs), authzenreq.WithContext(map[string]any{"ip": "10.0.0.8", "tier": "gold"}),
		authzenreq.WithProperties(claims), authzenreq.WithProperties(map[string]any{"department": "sales"}))
	attrs["ip"], claims["preferred_username"] = "changed", "changed"
	delete(attrs, "ip")

	body := normalized(t, r.Body)
	for _, want := range []string{`"ip":"10.0.0.8"`, `"tier":"gold"`, `"preferred_username":"bob"`, `"department":"sales"`} {
		if !strings.Contains(body, want) {
			t.Errorf("%s is missing from %s", want, body)
		}
	}
	// The same options build the same request twice, and the first one is not changed by the second.
	opts := []authzenreq.Option{authzenreq.Explain(), authzenreq.WithContext(map[string]any{"a": 1})}
	first := authzenreq.Evaluation("orders", "alice", authzenreq.Whole("flag"), "enabled", opts...)
	second := authzenreq.Evaluation("orders", "alice", authzenreq.Whole("flag"), "enabled", opts...)
	if normalized(t, first.Body) != normalized(t, second.Body) {
		t.Errorf("the same options built different requests:\n%s\n%s", normalized(t, first.Body), normalized(t, second.Body))
	}
}

// Explain is the Licetum setting of the top-level key licetum of the context. An attribute named explain is an attribute like
// any other, and an attribute named licetum gives way to the setting.
func TestExplainIsTheLicetumSettingOfTheContext(t *testing.T) {
	r := authzenreq.Evaluation("orders", "alice", authzenreq.Whole("flag"), "enabled",
		authzenreq.WithContext(map[string]any{"explain": false, "licetum": "x"}), authzenreq.Explain())
	if got := normalized(t, r.Body); !strings.Contains(got, `"context":{"explain":false,"licetum":{"explain":true}}`) {
		t.Errorf("%s", got)
	}
}

// The type as a whole is * on the wire, and so is a resource without an id: an evaluation requires the id.
func TestTheTypeAsAWholeIsSentAsAnAsterisk(t *testing.T) {
	for _, r := range []authzenreq.Resource{authzenreq.Whole("flag"), {Type: "flag"}} {
		req := authzenreq.Evaluation("orders", "alice", r, "enabled")
		if got := normalized(t, req.Body); !strings.Contains(got, `"resource":{"id":"*","type":"flag"}`) {
			t.Errorf("%+v: %s", r, got)
		}
	}
	if got := normalized(t, authzenreq.SearchAction("orders", "alice", authzenreq.Whole("flag")).Body); !strings.Contains(got, `"resource":{"id":"*","type":"flag"}`) {
		t.Errorf("the action search of the type as a whole: %s", got)
	}
}

func TestAnOptionTheRequestCannotCarryPanics(t *testing.T) {
	eval := func(opts ...authzenreq.Option) func() {
		return func() { authzenreq.Evaluation("orders", "alice", authzenreq.Whole("flag"), "enabled", opts...) }
	}
	for name, c := range map[string]struct {
		build func()
		names string
	}{
		"page on an evaluation":     {eval(authzenreq.Page(10, "")), "Evaluation cannot carry Page"},
		"semantic on an evaluation": {eval(authzenreq.Semantic(model.AuthzenExecuteAll)), "Evaluation cannot carry Semantic"},
		"several at once":           {eval(authzenreq.Page(1, ""), authzenreq.Semantic("x")), "Semantic, Page"},
		"explain on a search":       {func() { authzenreq.SearchResource("orders", "alice", "branch", "read", authzenreq.Explain()) }, "SearchResource cannot carry Explain"},
		"semantic on a search":      {func() { authzenreq.SearchAction("orders", "alice", authzenreq.Whole("flag"), authzenreq.Semantic("x")) }, "SearchAction cannot carry Semantic"},
		"properties on the subjects": {func() {
			authzenreq.SearchSubject("orders", authzenreq.Whole("flag"), "read", authzenreq.WithProperties(nil))
		}, "SearchSubject cannot carry WithProperties"},
		"page on a batch": {func() { authzenreq.Evaluations("orders", "alice", nil, authzenreq.Page(1, "")) }, "Evaluations cannot carry Page"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, c.names) {
					t.Errorf("panic %q does not say %q", msg, c.names)
				}
			}()
			c.build()
		})
	}
}

func TestOptionsEveryRequestCanCarry(t *testing.T) {
	all := []authzenreq.Option{authzenreq.Fresh(), authzenreq.AtLeast(2), authzenreq.WithContext(map[string]any{"ip": "10.0.0.7"})}
	for name, build := range map[string]func(...authzenreq.Option) authzenreq.Request{
		"evaluation": func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.Evaluation("s", "a", authzenreq.Whole("t"), "x", o...)
		},
		"evaluations": func(o ...authzenreq.Option) authzenreq.Request { return authzenreq.Evaluations("s", "a", nil, o...) },
		"resource": func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchResource("s", "a", "t", "x", o...)
		},
		"action": func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchAction("s", "a", authzenreq.Whole("t"), o...)
		},
		"subject": func(o ...authzenreq.Option) authzenreq.Request {
			return authzenreq.SearchSubject("s", authzenreq.Whole("t"), "x", o...)
		},
	} {
		r := build(all...)
		if r.Header.Get(authzenreq.ConsistencyHeader) != "higher" || !strings.Contains(normalized(t, r.Body), `"min_revision":"2"`) ||
			!strings.Contains(normalized(t, r.Body), `"ip":"10.0.0.7"`) {
			t.Errorf("%s: %s %v %s", name, r.Path, r.Header, normalized(t, r.Body))
		}
		if r.Method != http.MethodPost {
			t.Errorf("%s: %s", name, r.Method)
		}
	}
}
