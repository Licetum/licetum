//go:build integration

package contracts_test

// The decision contracts DEC01 ... DEC13 of contracts.yaml, checked against the engine of the check API: every fixture of
// core/testsupport/policyfix/testdata/decisions that names a contract is loaded into a cluster of its own and its questions
// are asked through POST /service/{slug}/access/v1/evaluations (see package contracts); the permission list of each case
// (GET /service/{slug}/permission) must agree with those answers, and so must POST /service/{slug}/check, which the
// evaluations replace, while the check API has it (contracts.Loaded.SecondOpinion). A fixture that checks several contracts
// is loaded and asked once per test binary; the test of each contract compares the answers with what the fixture expects.

import (
	"fmt"
	"sync"
	"testing"

	"github.com/licetum/licetum/admin-api/tests/integration/contracts"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

func TestContract_DEC01_ADenialBeatsAnyAllow(t *testing.T) {
	runContract(t, "DEC01")
}

func TestContract_DEC02_ExceptionLiftsOnlyStrictlyBelow(t *testing.T) {
	runContract(t, "DEC02")
}

func TestContract_DEC03_BelowIsDecidedPerLevel(t *testing.T) {
	runContract(t, "DEC03")
}

func TestContract_DEC04_AccessImpliesWorksBothWays(t *testing.T) {
	runContract(t, "DEC04")
}

func TestContract_DEC05_InheritMasksLimitWhatFlowsDown(t *testing.T) {
	runContract(t, "DEC05")
}

func TestContract_DEC06_NotSyncTypesAreDecidedByWholeTypeRules(t *testing.T) {
	runContract(t, "DEC06")
}

func TestContract_DEC07_ValidityWindowsAtTheTimeOfTheDecision(t *testing.T) {
	runContract(t, "DEC07")
}

func TestContract_DEC08_ConditionOperatorsAndMissingAttributes(t *testing.T) {
	runContract(t, "DEC08")
}

func TestContract_DEC09_OnMissingAttributeDecidesUnknownConditions(t *testing.T) {
	runContract(t, "DEC09")
}

func TestContract_DEC10_GuardsAndResourceTargets(t *testing.T) {
	runContract(t, "DEC10")
}

func TestContract_DEC11_UnresolvedParentsFailClosed(t *testing.T) {
	runContract(t, "DEC11")
}

func TestContract_DEC12_RolesNestedGroupsAndAccessGroupsExpand(t *testing.T) {
	runContract(t, "DEC12")
}

func TestContract_DEC13_ExplanationsNameTheRules(t *testing.T) {
	runContract(t, "DEC13")
}

// runContract asks the questions of every fixture that checks the contract and reports each answer that differs from the
// expectation, and each question on which the permission list, or POST /check, disagrees with the evaluations. A contract no
// fixture checks yet is skipped: its table is still being written.
func runContract(t *testing.T, contract string) {
	fixtures, err := policyfix.Decisions()
	if err != nil {
		t.Fatalf("read the fixtures: %v", err)
	}
	mine := policyfix.For(fixtures, contract)
	if len(mine) == 0 {
		t.Skipf("no fixture checks %s yet (core/testsupport/policyfix/testdata/decisions)", contract)
	}
	for _, f := range mine {
		t.Run(f.Name, func(t *testing.T) {
			got := answersOf(t, f)
			for i := range f.Cases {
				c := &f.Cases[i]
				t.Run(c.Name, func(t *testing.T) {
					for j := range c.Questions {
						q := &c.Questions[j]
						a := got[i].answers[j]
						for _, mismatch := range contracts.Compare(q, a) {
							t.Errorf("question %d, %s: %s\n\tthe answer: %+v", j+1, describe(f, c, q), mismatch, a)
						}
					}
					for _, d := range got[i].listDisagrees {
						t.Errorf("GET /permission against the evaluations: %s", d)
					}
					for _, d := range got[i].secondOpinion {
						t.Errorf("POST /check against the evaluations: %s", d)
					}
				})
			}
		})
	}
}

// caseAnswers is what a case of a fixture got: the answers of the evaluations to its questions, the questions on which its
// permission list disagrees with them (contracts.Loaded.CrossCheck), and those on which POST /check does
// (contracts.Loaded.SecondOpinion).
type caseAnswers struct {
	answers       []contracts.Answer
	listDisagrees []string
	secondOpinion []string
}

// answered keeps the answers of the fixtures loaded so far, by fixture name: a fixture that checks several contracts is
// loaded and asked once. The tests of the package do not run in parallel (the clusters share one Redis).
var (
	answeredMu sync.Mutex
	answered   = map[string][]caseAnswers{}
)

// answersOf returns what every case of the fixture got, case by case.
func answersOf(t *testing.T, f *policyfix.Fixture) []caseAnswers {
	t.Helper()
	answeredMu.Lock()
	defer answeredMu.Unlock()
	if out, ok := answered[f.Name]; ok {
		return out
	}
	l := contracts.Load(t, infra, f)
	out := make([]caseAnswers, len(f.Cases))
	for i := range f.Cases {
		c := &f.Cases[i]
		out[i].answers = l.Ask(c)
		out[i].listDisagrees = l.CrossCheck(c, out[i].answers)
		out[i].secondOpinion = l.SecondOpinion(c, out[i].answers)
	}
	answered[f.Name] = out
	return out
}

// describe names a question for a failure: what was asked, by whom, of which service and when.
func describe(f *policyfix.Fixture, c *policyfix.Case, q *policyfix.Question) string {
	on := q.Type + " as a whole"
	if q.Resource != "" {
		on = q.Type + "/" + q.Resource
	}
	return fmt.Sprintf("%s on %s by %s of %s at %s", q.Action, on, c.Subject, f.ServiceOf(c), f.NowOf(c).Format("2006-01-02T15:04:05Z07:00"))
}
