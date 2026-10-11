package model

// The OpenID AuthZEN Authorization API 1.0 (access evaluation), served per service: the service is
// in the path, the resource type is the name of a resource type of that service, the resource id is
// the resource id in the external system (omitted — the type as a whole), the action name is an
// access level. The subject is the caller: subject.id must be the subject of the bearer token.

// AuthzenSubject is the subject of an evaluation.
type AuthzenSubject struct {
	Type       string         `json:"type" doc:"Subject type (not interpreted)" example:"user"`
	ID         string         `json:"id" doc:"Subject id: must be the subject of the bearer token (preferred_username, or sub)" example:"alice"`
	Properties map[string]any `json:"properties" required:"false" doc:"Ignored: the claims of the token are used"`
}

// AuthzenResource is the resource of an evaluation.
type AuthzenResource struct {
	Type       string         `json:"type" doc:"Name of the resource type in the service" example:"branch"`
	ID         string         `json:"id" required:"false" doc:"Resource id in the external system; empty — the type as a whole" example:"b1"`
	Properties map[string]any `json:"properties" required:"false" doc:"Ignored"`
}

// AuthzenAction is the action of an evaluation.
type AuthzenAction struct {
	Name       string         `json:"name" doc:"Access level" example:"write"`
	Properties map[string]any `json:"properties" required:"false" doc:"Ignored"`
}

// AuthzenEvaluationRequest is a single access evaluation request.
type AuthzenEvaluationRequest struct {
	Subject  *AuthzenSubject  `json:"subject" required:"false" doc:"The caller"`
	Resource *AuthzenResource `json:"resource" required:"false" doc:"What is accessed"`
	Action   *AuthzenAction   `json:"action" required:"false" doc:"The access level asked for"`
	Context  map[string]any   `json:"context" required:"false" doc:"Optional; {\"explain\": true} adds the explanation of the decision to the response context"`
}

// AuthzenEvaluationResponse is the decision.
type AuthzenEvaluationResponse struct {
	Decision bool           `json:"decision" doc:"Whether the access is granted"`
	Context  map[string]any `json:"context,omitempty" doc:"With explain: reason_admin holds the explanation"`
}

// AuthzenEvaluationsRequest is a batch: the top-level subject, resource, action and context are
// defaults for the evaluations that omit them.
type AuthzenEvaluationsRequest struct {
	Subject     *AuthzenSubject            `json:"subject" required:"false" doc:"Default subject"`
	Resource    *AuthzenResource           `json:"resource" required:"false" doc:"Default resource"`
	Action      *AuthzenAction             `json:"action" required:"false" doc:"Default action"`
	Context     map[string]any             `json:"context" required:"false" doc:"Default context"`
	Evaluations []AuthzenEvaluationRequest `json:"evaluations" minItems:"1" maxItems:"100" doc:"The evaluations, up to 100"`
}

// AuthzenEvaluationsResponse holds one decision per evaluation, in order.
type AuthzenEvaluationsResponse struct {
	Evaluations []AuthzenEvaluationResponse `json:"evaluations" nullable:"false" doc:"Decisions in the order asked"`
}
