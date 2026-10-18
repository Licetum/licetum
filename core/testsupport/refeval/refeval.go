// Package refeval is the reference evaluator of the decision semantics: it decides the questions of a policyfix.Fixture
// in memory, from the policy and the resource links of the fixture alone. It is the oracle of the decision contracts
// (DEC* and DECP in contracts.yaml), so it is written from the description of the semantics (CLAUDE.md: "Domain model",
// "Conditions and request context", "Permission computation", "Decision semantics"), the validation rules of core/model
// and what the integration tests of the admin API assert, and shares no code with core/engine: it builds the lineage of
// every resource by walking the declared links itself, with no closure table and no type graph of the engine.
//
// The semantics, as implemented here:
//
//   - Identity: the subject, and every group it belongs to. A membership names the subject, or has a predicate over the
//     claims, the request context and the clock (an empty one, everybody, always holds), or nests another group; a group
//     the subject matches directly is level 1 and nesting resolves through 16 groups, up to level 17 (maxNesting). Disabled memberships and
//     memberships outside their validity window at the time of the case count for nothing.
//   - Three-valued conditions: a condition on an attribute the request does not carry (a claim or a context attribute
//     that is absent or null) is unknown, whatever the operator, ne and not_in included. AND is the minimum and OR the
//     maximum of no < unknown < yes, so an unknown membership makes the groups that nest it unknown.
//   - Applicable rules: enabled, in their validity window (valid_from included, expires_at excluded), and whose target
//     (subject, the group as a subject, a predicate, the members of a group) and guards (requires_predicate,
//     requires_group) are true; a deny rule also applies when they are unknown, unless its on_missing is skip.
//   - Instances: a rule (or each item of the role it grants, with the rule's effect and exception) has one instance per
//     anchor: the whole type; the resource it names (also when that resource is not in the fixture: it then covers
//     nothing for real but can still be the missing parent of an unresolved link); every resource whose attributes match
//     its resource_where; the ancestors-or-self, of the rule's type, of every resource of the lookup type that matches the
//     claim value (by name, id, or meta.<attribute>: equal, or a list holding it).
//   - Levels: '*' is every level, '@name' the levels of the access group of the target's type. The levels of an
//     instance flow down as written: a point anchor reaches its descendants through the resource links, a whole-type
//     anchor every resource of its type and of the types below it through the type links. What reaches a resource from
//     an anchor is the union over the routes of the intersection of the inherit masks of the type links along each route
//     (null inherit is every level); only a parent that is a resource of the fixture gives a route.
//   - access_implies is the one of the type of the resource decided, applied to the levels that reached it: a level
//     grants an action when it is the action or includes it, and denies it when the action is that level or includes it.
//     The access_implies of the anchor's type is not applied on the way down.
//   - Denials and exceptions are decided per level: a denial is one anchor with one denied level (a level the action
//     includes). An exception that grants the action lifts it when its anchor lies strictly below the denial's anchor for
//     that level (see below), and every denial needs its own exception, so the deepest of them decides and a tie goes to
//     the denial. The action is denied when some denial is lifted by no exception.
//   - Fail closed: an unresolved parent link at or above a resource puts the resource under every point denial whose
//     anchor's type is the missing parent's type or above it, with the levels that would flow from that type down the
//     link and the route. Such a denial is lifted only by an exception provably below its anchor: a point by the real
//     links, the whole of a type by the types. A point exception on the resource itself does not lift it, since the
//     resource is not known to lie below the anchor (the engine keeps the denial too: the conservative reading).
//   - A question about a type as a whole (no resource), and so every question about a type with no synchronizer, is
//     decided by the whole-type rules on that very type, with no cascade from the types above it.
//   - Explanations: granted_by names every allow rule (exceptions included) that grants the action, whatever the
//     denials; denials names every denying rule that reaches the resource with a level of the action, standing or lifted.
//     The decision is no_access when nothing grants, denied when something grants and a denial stands, allowed otherwise.
//     A no_access answer lists no denials: nothing granted the action, so no denial decided it.
//
// The choices where the description leaves room, taken as the most literal reading:
//
//   - Claim values, context values and resource attributes are compared as their Python text (pyStr): a list claim is
//     the text ['a', 'b'], not its items. The claim of a resource_from_claim is matched the same way.
//   - A scalar value of in and not_in is a list of one item; eq, ne, regex and the numeric operators with a list value
//     never hold (as in the engine and the Python version before it). Numeric operators and in_cidr are false (not
//     unknown) on an attribute that is not a number or an address, parsed as it is, without trimming.
//     On a list attribute of a resource_where, ne and not_in hold when no item matches, the other operators when one does.
//   - "Strictly below for a level" means that the denial, flowing down from its anchor, reaches the exception's anchor
//     with that level: the structural relation of CLAUDE.md, with the inherit masks along the way.
//   - The anchors of a resource_from_claim are found through the resource links regardless of their inherit masks.
//   - Nested groups: level 1 is a group that the subject matches directly; the last level that counts is 17, sixteen
//     nestings above it (the engine's MaxGroupDepth).
//   - A question about a type with a synchronizer but no resource is decided as one about a type with none.
//   - In an explanation, a rule with several denials (anchors or levels) is listed once: with no exception when one of
//     its denials stands, otherwise with every exception that lifts one of them; unresolved_parent is set when no denial
//     of the rule that decides it reached the resource for real.
package refeval

import (
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// Answer is the decision on one question with its explanation, in the names of the fixture. The lists are sorted.
type Answer struct {
	Allowed bool
	// Decision is one of policyfix.DecisionAllowed, DecisionDenied, DecisionNoAccess, DecisionResourceNotFound and
	// DecisionTypeNotFound.
	Decision string
	// GrantedBy names the allow rules that grant the action, through access_implies too.
	GrantedBy []string
	// Denials are the deny rules that reach the resource with a level of the action, lifted or not.
	Denials []Denial
}

// Denial is a deny rule of an explanation.
type Denial struct {
	Rule string
	// LiftedBy names the exceptions that lift the denial; a denial with none stands.
	LiftedBy []string
	// UnresolvedParent says the denial reaches the resource only because a parent link at or above it is unresolved.
	UnresolvedParent bool
}

// Evaluator decides the questions of one fixture. It keeps the lineage it computes and the state of each case, so a
// caller that asks many questions of a large fixture builds one. It is not safe for concurrent use.
type Evaluator struct {
	f     *policyfix.Fixture
	g     *graph
	cases map[*policyfix.Case]*caseState
}

// New returns an evaluator of a fixture. The fixture must not change while the evaluator is used.
func New(f *policyfix.Fixture) *Evaluator {
	return &Evaluator{f: f, g: newGraph(f), cases: map[*policyfix.Case]*caseState{}}
}

// Decide answers one question of a case of a fixture.
func Decide(f *policyfix.Fixture, c *policyfix.Case, q *policyfix.Question) Answer {
	return New(f).Decide(c, q)
}

// Decide answers one question of a case. The question is about a type of the service the case is asked of
// (Fixture.ServiceOf): a type of another service, or no type at all, is type_not_found; a resource the fixture does not
// have is resource_not_found.
func (e *Evaluator) Decide(c *policyfix.Case, q *policyfix.Question) Answer {
	t, ok := e.g.types[q.Type]
	if !ok || t.Service != e.f.ServiceOf(c) {
		return Answer{Decision: policyfix.DecisionTypeNotFound}
	}
	s := e.cases[c]
	if s == nil {
		s = newCaseState(e.f, e.g, c)
		e.cases[c] = s
	}
	if q.Resource == "" {
		return s.decideWhole(q.Type, q.Action)
	}
	r := resKey{q.Type, q.Resource}
	if _, ok := e.g.resources[r]; !ok {
		return Answer{Decision: policyfix.DecisionResourceNotFound}
	}
	return s.decideResource(r, q.Action)
}
