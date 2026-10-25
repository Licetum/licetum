package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// PredicateValue is a string or a list of strings (like `str | list[str]` in the Python version).
type PredicateValue struct {
	Str    string
	List   []string
	IsList bool
}

func (v PredicateValue) MarshalJSON() ([]byte, error) {
	if v.IsList {
		if v.List == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(v.List)
	}
	return json.Marshal(v.Str)
}

// Schema describes the value for OpenAPI: a string or an array of strings.
func (PredicateValue) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Description: "Value to compare with: a string (eq, regex) or a list of strings (in)",
		OneOf: []*huma.Schema{
			{Type: huma.TypeString},
			{Type: huma.TypeArray, Items: &huma.Schema{Type: huma.TypeString}},
		},
		Examples: []any{"sales", []string{"sales", "marketing"}},
	}
}

func (v *PredicateValue) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var list []string
		if err := json.Unmarshal(data, &list); err != nil {
			return errors.New("value: expected a string or a list of strings")
		}
		*v = PredicateValue{List: list, IsList: true}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("value: expected a string or a list of strings")
	}
	*v = PredicateValue{Str: s}
	return nil
}

// PredicateCondition is a single condition. Conditions in a list are ANDed. Source says where the attribute is read
// from: a claim of the subject (the default), an attribute the request carries (context), or the time of the decision
// (now: hour and weekday, in UTC).
type PredicateCondition struct {
	Attr   string         `json:"attr" doc:"The attribute: a claim name (source claim), a key of the request context (source context), or hour or weekday (source now)" example:"department"`
	Op     string         `json:"op" enum:"eq,ne,in,not_in,regex,gt,ge,lt,le,in_cidr" doc:"Operator: eq — equality, ne — inequality, in — membership in a list, not_in — absence from a list, regex — regular expression search (RE2), gt, ge, lt, le — numeric comparison, in_cidr — the address lies in one of the networks. An attribute the request does not carry never satisfies a condition of an allow rule; ne and not_in included"`
	Value  PredicateValue `json:"value" doc:"The value to compare with: a string, or a list for in, not_in and in_cidr"`
	Source string         `json:"source,omitempty" enum:"claim,context,now" doc:"Where the attribute comes from: claim (a claim of the subject, the default), context (a key of the request context) or now (hour 0-23 or weekday 1-7, Monday is 1, in UTC)"`
}

const (
	OpEq     = "eq"
	OpNe     = "ne"
	OpIn     = "in"
	OpNotIn  = "not_in"
	OpRegex  = "regex"
	OpGt     = "gt"
	OpGe     = "ge"
	OpLt     = "lt"
	OpLe     = "le"
	OpInCIDR = "in_cidr"
)

// Sources of a condition.
const (
	SourceClaim   = "claim"
	SourceContext = "context"
	SourceNow     = "now"
)

// predicateConditionRaw is used to check required fields while decoding input.
type predicateConditionRaw struct {
	Attr   *string         `json:"attr"`
	Op     *string         `json:"op"`
	Value  *PredicateValue `json:"value"`
	Source *string         `json:"source"`
}

func (c *PredicateCondition) UnmarshalJSON(data []byte) error {
	var raw predicateConditionRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Attr == nil || raw.Op == nil || raw.Value == nil {
		return errors.New("predicate: every condition requires attr, op and value")
	}
	*c = PredicateCondition{Attr: *raw.Attr, Op: *raw.Op, Value: *raw.Value}
	if raw.Source != nil {
		c.Source = *raw.Source
	}
	return nil
}

// validatePredicate checks the conditions of a predicate or of a guard (any source).
func validatePredicate(conds []PredicateCondition) error {
	for _, c := range conds {
		if err := validateCondition(c); err != nil {
			return err
		}
	}
	return nil
}

// validateResourceWhere checks the conditions over the attributes of the resources: they take no source, the
// attributes are the ones the synchronizer reported for each resource.
func validateResourceWhere(conds []PredicateCondition) error {
	for _, c := range conds {
		if c.Source != "" {
			return apperr.Unprocessable(i18n.ResourceWhereSource)
		}
		if !ValidMetaKey(c.Attr) {
			return apperr.Unprocessable(i18n.ResourceWhereAttrInvalid, c.Attr)
		}
		if err := validateCondition(c); err != nil {
			return err
		}
	}
	return nil
}

func validateCondition(c PredicateCondition) error {
	switch c.Source {
	case "", SourceClaim, SourceContext:
	case SourceNow:
		if c.Attr != "hour" && c.Attr != "weekday" {
			return apperr.Unprocessable(i18n.PredicateNowAttrInvalid, c.Attr)
		}
		if c.Op == OpRegex || c.Op == OpInCIDR {
			return apperr.Unprocessable(i18n.PredicateOpInvalid, c.Op)
		}
	default:
		return apperr.Unprocessable(i18n.PredicateSourceInvalid, c.Source)
	}
	switch c.Op {
	case OpEq, OpIn, OpNotIn:
	case OpNe:
		if c.Value.IsList {
			return apperr.Unprocessable(i18n.PredicateNeNotString)
		}
	case OpRegex:
		if c.Value.IsList {
			return apperr.Unprocessable(i18n.PredicateRegexNotString)
		}
		if _, err := regexp.Compile(c.Value.Str); err != nil {
			return apperr.Unprocessable(i18n.PredicateRegexInvalid, c.Value.Str, err)
		}
	case OpGt, OpGe, OpLt, OpLe:
		if c.Value.IsList {
			return apperr.Unprocessable(i18n.PredicateNumberInvalid, c.Op, strings.Join(c.Value.List, ","))
		}
		if _, err := strconv.ParseFloat(c.Value.Str, 64); err != nil {
			return apperr.Unprocessable(i18n.PredicateNumberInvalid, c.Op, c.Value.Str)
		}
	case OpInCIDR:
		networks := c.Value.List
		if !c.Value.IsList {
			networks = []string{c.Value.Str}
		}
		for _, n := range networks {
			if _, _, err := net.ParseCIDR(n); err != nil {
				return apperr.Unprocessable(i18n.PredicateCIDRInvalid, n)
			}
		}
	default:
		return apperr.Unprocessable(i18n.PredicateOpInvalid, c.Op)
	}
	return nil
}

// ResourceFromClaim computes the rule's resource_id dynamically from a claim of the subject: the claim
// value is looked up among resources of type lookup_type_resource_id (by name or id), then we climb
// the hierarchy up to the rule's own type (the number of levels is derived from the type tree).
type ResourceFromClaim struct {
	_                    struct{}  `nullable:"true" additionalProperties:"true"`
	Claim                string    `json:"claim" doc:"Name of the claim of the subject whose value is looked up in the source" example:"branch_name"`
	LookupTypeResourceID uuid.UUID `json:"lookup_type_resource_id" format:"uuid" doc:"Resource type in which the claim value is looked up (may belong to any service)"`
	MatchBy              string    `json:"match_by" required:"false" default:"name" doc:"Which resource field the claim value is matched against: name, id, or meta.<attribute> — an attribute the source reported (equal to the claim value, or containing it if the attribute is a list)" example:"meta.owner"`
}

const (
	MatchByName       = "name"
	MatchByID         = "id"
	MatchByMetaPrefix = "meta."
)

// MetaKeyOf returns the attribute name of a match_by of the form meta.<attribute>.
func MetaKeyOf(matchBy string) (string, bool) {
	key, ok := strings.CutPrefix(matchBy, MatchByMetaPrefix)
	return key, ok && ValidMetaKey(key)
}

// ValidMetaKey reports whether a name may be a resource attribute: letters, digits, '_' and '-',
// up to 63 characters. The names of the fixed resource fields are reserved.
func ValidMetaKey(key string) bool {
	if key == "" || len(key) > 63 {
		return false
	}
	switch key {
	case "id", "name", "parent_id", "parents", "meta":
		return false
	}
	for _, c := range key {
		ok := c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			return false
		}
	}
	return true
}

type resourceFromClaimRaw struct {
	Claim                *string    `json:"claim"`
	LookupTypeResourceID *uuid.UUID `json:"lookup_type_resource_id" format:"uuid"`
	MatchBy              *string    `json:"match_by"`
}

func (r *ResourceFromClaim) UnmarshalJSON(data []byte) error {
	var raw resourceFromClaimRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Claim == nil || raw.LookupTypeResourceID == nil {
		return errors.New("resource_from_claim: claim and lookup_type_resource_id are required")
	}
	matchBy := MatchByName
	if raw.MatchBy != nil {
		matchBy = *raw.MatchBy
	}
	if _, isMeta := MetaKeyOf(matchBy); matchBy != MatchByName && matchBy != MatchByID && !isMeta {
		return errors.New("resource_from_claim: match_by must be name, id or meta.<attribute>")
	}
	*r = ResourceFromClaim{Claim: *raw.Claim, LookupTypeResourceID: *raw.LookupTypeResourceID, MatchBy: matchBy}
	return nil
}

// AssignmentRuleInput is the body of POST/PATCH /assignment_rule/. PATCH replaces the whole rule, as in the Python version.
//
// What is granted is either a role (role_id: all its permissions) or an inline permission (type_resource_id
// with access, and optionally a target). To whom it is granted is one of three ways: subject, predicate
// or member_of_group_id.
type AssignmentRuleInput struct {
	_                  struct{}             `additionalProperties:"true"`
	RoleID             *uuid.UUID           `json:"role_id" format:"uuid" required:"false" doc:"Id of a role: grants all its permissions. Mutually exclusive with the inline permission (type_resource_id, resource_id, resource_from_claim, access)"`
	TypeResourceID     *uuid.UUID           `json:"type_resource_id" format:"uuid" required:"false" doc:"Id of the resource type the inline permission applies to (of any service); required without role_id"`
	ResourceID         *string              `json:"resource_id" required:"false" doc:"Resource id; null means the rule covers the whole resource type (if resource_from_claim is not set either)"`
	ResourceFromClaim  *ResourceFromClaim   `json:"resource_from_claim" required:"false" doc:"Compute resource_id dynamically from a claim of the subject; mutually exclusive with resource_id"`
	Access             []string             `json:"access" minItems:"1" required:"false" doc:"Access levels of the inline permission (read, write, ...); required without role_id" example:"[\"read\",\"write\"]"`
	Subject            *string              `json:"subject" required:"false" doc:"A specific subject or group (group:{uuid}); mutually exclusive with predicate and member_of_group_id" example:"alice"`
	Predicate          []PredicateCondition `json:"predicate" required:"false" doc:"AND conditions over the claims and the request; mutually exclusive with subject and member_of_group_id"`
	MemberOfGroupID    *uuid.UUID           `json:"member_of_group_id" format:"uuid" required:"false" doc:"The rule applies to everyone who is a member of this group; mutually exclusive with subject and predicate"`
	Enabled            *bool                `json:"enabled" required:"false" default:"true" doc:"Whether the rule is active"`
	Effect             string               `json:"effect" required:"false" enum:"allow,deny" default:"allow" doc:"allow grants the access levels; deny takes them away from everything the rule covers, whatever the allow rules say, unless an exception below the denial lifts it"`
	Exception          bool                 `json:"exception" required:"false" doc:"Only for allow rules: the rule lifts the denials that lie strictly above it in the hierarchy, for the resources it covers"`
	ValidFrom          *time.Time           `json:"valid_from" required:"false" nullable:"true" doc:"The rule takes effect from this moment (RFC 3339); omitted or null — right away"`
	ExpiresAt          *time.Time           `json:"expires_at" required:"false" nullable:"true" doc:"The rule stops taking effect at this moment (RFC 3339); omitted or null — never"`
	Name               *string              `json:"name" required:"false" nullable:"true" maxLength:"200" doc:"A name for the rule: it names the rule in the change log and in the explanations. Optional" example:"Sales can read the branches"`
	Description        *string              `json:"description" required:"false" nullable:"true" maxLength:"2000" doc:"What the rule is for. Optional"`
	OnMissingAttribute *string              `json:"on_missing_attribute" required:"false" nullable:"true" enum:"skip,apply" doc:"Only for deny rules: what the denial does when a condition reads an attribute the request does not carry. skip: the condition does not hold, the denial does not apply (the behaviour before this option existed); apply: the denial applies (fail closed). A new deny rule defaults to apply; an allow rule always skips, it grants nothing on a missing attribute"`
	RequiresPredicate  []PredicateCondition `json:"requires_predicate" required:"false" nullable:"false" doc:"A guard: conditions that must hold as well for the rule to apply, in addition to its subject, group or predicate"`
	RequiresGroupID    *uuid.UUID           `json:"requires_group_id" required:"false" nullable:"true" format:"uuid" doc:"A guard: the subject must be a member of this group for the rule to apply"`
	ResourceWhere      []PredicateCondition `json:"resource_where" required:"false" nullable:"true" doc:"Covers the resources of the type whose attributes (as the synchronizer reported them) match these conditions. Mutually exclusive with resource_id and resource_from_claim; needs a synchronizer"`
	SubjectKind        *string              `json:"subject_kind" required:"false" nullable:"true" enum:"user,service,agent,group" doc:"A label of the subject: user, service, agent or group. It classifies the rule; it does not change what matches"`
}

// Values of on_missing_attribute (see AssignmentRuleInput).
const (
	OnMissingSkip  = "skip"
	OnMissingApply = "apply"
)

// AccessGroupPrefix marks a reference to an access group of the resource type in an access list: "@name" stands for
// the levels the group names (see service.expandAccess).
const AccessGroupPrefix = "@"

func (a *AssignmentRuleInput) Validate() error {
	a.Name = trimmedOrNil(a.Name)
	a.Description = trimmedOrNil(a.Description)
	if a.RequiresPredicate == nil {
		a.RequiresPredicate = []PredicateCondition{}
	}
	if a.RoleID != nil {
		if a.TypeResourceID != nil || a.ResourceID != nil || a.ResourceFromClaim != nil || len(a.Access) > 0 || len(a.ResourceWhere) > 0 {
			return apperr.Unprocessable(i18n.RoleAssignmentInlineExclusive)
		}
	} else {
		if a.TypeResourceID == nil {
			return apperr.Unprocessable(i18n.AssignmentRuleTargetRequired)
		}
		if len(a.Access) == 0 {
			return apperr.Unprocessable(i18n.AccessEmpty)
		}
	}
	if a.Effect == "" {
		a.Effect = EffectAllow
	}
	if a.Effect != EffectAllow && a.Effect != EffectDeny {
		return apperr.Unprocessable(i18n.RuleEffectInvalid, a.Effect)
	}
	if a.Exception && a.Effect != EffectAllow {
		return apperr.Unprocessable(i18n.RuleExceptionNeedsAllow)
	}
	for _, level := range a.Access {
		if level == "" || (level == AllLevels && a.Effect != EffectDeny) {
			return apperr.Unprocessable(i18n.AccessLevelInvalid, "access")
		}
	}
	if err := validateValidity(a.ValidFrom, a.ExpiresAt); err != nil {
		return err
	}
	if a.Predicate == nil {
		a.Predicate = []PredicateCondition{}
	}
	if a.Enabled == nil {
		t := true
		a.Enabled = &t
	}
	if err := validatePredicate(a.Predicate); err != nil {
		return err
	}
	if err := validatePredicate(a.RequiresPredicate); err != nil {
		return err
	}
	if a.OnMissingAttribute != nil && *a.OnMissingAttribute == OnMissingApply && a.Effect != EffectDeny {
		return apperr.Unprocessable(i18n.OnMissingApplyNeedsDeny)
	}
	if len(a.ResourceWhere) > 0 {
		if err := validateResourceWhere(a.ResourceWhere); err != nil {
			return err
		}
	}
	if a.ResourceID != nil && a.ResourceFromClaim != nil {
		return apperr.Unprocessable(i18n.ResourceTargetExclusive)
	}
	if len(a.ResourceWhere) > 0 && (a.ResourceID != nil || a.ResourceFromClaim != nil) {
		return apperr.Unprocessable(i18n.ResourceTargetExclusive)
	}
	modes := 0
	for _, set := range []bool{a.Subject != nil, a.MemberOfGroupID != nil, len(a.Predicate) > 0} {
		if set {
			modes++
		}
	}
	if modes > 1 {
		return apperr.Unprocessable(i18n.SubjectTargetExclusive)
	}
	return nil
}

type AssignmentRule struct {
	ID                 uuid.UUID            `json:"id" format:"uuid" doc:"Rule id"`
	RoleID             *uuid.UUID           `json:"role_id" format:"uuid" doc:"Role whose permissions the rule grants; null for an inline rule"`
	TypeResourceID     *uuid.UUID           `json:"type_resource_id" format:"uuid" doc:"Resource type id of the inline permission; null for a role rule"`
	ResourceID         *string              `json:"resource_id" doc:"Resource id; null means the whole type"`
	ResourceFromClaim  *ResourceFromClaim   `json:"resource_from_claim" doc:"Dynamic resource_id computed from a claim"`
	Access             []string             `json:"access" doc:"Access levels of the inline permission; null for a role rule"`
	Subject            *string              `json:"subject" doc:"A specific subject or group (group:{uuid})"`
	Predicate          []PredicateCondition `json:"predicate" doc:"AND conditions over the claims and the request"`
	MemberOfGroupID    *uuid.UUID           `json:"member_of_group_id" format:"uuid" doc:"Group whose members are granted access"`
	Enabled            bool                 `json:"enabled" doc:"Whether the rule is active"`
	Effect             string               `json:"effect" enum:"allow,deny" doc:"allow or deny"`
	Exception          bool                 `json:"exception" doc:"An allow rule that lifts the denials above it"`
	ValidFrom          *time.Time           `json:"valid_from" nullable:"true" doc:"The rule takes effect from this moment; null — right away"`
	ExpiresAt          *time.Time           `json:"expires_at" nullable:"true" doc:"The rule stops taking effect at this moment; null — never"`
	Name               *string              `json:"name" nullable:"true" doc:"The name of the rule; null when it has none"`
	Description        *string              `json:"description" nullable:"true" doc:"What the rule is for; null when there is no description"`
	OnMissingAttribute string               `json:"on_missing_attribute" enum:"skip,apply" doc:"What a denial does when a condition reads an attribute the request does not carry: skip or apply (see the input)"`
	RequiresPredicate  []PredicateCondition `json:"requires_predicate" nullable:"false" doc:"The guard conditions that must hold as well for the rule to apply"`
	RequiresGroupID    *uuid.UUID           `json:"requires_group_id" format:"uuid" nullable:"true" doc:"The guard: the subject must be a member of this group; null when there is no such guard"`
	ResourceWhere      []PredicateCondition `json:"resource_where" nullable:"true" doc:"The conditions over the resource attributes the rule covers; null for a rule on a resource id, on a claim or on the whole type"`
	SubjectKind        *string              `json:"subject_kind" nullable:"true" doc:"The label of the subject: user, service, agent or group; null when there is none"`
}

// validateTarget checks the target of a permission (inline rule or role item): at most one of a resource id, a claim
// and a resource_where, and the conditions of resource_where.
func validateTarget(resourceID *string, fromClaim *ResourceFromClaim, where []PredicateCondition) error {
	set := 0
	for _, target := range []bool{resourceID != nil, fromClaim != nil, len(where) > 0} {
		if target {
			set++
		}
	}
	if set > 1 {
		return apperr.Unprocessable(i18n.ResourceTargetExclusive)
	}
	if len(where) > 0 {
		return validateResourceWhere(where)
	}
	return nil
}

// trimmedOrNil drops the surrounding spaces of an optional text and turns an empty one into null.
func trimmedOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// Rule effects.
const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)

// validateValidity checks a validity period: the end must be after the start.
func validateValidity(from, to *time.Time) error {
	if from != nil && to != nil && !to.After(*from) {
		return apperr.Unprocessable(i18n.ValidityInvalid)
	}
	return nil
}

type AssignmentRuleWithContext struct {
	AssignmentRule
	ServiceID        *uuid.UUID `json:"service_id" format:"uuid" doc:"Id of the service that owns the resource type; null for a role rule"`
	ServiceName      *string    `json:"service_name" doc:"Name of the service that owns the resource type; null for a role rule"`
	TypeResourceName *string    `json:"type_resource_name" doc:"Resource type name; null for a role rule"`
	RoleName         *string    `json:"role_name" doc:"Name of the granted role; null for an inline rule"`
	TargetValid      bool       `json:"target_valid" doc:"Whether the rule's target can take effect: false for a rule on a specific resource (resource_id or resource_from_claim) of a type that has no synchronizer and hence no resources — e.g. after its synchronizer was deleted — and for a resource_from_claim that looks a resource up in, or climbs through, such a type. A denial of that kind still applies below the links to the resources the type left"`
}
