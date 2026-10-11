package model

import "time"

// DelegatedCheckInput is the body of POST /service/{id}/on_behalf/check: the questions of POST /service/{id}/check, asked
// for a person the caller names instead of the holder of the token.
type DelegatedCheckInput struct {
	_       struct{}       `additionalProperties:"true"`
	Subject string         `json:"subject" minLength:"1" maxLength:"256" doc:"The person, as the rules name them (their preferred_username, or sub). It must not look like a group (group:…)" example:"alice"`
	Claims  map[string]any `json:"claims" required:"false" nullable:"true" doc:"The claims the person's own token would carry: the ones the rules of the service read (see GET /service/{id}/policy/claims in the admin API). preferred_username defaults to the subject. The caller vouches for them"`
	Checks  []CheckRequest `json:"checks" minItems:"1" maxItems:"100" doc:"Questions to answer, up to 100 per request"`
	Explain bool           `json:"explain" required:"false" doc:"Return an explanation with every answer"`
	Context RequestContext `json:"context" required:"false" nullable:"true" doc:"Attributes of the request the rules may read (conditions with source context)"`
}

// DelegatedPermissionInput is the body of POST /service/{id}/on_behalf/permission: GET /service/{id}/permission for a
// person the caller names.
type DelegatedPermissionInput struct {
	_       struct{}       `additionalProperties:"true"`
	Subject string         `json:"subject" minLength:"1" maxLength:"256" doc:"The person, as the rules name them (their preferred_username, or sub). It must not look like a group (group:…)" example:"alice"`
	Claims  map[string]any `json:"claims" required:"false" nullable:"true" doc:"The claims the person's own token would carry (see the check). preferred_username defaults to the subject"`
	Context RequestContext `json:"context" required:"false" nullable:"true" doc:"Attributes of the request the rules may read (conditions with source context)"`
	Type    string         `json:"type" required:"false" doc:"Only this resource type"`
	Access  string         `json:"access" required:"false" doc:"Only the resources (and types) that have this access level"`
	Limit   int            `json:"limit" required:"false" minimum:"1" maximum:"1000" doc:"At most this many resources of the type; needs type. Absent: all of them"`
	After   string         `json:"after" required:"false" doc:"The resource id after which the page starts: the next_after of the previous page"`
}

// DelegatedCall is one record of the audit of calls made on behalf of a person.
type DelegatedCall struct {
	ID            int64     `json:"id" doc:"Record id; a later call has a higher one"`
	At            time.Time `json:"at" doc:"When the call was made"`
	CallerClient  string    `json:"caller_client" doc:"The client the caller's token was issued to (azp or client_id): the trusted client that asked"`
	CallerSubject *string   `json:"caller_subject" doc:"The subject of the caller's token (for a client-credentials token, its service account); null when the token had none"`
	Subject       string    `json:"subject" doc:"The person the call was made for"`
	Operation     string    `json:"operation" enum:"check,permission" doc:"check or permission"`
	Questions     int       `json:"questions" doc:"How many questions a check asked; 0 for a permission"`
	Allowed       int       `json:"allowed" doc:"How many of them were allowed"`
	RequestID     *string   `json:"request_id" doc:"The id of the request, as the logs carry it"`
}

// DelegatedCallList is a page of the audit, the latest first.
type DelegatedCallList struct {
	Items      []DelegatedCall `json:"items" nullable:"false" doc:"The calls, the latest first"`
	NextBefore *int64          `json:"next_before" doc:"Pass it as before to read the next page; null on the last page"`
}

// PolicyClaim is an attribute the policy of a service reads, and how.
type PolicyClaim struct {
	Name        string `json:"name" doc:"The claim, or the request attribute"`
	Source      string `json:"source" enum:"claim,context" doc:"claim — read from the token; context — read from the attributes of the request"`
	Rules       int    `json:"rules" doc:"In how many conditions of rules and their guards that apply to the service"`
	Targets     int    `json:"targets" doc:"In how many claim-based targets (resource_from_claim) of those rules and their roles"`
	Memberships int    `json:"memberships" doc:"In how many conditions of group memberships (groups are shared by every service)"`
}
