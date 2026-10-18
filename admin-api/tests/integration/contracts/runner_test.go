//go:build integration

package contracts_test

// Tests of the runner itself: that every field of a fixture reaches the engine as the fixture means it (the answers are
// written from the semantics, CLAUDE.md "Decision semantics"), that ReplaceRules swaps one policy for another on the same
// graph, and that Compare reports what differs. Each case is also cross-checked against its permission list (CrossCheck).

import (
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/admin-api/tests/integration/contracts"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// everyField uses every field of the fixture format: two services, a type with two parents of different types and an inherit
// mask, access_implies, a type without a synchronizer, a type whose source has no resources, attributes, an unresolved parent,
// an access group, memberships by subject, by predicate, of everybody, of a nested group, disabled and out of their window,
// a role, and rules with every kind of subject, target and guard, denials with both on_missing values, a wildcard denial,
// validity windows and a condition on the hour of the decision.
const everyField = `
contracts: []
services: [shop, crm]
now: 2030-01-01T12:00:00Z
types:
  - {name: region, service: shop, sync: true}
  - {name: team, service: crm, sync: true}
  - {name: store, service: shop, sync: true, implies: {write: [read]}, parents: [{type: region}, {type: team, inherit: [read]}]}
  - {name: shelf, service: shop, sync: true, parents: [{type: store}]}
  - {name: reports, service: shop, sync: false}
  - {name: empty, service: shop, sync: true}
resources:
  - {type: region, id: north, name: North}
  - {type: region, id: south, name: South}
  - {type: team, id: alpha, name: Alpha}
  - {type: store, id: s1, name: Store 1, parents: [{type: region, id: north}, {type: team, id: alpha}], meta: {owner: ann, tags: [a, b]}}
  - {type: store, id: s2, name: Store 2, parents: [{type: region, id: south}], meta: {owner: bob, tags: [c]}}
  - {type: store, id: s3, name: Store 3, parents: [{type: region, id: ghost}]}
  - {type: shelf, id: x1, parents: [{type: store, id: s1}]}
  - {type: shelf, id: x3, parents: [{type: store, id: s3}]}
access_groups:
  - {type: store, name: editor, levels: [read, write]}
groups:
  - name: staff
    members:
      - {subject: ann}
      - {predicate: [{attr: dept, op: eq, value: sales}]}
      - {subject: old, expires_at: 2029-01-01T00:00:00Z}
      - {subject: gone, disabled: true}
  - name: managers
    members: [{group: staff}]
  - name: everyone
    members: [{everybody: true}]
roles:
  - name: auditor
    items:
      - {type: shelf, access: [audit]}
      - {type: store, resource: s2, access: [read]}
rules:
  - {name: inherit-team, subject: u-inherit, type: team, resource: alpha, access: [read, write]}
  - {name: implies-write, subject: u-implies, type: store, resource: s2, access: [write]}
  - {name: implies-allow, subject: u-implies-deny, type: store, access: [write]}
  - {name: implies-deny-read, subject: u-implies-deny, type: store, resource: s2, access: [read], deny: true}
  - {name: editor, subject: u-editor, type: store, resource: s1, access: ["@editor"]}
  - {name: staff-north, member_of: staff, type: region, resource: north, access: [read]}
  - {name: managers-south, member_of: managers, type: region, resource: south, access: [read]}
  - {name: managers-audit, subject_group: managers, type: store, resource: s2, access: [audit]}
  - {name: everyone-list, subject_group: everyone, type: reports, access: [list]}
  - {name: auditor-role, subject: u-role, role: auditor}
  - {name: everybody-view, everybody: true, type: empty, access: [view]}
  - {name: level-gt, predicate: [{attr: level, op: gt, value: "5"}], type: region, resource: south, access: [read]}
  - {name: net, predicate: [{attr: ip, op: in_cidr, value: ["10.0.0.0/8"], source: context}], type: region, resource: north, access: [read]}
  - {name: guarded, subject: guarded, type: region, resource: north, access: [read], requires_predicate: [{attr: mfa, op: eq, value: "yes"}]}
  - {name: needs-staff, subject: rg, type: region, resource: north, access: [read], requires_group: staff}
  - {name: temporary, subject: temp, type: region, resource: north, access: [read], valid_from: 2030-01-01T00:00:00Z, expires_at: 2030-01-02T00:00:00Z}
  - {name: morning, subject: clock, type: region, resource: north, access: [read], requires_predicate: [{attr: hour, op: lt, value: "13", source: now}]}
  - {name: disabled, subject: dis, type: region, resource: north, access: [read], disabled: true}
  - {name: om-allow, subject: om, type: region, resource: north, access: [read]}
  - {name: om-deny, subject: om, type: region, resource: north, access: [read], deny: true, requires_predicate: [{attr: country, op: eq, value: x, source: context}]}
  - {name: om2-allow, subject: om2, type: region, resource: north, access: [read]}
  - {name: om2-deny, subject: om2, type: region, resource: north, access: [read], deny: true, on_missing: skip, requires_predicate: [{attr: country, op: eq, value: x, source: context}]}
  - {name: by-name, subject: fc, type: region, from_claim: {claim: store_name, lookup_type: store}, access: [read]}
  - {name: by-owner, subject: bob, type: store, from_claim: {claim: preferred_username, lookup_type: store, match_by: meta.owner}, access: [write]}
  - {name: by-id, subject: fcid, type: store, from_claim: {claim: store_id, lookup_type: store, match_by: id}, access: [read]}
  - {name: tagged, subject: wh, type: store, where: [{attr: tags, op: eq, value: b}], access: [read]}
  - {name: star-allow, subject: star, type: store, access: [read, write]}
  - {name: star-deny, subject: star, type: store, resource: s1, access: ["*"], deny: true}
  - {name: un-allow, subject: un, type: store, access: [read]}
  - {name: un-allow-shelf, subject: un, type: shelf, access: [read]}
  - {name: un-deny, subject: un, type: region, resource: north, access: [read], deny: true}
  - {name: reports-read, subject: rep, type: reports, access: [read]}
cases:
  - name: two parents, the second lets only read down
    subject: u-inherit
    questions:
      - {type: store, resource: s1, action: read, allowed: true, decision: allowed, granted_by: [inherit-team]}
      - {type: store, resource: s1, action: write, allowed: false, decision: no_access}
      - {type: shelf, resource: x1, action: read, allowed: true}
      - {type: shelf, resource: x1, action: write, allowed: false}
  - name: the second service
    subject: u-inherit
    service: crm
    questions:
      - {type: team, resource: alpha, action: write, allowed: true, granted_by: [inherit-team]}
      - {type: store, resource: s1, action: read, allowed: false, decision: type_not_found}
  - name: granting write grants read
    subject: u-implies
    questions:
      - {type: store, resource: s2, action: read, allowed: true, granted_by: [implies-write]}
      - {type: store, resource: s2, action: write, allowed: true}
      - {type: store, resource: s1, action: read, allowed: false, decision: no_access}
  - name: denying read denies write
    subject: u-implies-deny
    questions:
      - {type: store, resource: s2, action: write, allowed: false, decision: denied, granted_by: [implies-allow], denials: [{rule: implies-deny-read}]}
      - {type: store, resource: s2, action: read, allowed: false, decision: denied}
      - {type: store, resource: s1, action: write, allowed: true}
  - name: an access group
    subject: u-editor
    questions:
      - {type: store, resource: s1, action: write, allowed: true, granted_by: [editor]}
      - {type: store, resource: s1, action: read, allowed: true}
      - {type: store, resource: s2, action: read, allowed: false}
  - name: a member by subject, of a nested group, and the group as a subject
    subject: ann
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [staff-north]}
      - {type: region, resource: south, action: read, allowed: true, granted_by: [managers-south]}
      - {type: store, resource: s2, action: audit, allowed: true, granted_by: [managers-audit]}
      - {type: reports, action: list, allowed: true, granted_by: [everyone-list]}
  - name: a member by predicate
    subject: carl
    claims: {dept: sales}
    questions:
      - {type: region, resource: north, action: read, allowed: true}
      - {type: region, resource: south, action: read, allowed: true}
  - name: not a member by predicate
    subject: dave
    claims: {dept: hr}
    questions:
      - {type: region, resource: north, action: read, allowed: false, decision: no_access}
  - name: a membership after its end
    subject: old
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a membership before its end
    subject: old
    at: 2028-06-01T00:00:00Z
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [staff-north]}
  - name: a disabled membership
    subject: gone
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a role
    subject: u-role
    questions:
      - {type: shelf, resource: x1, action: audit, allowed: true, granted_by: [auditor-role]}
      - {type: shelf, resource: x3, action: audit, allowed: true}
      - {type: store, resource: s2, action: read, allowed: true, granted_by: [auditor-role]}
      - {type: store, resource: s1, action: read, allowed: false}
  - name: everybody, on a type whose source has no resources
    subject: zed
    questions:
      - {type: empty, action: view, allowed: true, granted_by: [everybody-view]}
      - {type: reports, action: list, allowed: true}
      - {type: nope, action: read, allowed: false, decision: type_not_found}
      - {type: store, resource: s9, action: read, allowed: false, decision: resource_not_found}
  - name: a numeric condition that holds
    subject: p1
    claims: {level: 7}
    questions:
      - {type: region, resource: south, action: read, allowed: true, granted_by: [level-gt]}
  - name: a numeric condition that fails
    subject: p2
    claims: {level: 3}
    questions:
      - {type: region, resource: south, action: read, allowed: false}
  - name: a numeric condition on a missing claim
    subject: p3
    questions:
      - {type: region, resource: south, action: read, allowed: false}
  - name: a request attribute inside the network
    subject: n1
    context: {ip: 10.1.2.3}
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [net]}
  - name: a request attribute outside the network
    subject: n2
    context: {ip: 192.168.1.1}
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a guard by claims that holds
    subject: guarded
    claims: {mfa: "yes"}
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [guarded]}
  - name: a guard by claims that does not
    subject: guarded
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a guard by a group the subject is not in
    subject: rg
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a guard by a group the subject is in
    subject: rg
    claims: {dept: sales}
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [needs-staff, staff-north]}
  - name: inside the window of a rule
    subject: temp
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [temporary]}
  - name: after the window of a rule
    subject: temp
    at: 2030-01-03T00:00:00Z
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: before the window of a rule
    subject: temp
    at: 2029-12-31T23:00:00Z
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: the hour of the decision, in the morning
    subject: clock
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [morning]}
  - name: the hour of the decision, in the afternoon
    subject: clock
    at: 2030-01-01T15:00:00Z
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a disabled rule
    subject: dis
    questions:
      - {type: region, resource: north, action: read, allowed: false}
  - name: a denial applies on a missing attribute
    subject: om
    questions:
      - {type: region, resource: north, action: read, allowed: false, decision: denied, denials: [{rule: om-deny}]}
  - name: a denial whose condition fails
    subject: om
    context: {country: "y"}
    questions:
      - {type: region, resource: north, action: read, allowed: true, denials: []}
  - name: a denial whose condition holds
    subject: om
    context: {country: x}
    questions:
      - {type: region, resource: north, action: read, allowed: false, denials: [{rule: om-deny}]}
  - name: a denial skipped on a missing attribute
    subject: om2
    questions:
      - {type: region, resource: north, action: read, allowed: true, granted_by: [om2-allow], denials: []}
  - name: a resource found by the name in a claim
    subject: fc
    claims: {store_name: Store 2}
    questions:
      - {type: region, resource: south, action: read, allowed: true, granted_by: [by-name]}
      - {type: region, resource: north, action: read, allowed: false}
  - name: a resource found by an attribute
    subject: bob
    questions:
      - {type: store, resource: s2, action: write, allowed: true, granted_by: [by-owner]}
      - {type: store, resource: s1, action: write, allowed: false}
  - name: a resource found by its id
    subject: fcid
    claims: {store_id: s1}
    questions:
      - {type: store, resource: s1, action: read, allowed: true, granted_by: [by-id]}
      - {type: store, resource: s2, action: read, allowed: false}
  - name: resources by an attribute that is a list
    subject: wh
    questions:
      - {type: store, resource: s1, action: read, allowed: true, granted_by: [tagged]}
      - {type: store, resource: s2, action: read, allowed: false}
  - name: the wildcard denies every level
    subject: star
    questions:
      - {type: store, resource: s1, action: write, allowed: false, denials: [{rule: star-deny}]}
      - {type: store, resource: s1, action: read, allowed: false}
      - {type: store, resource: s2, action: write, allowed: true}
  - name: an unresolved parent fails closed, also below it
    subject: un
    questions:
      - {type: store, resource: s3, action: read, allowed: false, decision: denied, denials: [{rule: un-deny, unresolved_parent: true}]}
      - {type: shelf, resource: x3, action: read, allowed: false, denials: [{rule: un-deny, unresolved_parent: true}]}
      - {type: store, resource: s1, action: read, allowed: false, denials: [{rule: un-deny}]}
      - {type: store, resource: s2, action: read, allowed: true, denials: []}
  - name: a type without a synchronizer
    subject: rep
    questions:
      - {type: reports, action: read, allowed: true, granted_by: [reports-read]}
      - {type: reports, action: write, allowed: false, decision: no_access}
`

// TestRunner_EveryFieldReachesTheEngine: the answers to a fixture that uses every field are those of the semantics. A
// failure here is the runner translating a field wrongly, or the engine disagreeing with the semantics: the message names
// the question.
func TestRunner_EveryFieldReachesTheEngine(t *testing.T) {
	f, err := policyfix.Parse("every_field", []byte(everyField))
	if err != nil {
		t.Fatal(err)
	}
	l := contracts.Load(t, infra, f)
	askAndCompare(t, l, f)
}

// The fixtures of TestRunner_ReplaceRulesSwapsThePolicy: one graph, two policies, and a change of the resources.
const (
	replaceGraph = `
contracts: []
services: [shop]
types:
  - {name: region, service: shop, sync: true}
  - {name: store, service: shop, sync: true, parents: [{type: region}]}
`
	replaceResources = `
resources:
  - {type: region, id: north}
  - {type: region, id: south}
  - {type: store, id: s1, parents: [{type: region, id: north}]}
  - {type: store, id: s2, parents: [{type: region, id: south}]}
`
	// s1 moved under south, and s3 appeared there.
	replaceMovedResources = `
resources:
  - {type: region, id: north}
  - {type: region, id: south}
  - {type: store, id: s1, parents: [{type: region, id: south}]}
  - {type: store, id: s2, parents: [{type: region, id: south}]}
  - {type: store, id: s3, parents: [{type: region, id: south}]}
`
	replaceFirstPolicy = `
access_groups: [{type: store, name: editor, levels: [read, write]}]
groups: [{name: team, members: [{subject: ann}]}, {name: outer, members: [{group: team}]}]
roles: [{name: keeper, items: [{type: store, resource: s1, access: ["@editor"]}]}]
rules:
  - {name: keeps, member_of: outer, role: keeper}
cases:
  - name: the role through the nested group
    subject: ann
    questions:
      - {type: store, resource: s1, action: write, allowed: true, granted_by: [keeps]}
      - {type: store, resource: s2, action: read, allowed: false}
`
	// The same names as the first policy, for other entities.
	replaceSecondPolicy = `
access_groups: [{type: store, name: editor, levels: [read]}]
groups: [{name: team, members: [{subject: bob}]}]
roles: [{name: keeper, items: [{type: region, resource: south, access: ["read"]}]}]
rules:
  - {name: keeps, member_of: team, role: keeper}
  - {name: edits, subject: ann, type: store, resource: s2, access: ["@editor"]}
`
	replaceSecondCases = `
cases:
  - name: the old policy is gone
    subject: ann
    questions:
      - {type: store, resource: s1, action: write, allowed: false, decision: no_access}
      - {type: store, resource: s2, action: read, allowed: true, granted_by: [edits]}
      - {type: store, resource: s2, action: write, allowed: false}
  - name: the new members
    subject: bob
    questions:
      - {type: store, resource: s2, action: read, allowed: true, granted_by: [keeps]}
      - {type: store, resource: s1, action: read, allowed: false}
`
	replaceMovedCases = `
cases:
  - name: the resources that moved under south
    subject: bob
    questions:
      - {type: store, resource: s1, action: read, allowed: true, granted_by: [keeps]}
      - {type: store, resource: s3, action: read, allowed: true}
`
)

// TestRunner_ReplaceRulesSwapsThePolicy: ReplaceRules removes every entity of the policy it wrote (the same names can be
// used again) and writes the next one; a fixture with other resources reaches the sources and the hierarchy.
func TestRunner_ReplaceRulesSwapsThePolicy(t *testing.T) {
	parse := func(name string, parts ...string) *policyfix.Fixture {
		t.Helper()
		f, err := policyfix.Parse(name, []byte(strings.Join(parts, "")))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	first := parse("first", replaceGraph, replaceResources, replaceFirstPolicy)
	second := parse("second", replaceGraph, replaceResources, replaceSecondPolicy, replaceSecondCases)
	moved := parse("moved", replaceGraph, replaceMovedResources, replaceSecondPolicy, replaceMovedCases)

	l := contracts.Load(t, infra, first)
	askAndCompare(t, l, first)
	l.ReplaceRules(second)
	askAndCompare(t, l, second)
	l.ReplaceRules(moved)
	askAndCompare(t, l, moved)
}

// TestRunner_CompareReportsEveryMismatch: Compare reports each expectation the answer misses, ignores the order and the
// repeats of the rules, and checks the explanation only where the question sets it.
func TestRunner_CompareReportsEveryMismatch(t *testing.T) {
	answer := contracts.Answer{
		Allowed: false, Decision: policyfix.DecisionDenied, GrantedBy: []string{"a", "b"},
		Denials: []contracts.Denial{
			{Rule: "d", Level: "read", LiftedBy: []string{"x"}},
			{Rule: "d", Level: "write", LiftedBy: []string{"x"}},
			{Rule: "e", Level: "read", UnresolvedParent: true},
		},
	}
	agrees := policyfix.Question{
		Allowed: false, Decision: policyfix.DecisionDenied, GrantedBy: []string{"b", "a"},
		Denials: []policyfix.ExpectedDenial{{Rule: "e", UnresolvedParent: true}, {Rule: "d", LiftedBy: []string{"x"}}},
	}
	if got := contracts.Compare(&agrees, answer); len(got) != 0 {
		t.Fatalf("an answer that agrees: %v", got)
	}
	if got := contracts.Compare(&policyfix.Question{Allowed: false}, answer); len(got) != 0 {
		t.Fatalf("a question that sets only allowed: %v", got)
	}
	differs := policyfix.Question{
		Allowed: true, Decision: policyfix.DecisionAllowed, GrantedBy: []string{"a"},
		Denials: []policyfix.ExpectedDenial{{Rule: "d"}, {Rule: "e", UnresolvedParent: true}},
	}
	got := contracts.Compare(&differs, answer)
	for _, want := range []string{"allowed: got false", `decision: got "denied"`, `unexpected ["b"]`, `missing ["d"]`, `unexpected ["d lifted by [x]"]`} {
		if !slices.ContainsFunc(got, func(m string) bool { return strings.Contains(m, want) }) {
			t.Errorf("no mismatch says %q: %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("want 4 mismatches (allowed, decision, granted_by, denials), got %d: %v", len(got), got)
	}
	// An empty list in the fixture expects none.
	none := policyfix.Question{Allowed: false, GrantedBy: []string{}, Denials: []policyfix.ExpectedDenial{}}
	if got := contracts.Compare(&none, answer); len(got) != 2 {
		t.Errorf("empty lists must expect no rules: %v", got)
	}
}

// askAndCompare asks every case of the fixture and reports each answer that differs from the expectation, and each
// question on which the permission list of the case disagrees with the check.
func askAndCompare(t *testing.T, l *contracts.Loaded, f *policyfix.Fixture) {
	t.Helper()
	for i := range f.Cases {
		c := &f.Cases[i]
		answers := l.Ask(c)
		for j := range c.Questions {
			q := &c.Questions[j]
			for _, mismatch := range contracts.Compare(q, answers[j]) {
				t.Errorf("%s, case %q, question %d (%s): %s\n\tthe answer: %+v", f.Name, c.Name, j+1, describe(f, c, q), mismatch, answers[j])
			}
		}
		for _, d := range l.CrossCheck(c, answers) {
			t.Errorf("%s, case %q, GET /permission against POST /check: %s", f.Name, c.Name, d)
		}
	}
}
