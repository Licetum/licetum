package model

import (
	"time"

	"github.com/google/uuid"
)

// Policy tests, the linter and the policy document: the tools that let a policy be checked, and kept in a file. A test
// is a set of scenarios with the answers they must get; the linter reads the policy of a service for mistakes; the
// document is the policy of a service with names instead of ids, so that it can be read, reviewed and applied again.

// PolicyTestCheck is one expectation of a scenario: may the subject do this, or may it not.
type PolicyTestCheck struct {
	Type       string  `json:"type" doc:"Name of the resource type in the service" example:"branch"`
	ResourceID *string `json:"resource_id,omitempty" doc:"The resource; omit it to ask about the type as a whole"`
	Action     string  `json:"action" doc:"The access level asked for" example:"read"`
	Expect     string  `json:"expect" enum:"allow,deny" doc:"allow — the answer must be allowed; deny — it must not be"`
}

// PolicyTestCase is a scenario: a subject with the claims and the request attributes it has, and the answers it must get.
type PolicyTestCase struct {
	Name    string            `json:"name" minLength:"1" maxLength:"200" doc:"What the scenario is about" example:"sales read the branches"`
	Subject string            `json:"subject" minLength:"1" doc:"The subject, named as the rules name it (preferred_username, or sub)" example:"alice"`
	Claims  map[string]any    `json:"claims,omitempty" doc:"The claims of the subject (subject.properties of an AuthZEN request)"`
	Context RequestContext    `json:"context,omitempty" doc:"The attributes of the request"`
	Checks  []PolicyTestCheck `json:"checks" minItems:"1" maxItems:"100" nullable:"false" doc:"The answers the scenario must get"`
}

// PolicyScenario is a stored scenario of a service.
type PolicyScenario struct {
	ID   uuid.UUID      `json:"id" format:"uuid" doc:"Scenario id"`
	Case PolicyTestCase `json:"case" doc:"The scenario"`
}

// PolicyTestInput is the body of POST /service/{service_id}/policy/test.
type PolicyTestInput struct {
	_     struct{}         `additionalProperties:"true"`
	Cases []PolicyTestCase `json:"cases" minItems:"1" maxItems:"200" doc:"The scenarios, answered as the simulation answers them, with the data as it is now"`
}

// PolicyTestCheckResult is the outcome of one expectation.
type PolicyTestCheckResult struct {
	Index    int      `json:"index" doc:"Position of the check in its scenario"`
	Expect   string   `json:"expect" enum:"allow,deny" doc:"What was expected"`
	Decision string   `json:"decision" enum:"allowed,denied,no_access,resource_not_found,type_not_found" doc:"What the rules answered"`
	Passed   bool     `json:"passed" doc:"Whether the answer is the expected one"`
	Rules    []string `json:"rules" nullable:"false" doc:"The rules that decided the answer: the ones that granted the level and the denials"`
}

// PolicyTestCaseResult is the outcome of one scenario.
type PolicyTestCaseResult struct {
	Name   string                  `json:"name" doc:"The name of the scenario"`
	Passed bool                    `json:"passed" doc:"Whether every expectation of the scenario holds"`
	Checks []PolicyTestCheckResult `json:"checks" nullable:"false" doc:"The answers of the scenario, one per expectation"`
}

// PolicyCoverage says how much of the policy of a service the scenarios exercised: the rules in force that apply to the
// service's resource types, and which of them decided at least one answer.
type PolicyCoverage struct {
	Rules     int      `json:"rules" doc:"The rules in force that apply to the resource types of the service"`
	Covered   int      `json:"covered" doc:"How many of them decided an answer of a scenario"`
	Uncovered []string `json:"uncovered" nullable:"false" doc:"The ids of the rules that decided no answer"`
}

// PolicyTestReport is the body of the answer of POST /service/{service_id}/policy/test.
type PolicyTestReport struct {
	Passed   int                    `json:"passed" doc:"How many scenarios hold"`
	Failed   int                    `json:"failed" doc:"How many scenarios do not hold"`
	Cases    []PolicyTestCaseResult `json:"cases" nullable:"false" doc:"The outcome of each scenario, in order"`
	Coverage PolicyCoverage         `json:"coverage" doc:"How much of the policy the scenarios exercised"`
}

// Severities of a lint finding.
const (
	LintError   = "error"
	LintWarning = "warning"
	LintInfo    = "info"
)

// LintFinding is one thing the linter found in the policy of a service.
type LintFinding struct {
	Severity string     `json:"severity" enum:"error,warning,info" doc:"error — the policy does not do what it says; warning — it probably does not do what its author wants; info — worth a look"`
	Code     string     `json:"code" doc:"A stable code for the finding (the message catalog key)" example:"lint_rule_target_dead"`
	Message  string     `json:"message" doc:"What the finding says, in the request language"`
	RuleID   *uuid.UUID `json:"rule_id,omitempty" format:"uuid" doc:"The rule the finding is about"`
	RoleID   *uuid.UUID `json:"role_id,omitempty" format:"uuid" doc:"The role the finding is about"`
	GroupID  *uuid.UUID `json:"group_id,omitempty" format:"uuid" doc:"The group the finding is about"`
}

// LintReport is the body of the answer of GET /service/{service_id}/policy/lint.
type LintReport struct {
	Findings []LintFinding `json:"findings" nullable:"false" doc:"What the linter found; empty when nothing is wrong"`
}

// PolicyDocumentFormat is the version of the policy document.
const PolicyDocumentFormat = "licetum.policy/v1"

// PolicyDocument is the policy of one service in a file. Resource types are named service/type, groups and roles by
// their names, and a rule by its name (a rule without one is named rule-<id>, and is matched by its id).
type PolicyDocument struct {
	Format       string              `json:"format" enum:"licetum.policy/v1" doc:"The version of the format: licetum.policy/v1"`
	Service      string              `json:"service" doc:"The slug of the service whose rules the document holds; an import for another service is refused" example:"org"`
	AccessGroups []PolicyAccessGroup `json:"access_groups" required:"false" nullable:"true" doc:"Named sets of access levels of resource types"`
	Groups       []PolicyGroup       `json:"groups" required:"false" nullable:"true" doc:"Groups of subjects, with their memberships"`
	Roles        []PolicyRole        `json:"roles" required:"false" nullable:"true" doc:"Named sets of permissions, with their items"`
	Rules        []PolicyRule        `json:"rules" required:"false" nullable:"true" doc:"The assignment rules of the service"`
}

// PolicyAccessGroup is an access group of a resource type.
type PolicyAccessGroup struct {
	Type   string   `json:"type" doc:"The resource type, as service/type" example:"org/branch"`
	Name   string   `json:"name" doc:"The name, used as @name in the access lists"`
	Levels []string `json:"levels" nullable:"false" doc:"The access levels the group stands for"`
}

// PolicyGroup is a group of subjects and its memberships.
type PolicyGroup struct {
	Name    string              `json:"name" doc:"The group name"`
	Members []PolicyGroupMember `json:"members" nullable:"false" doc:"The memberships of the group"`
}

// PolicyGroupMember is a membership: a predicate over the claims, or a group nested as a whole.
type PolicyGroupMember struct {
	Group     *string              `json:"group,omitempty" doc:"The name of a group nested as a whole; mutually exclusive with predicate"`
	Predicate []PredicateCondition `json:"predicate,omitempty" doc:"AND conditions over the claims; an empty list matches any subject"`
	Enabled   *bool                `json:"enabled,omitempty" doc:"Whether the membership is active; absent — active"`
	ValidFrom *time.Time           `json:"valid_from,omitempty" doc:"The membership starts at this moment"`
	ExpiresAt *time.Time           `json:"expires_at,omitempty" doc:"The membership ends at this moment"`
}

// PolicyRole is a role and its items.
type PolicyRole struct {
	Name  string           `json:"name" doc:"The role name"`
	Items []PolicyRoleItem `json:"items" nullable:"false" doc:"The permissions of the role"`
}

// PolicyRoleItem is one permission of a role: access levels on a target inside a resource type.
type PolicyRoleItem struct {
	Type              string                   `json:"type" doc:"The resource type, as service/type" example:"org/branch"`
	ResourceID        *string                  `json:"resource_id,omitempty" doc:"A resource of the type"`
	ResourceFromClaim *PolicyResourceFromClaim `json:"resource_from_claim,omitempty" doc:"A resource computed from a claim of the subject"`
	ResourceWhere     []PredicateCondition     `json:"resource_where,omitempty" doc:"The resources whose attributes match"`
	Access            []string                 `json:"access" nullable:"false" doc:"Access levels, or access groups as @name"`
}

// PolicyResourceFromClaim is resource_from_claim with the lookup type named.
type PolicyResourceFromClaim struct {
	Claim      string `json:"claim" doc:"The claim whose value is looked up"`
	LookupType string `json:"lookup_type" doc:"The resource type the value is looked up in, as service/type"`
	MatchBy    string `json:"match_by,omitempty" doc:"name, id or meta.<attribute>; absent — name"`
}

// PolicyRule is an assignment rule of the service, with names instead of ids.
type PolicyRule struct {
	Name               string                   `json:"name" minLength:"1" maxLength:"200" doc:"The name: unique in the document. A rule without one has the name rule-<id>"`
	Description        *string                  `json:"description,omitempty" doc:"What the rule is for"`
	Type               *string                  `json:"type,omitempty" doc:"The resource type of the inline permission, as service/type; absent for a role rule"`
	Role               *string                  `json:"role,omitempty" doc:"The name of the role granted; absent for an inline rule"`
	ResourceID         *string                  `json:"resource_id,omitempty" doc:"A resource of the type"`
	ResourceFromClaim  *PolicyResourceFromClaim `json:"resource_from_claim,omitempty" doc:"A resource computed from a claim of the subject"`
	ResourceWhere      []PredicateCondition     `json:"resource_where,omitempty" doc:"The resources whose attributes match"`
	Access             []string                 `json:"access,omitempty" doc:"Access levels, or access groups as @name"`
	Subject            *string                  `json:"subject,omitempty" doc:"A specific subject, or group:<id>"`
	Predicate          []PredicateCondition     `json:"predicate,omitempty" doc:"AND conditions over the claims and the request"`
	MemberOf           *string                  `json:"member_of,omitempty" doc:"The name of the group whose members the rule covers"`
	Enabled            *bool                    `json:"enabled,omitempty" doc:"Whether the rule is active; absent — active"`
	Effect             string                   `json:"effect,omitempty" enum:"allow,deny" doc:"allow or deny; absent — allow"`
	Exception          bool                     `json:"exception,omitempty" doc:"An allow rule that lifts the denials above it"`
	ValidFrom          *time.Time               `json:"valid_from,omitempty" doc:"The rule takes effect from this moment"`
	ExpiresAt          *time.Time               `json:"expires_at,omitempty" doc:"The rule stops taking effect at this moment"`
	OnMissingAttribute string                   `json:"on_missing_attribute,omitempty" enum:"skip,apply" doc:"What a denial does when an attribute is missing"`
	RequiresPredicate  []PredicateCondition     `json:"requires_predicate,omitempty" doc:"Guard conditions"`
	RequiresGroup      *string                  `json:"requires_group,omitempty" doc:"The name of a group the subject must belong to"`
	SubjectKind        *string                  `json:"subject_kind,omitempty" enum:"user,service,agent,group" doc:"A label of the subject"`
}

// PolicyPlanCounts counts what an import does to the entities of the document.
type PolicyPlanCounts struct {
	Created   int `json:"created" doc:"Entities that do not exist yet"`
	Updated   int `json:"updated" doc:"Entities that differ and are rewritten"`
	Deleted   int `json:"deleted" doc:"Rules of the service that the document no longer has (with prune)"`
	Unchanged int `json:"unchanged" doc:"Entities that already match the document"`
}

// PolicyImportPlan is the answer of POST /service/{service_id}/policy/import: what the import does, and whether it was
// applied. A dry run applies nothing.
type PolicyImportPlan struct {
	DryRun     bool             `json:"dry_run" doc:"Whether nothing was applied"`
	Counts     PolicyPlanCounts `json:"counts" doc:"What the operations do to the entities"`
	Operations []BatchOperation `json:"operations" nullable:"false" doc:"The writes, in the order they run; the same as POST /batch takes"`
	Results    []BatchResult    `json:"results" nullable:"false" doc:"What the writes did; empty for a dry run"`
}
