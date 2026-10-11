package model

// SimulateCheckInput is the body of POST /service/{service_id}/simulate/check: the answers for a subject with the
// given claims, as if it had presented a token with them. Nothing is verified: the claims are whatever the admin
// passes, and the answer is what the rules say for them.
type SimulateCheckInput struct {
	_       struct{}       `additionalProperties:"true"`
	Subject string         `json:"subject" minLength:"1" doc:"The subject to answer for, named as the rules name it (preferred_username, or sub)" example:"alice"`
	Claims  map[string]any `json:"claims" required:"false" nullable:"false" doc:"The claims the subject would have in its token; the rules look at them"`
	Context RequestContext `json:"context" required:"false" nullable:"true" doc:"The attributes the request would carry (see CheckInput.context); the rules with source context look at them"`
	Checks  []CheckRequest `json:"checks" minItems:"1" maxItems:"100" doc:"Questions to answer, up to 100 per request"`
	Explain bool           `json:"explain" required:"false" doc:"Return an explanation with every answer"`
}

// SimulatePermissionInput is the body of POST /service/{service_id}/simulate/permission: the permissions of a subject
// with the given claims (see SimulateCheckInput), filtered and paged like GET /service/{service_id}/permission.
type SimulatePermissionInput struct {
	_       struct{}       `additionalProperties:"true"`
	Subject string         `json:"subject" minLength:"1" doc:"The subject, named as the rules name it (preferred_username, or sub)" example:"alice"`
	Claims  map[string]any `json:"claims" required:"false" nullable:"false" doc:"The claims the subject would have in its token; the rules look at them"`
	Context RequestContext `json:"context" required:"false" nullable:"true" doc:"The attributes the request would carry (see CheckInput.context); the rules with source context look at them"`
	Type    string         `json:"type" required:"false" doc:"Only this resource type"`
	Access  string         `json:"access" required:"false" doc:"Only the resources (and types) that have this access level"`
	Limit   *int           `json:"limit" required:"false" minimum:"1" maximum:"1000" doc:"At most this many resources of the type; needs type"`
	After   string         `json:"after" required:"false" doc:"The resource id after which the page starts: the next_after of the previous page"`
}
