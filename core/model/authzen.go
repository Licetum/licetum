package model

// The OpenID AuthZEN Authorization API 1.0, served per service: the service is in the path, the resource type is the
// name of a resource type of that service, the resource id is the resource id in the external system (omitted — the type
// as a whole), the action name is an access level. The subject is the caller (subject.id is the subject of the bearer
// token), or — for a client application the service trusts — any person, with the claims given as subject.properties.

// AuthzenSubject is the subject of an evaluation or of a search.
type AuthzenSubject struct {
	_          struct{}       `additionalProperties:"true"`
	Type       string         `json:"type" doc:"Subject type (not interpreted)" example:"user"`
	ID         string         `json:"id" required:"false" doc:"Subject id. Required by evaluations and by the resource and action searches; omitted by the subject search, which returns it. It is the subject of the bearer token (preferred_username, or sub) unless the caller is a client application the service trusts" example:"alice"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"The claims of a person other than the holder of the token, given by a trusted client; ignored for the holder of the token, whose claims come from the token"`
}

// AuthzenResource is the resource of an evaluation or of a search.
type AuthzenResource struct {
	_          struct{}       `additionalProperties:"true"`
	Type       string         `json:"type" doc:"Name of the resource type in the service" example:"branch"`
	ID         string         `json:"id,omitempty" required:"false" doc:"Resource id in the external system; empty — the type as a whole. Ignored by the resource search" example:"b1"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"Ignored in a request; the resource search returns the name of the resource here"`
}

// AuthzenAction is the action of an evaluation or of a search.
type AuthzenAction struct {
	_          struct{}       `additionalProperties:"true"`
	Name       string         `json:"name" doc:"Access level" example:"write"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"Ignored"`
}

// AuthzenEvaluationRequest is a single access evaluation request.
type AuthzenEvaluationRequest struct {
	_        struct{}         `additionalProperties:"true"`
	Subject  *AuthzenSubject  `json:"subject" required:"false" doc:"The person asked about"`
	Resource *AuthzenResource `json:"resource" required:"false" doc:"What is accessed"`
	Action   *AuthzenAction   `json:"action" required:"false" doc:"The access level asked for"`
	Context  map[string]any   `json:"context" required:"false" doc:"The attributes of the request that the rules may read (conditions with source context); {\"explain\": true} adds the explanation of the decision to the response context"`
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

// AuthzenEvaluationsRequest is a batch: the top-level subject, resource, action and context are
// defaults for the evaluations that omit them. Without evaluations it is a single evaluation.
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

// AuthzenSearchRequest is the request of the subject, resource and action searches. The entity searched for is
// given without its identifier: the subject search ignores subject.id, the resource search resource.id, and the action
// search has no action.
type AuthzenSearchRequest struct {
	_        struct{}            `additionalProperties:"true"`
	Subject  *AuthzenSubject     `json:"subject" required:"false" doc:"The subject; for the subject search only its type is used"`
	Resource *AuthzenResource    `json:"resource" required:"false" doc:"The resource; for the resource search only its type is used"`
	Action   *AuthzenAction      `json:"action" required:"false" doc:"The access level; not used by the action search"`
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
