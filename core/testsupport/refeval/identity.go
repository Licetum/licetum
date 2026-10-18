package refeval

import (
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// maxNesting is the number of nestings of groups a membership resolves through: a group the subject matches directly is
// level 1, a group that nests it level 2, and so on up to level maxNesting+1 (sixteen groups nested in one another
// above the one matched).
const maxNesting = 16

// instance is a permission of a rule on one anchor: the whole of a type, or one resource. A rule that grants a role has
// one instance per anchor of every item of the role, with the effect and exception of the rule.
type instance struct {
	rule      string
	deny      bool
	exception bool
	whole     bool
	typ       string
	id        string // the anchor resource; it need not be a resource of the fixture
	levels    levelSet
}

func (i *instance) key() resKey { return resKey{i.typ, i.id} }

// caseState is what one case decides with: the groups of the subject and the instances of the rules that apply to it.
type caseState struct {
	g         *graph
	f         *policyfix.Fixture
	c         *policyfix.Case
	at        attrs
	groups    map[string]truth
	instances []*instance
}

func newCaseState(f *policyfix.Fixture, g *graph, c *policyfix.Case) *caseState {
	s := &caseState{g: g, f: f, c: c, at: attrs{claims: c.Claims, context: c.Context, now: f.NowOf(c)}}
	s.resolveGroups()
	for i := range f.Rules {
		r := &f.Rules[i]
		if !s.applies(r) {
			continue
		}
		if r.Role != "" {
			for _, role := range f.Roles {
				if role.Name != r.Role {
					continue
				}
				for j := range role.Items {
					s.addTarget(r, &role.Items[j].Target, role.Items[j].Access)
				}
			}
			continue
		}
		if r.Target != nil {
			s.addTarget(r, r.Target, r.Access)
		}
	}
	return s
}

// resolveGroups computes whether the subject is a member of every group, in three-valued logic: a membership whose
// predicate reads a missing attribute is unknown. Level 1 is the memberships that name the subject or whose predicate
// holds; each further level adds the groups nested into a group of the level below, up to maxNesting. Memberships that
// are disabled or out of their validity window at the time of the case count for nothing.
func (s *caseState) resolveGroups() {
	direct := map[string]truth{}
	for _, g := range s.f.Groups {
		t := no
		for _, m := range g.Members {
			if !s.memberInForce(m) {
				continue
			}
			switch {
			case m.Subject != "":
				t = max(t, truthOf(m.Subject == s.c.Subject))
			case len(m.Predicate) > 0 || m.Everybody:
				t = max(t, s.at.holds(m.Predicate))
			}
		}
		direct[g.Name] = t
	}
	current := direct
	for level := 2; level <= maxNesting+1; level++ {
		next := map[string]truth{}
		for _, g := range s.f.Groups {
			t := direct[g.Name]
			for _, m := range g.Members {
				if m.Group != "" && s.memberInForce(m) {
					t = max(t, current[m.Group])
				}
			}
			next[g.Name] = t
		}
		current = next
	}
	s.groups = current
}

func (s *caseState) memberInForce(m policyfix.Member) bool {
	return !m.Disabled && inForce(m.ValidFrom, m.ExpiresAt, s.at.now)
}

// applies reports whether a rule applies to the subject of the case. Who it targets (the subject, the group as a subject,
// a predicate, the members of a group) and its guards are joined by AND in three-valued logic. An allow rule applies only
// when that is true. A deny rule applies when it is true, or unknown with on_missing apply (the default of a denial).
func (s *caseState) applies(r *policyfix.Rule) bool {
	if r.Disabled || !inForce(r.ValidFrom, r.ExpiresAt, s.at.now) {
		return false
	}
	var t truth
	switch {
	case r.Subject != "":
		t = truthOf(r.Subject == s.c.Subject)
	case r.SubjectGroup != "":
		t = s.groups[r.SubjectGroup]
	case r.MemberOf != "":
		t = s.groups[r.MemberOf]
	default:
		t = s.at.holds(r.Predicate) // an empty predicate (everybody) holds
	}
	t = min(t, s.at.holds(r.RequiresPredicate))
	if r.RequiresGroup != "" {
		t = min(t, s.groups[r.RequiresGroup])
	}
	switch t {
	case yes:
		return true
	case unknown:
		return r.Deny && r.OnMissing != model.OnMissingSkip
	}
	return false
}

// addTarget adds the instances of one permission (an inline rule, or an item of a granted role) of a rule.
func (s *caseState) addTarget(r *policyfix.Rule, t *policyfix.Target, access []string) {
	if s.g.types[t.Type] == nil {
		return
	}
	levels := s.g.accessLevels(t.Type, access)
	add := func(whole bool, id string) {
		s.instances = append(s.instances, &instance{
			rule: r.Name, deny: r.Deny, exception: r.Exception, whole: whole, typ: t.Type, id: id, levels: levels,
		})
	}
	switch {
	case t.Resource != "":
		add(false, t.Resource)
	case t.FromClaim != nil:
		for _, id := range s.claimAnchors(t) {
			add(false, id)
		}
	case len(t.Where) > 0:
		for _, res := range s.g.byType[t.Type] {
			if whereMatches(s.g.metaOf(resKey{res.Type, res.ID}), t.Where) {
				add(false, res.ID)
			}
		}
	default:
		add(true, "")
	}
}

// claimAnchors returns the anchors of a resource_from_claim target: the resources of the lookup type that match the
// claim value (by name, id or attribute), each replaced by its ancestors-or-self of the target's type. A missing claim, or
// a match with no ancestor of that type, gives no anchor.
func (s *caseState) claimAnchors(t *policyfix.Target) []string {
	raw, ok := s.at.claims[t.FromClaim.Claim]
	if !ok || raw == nil {
		return nil
	}
	value := pyStr(raw)
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, res := range s.g.byType[t.FromClaim.LookupType] {
		if !s.g.matchesClaim(res, t.FromClaim.MatchBy, value) {
			continue
		}
		if res.Type == t.Type {
			add(res.ID)
		}
		for anc := range s.g.ancestors(resKey{res.Type, res.ID}) {
			if anc.typ == t.Type {
				add(anc.id)
			}
		}
	}
	return out
}
