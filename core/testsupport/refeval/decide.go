package refeval

import (
	"maps"
	"slices"

	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// cover returns the levels through which an instance reaches a resource for real: every level on its own anchor, the
// flow of the resource links from a point anchor above it, the flow of the type links from a whole-type anchor on its
// type or above it. Nothing when the instance does not reach the resource.
func (s *caseState) cover(i *instance, r resKey) levelSet {
	if i.whole {
		flow, _ := s.g.typeFlow(i.typ, r.typ)
		return flow
	}
	flow, _ := s.g.resourceFlow(i.key(), r)
	return flow
}

// assumed returns the levels through which a point instance may reach a resource through an unresolved parent link at
// or above it: the missing parent could be the anchor, or lie below it, when the anchor's type is the missing parent's
// type or a type above it. The levels are those that would flow from the anchor's type to the missing parent's type,
// then down the link and the route to the resource.
func (s *caseState) assumed(i *instance, r resKey) levelSet {
	if i.whole {
		return noLevel
	}
	out := noLevel
	for _, l := range s.g.openAbove(r) {
		if flow, ok := s.g.typeFlow(i.typ, l.missing); ok {
			out = out.or(flow.and(l.mask))
		}
	}
	return out
}

// below reports whether the anchor of an exception lies strictly below the anchor of a denial for one level: the denial,
// flowing down from its anchor, reaches the exception's anchor with that level. A point lies below a point that is its
// ancestor through the resource links, and below the whole of its own type or of a type above its type. The whole of a
// type lies below any anchor, point or whole, of a strictly higher type through the type links: "a whole-type anchor
// is below another if its type is a lower type" (CLAUDE.md), whatever the resources of that type under the point. Equal
// anchors, and the whole of the type of a point, are a tie, which goes to the denial.
func (s *caseState) below(e, d *instance, level string) bool {
	var flow levelSet
	var ok bool
	switch {
	case e.whole:
		if e.typ == d.typ {
			return false
		}
		flow, ok = s.g.typeFlow(d.typ, e.typ)
	case d.whole:
		flow, ok = s.g.typeFlow(d.typ, e.typ)
	default:
		if e.key() == d.key() {
			return false
		}
		flow, ok = s.g.resourceFlow(d.key(), e.key())
	}
	return ok && flow.and(d.levels).has(level)
}

// grants reports whether levels that reached a resource of a type grant an action: one of them is the action or
// includes it through access_implies.
func (s *caseState) grants(typ string, reach levelSet, action string) bool {
	if reach.all {
		return true
	}
	for level := range reach.set {
		if s.g.includes(typ, level).has(action) {
			return true
		}
	}
	return false
}

// denied returns the levels of a denial that reached a resource of a type and deny an action: the action itself and
// every level it includes (denying read denies write when write includes read). A wildcard denies all of them.
func (s *caseState) denied(typ string, reach levelSet, action string) levelSet {
	return s.g.includes(typ, action).and(reach)
}

// denialFacts collects the denials of one rule in an answer: one per anchor and denied level.
type denialFacts struct {
	standing           int             // the denials no exception lifts
	real, standingReal bool            // some denial (some standing one) reached the resource for real
	liftedBy           map[string]bool // the exceptions that lift some denial
}

// factsOf returns the denials of a rule, adding the rule.
func factsOf(byRule map[string]*denialFacts, rule string) *denialFacts {
	acc := byRule[rule]
	if acc == nil {
		acc = &denialFacts{liftedBy: map[string]bool{}}
		byRule[rule] = acc
	}
	return acc
}

// decideResource decides an action on a resource.
func (s *caseState) decideResource(r resKey, action string) Answer {
	granted := map[string]bool{}
	var exceptions []*instance
	for _, i := range s.instances {
		if !i.deny && s.grants(r.typ, s.cover(i, r).and(i.levels), action) {
			granted[i.rule] = true
			if i.exception {
				exceptions = append(exceptions, i)
			}
		}
	}

	byRule := map[string]*denialFacts{}
	for _, d := range s.instances {
		if !d.deny {
			continue
		}
		reached := s.denied(r.typ, s.cover(d, r).and(d.levels), action)
		possible := reached.or(s.denied(r.typ, s.assumed(d, r).and(d.levels), action))
		if possible.empty() {
			continue
		}
		acc := factsOf(byRule, d.rule)
		// possible is never the wildcard: it is a subset of the levels the action includes.
		for _, level := range possible.sorted() {
			isReal := reached.has(level)
			acc.real = acc.real || isReal
			lifted := false
			for _, e := range exceptions {
				if s.below(e, d, level) {
					lifted = true
					acc.liftedBy[e.rule] = true
				}
			}
			if !lifted {
				acc.standing++
				acc.standingReal = acc.standingReal || isReal
			}
		}
	}
	return answer(granted, byRule)
}

// decideWhole decides an action on a type as a whole: by the whole-type rules on the type itself, with no cascade from
// the types above it. An exception on the same type ties with a denial there, so no denial is lifted.
func (s *caseState) decideWhole(typ, action string) Answer {
	granted := map[string]bool{}
	byRule := map[string]*denialFacts{}
	for _, i := range s.instances {
		if !i.whole || i.typ != typ {
			continue
		}
		if !i.deny {
			if s.grants(typ, i.levels, action) {
				granted[i.rule] = true
			}
			continue
		}
		if s.denied(typ, i.levels, action).empty() {
			continue
		}
		acc := factsOf(byRule, i.rule)
		acc.standing++
		acc.real, acc.standingReal = true, true
	}
	return answer(granted, byRule)
}

// answer builds the answer from the rules that grant the action and the denials of it, per rule. A rule denies when one
// of its denials (an anchor and a denied level) is lifted by no exception; such a rule is listed with no exception, and a
// rule whose every denial is lifted with the exceptions that lift them. A denial is reported as coming from an unresolved
// parent when none of the denials that decide it reached the resource for real.
func answer(granted map[string]bool, byRule map[string]*denialFacts) Answer {
	a := Answer{GrantedBy: slices.Sorted(maps.Keys(granted))}
	deniedNow := false
	for _, name := range slices.Sorted(maps.Keys(byRule)) {
		acc := byRule[name]
		d := Denial{Rule: name}
		if acc.standing > 0 {
			deniedNow = true
			d.UnresolvedParent = !acc.standingReal
		} else {
			d.LiftedBy = slices.Sorted(maps.Keys(acc.liftedBy))
			d.UnresolvedParent = !acc.real
		}
		a.Denials = append(a.Denials, d)
	}
	switch {
	case len(granted) == 0:
		a.Decision = policyfix.DecisionNoAccess
		a.Denials = nil
	case deniedNow:
		a.Decision = policyfix.DecisionDenied
	default:
		a.Allowed = true
		a.Decision = policyfix.DecisionAllowed
	}
	return a
}
