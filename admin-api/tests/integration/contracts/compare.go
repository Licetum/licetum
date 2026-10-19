//go:build integration

package contracts

import (
	"fmt"
	"slices"

	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// Compare returns every way the answer differs from what the question expects, in words; none when they agree. Allowed is
// always compared; the decision, the granting rules and the denials only when the question sets them (an empty list set in
// the fixture expects none). The rules are compared as sets: the order of an explanation is not part of the contract, and
// a rule that takes part through several instances (a role, several anchors, a denial matching several levels) is named
// once. A denial is its rule, the exceptions that lift it and whether it is assumed for an unresolved parent, and whether
// it applies on a missing attribute when the expectation of that rule says. An entry that the explanation repeats where a
// rule written inline has one (Answer.Repeated) is a mismatch whatever the question expects.
func Compare(q *policyfix.Question, a Answer) []string {
	var out []string
	for _, r := range a.Repeated {
		out = append(out, "the explanation lists more than once "+r)
	}
	if a.Allowed != q.Allowed {
		out = append(out, fmt.Sprintf("allowed: got %t, want %t", a.Allowed, q.Allowed))
	}
	if q.Decision != "" && a.Decision != q.Decision {
		out = append(out, fmt.Sprintf("decision: got %q, want %q", a.Decision, q.Decision))
	}
	if q.GrantedBy != nil {
		if diff := setDiff(a.GrantedBy, q.GrantedBy); diff != "" {
			out = append(out, "granted_by: "+diff)
		}
	}
	if q.Denials != nil {
		statesMissing := map[string]bool{} // the rules whose expectation says whether they apply on a missing attribute
		want := make([]string, len(q.Denials))
		for i, d := range q.Denials {
			expected := Denial{Rule: d.Rule, LiftedBy: sortedUnique(d.LiftedBy), UnresolvedParent: d.UnresolvedParent}
			want[i] = expected.String()
			if d.MissingAttribute != nil {
				statesMissing[d.Rule] = true
				expected.MissingAttribute = *d.MissingAttribute
				want[i] = expected.withMissing()
			}
		}
		got := make([]string, len(a.Denials))
		for i, d := range a.Denials {
			got[i] = d.String()
			if statesMissing[d.Rule] {
				got[i] = d.withMissing()
			}
		}
		if diff := setDiff(got, want); diff != "" {
			out = append(out, "denials: "+diff)
		}
	}
	return out
}

// Same returns every way the answer got differs from the answer want, in words; none when they agree. Unlike Compare it
// compares the decision whether or not it is set, and an explanation that names no rule against one that names some: it
// is for two answers to one question, from two routes of the API. The rules are compared as Compare does.
func Same(want, got Answer) []string {
	var out []string
	if got.Allowed != want.Allowed {
		out = append(out, fmt.Sprintf("allowed: got %t, want %t", got.Allowed, want.Allowed))
	}
	if got.Decision != want.Decision {
		out = append(out, fmt.Sprintf("decision: got %q, want %q", got.Decision, want.Decision))
	}
	if diff := setDiff(got.GrantedBy, want.GrantedBy); diff != "" {
		out = append(out, "granted_by: "+diff)
	}
	names := func(denials []Denial) []string {
		out := make([]string, len(denials))
		for i, d := range denials {
			out[i] = d.String()
		}
		return out
	}
	if diff := setDiff(names(got.Denials), names(want.Denials)); diff != "" {
		out = append(out, "denials: "+diff)
	}
	return out
}

// setDiff says how two lists differ as sets: what is missing from got and what got has besides; "" when they are equal.
func setDiff(got, want []string) string {
	got, want = sortedUnique(got), sortedUnique(want)
	var missing, unexpected []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			unexpected = append(unexpected, g)
		}
	}
	if len(missing) == 0 && len(unexpected) == 0 {
		return ""
	}
	s := fmt.Sprintf("got %q, want %q", got, want)
	if len(missing) > 0 {
		s += fmt.Sprintf("; missing %q", missing)
	}
	if len(unexpected) > 0 {
		s += fmt.Sprintf("; unexpected %q", unexpected)
	}
	return s
}
