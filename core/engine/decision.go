package engine

import (
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// The decision engine: given the rules that cover a resource, which access levels does the subject
// finally have? It is pure — it knows nothing about the database, only about the covering rules
// (instances), the type graph and how anchors relate — so it is easy to test exhaustively.
//
// Semantics (model C, see CLAUDE.md for the worked examples):
//
//   - allow rules add levels; deny rules take levels away from everything they cover;
//   - a denial wins over any allow rule, wherever the allow rule lies — unless the allow rule is
//     marked as an exception and lies strictly below the denial: then that denial is lifted for the
//     resources the exception covers. Every denial covering a resource needs its own exception below
//     it, so the deepest of the denials and exceptions above a resource decides: a denial below an
//     exception still stands (exceptions only lift denials above them). Ties — the same anchor, or
//     the same type — go to the denial;
//   - "strictly below" is a relation between anchors, decided per level (a level filter on a link
//     may stop a denial before it reaches the exception): a resource anchor is below another if the
//     other is its ancestor; a whole-type anchor is below another if its type is a lower type.
//     A point anchor is also below a whole-type anchor on its own type;
//   - level hierarchies (type.access_implies): granting write grants read, denying read denies write;
//   - a denial covers a resource with an unresolved parent link when the missing parent could be
//     under the denial's anchor — the engine fails closed where the hierarchy is not fully known.

// Instance is one applicable rule anchored at one place: a whole type, or one resource. A rule with
// resource_from_claim yields an Instance per resolved anchor.
type Instance struct {
	Rule       *model.AssignmentRule
	Deny       bool
	Exception  bool
	Levels     []string // may contain model.AllLevels for a denial
	AnchorType uuid.UUID
	AnchorRow  *uuid.UUID // nil: the whole type
	AnchorExt  string
	Note       string // how the rule reached the subject / how the anchor was found, for explanations
	// Missing marks a denial that applies only because a condition reads an attribute the request does not carry
	// (on_missing_attribute = apply): it is applied as a fail-closed guess, and explanations say so.
	Missing bool
}

func (in *Instance) Whole() bool { return in.AnchorRow == nil }

// Cover says an instance covers a resource: mask is the set of levels that flow from the anchor to it.
type Cover struct {
	Inst int
	Mask LevelSet
	// Assumed marks a denial applied because the resource has an unresolved parent link.
	Assumed bool
}

// denial is a denial that applies to a level of a resource; lifted by the listed exceptions if any.
type denial struct {
	Inst     int
	Level    string // the denied level that matched (the level of the rule, or of the hierarchy)
	LiftedBy []int
}

func (d denial) lifted() bool { return len(d.LiftedBy) > 0 }

// levelVerdict is the decision for one level of a resource.
type levelVerdict struct {
	Level     string
	Allowed   bool
	GrantedBy []int // allow instances granting the level, including through the level hierarchy
	Denials   []denial
}

// nodeVerdict is the decision for one resource.
type nodeVerdict struct {
	Allowed []string // sorted
	Blocked []string // levels that were granted but are denied, sorted
	Levels  []levelVerdict
}

// engine decides levels of resources.
type engine struct {
	graph *TypeGraph
	insts []Instance
	// pairs holds, for a denial and an exception both anchored at resources, the levels that flow
	// from the denial's anchor down to the exception's anchor; absent if it is not below.
	pairs map[[2]int]LevelSet
	memo  map[string]nodeVerdict
}

func newEngine(g *TypeGraph, insts []Instance, pairs map[[2]int]LevelSet) *engine {
	return &engine{graph: g, insts: insts, pairs: pairs, memo: map[string]nodeVerdict{}}
}

// below reports whether exception ex lies strictly below denial d for a level.
func (e *engine) below(ex, d int, level string) bool {
	x, y := &e.insts[ex], &e.insts[d]
	switch {
	case !x.Whole() && !y.Whole():
		m, ok := e.pairs[[2]int{d, ex}]
		return ok && m.has(level)
	case !x.Whole() && y.Whole():
		// A specific resource beats the whole type it belongs to, and the lower types too.
		return x.AnchorType == y.AnchorType || e.graph.reaches(y.AnchorType, x.AnchorType, level)
	default:
		return e.graph.reaches(y.AnchorType, x.AnchorType, level)
	}
}

func withImplied(level string, implies map[string][]string) []string {
	return append([]string{level}, implies[level]...)
}

// denyMatches finds the levels of a denial that deny the given level of a node: the level itself,
// a level it includes (denying read denies write), or the wildcard. Returns every matching level
// that flows along the covering mask.
func denyMatches(in *Instance, mask LevelSet, level string, implies map[string][]string) []string {
	var out []string
	for _, k := range in.Levels {
		switch {
		case k == model.AllLevels:
			if mask.has(level) {
				out = append(out, level)
			}
		case mask.has(k) && (k == level || slices.Contains(implies[level], k)):
			out = append(out, k)
		}
	}
	return out
}

// decide computes the verdict for a resource of the given type covered by the given rules.
func (e *engine) decide(nodeType uuid.UUID, covers []Cover) nodeVerdict {
	var implies map[string][]string
	if n, ok := e.graph.Node(nodeType); ok {
		implies = n.Implies
	}

	grantedBy := map[string][]int{}
	exceptionBy := map[string][]int{}
	var denies []Cover
	for _, c := range covers {
		in := &e.insts[c.Inst]
		if in.Deny {
			denies = append(denies, c)
			continue
		}
		for _, k := range in.Levels {
			if !c.Mask.has(k) {
				continue
			}
			for _, l := range withImplied(k, implies) {
				grantedBy[l] = append(grantedBy[l], c.Inst)
				if in.Exception {
					exceptionBy[l] = append(exceptionBy[l], c.Inst)
				}
			}
		}
	}

	levels := make([]string, 0, len(grantedBy))
	for l := range grantedBy {
		levels = append(levels, l)
	}
	slices.Sort(levels)

	var v nodeVerdict
	for _, l := range levels {
		lv := levelVerdict{Level: l, GrantedBy: uniqueSorted(grantedBy[l]), Allowed: true}
		for _, c := range denies {
			for _, k := range denyMatches(&e.insts[c.Inst], c.Mask, l, implies) {
				d := denial{Inst: c.Inst, Level: k}
				for _, ex := range uniqueSorted(exceptionBy[l]) {
					if e.below(ex, c.Inst, k) {
						d.LiftedBy = append(d.LiftedBy, ex)
					}
				}
				lv.Denials = append(lv.Denials, d)
				if !d.lifted() {
					lv.Allowed = false
				}
			}
		}
		if lv.Allowed {
			v.Allowed = append(v.Allowed, l)
		} else {
			v.Blocked = append(v.Blocked, l)
		}
		v.Levels = append(v.Levels, lv)
	}
	return v
}

// decideMemo is decide with the result cached by the set of covering rules: thousands of resources
// under the same anchors share one verdict.
func (e *engine) decideMemo(nodeType uuid.UUID, covers []Cover) nodeVerdict {
	slices.SortFunc(covers, func(a, b Cover) int {
		if a.Inst != b.Inst {
			return a.Inst - b.Inst
		}
		return strings.Compare(a.Mask.key(), b.Mask.key())
	})
	var b strings.Builder
	b.WriteString(nodeType.String())
	for _, c := range covers {
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(c.Inst))
		b.WriteString(c.Mask.key())
		if c.Assumed {
			b.WriteByte('!')
		}
	}
	key := b.String()
	if v, ok := e.memo[key]; ok {
		return v
	}
	v := e.decide(nodeType, covers)
	e.memo[key] = v
	return v
}

// decideWhole decides the levels on a type as a whole (creating entities, capabilities): only the
// rules anchored on the type itself count, and a denial always beats an allow rule there.
func (e *engine) decideWhole(typeID uuid.UUID) nodeVerdict {
	var covers []Cover
	for i := range e.insts {
		in := &e.insts[i]
		if in.Whole() && in.AnchorType == typeID {
			covers = append(covers, Cover{Inst: i, Mask: AllLevels})
		}
	}
	return e.decide(typeID, covers)
}

func uniqueSorted(in []int) []int {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
