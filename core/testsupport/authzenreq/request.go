// Package authzenreq builds the requests of the AuthZEN API of a service (the OpenID AuthZEN Authorization API 1.0 as
// core/server/authzen.go serves it) and decodes its answers, for the integration tests, the contracts runner and the load
// test of the check API. It is the one place that knows the wire format: where the service and the type go in the path,
// how the type as a whole is named, how freshness is asked for, where the claims of another person and the request
// attributes go, and where a decision, an explanation or an error comes back. A change of that format is a change here.
//
// A request is a Request: the method, the path with its query, and the body to send as JSON. The caller adds the address of
// the check API and the token. The shapes are those of core/model/authzen.go; the specification's names and nesting are
// kept, and only the members Licetum reads are written.
//
//	req := authzenreq.SearchResource("orders", authzenreq.SubjectOf(token), "branch", "read", authzenreq.AtLeast(rev))
//	status, body := send(req.Method, base+req.Path, token, req.Body)
//	ids, next, err := authzenreq.ResourceIDs(status, body)
//
// A builder panics when an option does not fit its request (Explain on a search, Page on an evaluation): a test that
// asks for something the request cannot carry is wrong, and must not run with the option silently lost.
//
// The decoders refuse what a test must not mistake for an answer. A batch of evaluations answers an evaluation it could not
// decide with decision false and context.error, so a decoder that read only the decision would count a failure as a
// denial: Decisions returns an *EvaluationError instead. An error status is a *StatusError, whatever its body holds.
package authzenreq

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/licetum/licetum/core/model"
)

// SubjectType is the type of the subjects the builders write.
const SubjectType = "user"

// MaxEvaluations is the most evaluations the API takes in one request.
const MaxEvaluations = 100

// ConsistencyHeader is the request header a test server turns into the freshness lever of the request (engine.WithConsistency);
// the real PDP has no such header, and so no network request can set the lever. Fresh sets it.
const ConsistencyHeader = "X-Test-Consistency"

// Request is an HTTP request to the AuthZEN API of a service.
type Request struct {
	Method string
	// Path is the path to be appended to the address of the PDP: /service/<slug>/access/v1/...
	Path string
	// Body is what is sent as JSON.
	Body any
	// Header holds the headers of the request that are not the credential: the lever of Fresh, which only a test server reads.
	// Nil when there are none.
	Header http.Header
}

// Resource is what an evaluation or a search is about: a type of the service and, for one resource, its id in the
// external system. An empty ID is the type as a whole, which the API spells * (model.AuthzenWholeType).
type Resource struct {
	Type string
	ID   string
}

// On is the resource of the type with the id.
func On(resourceType, id string) Resource { return Resource{Type: resourceType, ID: id} }

// Whole is the type as a whole: the capability of a type without resources, or the rules that cover all of its resources.
func Whole(resourceType string) Resource { return Resource{Type: resourceType} }

// wire is the resource as the API reads it. The id is always sent: an evaluation requires it, and the type as a whole is *.
func (r Resource) wire() map[string]any {
	id := r.ID
	if id == "" {
		id = model.AuthzenWholeType
	}
	return map[string]any{"type": r.Type, "id": id}
}

// Question is one evaluation of a batch: the resource and the action. The person and the context are the batch's.
type Question struct {
	Resource Resource
	Action   string
}

// Option adjusts a request. Fresh, AtLeast and WithContext fit every request; the others say what they fit.
type Option func(*spec)

// feature is an option that only some requests can carry.
type feature uint

const (
	featExplain feature = 1 << iota
	featSemantic
	featPage
	featProperties
)

var featureNames = []struct {
	feature feature
	name    string
}{{featExplain, "Explain"}, {featSemantic, "Semantic"}, {featPage, "Page"}, {featProperties, "WithProperties"}}

// spec is a request in the making: what the options asked for.
type spec struct {
	fresh      bool
	revision   int64
	attrs      map[string]any
	properties map[string]any
	semantic   string
	limit      int
	token      string
	explain    bool
	used       feature
}

func newSpec(opts []Option) *spec {
	s := &spec{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// allow panics when an option was given that the builder's request cannot carry.
func (s *spec) allow(builder string, fits feature) {
	var wrong []string
	for _, f := range featureNames {
		if s.used&f.feature != 0 && fits&f.feature == 0 {
			wrong = append(wrong, f.name)
		}
	}
	if len(wrong) > 0 {
		panic("authzenreq: " + builder + " cannot carry " + strings.Join(wrong, ", "))
	}
}

// Fresh asks for the freshest answer there is (the lever consistency=higher): the result cache is skipped and the sources of the
// resources are asked again. The PDP lets no caller ask for it: the request carries the header ConsistencyHeader, which a test
// server (harness.Levers) turns into the lever and the real PDP ignores.
func Fresh() Option {
	return func(s *spec) { s.fresh = true }
}

// AtLeast asks for an answer that reflects every write up to the revision (context.licetum.min_revision), the one the admin API
// returned for the write. Zero or less asks for nothing, so the revision of a cluster that has seen no write can be passed as is.
func AtLeast(revision int64) Option {
	return func(s *spec) { s.revision = max(revision, 0) }
}

// WithContext adds attributes to the context of the request, the ones the rules read (conditions with source context).
// The map is copied; a later call overrides the names it repeats.
func WithContext(attrs map[string]any) Option {
	return func(s *spec) {
		if s.attrs == nil {
			s.attrs = map[string]any{}
		}
		for k, v := range attrs {
			s.attrs[k] = v
		}
	}
}

// Explain asks an evaluation to explain its decision: context.licetum.explain, answered in context.reason_admin.
func Explain() Option {
	return func(s *spec) { s.explain, s.used = true, s.used|featExplain }
}

// Semantic sets options.evaluations_semantic of a batch (model.AuthzenExecuteAll and the others).
func Semantic(semantic string) Option {
	return func(s *spec) { s.semantic, s.used = semantic, s.used|featSemantic }
}

// WithProperties gives the claims of the person asked about, subject.properties: what a client application the service
// trusts does to ask about somebody else. The map is copied.
func WithProperties(claims map[string]any) Option {
	return func(s *spec) {
		if s.properties == nil {
			s.properties = map[string]any{}
		}
		for k, v := range claims {
			s.properties[k] = v
		}
		s.used |= featProperties
	}
}

// Page asks for one page of a search: at most limit results (zero — the most the service gives), and the page after the
// next_token of the previous one (empty — the first).
func Page(limit int, token string) Option {
	return func(s *spec) { s.limit, s.token, s.used = limit, token, s.used|featPage }
}

// path is the path of an endpoint of the service.
func (s *spec) path(ref, endpoint string) string {
	return "/service/" + url.PathEscape(ref) + "/access/v1/" + endpoint
}

// header is the headers the options asked for, nil when there are none.
func (s *spec) header() http.Header {
	if !s.fresh {
		return nil
	}
	return http.Header{ConsistencyHeader: {"higher"}}
}

// context is the context of the request, nil when it has no member.
func (s *spec) context() map[string]any {
	out := map[string]any{}
	for k, v := range s.attrs {
		out[k] = v
	}
	if s.explain || s.revision > 0 {
		// The settings of Licetum are the top-level key licetum of the context: an attribute of that name gives way to them.
		settings := map[string]any{}
		if s.explain {
			settings["explain"] = true
		}
		if s.revision > 0 {
			settings["min_revision"] = strconv.FormatInt(s.revision, 10)
		}
		out["licetum"] = settings
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *spec) subject(id string) map[string]any {
	out := map[string]any{"type": SubjectType, "id": id}
	if len(s.properties) > 0 {
		props := make(map[string]any, len(s.properties))
		for k, v := range s.properties {
			props[k] = v
		}
		out["properties"] = props
	}
	return out
}

func (s *spec) page() map[string]any {
	out := map[string]any{}
	if s.limit > 0 {
		out["limit"] = s.limit
	}
	if s.token != "" {
		out["token"] = s.token
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *spec) with(body map[string]any) map[string]any {
	if ctx := s.context(); ctx != nil {
		body["context"] = ctx
	}
	return body
}

// Evaluation is the access evaluation of the subject doing the action on the resource (POST …/access/v1/evaluation).
// The subject is the holder of the token (see SubjectOf), or, for a PEP client, another person given WithProperties.
func Evaluation(ref, subject string, resource Resource, action string, opts ...Option) Request {
	s := newSpec(opts)
	s.allow("Evaluation", featExplain|featProperties)
	return Request{Method: "POST", Path: s.path(ref, "evaluation"), Header: s.header(), Body: s.with(map[string]any{
		"subject":  s.subject(subject),
		"resource": resource.wire(),
		"action":   map[string]any{"name": action},
	})}
}

// Evaluations is the batch of the questions, all about the subject (POST …/access/v1/evaluations): the subject and the
// context are the defaults of the evaluations, which carry only the resource and the action. The API takes at most
// MaxEvaluations of them; the builder does not stop a test that sends more to see the refusal.
func Evaluations(ref, subject string, questions []Question, opts ...Option) Request {
	s := newSpec(opts)
	s.allow("Evaluations", featExplain|featProperties|featSemantic)
	items := make([]any, len(questions))
	for i, q := range questions {
		items[i] = map[string]any{"resource": q.Resource.wire(), "action": map[string]any{"name": q.Action}}
	}
	body := map[string]any{"subject": s.subject(subject), "evaluations": items}
	if s.semantic != "" {
		body["options"] = map[string]any{"evaluations_semantic": s.semantic}
	}
	return Request{Method: "POST", Path: s.path(ref, "evaluations"), Header: s.header(), Body: s.with(body)}
}

// SearchResource lists the resources of the type the subject may do the action on (POST …/access/v1/search/resource).
func SearchResource(ref, subject, resourceType, action string, opts ...Option) Request {
	s := newSpec(opts)
	s.allow("SearchResource", featPage|featProperties)
	body := map[string]any{
		"subject":  s.subject(subject),
		"resource": map[string]any{"type": resourceType},
		"action":   map[string]any{"name": action},
	}
	if p := s.page(); p != nil {
		body["page"] = p
	}
	return Request{Method: "POST", Path: s.path(ref, "search/resource"), Header: s.header(), Body: s.with(body)}
}

// SearchAction lists the actions the subject may do on the resource, or on the type as a whole (POST …/access/v1/search/action).
func SearchAction(ref, subject string, resource Resource, opts ...Option) Request {
	s := newSpec(opts)
	s.allow("SearchAction", featPage|featProperties)
	body := map[string]any{"subject": s.subject(subject), "resource": resource.wire()}
	if p := s.page(); p != nil {
		body["page"] = p
	}
	return Request{Method: "POST", Path: s.path(ref, "search/action"), Header: s.header(), Body: s.with(body)}
}

// SearchSubject lists the people of the directory of the service that may do the action on the resource (POST
// …/access/v1/search/subject). Only a PEP client with the search_subject operation
// may ask.
func SearchSubject(ref string, resource Resource, action string, opts ...Option) Request {
	s := newSpec(opts)
	s.allow("SearchSubject", featPage)
	body := map[string]any{
		"subject":  map[string]any{"type": SubjectType},
		"resource": resource.wire(),
		"action":   map[string]any{"name": action},
	}
	if p := s.page(); p != nil {
		body["page"] = p
	}
	return Request{Method: "POST", Path: s.path(ref, "search/subject"), Header: s.header(), Body: s.with(body)}
}
