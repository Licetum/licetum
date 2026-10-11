package model

import (
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// CheckRequest is one question: may the caller do action with a resource (or with a whole type)?
type CheckRequest struct {
	Type       string  `json:"type" doc:"Name of the resource type in this service" example:"branch"`
	ResourceID *string `json:"resource_id" required:"false" nullable:"true" doc:"Resource id in the external system. Omit it to ask about the type as a whole — creating new resources, or a permission without resources (a not_sync type)" example:"b1"`
	Action     string  `json:"action" doc:"Access level asked for" example:"write"`
}

// CheckInput is the body of POST /service/{id}/check.
type CheckInput struct {
	_       struct{}       `additionalProperties:"true"`
	Checks  []CheckRequest `json:"checks" minItems:"1" maxItems:"100" doc:"Questions to answer, up to 100 per request"`
	Explain bool           `json:"explain" required:"false" doc:"Return an explanation with every answer: which rules grant the level, which deny it, and which exceptions lift a denial"`
	Context RequestContext `json:"context" required:"false" nullable:"true" doc:"Attributes of this request that the rules may read (conditions with source context). Claims come from the token; these are what the request itself carries, such as the client address or the channel"`
}

// RequestContext holds the attributes a request carries besides its token: the values of the conditions with source
// context. A value is a scalar or a list of scalars.
type RequestContext map[string]any

// maxContextAttrs bounds the attributes of one request.
const maxContextAttrs = 64

// The bounds of a value: a string, and a list of strings.
const (
	maxContextValueLen = 1024
	maxContextListLen  = 64
)

// Validate checks the names and the number of the attributes; the values are compared as their text (see service.PyStr).
func (c RequestContext) Validate() error {
	if len(c) > maxContextAttrs {
		return apperr.Unprocessable(i18n.ContextTooLarge, maxContextAttrs)
	}
	for name, value := range c {
		if !ValidContextName(name) {
			return apperr.Unprocessable(i18n.ContextNameInvalid, name)
		}
		if !isScalarContextValue(value) {
			return apperr.Unprocessable(i18n.ContextValueNotScalar, name)
		}
		if !contextValueFits(value) {
			return apperr.Unprocessable(i18n.ContextValueTooLarge, name, maxContextValueLen, maxContextListLen)
		}
	}
	return nil
}

// isScalarContextValue reports whether a request attribute is a scalar, or a list of scalars: an object is not, nor is a
// list within a list. The rules compare an attribute as its text, so an object would be rendered whole into a cache key.
func isScalarContextValue(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return false
	case []any:
		for _, item := range x {
			switch item.(type) {
			case map[string]any, []any:
				return false
			}
		}
	}
	return true
}

// contextValueFits bounds the values of the request attributes: a string of at most maxContextValueLen characters, or a
// list of at most maxContextListLen items (each of them a string of the same bound). Anything else is a scalar.
func contextValueFits(v any) bool {
	switch x := v.(type) {
	case string:
		return len(x) <= maxContextValueLen
	case []any:
		if len(x) > maxContextListLen {
			return false
		}
		for _, item := range x {
			if s, ok := item.(string); ok && len(s) > maxContextValueLen {
				return false
			}
		}
	}
	return true
}

// ValidContextName reports whether a context attribute may have the name: letters, digits, '_', '-', '.' and ':',
// up to 128 characters.
func ValidContextName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		ok := c == '_' || c == '-' || c == '.' || c == ':' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			return false
		}
	}
	return true
}

// Anchor says where a rule is anchored.
type Anchor struct {
	Type       string  `json:"type" doc:"Name of the resource type the rule is anchored on"`
	ResourceID *string `json:"resource_id" doc:"The resource the rule is anchored on; null — the whole type"`
}

// RuleRef describes a rule that took part in an answer.
type RuleRef struct {
	RuleID    uuid.UUID `json:"rule_id" format:"uuid" doc:"Id of the assignment rule"`
	Effect    string    `json:"effect" enum:"allow,deny" doc:"allow or deny"`
	Exception bool      `json:"exception" doc:"An allow rule that lifts the denials above it"`
	Access    []string  `json:"access" nullable:"false" doc:"Access levels of the rule"`
	Anchor    Anchor    `json:"anchor" doc:"Where the rule is anchored; it covers this resource through the hierarchy"`
	AppliesTo string    `json:"applies_to" doc:"Why the rule applies to the caller: the subject, a group, a role, a claims predicate, or everyone" example:"group 4e2c…"`
}

// DenialRef is a denial that applies to the asked level.
type DenialRef struct {
	Rule             RuleRef   `json:"rule" doc:"The denying rule"`
	Level            string    `json:"level" doc:"The denied level that matched: the asked level, a level it includes, or the wildcard"`
	LiftedBy         []RuleRef `json:"lifted_by" nullable:"false" doc:"Exceptions below the denial that lift it; a denial with none stands"`
	UnresolvedParent bool      `json:"unresolved_parent" doc:"The denial applies because the resource has a parent that is not in the cache, so the hierarchy above it is not fully known"`
	MissingAttribute bool      `json:"missing_attribute" doc:"The denial applies because a condition reads an attribute the request does not carry, and the rule says to apply it then (on_missing_attribute = apply)"`
}

// Explanation shows how an answer came about.
type Explanation struct {
	Decision  string      `json:"decision" enum:"allowed,denied,no_access,resource_not_found,type_not_found" doc:"allowed; denied — a denial removes a granted level; no_access — no rule grants the level; resource_not_found and type_not_found — there is nothing to check"`
	GrantedBy []RuleRef   `json:"granted_by" nullable:"false" doc:"Rules that grant the level, including through the level hierarchy of the type"`
	Denials   []DenialRef `json:"denials" nullable:"false" doc:"Denials that apply to the level, lifted or not"`
}

// CheckResult is the answer to one CheckRequest.
type CheckResult struct {
	Allowed     bool         `json:"allowed" doc:"Whether the caller may do it"`
	Explanation *Explanation `json:"explanation,omitempty" doc:"Present when explain was asked for"`
}

// CheckResponse is the body of the POST /service/{id}/check answer.
type CheckResponse struct {
	Results []CheckResult `json:"results" nullable:"false" doc:"One answer per question, in the order asked"`
}
