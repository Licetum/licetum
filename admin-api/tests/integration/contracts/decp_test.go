//go:build integration

package contracts_test

// DECP of contracts.yaml: on generated policies and resource graphs (core/testsupport/policygen), the engine of the check
// API and the reference evaluator (core/testsupport/refeval, which shares no code with the engine) give the same answers
// and explanations, and the permission list of each case agrees with the evaluations. Each seed is loaded into a
// cluster of its own and every question of every case is asked; a failure names the seed, and LICETUM_DECP_SEEDS=<seed>
// runs that one again. While the check API has POST /check, which the evaluations replace, each case is asked through it
// as well (contracts.Loaded.SecondOpinion) and it must agree with the evaluations.

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/licetum/licetum/admin-api/tests/integration/contracts"
	"github.com/licetum/licetum/core/testsupport/policyfix"
	"github.com/licetum/licetum/core/testsupport/policygen"
	"github.com/licetum/licetum/core/testsupport/refeval"
)

// decpSeeds is the range a pull request checks; the nightly suite sets LICETUM_DECP_SEEDS to a wider one.
const decpSeeds = "1-20"

func TestContract_DECP_TheEngineAnswersLikeTheReferenceOnGeneratedPolicies(t *testing.T) {
	first, last := seedRange(t)
	for seed := first; seed <= last; seed++ {
		f := policygen.Generate(seed)
		t.Run(f.Name, func(t *testing.T) {
			l := contracts.Load(t, infra, f)
			ref := refeval.New(f)
			for i := range f.Cases {
				c := &f.Cases[i]
				answers := l.Ask(c)
				for j := range c.Questions {
					q := &c.Questions[j]
					want := expectationOf(q, ref.Decide(c, q))
					for _, mismatch := range contracts.Compare(&want, answers[j]) {
						t.Errorf("seed %d, case %q, question %d, %s: %s (engine) against the reference\n\tthe engine: %+v",
							seed, c.Name, j+1, describe(f, c, q), mismatch, answers[j])
					}
				}
				for _, d := range l.CrossCheck(c, answers) {
					t.Errorf("seed %d, case %q, GET /permission against the evaluations: %s", seed, c.Name, d)
				}
				for _, d := range l.SecondOpinion(c, answers) {
					t.Errorf("seed %d, case %q, POST /check against the evaluations: %s", seed, c.Name, d)
				}
			}
			if t.Failed() {
				t.Logf("reproduce: LICETUM_DECP_SEEDS=%d go test -tags integration -run 'TestContract_DECP' ./tests/integration/contracts/", seed)
			}
		})
	}
}

// expectationOf states the answer of the reference evaluator as what a question expects: the decision and the whole
// explanation are compared, an empty list included.
func expectationOf(q *policyfix.Question, a refeval.Answer) policyfix.Question {
	want := *q
	want.Allowed, want.Decision = a.Allowed, a.Decision
	want.GrantedBy = append([]string{}, a.GrantedBy...)
	want.Denials = []policyfix.ExpectedDenial{}
	for _, d := range a.Denials {
		want.Denials = append(want.Denials, policyfix.ExpectedDenial{Rule: d.Rule, LiftedBy: d.LiftedBy, UnresolvedParent: d.UnresolvedParent})
	}
	return want
}

// seedRange reads LICETUM_DECP_SEEDS: one seed, or "first-last".
func seedRange(t *testing.T) (first, last int64) {
	spec := os.Getenv("LICETUM_DECP_SEEDS")
	if spec == "" {
		spec = decpSeeds
	}
	from, to, ranged := strings.Cut(spec, "-")
	if !ranged {
		to = from
	}
	var err error
	if first, err = strconv.ParseInt(from, 10, 64); err == nil {
		last, err = strconv.ParseInt(to, 10, 64)
	}
	if err != nil || last < first {
		t.Fatalf("LICETUM_DECP_SEEDS=%q: want a seed or first-last", spec)
	}
	return first, last
}
