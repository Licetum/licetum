package model

// The OpenID AuthZEN Authorization API 1.0, served per service: the service is in the path, the resource type is the
// name of a resource type of that service, the resource id is the resource id in the external system ("*" — the type as
// a whole), the action name is an access level. The subject is the caller (subject.id is the subject of the bearer
// token), or — for a client application the service trusts — any person, with the claims given as subject.properties.
//
// Each endpoint has its own request type. Validate checks a request as its endpoint states it: which fields are
// required, which are ignored, and the bounds of the subject and of the context. A validation error is apperr 422; the
// AuthZEN surface answers it as 400 (the server maps it, see badRequest).

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// AuthzenWholeType is the resource id that names the type as a whole in an evaluation, and in the searches that read a
// resource id. A resource whose id is "*" cannot be asked about on its own.
const AuthzenWholeType = "*"

// authzenLicetumKey is the key of the Licetum settings in the top-level context of an AuthZEN request. It is not an
// attribute of the rules (see ParseAuthzenContext).
const authzenLicetumKey = "licetum"

// The bounds of a subject, in bytes: the checks of the on_behalf routes have always counted them so.
const (
	subjectTypeMax       = 64
	subjectIDMax         = 256
	subjectPropertiesMax = 100
	subjectPropertiesLen = 16 * 1024
	// subjectGroupType is the subject type of a group in the vocabulary of the API: a person is never a group.
	subjectGroupType = "group"
)

// AuthzenSubject is the subject of an evaluation or of a search.
type AuthzenSubject struct {
	_          struct{}       `additionalProperties:"true"`
	Type       string         `json:"type" doc:"Subject type (not interpreted): 1 to 64 bytes, and not group" example:"user"`
	ID         string         `json:"id" required:"false" doc:"Subject id: 1 to 256 bytes, no control characters, and not a group (group:…). Required by evaluations and by the resource and action searches; ignored by the subject search, which returns it. It is the subject of the bearer token (preferred_username, or sub) unless the caller is a client application the service trusts" example:"alice"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"The claims of a person other than the holder of the token, given by a trusted client: at most 100 claims and 16 KiB of JSON. Ignored for the holder of the token, whose claims come from the token"`
}

// AuthzenResource is the resource of an evaluation or of a search.
type AuthzenResource struct {
	_          struct{}       `additionalProperties:"true"`
	Type       string         `json:"type" doc:"Name of the resource type in the service" example:"branch"`
	ID         string         `json:"id,omitempty" required:"false" doc:"Resource id in the external system. An evaluation requires it, and * names the type as a whole; the action search reads an empty id or * as the type as a whole; the resource search ignores it" example:"b1"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"Ignored in a request; the resource search returns the name of the resource here"`
}

// AuthzenAction is the action of an evaluation or of a search.
type AuthzenAction struct {
	_          struct{}       `additionalProperties:"true"`
	Name       string         `json:"name" doc:"Access level" example:"write"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"Ignored"`
}

// AuthzenLicetum holds the settings of Licetum that an AuthZEN request carries in the top-level key licetum of its context.
// A licetum nested in an attribute is an attribute like any other.
type AuthzenLicetum struct {
	MinRevision string `json:"min_revision,omitempty" doc:"The revision of an admin write (the X-Authz-Revision header of its response): the answer reflects every write up to it. A decimal number, as a string"`
	Explain     bool   `json:"explain,omitempty" doc:"Return the explanation of the decision in context.reason_admin"`
}

// AuthzenEvaluationRequest is a single access evaluation request: the subject, the resource (its type and id) and the
// action are required.
type AuthzenEvaluationRequest struct {
	_        struct{}         `additionalProperties:"true"`
	Subject  *AuthzenSubject  `json:"subject" required:"false" doc:"The person asked about (subject.type and subject.id are required)"`
	Resource *AuthzenResource `json:"resource" required:"false" doc:"What is accessed (resource.type and resource.id are required; * is the type as a whole)"`
	Action   *AuthzenAction   `json:"action" required:"false" doc:"The access level asked for (action.name is required)"`
	Context  map[string]any   `json:"context" required:"false" doc:"The attributes of the request that the rules may read (conditions with source context). context.licetum holds the settings of Licetum: {\"explain\": true} adds the explanation of the decision to the response context"`
}

// AuthzenEvaluationResponse is the decision.
type AuthzenEvaluationResponse struct {
	Decision bool           `json:"decision" doc:"Whether the access is granted"`
	Context  map[string]any `json:"context,omitempty" doc:"With explain: reason_admin holds the explanation. In a batch, error holds the status and the message of an evaluation that could not be decided (the decision is then false); a short-circuited batch names the semantic in reason"`
}

// The evaluation semantics of a batch (options.evaluations_semantic).
const (
	AuthzenExecuteAll          = "execute_all"
	AuthzenDenyOnFirstDeny     = "deny_on_first_deny"
	AuthzenPermitOnFirstPermit = "permit_on_first_permit"
)

// AuthzenOptions are the options of a batch.
type AuthzenOptions struct {
	_                   struct{} `additionalProperties:"true"`
	EvaluationsSemantic string   `json:"evaluations_semantic" required:"false" enum:"execute_all,deny_on_first_deny,permit_on_first_permit" doc:"execute_all (the default) answers every evaluation; deny_on_first_deny stops after the first denial or error; permit_on_first_permit stops after the first permit. The evaluations after the one that stops the batch are not answered"`
}

// AuthzenEvaluationsRequest is a batch: the top-level subject, resource, action and context are defaults for the
// evaluations that omit them. Without evaluations it is a single evaluation.
type AuthzenEvaluationsRequest struct {
	_           struct{}                   `additionalProperties:"true"`
	Subject     *AuthzenSubject            `json:"subject" required:"false" doc:"Default subject"`
	Resource    *AuthzenResource           `json:"resource" required:"false" doc:"Default resource"`
	Action      *AuthzenAction             `json:"action" required:"false" doc:"Default action"`
	Context     map[string]any             `json:"context" required:"false" doc:"Default context"`
	Options     *AuthzenOptions            `json:"options" required:"false" doc:"How the evaluations are executed"`
	Evaluations []AuthzenEvaluationRequest `json:"evaluations" required:"false" maxItems:"100" doc:"The evaluations, up to 100. Absent or empty: the top-level subject, resource, action and context are one evaluation, and the answer is a single decision"`
}

// AuthzenEvaluationsResponse holds one decision per evaluation, in order; a request without evaluations gets the
// decision itself.
type AuthzenEvaluationsResponse struct {
	Decision    *bool                       `json:"decision,omitempty" doc:"The decision of a request without evaluations; absent when evaluations were given"`
	Context     map[string]any              `json:"context,omitempty" doc:"With explain: reason_admin holds the explanation of the decision of a request without evaluations"`
	Evaluations []AuthzenEvaluationResponse `json:"evaluations,omitempty" doc:"Decisions in the order asked (fewer when the semantic stopped the batch)"`
}

// AuthzenPageRequest asks for one page of a search.
type AuthzenPageRequest struct {
	_          struct{}       `additionalProperties:"true"`
	Token      string         `json:"token" required:"false" doc:"The next_token of the previous page. The subject, resource, action, context and limit must be the same as in the request that gave it"`
	Limit      *int           `json:"limit" required:"false" minimum:"0" doc:"At most this many results; absent or 0 — the most the service gives in one page (1000)"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"Ignored"`
}

// AuthzenPageResponse describes a page of a search.
type AuthzenPageResponse struct {
	NextToken string `json:"next_token" doc:"Pass it as page.token to read the next page; empty on the last page"`
	Count     int    `json:"count" doc:"The number of results on this page"`
	Total     *int   `json:"total,omitempty" required:"false" doc:"The number of results of the whole search at the time of the request; absent when the search does not know it (the subject search)"`
}

// AuthzenSubjectSearchRequest asks for the subjects that may do the action on the resource. Only the type of the subject is
// used: subject.id is ignored. The resource type and the action name are required; the resource id is optional.
type AuthzenSubjectSearchRequest struct {
	_        struct{}            `additionalProperties:"true"`
	Subject  *AuthzenSubject     `json:"subject" required:"false" doc:"The subject: only its type is used (subject.type is required; subject.id is ignored)"`
	Resource *AuthzenResource    `json:"resource" required:"false" doc:"The resource (resource.type is required; an empty resource.id, or *, is the type as a whole)"`
	Action   *AuthzenAction      `json:"action" required:"false" doc:"The access level (action.name is required)"`
	Context  map[string]any      `json:"context" required:"false" doc:"The attributes of the request that the rules may read (conditions with source context)"`
	Page     *AuthzenPageRequest `json:"page" required:"false" doc:"Which page of the results is asked for"`
}

// AuthzenResourceSearchRequest asks for the resources of a type on which the subject may do the action. The resource id is
// ignored: the subject (its type and id), the resource type and the action name are required.
type AuthzenResourceSearchRequest struct {
	_        struct{}            `additionalProperties:"true"`
	Subject  *AuthzenSubject     `json:"subject" required:"false" doc:"The person asked about (subject.type and subject.id are required)"`
	Resource *AuthzenResource    `json:"resource" required:"false" doc:"The type of the resources searched for (resource.type is required; resource.id is ignored)"`
	Action   *AuthzenAction      `json:"action" required:"false" doc:"The access level (action.name is required)"`
	Context  map[string]any      `json:"context" required:"false" doc:"The attributes of the request that the rules may read (conditions with source context)"`
	Page     *AuthzenPageRequest `json:"page" required:"false" doc:"Which page of the results is asked for"`
}

// AuthzenActionSearchRequest asks for the access levels the subject has on a resource, or on the type as a whole when the
// resource id is empty (or *). The request has no action: none is required.
type AuthzenActionSearchRequest struct {
	_        struct{}            `additionalProperties:"true"`
	Subject  *AuthzenSubject     `json:"subject" required:"false" doc:"The person asked about (subject.type and subject.id are required)"`
	Resource *AuthzenResource    `json:"resource" required:"false" doc:"The resource (resource.type is required; an empty resource.id, or *, is the type as a whole)"`
	Context  map[string]any      `json:"context" required:"false" doc:"The attributes of the request that the rules may read (conditions with source context)"`
	Page     *AuthzenPageRequest `json:"page" required:"false" doc:"Which page of the results is asked for"`
}

// AuthzenSubjectSearchResponse lists the subjects that are allowed.
type AuthzenSubjectSearchResponse struct {
	Page    *AuthzenPageResponse `json:"page,omitempty" doc:"Paging of the results"`
	Context map[string]any       `json:"context,omitempty" doc:"Extra information; subjects_registry is disabled while the registry of known subjects is off"`
	Results []AuthzenSubject     `json:"results" nullable:"false" doc:"The subjects that may do the action on the resource"`
}

// AuthzenResourceSearchResponse lists the resources that are allowed.
type AuthzenResourceSearchResponse struct {
	Page    *AuthzenPageResponse `json:"page,omitempty" doc:"Paging of the results"`
	Context map[string]any       `json:"context,omitempty" doc:"Extra information"`
	Results []AuthzenResource    `json:"results" nullable:"false" doc:"The resources the subject may do the action on; properties.name is the name of the resource"`
}

// AuthzenActionSearchResponse lists the actions that are allowed.
type AuthzenActionSearchResponse struct {
	Page    *AuthzenPageResponse `json:"page,omitempty" doc:"Paging of the results"`
	Context map[string]any       `json:"context,omitempty" doc:"Extra information"`
	Results []AuthzenAction      `json:"results" nullable:"false" doc:"The access levels the subject has on the resource"`
}

// AuthzenMetadata is the Policy Decision Point metadata of a service, served at
// /.well-known/authzen-configuration/service/{id}.
type AuthzenMetadata struct {
	PolicyDecisionPoint       string `json:"policy_decision_point" doc:"The identifier of the Policy Decision Point: the base URL of the service, which is also where the default paths of the API are appended" example:"https://licetum.example.com/service/orders"`
	AccessEvaluationEndpoint  string `json:"access_evaluation_endpoint" doc:"URL of the Access Evaluation API"`
	AccessEvaluationsEndpoint string `json:"access_evaluations_endpoint" doc:"URL of the Access Evaluations API"`
	SearchSubjectEndpoint     string `json:"search_subject_endpoint" doc:"URL of the Subject Search API"`
	SearchActionEndpoint      string `json:"search_action_endpoint" doc:"URL of the Action Search API"`
	SearchResourceEndpoint    string `json:"search_resource_endpoint" doc:"URL of the Resource Search API"`
}

// ---------- validation ----------

// Validate checks an evaluation on its own: the subject, the resource (its type and id) and the action are required.
func (r *AuthzenEvaluationRequest) Validate() error {
	return checkEvaluation("", r.Subject, r.Resource, r.Action, r.Context)
}

// Validate checks a batch. Without evaluations, the top-level fields are one evaluation, which must be complete. With
// evaluations, each one is checked with the defaults of the batch filled in for what it omits, and named by its index.
func (r *AuthzenEvaluationsRequest) Validate() error {
	if len(r.Evaluations) == 0 {
		return checkEvaluation("", r.Subject, r.Resource, r.Action, r.Context)
	}
	for i, e := range r.Evaluations {
		sub, res, act, attrs := e.Subject, e.Resource, e.Action, e.Context
		if sub == nil {
			sub = r.Subject
		}
		if res == nil {
			res = r.Resource
		}
		if act == nil {
			act = r.Action
		}
		if attrs == nil {
			attrs = r.Context
		}
		if err := checkEvaluation("evaluations["+strconv.Itoa(i)+"].", sub, res, act, attrs); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a subject search: the subject type, the resource type and the action name are required; the subject id
// is not read.
func (r *AuthzenSubjectSearchRequest) Validate() error {
	if err := checkSubject("subject", r.Subject, false); err != nil {
		return err
	}
	if err := checkResourceType(r.Resource); err != nil {
		return err
	}
	if err := checkActionName(r.Action); err != nil {
		return err
	}
	return checkContext(r.Context)
}

// Validate checks a resource search: the subject (its type and id), the resource type and the action name are required; the
// resource id is not read.
func (r *AuthzenResourceSearchRequest) Validate() error {
	if err := checkSubject("subject", r.Subject, true); err != nil {
		return err
	}
	if err := checkResourceType(r.Resource); err != nil {
		return err
	}
	if err := checkActionName(r.Action); err != nil {
		return err
	}
	return checkContext(r.Context)
}

// Validate checks an action search: the subject (its type and id) and the resource type are required; the resource id is
// optional, and there is no action to check.
func (r *AuthzenActionSearchRequest) Validate() error {
	if err := checkSubject("subject", r.Subject, true); err != nil {
		return err
	}
	if err := checkResourceType(r.Resource); err != nil {
		return err
	}
	return checkContext(r.Context)
}

// authzenRequired is the error of a field that the endpoint requires and the request does not carry.
func authzenRequired(field string) error {
	return apperr.Unprocessable(i18n.AuthzenFieldRequired, field)
}

// checkEvaluation checks the subject, the resource and the action of an evaluation, and its context. field names the
// evaluation in the errors: "" for a request on its own, "evaluations[2]." for an evaluation of a batch.
func checkEvaluation(field string, sub *AuthzenSubject, res *AuthzenResource, act *AuthzenAction, attrs map[string]any) error {
	if err := checkSubject(field+"subject", sub, true); err != nil {
		return err
	}
	if res == nil {
		return authzenRequired(field + "resource")
	}
	if res.Type == "" {
		return authzenRequired(field + "resource.type")
	}
	if res.ID == "" {
		return authzenRequired(field + "resource.id")
	}
	if err := checkActionName(act); err != nil {
		return err
	}
	return checkContext(attrs)
}

// checkSubject checks the subject of a request: it is required, and its type is required and bounded. With withID, its id
// is required and the subject is checked as PrepareSubject checks it; without it (the subject search, which returns the
// ids) the type and the claims are checked. field names the subject in the errors.
func checkSubject(field string, s *AuthzenSubject, withID bool) error {
	if s == nil {
		return authzenRequired(field)
	}
	if s.Type == "" {
		return authzenRequired(field + ".type")
	}
	if !withID {
		if err := checkSubjectType(s.Type); err != nil {
			return err
		}
		return checkSubjectProperties(s.Properties)
	}
	if s.ID == "" {
		return authzenRequired(field + ".id")
	}
	_, err := PrepareSubject(s.Type, s.ID, s.Properties)
	return err
}

// checkResourceType checks that the resource names a type. Its id is for the endpoint to read or to ignore.
func checkResourceType(r *AuthzenResource) error {
	if r == nil {
		return authzenRequired("resource")
	}
	if r.Type == "" {
		return authzenRequired("resource.type")
	}
	return nil
}

// checkActionName checks that the action names an access level.
func checkActionName(a *AuthzenAction) error {
	if a == nil {
		return authzenRequired("action")
	}
	if a.Name == "" {
		return authzenRequired("action.name")
	}
	return nil
}

// checkContext checks the context of a request: its Licetum settings, and the bounds of its attributes.
func checkContext(raw map[string]any) error {
	_, _, err := ParseAuthzenContext(raw)
	return err
}

// ---------- subjects ----------

// PreparedSubject is a subject checked for a decision: the type and the id as they were given, and the claims the rules
// read.
type PreparedSubject struct {
	Type   string
	ID     string
	Claims map[string]any
}

// PrepareSubject checks a subject and builds the claims the decision reads from its properties. The type is checked when
// it is given: the legacy calls on behalf of a person name the person by the id alone, and pass "". The claims are a copy of
// the properties, with preferred_username set to the id unless the properties hold a truthy one (the rules that name a
// person read it).
//
// The id is 1 to 256 bytes, holds no control character, and does not look like a group (group:…). The properties are at
// most 100 claims and 16 KiB of JSON.
func PrepareSubject(typ, id string, properties map[string]any) (PreparedSubject, error) {
	if typ != "" {
		if err := checkSubjectType(typ); err != nil {
			return PreparedSubject{}, err
		}
	}
	if id == "" || len(id) > subjectIDMax || strings.HasPrefix(id, GroupSubjectPrefix) || strings.ContainsFunc(id, unicode.IsControl) {
		return PreparedSubject{}, apperr.Unprocessable(i18n.DelegationSubjectInvalid)
	}
	if err := checkSubjectProperties(properties); err != nil {
		return PreparedSubject{}, err
	}
	claims := make(map[string]any, len(properties)+1)
	for k, v := range properties {
		claims[k] = v
	}
	if !truthy(claims["preferred_username"]) {
		claims["preferred_username"] = id
	}
	return PreparedSubject{Type: typ, ID: id, Claims: claims}, nil
}

// checkSubjectType checks the type of a subject: 1 to 64 bytes, and not group.
func checkSubjectType(typ string) error {
	if typ == "" || len(typ) > subjectTypeMax || typ == subjectGroupType {
		return apperr.Unprocessable(i18n.AuthzenSubjectTypeInvalid)
	}
	return nil
}

// checkSubjectProperties checks the claims of a subject: at most 100 claims, and 16 KiB of JSON.
func checkSubjectProperties(properties map[string]any) error {
	if len(properties) > subjectPropertiesMax {
		return apperr.Unprocessable(i18n.DelegationClaimsInvalid)
	}
	if len(properties) > 0 {
		encoded, err := json.Marshal(properties)
		if err != nil || len(encoded) > subjectPropertiesLen {
			return apperr.Unprocessable(i18n.DelegationClaimsInvalid)
		}
	}
	return nil
}

// truthy is the truth value of a claim as Python has it, which the rules and the on_behalf routes follow (the same rule
// as engine.PyTruthy): "", 0, false, null and empty lists and objects are false.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// ---------- context ----------

// ParseAuthzenContext reads the context of an AuthZEN request. The Licetum settings are read from the key licetum of the
// top-level context: an object with min_revision (a decimal revision, as a string) and explain (a boolean), and no other
// field. Anything else under that key is refused.
//
// The other keys are the attributes of the request, and they are read leniently: a value that is an object, or a list that
// holds a list or an object, and a name that is not a valid one, are ignored, as if the request did not carry them. The
// bounds are refused: at most 64 attributes (counted once the ignored ones are left out), a string of at most 1024 bytes,
// and a list of at most 64 items.
func ParseAuthzenContext(raw map[string]any) (RequestContext, AuthzenLicetum, error) {
	var settings AuthzenLicetum
	if v, ok := raw[authzenLicetumKey]; ok {
		s, err := parseAuthzenLicetum(v)
		if err != nil {
			return nil, AuthzenLicetum{}, err
		}
		settings = s
	}
	attrs := RequestContext{}
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		v := raw[name]
		if name == authzenLicetumKey || !ValidContextName(name) || !isScalarContextValue(v) {
			continue
		}
		if !contextValueFits(v) {
			return nil, AuthzenLicetum{}, apperr.Unprocessable(i18n.ContextValueTooLarge, name, maxContextValueLen, maxContextListLen)
		}
		attrs[name] = v
	}
	if len(attrs) > maxContextAttrs {
		return nil, AuthzenLicetum{}, apperr.Unprocessable(i18n.ContextTooLarge, maxContextAttrs)
	}
	return attrs, settings, nil
}

// parseAuthzenLicetum reads the value of the key licetum: an object with min_revision and explain, and no other field.
func parseAuthzenLicetum(v any) (AuthzenLicetum, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return AuthzenLicetum{}, apperr.Unprocessable(i18n.AuthzenLicetumInvalid)
	}
	var out AuthzenLicetum
	for key, value := range obj {
		switch key {
		case "min_revision":
			s, ok := value.(string)
			if !ok {
				return AuthzenLicetum{}, apperr.Unprocessable(i18n.AuthzenLicetumInvalid)
			}
			if s != "" {
				if _, err := strconv.ParseUint(s, 10, 63); err != nil {
					return AuthzenLicetum{}, apperr.Unprocessable(i18n.AuthzenLicetumInvalid)
				}
			}
			out.MinRevision = s
		case "explain":
			b, ok := value.(bool)
			if !ok {
				return AuthzenLicetum{}, apperr.Unprocessable(i18n.AuthzenLicetumInvalid)
			}
			out.Explain = b
		default:
			return AuthzenLicetum{}, apperr.Unprocessable(i18n.AuthzenLicetumInvalid)
		}
	}
	return out, nil
}
