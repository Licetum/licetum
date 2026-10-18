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

// CrossCheck asks the permission list of a case — GET /service/{slug}/permission?consistency=higher, as the case's subject
// with its claims in the token, its request attributes in the context parameter, at its time — and compares it with the
// answers the check API gave to the questions of the same case (Ask). It returns every question on which the two paths
// disagree, in words; none when they agree. A request that fails, or a list made without the data of some type, fails the
// test. See crossCheck for what is compared.
func (l *Loaded) CrossCheck(c *policyfix.Case, answers []Answer) []string {
	l.t.Helper()
	if len(answers) != len(c.Questions) {
		l.t.Fatalf("fixture %s, case %q: CrossCheck takes the %d answers of the case, got %d", l.f.Name, c.Name, len(c.Questions), len(answers))
	}
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
	return crossCheck(l.f, c, answers, list)
}

// crossCheck compares the permission list of a case with the check answers of its questions. For a question about a
// resource of a sync type, the check allows the action exactly when the list names the resource under its type with the
// action among its levels; for a question about a not_sync type as a whole, exactly when the action is among the levels
// the list gives the type. A resource the check answered resource_not_found must not be named by the list (a resource
// switched off, or left behind by a deleted synchronizer, is in neither). A question about a sync type as a whole is not
// compared (the list names resources only), nor one the check answered type_not_found (the list has nothing to say
// about it).
func crossCheck(f *policyfix.Fixture, c *policyfix.Case, answers []Answer, list engine.PermissionResult) []string {
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
