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
// once. A denial is its rule, the exceptions that lift it and whether it is assumed for an unresolved parent.
func Compare(q *policyfix.Question, a Answer) []string {
	var out []string
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
		got := make([]string, len(a.Denials))
		for i, d := range a.Denials {
			got[i] = d.String()
		}
		want := make([]string, len(q.Denials))
		for i, d := range q.Denials {
			want[i] = Denial{Rule: d.Rule, LiftedBy: sortedUnique(d.LiftedBy), UnresolvedParent: d.UnresolvedParent}.String()
		}
		if diff := setDiff(got, want); diff != "" {
			out = append(out, "denials: "+diff)
		}
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
