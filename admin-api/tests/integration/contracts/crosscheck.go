//go:build integration

package contracts

import (
	"fmt"
	"slices"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

const (
	// searchPage is how many results a search is asked for at a time: the most the API gives.
	searchPage = 1000
	// searchPages bounds how many pages one search is read through, so that an API that never ends a list fails the test.
	searchPages = 1000
)

// searchKind is what a search of a case asks.
type searchKind int

const (
	// resourcesOf lists the resources of a type with resources that the subject may do an action on.
	resourcesOf searchKind = iota
	// levelsOf lists the levels the subject has on a type without resources, as a whole.
	levelsOf
	// noResourcesOf lists the resources of a type without resources that the subject may do an action on: there are none.
	noResourcesOf
)

// search is one search a case needs. The action is the one asked for by a resource search (a levelsOf search has none).
type search struct {
	kind             searchKind
	typeName, action string
}

func (s search) request(service, subject string, opts ...authzenreq.Option) authzenreq.Request {
	if s.kind == levelsOf {
		return authzenreq.SearchAction(service, subject, authzenreq.Whole(s.typeName), opts...)
	}
	return authzenreq.SearchResource(service, subject, s.typeName, s.action, opts...)
}

func (s search) decode(status int, body []byte) (listed []string, next string, err error) {
	if s.kind == levelsOf {
		return authzenreq.Actions(status, body)
	}
	return authzenreq.ResourceIDs(status, body)
}

// searched is what the searches of a case listed, for crossCheck to compare with the evaluations.
type searched struct {
	// resources are the ids that the resource search of a type with resources listed for an action, in the order the API
	// gave them.
	resources map[search][]string
	// levels are the levels that the action search gave a type without resources, asked as a whole.
	levels map[string][]string
	// unexpected are the ids that the resource search of a type without resources listed, by type: there should be none.
	unexpected map[string][]string
}

// CrossCheck asks the searches of a case — POST /service/{slug}/access/v1/search/resource and …/search/action with
// consistency=higher, as the case's subject with its claims in the token, its request attributes in the context, at its time —
// and compares what they list with the answers the evaluations gave to the questions of the same case (Ask). It returns every
// question on which the two paths disagree, in words; none when they agree. A request that fails, or a list made without the
// data of some type, fails the test. The searches asked are the ones the questions need, each once: a resource search for each
// pair of a type with resources and an action asked about one of its resources (whether the evaluation finds it or not), and
// for each type without resources asked as a whole an action search and one resource search. See crossCheck for what is
// compared.
func (l *Loaded) CrossCheck(c *policyfix.Case, answers []Answer) []string {
	l.t.Helper()
	if len(answers) != len(c.Questions) {
		l.t.Fatalf("fixture %s, case %q: CrossCheck takes the %d answers of the case, got %d", l.f.Name, c.Name, len(c.Questions), len(answers))
	}
	l.clock.Set(l.f.NowOf(c))

	found := searched{resources: map[search][]string{}, levels: map[string][]string{}, unexpected: map[string][]string{}}
	for _, s := range searchesFor(l.f, c, answers) {
		listed := l.listed(c, s)
		switch s.kind {
		case resourcesOf:
			found.resources[s] = listed
		case levelsOf:
			found.levels[s.typeName] = listed
		case noResourcesOf:
			found.unexpected[s.typeName] = listed
		}
	}
	return crossCheck(l.f, c, answers, found)
}

// listed is everything the search lists for the case, read page by page from the first to the last.
func (l *Loaded) listed(c *policyfix.Case, s search) []string {
	l.t.Helper()
	token := l.tokenOf(c)
	all := []string{}
	page := ""
	for range searchPages {
		req := s.request(l.f.ServiceOf(c), c.Subject, authzenreq.Fresh(), authzenreq.WithContext(c.Context), authzenreq.Page(searchPage, page))
		res := l.askOnce(req, token)
		ids, next, err := s.decode(res.Status, res.Body)
		if err != nil {
			l.t.Fatalf("fixture %s, case %q: %s %s: %v", l.f.Name, c.Name, req.Method, req.Path, err)
		}
		all = append(all, ids...)
		if next == "" {
			return all
		}
		page = next
	}
	l.t.Fatalf("fixture %s, case %q: the search for %s (%s) has more than %d pages", l.f.Name, c.Name, s.typeName, s.action, searchPages)
	return nil
}

// searchesFor are the searches that the questions of a case need, each once, in the order the questions first need them.
func searchesFor(f *policyfix.Fixture, c *policyfix.Case, answers []Answer) []search {
	var out []search
	add := func(s search) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	noResources := map[string]bool{}
	for i := range c.Questions {
		q := &c.Questions[i]
		switch {
		case absentFromSearch(f, q, answers[i]):
			add(search{kind: resourcesOf, typeName: q.Type, action: q.Action})
		case !compared(f, q, answers[i]):
		case q.Resource != "":
			add(search{kind: resourcesOf, typeName: q.Type, action: q.Action})
		default:
			add(search{kind: levelsOf, typeName: q.Type})
			if !noResources[q.Type] {
				// One resource search is enough to see that the API lists no resource for a type that has none.
				noResources[q.Type] = true
				add(search{kind: noResourcesOf, typeName: q.Type, action: q.Action})
			}
		}
	}
	return out
}

// compared says whether the searches have something to say about a question: a resource of a type with resources, or a type
// without resources as a whole. A resource of a type without resources is resource_not_found, a type with resources asked as
// a whole is about no resource the searches list, and a question the evaluation answered type_not_found or
// resource_not_found has nothing for the searches to say about it (absentFromSearch is the one exception).
func compared(f *policyfix.Fixture, q *policyfix.Question, a Answer) bool {
	if a.Decision == policyfix.DecisionTypeNotFound || a.Decision == policyfix.DecisionResourceNotFound {
		return false
	}
	typ, ok := f.TypeByName(q.Type)
	return ok && typ.Sync == (q.Resource != "")
}

// absentFromSearch says whether a question asks about a resource of a type with resources that the evaluation does not find.
// The resource search of its type and action must not list such a resource: a resource switched off, or left behind by a
// deleted synchronizer, is in neither.
func absentFromSearch(f *policyfix.Fixture, q *policyfix.Question, a Answer) bool {
	if a.Decision != policyfix.DecisionResourceNotFound || q.Resource == "" {
		return false
	}
	typ, ok := f.TypeByName(q.Type)
	return ok && typ.Sync
}

// crossCheck compares what the searches of a case listed with the evaluations of its questions. For a question about a
// resource of a type with resources, the evaluation allows the action exactly when the resource search for the type and the
// action lists the resource; for a question about a type without resources as a whole, exactly when the action search gives
// the type the action among its levels. The resource search of a type without resources must list nothing. A resource of a
// type with resources that the evaluation answers resource_not_found must not be listed by the resource search of its type and
// action. A question about a type with resources as a whole is not compared (the searches list resources), nor one the
// evaluation answered type_not_found (the searches have nothing to say about it). A resource that one search lists twice is
// reported; the searches of other actions are other lists, so a resource that is named under two actions is not looked for.
func crossCheck(f *policyfix.Fixture, c *policyfix.Case, answers []Answer, found searched) []string {
	var out []string
	for i := range c.Questions {
		q, a := &c.Questions[i], answers[i]
		if absentFromSearch(f, q, a) {
			if slices.Contains(found.resources[search{kind: resourcesOf, typeName: q.Type, action: q.Action}], q.Resource) {
				out = append(out, fmt.Sprintf("question %d, %s on %s/%s: the evaluation says resource_not_found, the search lists it",
					i+1, q.Action, q.Type, q.Resource))
			}
			continue
		}
		if !compared(f, q, a) {
			continue
		}
		on, says, listed := q.Type+" as a whole", "", false
		if q.Resource != "" {
			on = q.Type + "/" + q.Resource
			ids := found.resources[search{kind: resourcesOf, typeName: q.Type, action: q.Action}]
			prefix := fmt.Sprintf("question %d, %s on %s: ", i+1, q.Action, on)
			if n := count(ids, q.Resource); n > 1 {
				out = append(out, fmt.Sprintf("%sthe resource search lists %s %d times", prefix, on, n))
			}
			listed, says = slices.Contains(ids, q.Resource), "does not list "+on
			if listed {
				says = "lists " + on
			}
		} else {
			levels := found.levels[q.Type]
			listed, says = slices.Contains(levels, q.Action), fmt.Sprintf("gives %s the levels %q", on, sortedUnique(levels))
		}
		if listed != a.Allowed {
			out = append(out, fmt.Sprintf("question %d, %s on %s: the evaluation says allowed=%t (%s), the search %s", i+1, q.Action, on, a.Allowed, a.Decision, says))
		}
	}
	types := make([]string, 0, len(found.unexpected))
	for typeName, ids := range found.unexpected {
		if len(ids) > 0 {
			types = append(types, typeName)
		}
	}
	slices.Sort(types)
	for _, typeName := range types {
		out = append(out, fmt.Sprintf("the resource search lists %q for %s, which has no resources", found.unexpected[typeName], typeName))
	}
	return out
}

// count is how many times the id is in the list.
func count(list []string, id string) int {
	n := 0
	for _, x := range list {
		if x == id {
			n++
		}
	}
	return n
}
