package refeval

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// The decision contracts: every fixture of policyfix that names a contract is decided by the reference evaluator and
// compared with what the fixture expects. A contract that no fixture checks yet is skipped.

func TestContract_DEC01_DenialBeatsAnyAllow(t *testing.T) { runContract(t, "DEC01") }

func TestContract_DEC02_ExceptionLiftsOnlyStrictlyBelow(t *testing.T) { runContract(t, "DEC02") }

func TestContract_DEC03_BelowIsDecidedPerLevel(t *testing.T) { runContract(t, "DEC03") }

func TestContract_DEC04_ImpliesWorksBothWaysAndTheWildcardDeniesAll(t *testing.T) {
	runContract(t, "DEC04")
}

func TestContract_DEC05_InheritMasksAreTheUnionOverRoutes(t *testing.T) { runContract(t, "DEC05") }

func TestContract_DEC06_NotSyncTypesAreDecidedAsAWhole(t *testing.T) { runContract(t, "DEC06") }

func TestContract_DEC07_ValidityWindowsAtTheTimeOfTheDecision(t *testing.T) { runContract(t, "DEC07") }

func TestContract_DEC08_ConditionOperatorsAndMissingAttributes(t *testing.T) { runContract(t, "DEC08") }

func TestContract_DEC09_OnMissingAttributeDecidesADenial(t *testing.T) { runContract(t, "DEC09") }

func TestContract_DEC10_GuardsAndResourceTargets(t *testing.T) { runContract(t, "DEC10") }

func TestContract_DEC11_UnresolvedParentsFailClosedTransitively(t *testing.T) {
	runContract(t, "DEC11")
}

func TestContract_DEC12_RolesNestedGroupsAndAccessGroupsExpand(t *testing.T) { runContract(t, "DEC12") }

func TestContract_DEC13_ExplanationsNameTheRules(t *testing.T) { runContract(t, "DEC13") }

// runContract decides every question of the fixtures of a contract.
func runContract(t *testing.T, contract string) {
	t.Helper()
	all, err := policyfix.Decisions()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := policyfix.For(all, contract)
	if len(fixtures) == 0 {
		t.Skipf("no fixture checks %s yet", contract)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
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
		})
	}
}

// mismatches compares an answer with the expectations of a question: allowed always, the decision, the granting rules
// and the denials when the question sets them. The lists are compared as sets, and exactly: an explanation names the
// rules the fixture expects, no fewer and no more.
func mismatches(q *policyfix.Question, a Answer) []string {
	var out []string
	if a.Allowed != q.Allowed {
		out = append(out, fmt.Sprintf("allowed %v, want %v (answer %+v)", a.Allowed, q.Allowed, a))
	}
	if q.Decision != "" && a.Decision != q.Decision {
		out = append(out, fmt.Sprintf("decision %s, want %s (answer %+v)", a.Decision, q.Decision, a))
	}
	if q.GrantedBy != nil && !sameSet(a.GrantedBy, q.GrantedBy) {
		out = append(out, fmt.Sprintf("granted_by %v, want %v", a.GrantedBy, q.GrantedBy))
	}
	if q.Denials != nil {
		want := make([]string, 0, len(q.Denials))
		for _, d := range q.Denials {
			want = append(want, describeExpected(d))
		}
		got := make([]string, 0, len(a.Denials))
		for _, d := range a.Denials {
			got = append(got, describe(d))
		}
		if !sameSet(got, want) {
			out = append(out, fmt.Sprintf("denials %v, want %v", got, want))
		}
	}
	return out
}

func describe(d Denial) string {
	return denialText(d.Rule, d.LiftedBy, d.UnresolvedParent)
}

func describeExpected(d policyfix.ExpectedDenial) string {
	return denialText(d.Rule, d.LiftedBy, d.UnresolvedParent)
}

func denialText(rule string, liftedBy []string, unresolved bool) string {
	lifted := slices.Clone(liftedBy)
	slices.Sort(lifted)
	text := rule
	if len(lifted) > 0 {
		text += " lifted by " + strings.Join(lifted, ",")
	}
	if unresolved {
		text += " (unresolved parent)"
	}
	return text
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}
