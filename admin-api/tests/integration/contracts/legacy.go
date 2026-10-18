//go:build integration

package contracts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// maxQuestions is the most questions POST /service/{id}/check takes at once.
const maxQuestions = 100

// The second opinion of the runner. Until the check API drops the route that the evaluations replace, the questions of a case
// are also asked through it, and any answer that differs from the one the AuthZEN API gave fails the test: that is what shows
// that moving the oracle from POST /service/{slug}/check to the evaluations lost nothing. This file goes with that route,
// and so do the calls of SecondOpinion in the tests.

// SecondOpinion asks the case through POST /service/{slug}/check?consistency=higher with explain (the case's subject with its
// claims in the token, its request attributes in the body, at its time) and returns every way its answers differ from the
// answers that Ask gave, in words; none when they agree. The explanation of each answer is compared as Compare does. A
// request that fails fails the test.
func (l *Loaded) SecondOpinion(c *policyfix.Case, answers []Answer) []string {
	l.t.Helper()
	if len(answers) != len(c.Questions) {
		l.t.Fatalf("fixture %s, case %q: SecondOpinion takes the %d answers of the case, got %d", l.f.Name, c.Name, len(c.Questions), len(answers))
	}
	var out []string
	for i, legacy := range l.askLegacy(c) {
		for _, d := range Same(answers[i], legacy) {
			out = append(out, fmt.Sprintf("question %d: POST /check differs from the evaluation: %s", i+1, d))
		}
	}
	return out
}

// askLegacy asks the questions of a case through POST /service/{slug}/check?consistency=higher with explain, in batches of
// at most maxQuestions, as Ask did before the evaluations.
func (l *Loaded) askLegacy(c *policyfix.Case) []Answer {
	l.t.Helper()
	l.clock.Set(l.f.NowOf(c))
	token := l.tokenOf(c)
	path := "/service/" + url.PathEscape(l.f.ServiceOf(c)) + "/check?consistency=higher"

	answers := make([]Answer, 0, len(c.Questions))
	for start := 0; start < len(c.Questions); start += maxQuestions {
		batch := c.Questions[start:min(start+maxQuestions, len(c.Questions))]
		checks := make([]model.CheckRequest, len(batch))
		for i, q := range batch {
			checks[i] = model.CheckRequest{Type: q.Type, Action: q.Action}
			if q.Resource != "" {
				checks[i].ResourceID = ptr(q.Resource)
			}
		}
		res := l.askOnce(authzenreq.Request{Method: http.MethodPost, Path: path, Body: model.CheckInput{Checks: checks, Explain: true, Context: c.Context}}, token)
		var out model.CheckResponse
		if err := json.Unmarshal(res.Body, &out); err != nil || len(out.Results) != len(checks) {
			l.t.Fatalf("fixture %s, case %q: the answer does not decode into %d results: %v %s", l.f.Name, c.Name, len(checks), err, res.Body)
		}
		for _, r := range out.Results {
			answers = append(answers, l.answerOf(r))
		}
	}
	return answers
}
