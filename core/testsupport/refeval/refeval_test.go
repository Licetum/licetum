package refeval

import (
	"strconv"
	"testing"
	"time"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// check parses a fixture written for a test and decides every question of it, as the contract tests do.
func check(t *testing.T, raw string) {
	t.Helper()
	f, err := policyfix.Parse(t.Name(), []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	ev := New(f)
	for ci := range f.Cases {
		c := &f.Cases[ci]
		for qi := range c.Questions {
			q := &c.Questions[qi]
			for _, problem := range mismatches(q, ev.Decide(c, q)) {
				t.Errorf("case %q, question %d (%s %s/%s): %s", c.Name, qi, q.Action, q.Type, q.Resource, problem)
			}
		}
	}
}

// With several parents, what reaches a resource is the union over the routes of the intersection of the masks along
// each route, for point anchors through the resource links and for whole-type anchors through the type links.
func TestMasksAreTheUnionOverRoutesOfTheIntersectionAlongEach(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: top, service: s, sync: true}
  - {name: left, service: s, sync: true, parents: [{type: top, inherit: [read]}]}
  - {name: right, service: s, sync: true, parents: [{type: top, inherit: [write, audit]}]}
  - {name: leaf, service: s, sync: true, parents: [{type: left}, {type: right, inherit: [write]}]}
  - {name: deep, service: s, sync: true, parents: [{type: leaf, inherit: [read, write]}]}
resources:
  - {type: top, id: T1}
  - {type: left, id: L1, parents: [{type: top, id: T1}]}
  - {type: right, id: R1, parents: [{type: top, id: T1}]}
  - {type: leaf, id: X1, parents: [{type: left, id: L1}, {type: right, id: R1}]}
  - {type: leaf, id: X2, parents: [{type: right, id: R1}]}
  - {type: deep, id: Z1, parents: [{type: leaf, id: X1}]}
rules:
  - {name: point, subject: p, type: top, resource: T1, access: [read, write, audit]}
  - {name: whole, subject: w, type: top, access: [read, write, audit]}
cases:
  - name: a point grant
    subject: p
    questions:
      - {type: leaf, resource: X1, action: read, allowed: true}
      - {type: leaf, resource: X1, action: write, allowed: true}
      - {type: leaf, resource: X1, action: audit, allowed: false, decision: no_access}
      - {type: leaf, resource: X2, action: read, allowed: false}
      - {type: leaf, resource: X2, action: write, allowed: true}
      - {type: right, resource: R1, action: audit, allowed: true}
      - {type: deep, resource: Z1, action: write, allowed: true}
      - {type: deep, resource: Z1, action: audit, allowed: false}
  - name: a whole-type grant
    subject: w
    questions:
      - {type: leaf, resource: X1, action: read, allowed: true}
      - {type: leaf, resource: X1, action: write, allowed: true}
      - {type: leaf, resource: X1, action: audit, allowed: false}
      - {type: deep, resource: Z1, action: audit, allowed: false}
`)
}

// An exception lifts a denial only when the denial reaches the exception's anchor with the level: on a route that does not
// carry the level the exception is not below the denial for it, even though its anchor is a descendant of the denial's.
func TestBelowIsDecidedPerLevel(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: top, service: s, sync: true}
  - {name: mid, service: s, sync: true, parents: [{type: top, inherit: [write]}]}
  - {name: leaf, service: s, sync: true, parents: [{type: mid}, {type: top}]}
resources:
  - {type: top, id: T1}
  - {type: mid, id: M1, parents: [{type: top, id: T1}]}
  - {type: leaf, id: X1, parents: [{type: mid, id: M1}, {type: top, id: T1}]}
rules:
  - {name: allow, subject: u, type: top, resource: T1, access: [read, write]}
  - {name: deny, subject: u, type: top, resource: T1, access: [read, write], deny: true}
  - {name: except, subject: u, type: mid, resource: M1, access: [read, write], exception: true}
cases:
  - name: read does not flow from T1 to M1, write does
    subject: u
    questions:
      - {type: leaf, resource: X1, action: read, allowed: false, decision: denied, granted_by: [allow, except], denials: [{rule: deny}]}
      - {type: leaf, resource: X1, action: write, allowed: true, decision: allowed, denials: [{rule: deny, lifted_by: [except]}]}
`)
}

// Equal anchors tie and the denial wins; a point is below the whole of its own type and of a type above it; the whole
// of a lower type is below any anchor of a higher type, the whole or a point; the whole of a type is not below a point
// of that same type.
func TestTiesAndTheShapesOfBelow(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: rrs, service: s, sync: true}
  - {name: branch, service: s, sync: true, parents: [{type: rrs}]}
  - {name: cap, service: s, sync: false}
resources:
  - {type: rrs, id: R1}
  - {type: branch, id: B1, parents: [{type: rrs, id: R1}]}
rules:
  - {name: same-allow, subject: same, type: branch, access: [read]}
  - {name: same-deny, subject: same, type: branch, resource: B1, access: [read], deny: true}
  - {name: same-except, subject: same, type: branch, resource: B1, access: [read], exception: true}
  - {name: whole-deny, subject: whole, type: branch, access: [read], deny: true}
  - {name: whole-except, subject: whole, type: branch, access: [read], exception: true}
  - {name: own-deny, subject: own, type: branch, access: [read], deny: true}
  - {name: own-except, subject: own, type: branch, resource: B1, access: [read], exception: true}
  - {name: higher-deny, subject: higher, type: rrs, access: [read], deny: true}
  - {name: higher-except, subject: higher, type: branch, resource: B1, access: [read], exception: true}
  - {name: lower-deny, subject: lower, type: rrs, access: [read], deny: true}
  - {name: lower-except, subject: lower, type: branch, access: [read], exception: true}
  - {name: point-deny, subject: point, type: rrs, resource: R1, access: [read], deny: true}
  - {name: point-except, subject: point, type: branch, access: [read], exception: true}
  - {name: tie-deny, subject: tie, type: branch, resource: B1, access: [read], deny: true}
  - {name: tie-except, subject: tie, type: branch, access: [read], exception: true}
  - {name: cap-allow, subject: cap, type: cap, access: [use]}
  - {name: cap-deny, subject: cap, type: cap, access: [use], deny: true}
  - {name: cap-except, subject: cap, type: cap, access: [use], exception: true}
cases:
  - {name: same resource, subject: same, questions: [{type: branch, resource: B1, action: read, allowed: false, decision: denied, denials: [{rule: same-deny}]}]}
  - {name: same whole type, subject: whole, questions: [{type: branch, resource: B1, action: read, allowed: false, decision: denied}]}
  - {name: a point below the whole of its type, subject: own, questions: [{type: branch, resource: B1, action: read, allowed: true, denials: [{rule: own-deny, lifted_by: [own-except]}]}]}
  - {name: a point below the whole of a higher type, subject: higher, questions: [{type: branch, resource: B1, action: read, allowed: true}]}
  - {name: a whole type below a higher one, subject: lower, questions: [{type: branch, resource: B1, action: read, allowed: true}]}
  - {name: a whole type is below a point of a higher type, subject: point, questions: [{type: branch, resource: B1, action: read, allowed: true, denials: [{rule: point-deny, lifted_by: [point-except]}]}]}
  - {name: a whole type is not below a point of its own type, subject: tie, questions: [{type: branch, resource: B1, action: read, allowed: false, decision: denied, denials: [{rule: tie-deny}]}]}
  - {name: a capability ties, subject: cap, questions: [{type: cap, action: use, allowed: false, decision: denied, granted_by: [cap-allow, cap-except], denials: [{rule: cap-deny}]}]}
`)
}

// A resource below an unresolved link is put under every point denial whose anchor could be the missing parent or lie
// above it, with the levels that would flow, also when the link is declared by an ancestor; only an exception provably
// below the denial's anchor lifts such a denial.
func TestUnresolvedLinksAtOrAboveFailClosed(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: division, service: s, sync: true}
  - {name: folder, service: s, sync: true, parents: [{type: division}]}
  - {name: rrs, service: s, sync: true, parents: [{type: division}]}
  - {name: branch, service: s, sync: true, parents: [{type: rrs}]}
  - {name: doc, service: s, sync: true, parents: [{type: branch}, {type: folder, inherit: [write]}]}
resources:
  - {type: division, id: D1}
  - {type: rrs, id: R1, parents: [{type: division, id: D1}]}
  - {type: rrs, id: R9, parents: [{type: division, id: D9}]}
  - {type: branch, id: B1, parents: [{type: rrs, id: R1}]}
  - {type: branch, id: B9, parents: [{type: rrs, id: R9}]}
  - {type: doc, id: X1, parents: [{type: branch, id: B1}, {type: folder, id: F9}]}
rules:
  - {name: all-branches, subject: u, type: branch, access: [read, write]}
  - {name: all-docs, subject: u, type: doc, access: [read, write]}
  - {name: deny-D1, subject: u, type: division, resource: D1, access: [read, write], deny: true}
  - {name: deny-D9, subject: v, type: division, resource: D9, access: [read], deny: true}
  - {name: v-branches, subject: v, type: branch, access: [read]}
  - {name: except-B9, subject: u, type: branch, resource: B9, access: [read, write], exception: true}
  - {name: except-B1, subject: w, type: branch, resource: B1, access: [read, write], exception: true}
  - {name: w-docs, subject: w, type: doc, access: [read, write]}
  - {name: w-deny-D1, subject: w, type: division, resource: D1, access: [read, write], deny: true}
  - {name: w-deny-R1, subject: w, type: rrs, resource: R1, access: [write], deny: true}
  - {name: w-deny-D2, subject: w, type: division, resource: D2, access: [read, write], deny: true}
cases:
  - name: the denial on D1 may cover B9, whose rrs hangs below the unknown D9
    subject: u
    questions:
      - {type: branch, resource: B9, action: read, allowed: false, decision: denied, denials: [{rule: deny-D1, unresolved_parent: true}]}
      - {type: branch, resource: B1, action: read, allowed: false, decision: denied, denials: [{rule: deny-D1}]}
      - {type: rrs, resource: R9, action: read, allowed: false}
  - name: the missing parent may be the anchor itself
    subject: v
    questions:
      - {type: branch, resource: B9, action: read, allowed: false, decision: denied, denials: [{rule: deny-D9, unresolved_parent: true}]}
      - {type: branch, resource: B1, action: read, allowed: true}
  - name: only an exception provably below the anchor lifts an assumed denial
    subject: w
    questions:
      # Below B1 for real and below D1 and D2 for all we know through F9; only write flows through folder.
      - {type: doc, resource: X1, action: read, allowed: true, denials: [{rule: w-deny-D1, lifted_by: [except-B1]}]}
      - {type: doc, resource: X1, action: write, allowed: false, decision: denied, denials: [{rule: w-deny-D1, lifted_by: [except-B1]}, {rule: w-deny-D2, unresolved_parent: true}, {rule: w-deny-R1, lifted_by: [except-B1]}]}
`)
}

// An exception on B9 is not provably below D1: it lifts nothing for the resources under the unknown division.
func TestAnAssumedDenialIsNotLiftedByAnExceptionAboveTheUnknown(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: division, service: s, sync: true}
  - {name: rrs, service: s, sync: true, parents: [{type: division, inherit: [read]}]}
  - {name: branch, service: s, sync: true, parents: [{type: rrs}]}
resources:
  - {type: division, id: D1}
  - {type: rrs, id: R9, parents: [{type: division, id: D9}]}
  - {type: branch, id: B9, parents: [{type: rrs, id: R9}]}
rules:
  - {name: allow, subject: u, type: branch, access: [read, write]}
  - {name: deny, subject: u, type: division, resource: D1, access: ["*"], deny: true}
  - {name: except, subject: u, type: branch, resource: B9, access: [read, write], exception: true}
cases:
  - name: only read would flow from the unknown division
    subject: u
    questions:
      - {type: branch, resource: B9, action: read, allowed: false, decision: denied, denials: [{rule: deny, unresolved_parent: true}]}
      - {type: branch, resource: B9, action: write, allowed: true, decision: allowed, denials: []}
`)
}

// Nested groups resolve up to level 16: a group the subject is in directly is level 1.
func TestNestedGroupsResolveUpToSixteenLevels(t *testing.T) {
	raw := `
services: [s]
types: [{name: cap, service: s, sync: false}]
groups:
  - {name: g1, members: [{subject: u}]}
`
	for i := 2; i <= 17; i++ {
		raw += "  - {name: g" + strconv.Itoa(i) + ", members: [{group: g" + strconv.Itoa(i-1) + "}]}\n"
	}
	raw += `rules:
  - {name: level16, member_of: g16, type: cap, access: [a]}
  - {name: level17, member_of: g17, type: cap, access: [b]}
  - {name: as-subject, subject_group: g3, type: cap, access: [c]}
cases:
  - name: the subject at the bottom of the chain
    subject: u
    questions:
      - {type: cap, action: a, allowed: true, granted_by: [level16]}
      - {type: cap, action: b, allowed: false, decision: no_access}
      - {type: cap, action: c, allowed: true}
  - name: somebody else
    subject: x
    questions:
      - {type: cap, action: a, allowed: false}
`
	check(t, raw)
}

// A condition on an attribute the request does not carry is unknown: an allow rule grants nothing on it, a denial applies
// on it unless it skips, and a membership that reads it is unknown, so a denial on the group applies too.
func TestUnknownConditions(t *testing.T) {
	check(t, `
services: [s]
types: [{name: cap, service: s, sync: false}]
groups:
  - {name: office, members: [{predicate: [{attr: ip, source: context, op: in_cidr, value: 10.0.0.0/8}]}]}
  - {name: outer, members: [{group: office}]}
  - {name: everyone, members: [{everybody: true}]}
rules:
  - {name: allow, member_of: everyone, type: cap, access: [use, see, act]}
  - {name: deny-outer, member_of: outer, type: cap, access: [use], deny: true}
  - {name: deny-skip, member_of: office, type: cap, access: [see], deny: true, on_missing: skip}
  - {name: allow-ne, predicate: [{attr: username, op: ne, value: ivanovich}], type: cap, access: [ne]}
  - {name: allow-office, member_of: office, type: cap, access: [office]}
  - {name: deny-guard, everybody: true, requires_predicate: [{attr: channel, source: context, op: eq, value: web}], type: cap, access: [act], deny: true}
cases:
  - name: without the address
    subject: u
    questions:
      - {type: cap, action: use, allowed: false, decision: denied, denials: [{rule: deny-outer}]}
      - {type: cap, action: see, allowed: true}
      - {type: cap, action: act, allowed: false, decision: denied}
      - {type: cap, action: ne, allowed: false, decision: no_access}
      - {type: cap, action: office, allowed: false}
  - name: outside the network
    subject: u
    claims: {username: petrov}
    context: {ip: 192.168.1.1, channel: api}
    questions:
      - {type: cap, action: use, allowed: true}
      - {type: cap, action: act, allowed: true}
      - {type: cap, action: ne, allowed: true}
  - name: inside the network
    subject: u
    context: {ip: 10.1.2.3}
    questions:
      - {type: cap, action: use, allowed: false}
      - {type: cap, action: see, allowed: false}
      - {type: cap, action: office, allowed: true}
`)
}

// access_implies of the type decided works both ways, the wildcard denies every level, and @name expands to the levels of
// the access group of the target's type, in a rule and in the item of a role granted with the rule's effect.
func TestImpliesWildcardRolesAndAccessGroups(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: rrs, service: s, sync: true}
  - {name: branch, service: s, sync: true, parents: [{type: rrs}], implies: {admin: [write], write: [read]}}
resources:
  - {type: rrs, id: R1}
  - {type: branch, id: B1, parents: [{type: rrs, id: R1}]}
  - {type: branch, id: B2, parents: [{type: rrs, id: R1}]}
access_groups:
  - {type: branch, name: editor, levels: [write]}
roles:
  - name: branch-admin
    items:
      - {type: branch, resource: B1, access: [admin]}
rules:
  - {name: admin-role, subject: a, role: branch-admin}
  - {name: deny-read, subject: a, type: branch, resource: B1, access: [read], deny: true, valid_from: 2031-01-01T00:00:00Z}
  - {name: editor, subject: e, type: branch, resource: B2, access: ["@editor"]}
  - {name: deny-all, subject: e, type: rrs, resource: R1, access: ["*"], deny: true, expires_at: 2030-01-01T12:00:00Z}
  - {name: role-deny, subject: d, role: branch-admin, deny: true}
  - {name: d-allow, subject: d, type: branch, access: [read, write, admin]}
cases:
  - name: admin includes write includes read
    subject: a
    questions:
      - {type: branch, resource: B1, action: read, allowed: true, granted_by: [admin-role]}
      - {type: branch, resource: B2, action: read, allowed: false}
  - name: denying read denies what includes it
    subject: a
    at: 2031-06-01T00:00:00Z
    questions:
      - {type: branch, resource: B1, action: read, allowed: false, decision: denied}
      - {type: branch, resource: B1, action: admin, allowed: false, decision: denied, denials: [{rule: deny-read}]}
  - name: the access group, and a wildcard denial that expired at the time of the case
    subject: e
    questions:
      - {type: branch, resource: B2, action: read, allowed: true, granted_by: [editor]}
      - {type: branch, resource: B2, action: admin, allowed: false, decision: no_access}
  - name: the wildcard denial in force
    subject: e
    at: 2030-01-01T11:59:59Z
    questions:
      - {type: branch, resource: B2, action: read, allowed: false, decision: denied, denials: [{rule: deny-all}]}
  - name: a role granted with a denial denies its items
    subject: d
    questions:
      - {type: branch, resource: B1, action: admin, allowed: false, decision: denied, denials: [{rule: role-deny}]}
      - {type: branch, resource: B1, action: read, allowed: true}
      - {type: branch, resource: B2, action: admin, allowed: true}
`)
}

// A type without a synchronizer is decided by the whole-type rules on it only; a whole-type rule on it still reaches the
// resources of the synchronized types below it. A question about another service's type is type_not_found.
func TestNotSyncTypesAndServices(t *testing.T) {
	check(t, `
services: [s, other]
types:
  - {name: area, service: s, sync: false}
  - {name: feature, service: s, sync: false, parents: [{type: area}]}
  - {name: shop, service: s, sync: true, parents: [{type: area}]}
  - {name: hidden, service: other, sync: false}
resources:
  - {type: shop, id: S1}
rules:
  - {name: area-read, subject: u, type: area, access: [read]}
  - {name: hidden-read, subject: u, type: hidden, access: [read]}
cases:
  - name: the service s
    subject: u
    questions:
      - {type: area, action: read, allowed: true, granted_by: [area-read]}
      - {type: feature, action: read, allowed: false, decision: no_access}
      - {type: shop, resource: S1, action: read, allowed: true}
      - {type: shop, resource: S2, action: read, allowed: false, decision: resource_not_found}
      - {type: area, resource: A1, action: read, allowed: false, decision: resource_not_found}
      - {type: hidden, action: read, allowed: false, decision: type_not_found}
      - {type: nowhere, action: read, allowed: false, decision: type_not_found}
`)
}

// A resource_from_claim finds the resources of the lookup type by name, id or attribute (a list attribute holds the
// value) and anchors the rule on their ancestors-or-self of its type; a resource_where covers the matching resources and
// what lies below them.
func TestClaimAndAttributeTargets(t *testing.T) {
	check(t, `
services: [s]
types:
  - {name: branch, service: s, sync: true}
  - {name: project, service: s, sync: true, parents: [{type: branch}]}
  - {name: task, service: s, sync: true, parents: [{type: project}]}
resources:
  - {type: branch, id: B1, name: North}
  - {type: branch, id: B2, name: South}
  - {type: project, id: P1, parents: [{type: branch, id: B1}], meta: {owner: alice, owners: [alice, dave], tier: 5}}
  - {type: project, id: P2, parents: [{type: branch, id: B1}], meta: {owner: bob, tier: 7}}
  - {type: project, id: P3, parents: [{type: branch, id: B2}], meta: {owner: alice, flag: true}}
  - {type: task, id: T2, parents: [{type: project, id: P2}]}
rules:
  - {name: own, everybody: true, type: project, from_claim: {claim: username, lookup_type: project, match_by: meta.owner}, access: [read]}
  - {name: co-own, everybody: true, type: project, from_claim: {claim: username, lookup_type: project, match_by: meta.owners}, access: [write]}
  - {name: branch-of-project, everybody: true, type: branch, from_claim: {claim: project, lookup_type: project, match_by: id}, access: [audit]}
  - {name: by-name, everybody: true, type: branch, from_claim: {claim: branch, lookup_type: branch}, access: [list]}
  - {name: big, subject: z, type: project, where: [{attr: tier, op: gt, value: "6"}], access: [read]}
  - {name: flagged, subject: z, type: project, where: [{attr: flag, op: eq, value: "True"}], access: [write]}
cases:
  - name: alice
    subject: alice
    claims: {username: alice, project: P2, branch: South}
    questions:
      - {type: project, resource: P1, action: read, allowed: true, granted_by: [own]}
      - {type: project, resource: P3, action: read, allowed: true}
      - {type: project, resource: P2, action: read, allowed: false}
      - {type: project, resource: P1, action: write, allowed: true}
      - {type: project, resource: P3, action: write, allowed: false}
      - {type: task, resource: T2, action: audit, allowed: true, granted_by: [branch-of-project]}
      - {type: project, resource: P1, action: audit, allowed: true}
      - {type: project, resource: P3, action: list, allowed: true}
      - {type: branch, resource: B1, action: list, allowed: false}
  - name: dave and a number claim
    subject: dave
    claims: {username: dave, project: 5}
    questions:
      - {type: project, resource: P1, action: write, allowed: true}
      - {type: project, resource: P1, action: read, allowed: false}
      - {type: branch, resource: B1, action: audit, allowed: false}
  - name: attributes
    subject: z
    questions:
      - {type: task, resource: T2, action: read, allowed: true, granted_by: [big]}
      - {type: project, resource: P1, action: read, allowed: false}
      - {type: project, resource: P3, action: write, allowed: true}
`)
}

func TestPyStrRendersLikePython(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"x", "x"}, {true, "True"}, {false, "False"}, {nil, "None"}, {float64(5), "5"}, {-3.0, "-3"}, {1.5, "1.5"},
		{1e16, "1e+16"}, {0.0001, "0.0001"}, {0.00001, "1e-05"}, {1234567.5, "1234567.5"},
		{[]any{"a", "b"}, "['a', 'b']"}, {[]any{"it's", float64(1), true}, `["it's", 1, True]`},
		{map[string]any{"b": "x", "a": []any{}}, "{'a': [], 'b': 'x'}"},
	} {
		if got := pyStr(tc.in); got != tc.want {
			t.Errorf("pyStr(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOperators(t *testing.T) {
	str := func(s string) model.PredicateValue { return model.PredicateValue{Str: s} }
	list := func(s ...string) model.PredicateValue { return model.PredicateValue{List: s, IsList: true} }
	for _, tc := range []struct {
		op    string
		value model.PredicateValue
		attr  string
		want  bool
	}{
		{model.OpEq, str("a"), "a", true}, {model.OpEq, str("a"), "b", false}, {model.OpEq, list("a", "b"), "b", true},
		{model.OpNe, str("a"), "b", true}, {model.OpNe, str("a"), "a", false},
		{model.OpIn, list("a", "b"), "a", true}, {model.OpIn, list("a"), "c", false}, {model.OpIn, str("a"), "a", true},
		{model.OpNotIn, list("a", "b"), "c", true}, {model.OpNotIn, list("a"), "a", false},
		{model.OpRegex, str("^ad"), "admin", true}, {model.OpRegex, str("min"), "admin", true}, {model.OpRegex, str("^min"), "admin", false},
		{model.OpGt, str("5"), "6", true}, {model.OpGt, str("5"), "5", false}, {model.OpGe, str("5"), "5", true},
		{model.OpLt, str("10"), "9", true}, {model.OpLt, str("10"), "x", false}, {model.OpLe, str("1.5"), "1.5", true},
		{model.OpGt, str("5"), "10", true}, // numbers, not text
		{model.OpInCIDR, str("10.0.0.0/8"), "10.1.2.3", true}, {model.OpInCIDR, list("192.168.0.0/16", "10.0.0.0/8"), "10.9.9.9", true},
		{model.OpInCIDR, str("10.0.0.0/8"), "11.0.0.1", false}, {model.OpInCIDR, str("10.0.0.0/8"), "not an address", false},
		{model.OpInCIDR, str("2001:db8::/32"), "2001:db8::1", true},
	} {
		if got := compare(tc.op, tc.value, tc.attr); got != tc.want {
			t.Errorf("%s %+v on %q = %v, want %v", tc.op, tc.value, tc.attr, got, tc.want)
		}
	}
}

// The clock is read at the time of the case, in UTC: hour 0-23, weekday 1-7 with Monday 1.
func TestClockConditions(t *testing.T) {
	sunday := time.Date(2030, 1, 6, 23, 30, 0, 0, time.UTC)
	a := attrs{now: sunday}
	for _, tc := range []struct {
		cond model.PredicateCondition
		want truth
	}{
		{model.PredicateCondition{Attr: "weekday", Source: model.SourceNow, Op: model.OpEq, Value: model.PredicateValue{Str: "7"}}, yes},
		{model.PredicateCondition{Attr: "hour", Source: model.SourceNow, Op: model.OpGe, Value: model.PredicateValue{Str: "23"}}, yes},
		{model.PredicateCondition{Attr: "hour", Source: model.SourceNow, Op: model.OpLt, Value: model.PredicateValue{Str: "9"}}, no},
		{model.PredicateCondition{Attr: "dept", Op: model.OpNe, Value: model.PredicateValue{Str: "x"}}, unknown},
		{model.PredicateCondition{Attr: "ip", Source: model.SourceContext, Op: model.OpNotIn, Value: model.PredicateValue{Str: "x"}}, unknown},
	} {
		if got := a.holds([]model.PredicateCondition{tc.cond}); got != tc.want {
			t.Errorf("%+v: %v, want %v", tc.cond, got, tc.want)
		}
	}
	monday := attrs{now: time.Date(2030, 1, 7, 1, 0, 0, 0, time.FixedZone("UTC+3", 3*3600))} // Sunday 22:00 UTC
	if got := monday.holds([]model.PredicateCondition{{Attr: "weekday", Source: model.SourceNow, Op: model.OpEq, Value: model.PredicateValue{Str: "7"}}}); got != yes {
		t.Errorf("the weekday is read in UTC: %v", got)
	}
}
