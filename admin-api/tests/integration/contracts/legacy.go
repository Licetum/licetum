//go:build integration

package contracts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// maxQuestions is the most questions POST /service/{id}/check takes at once.
const maxQuestions = 100

// The second opinions of the runner. Until the check API drops the routes that AuthZEN replaces, the questions of a case are
// also asked through them, and any answer that differs from the one the AuthZEN API gave fails the test: that is what shows
// that moving the oracle from POST /service/{slug}/check and GET /service/{slug}/permission to the evaluations and the
// searches lost nothing, in particular the checks that only the permission list can make (the mode of a type, a resource
// listed twice) and that the searches state more weakly. This file goes with those routes, and so do the calls of
// SecondOpinion in the tests.

// SecondOpinion asks the case through POST /service/{slug}/check?consistency=higher with explain and GET
// /service/{slug}/permission?consistency=higher (the case's subject with its claims in the token, its request attributes in
// the request, at its time) and returns every way the two differ from the answers that Ask gave, in words; none when they
// agree. The explanation of each answer is compared as Compare does. A request that fails fails the test.
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
	return append(out, crossCheckLegacy(l.f, c, answers, l.permissionList(c))...)
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

// permissionList asks the permission list of a case: GET /service/{slug}/permission?consistency=higher, with the request
// attributes of the case in the context parameter.
func (l *Loaded) permissionList(c *policyfix.Case) engine.PermissionResult {
	l.t.Helper()
	l.clock.Set(l.f.NowOf(c))
	query := url.Values{"consistency": {"higher"}}
	if c.Context != nil {
		raw, err := json.Marshal(c.Context)
		if err != nil {
			l.t.Fatalf("fixture %s, case %q: encode the context: %v", l.f.Name, c.Name, err)
		}
		query.Set("context", string(raw))
	}
	path := "/service/" + url.PathEscape(l.f.ServiceOf(c)) + "/permission?" + query.Encode()
	res := l.askOnce(authzenreq.Request{Method: http.MethodGet, Path: path}, l.tokenOf(c))
	var list engine.PermissionResult
	if err := json.Unmarshal(res.Body, &list); err != nil {
		l.t.Fatalf("fixture %s, case %q: the permission list does not decode: %v %s", l.f.Name, c.Name, err, res.Body)
	}
	return list
}

// crossCheckLegacy compares the permission list of a case with the answers to its questions. For a question about a
// resource of a sync type, the answer allows the action exactly when the list names the resource under its type with the
// action among its levels; for a question about a not_sync type as a whole, exactly when the action is among the levels
// the list gives the type. A resource the answer is resource_not_found for must not be named by the list (a resource switched
// off, or left behind by a deleted synchronizer, is in neither). A question about a sync type as a whole is not compared (the
// list names resources only), nor one the answer is type_not_found (the list has nothing to say about it).
func crossCheckLegacy(f *policyfix.Fixture, c *policyfix.Case, answers []Answer, list engine.PermissionResult) []string {
	var out []string
	for i := range c.Questions {
		q, a := &c.Questions[i], answers[i]
		if a.Decision == policyfix.DecisionTypeNotFound {
			continue
		}
		if a.Decision == policyfix.DecisionResourceNotFound {
			if q.Resource != "" && slices.ContainsFunc(list[q.Type].Resources, func(r model.ResourceAccess) bool { return r.ID == q.Resource }) {
				out = append(out, fmt.Sprintf("question %d, %s on %s/%s: the check does not find it, the permission list names it",
					i+1, q.Action, q.Type, q.Resource))
			}
			continue
		}
		// A resource of a sync type, or a not_sync type as a whole; a resource of a not_sync type is resource_not_found.
		if typ, ok := f.TypeByName(q.Type); !ok || typ.Sync != (q.Resource != "") {
			continue
		}
		entry, listed := list[q.Type]
		on, wantMode := q.Type+" as a whole", model.TypeModeNotSync
		if q.Resource != "" {
			on, wantMode = q.Type+"/"+q.Resource, model.TypeModeSync
		}
		prefix := fmt.Sprintf("question %d, %s on %s: ", i+1, q.Action, on)
		if listed && entry.Type != wantMode {
			out = append(out, fmt.Sprintf("%sthe permission list has %s as %s, not %s", prefix, q.Type, entry.Type, wantMode))
			continue
		}

		var levels []string
		found := listed
		if q.Resource != "" {
			found = false
			for _, r := range entry.Resources {
				if r.ID != q.Resource {
					continue
				}
				if found {
					out = append(out, fmt.Sprintf("%sthe permission list names %s more than once", prefix, on))
				}
				found, levels = true, append(levels, r.Access...)
			}
		} else {
			levels = entry.Access
		}
		if slices.Contains(levels, q.Action) == a.Allowed {
			continue
		}
		listSays := "does not name " + on
		if found {
			listSays = fmt.Sprintf("gives %s the levels %q", on, sortedUnique(levels))
		}
		out = append(out, fmt.Sprintf("%sthe check says allowed=%t (%s), the permission list %s", prefix, a.Allowed, a.Decision, listSays))
	}
	return out
}
