//go:build integration

package contracts

import (
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// crossCheck reports each question on which the searches and the evaluations disagree, in either direction, and leaves out
// the questions the searches cannot answer: a type with resources as a whole, an unknown type. A resource the evaluation does
// not find must not be listed.
func TestCrossCheckComparesTheSearchesWithTheEvaluations(t *testing.T) {
	f, err := policyfix.Parse("cross", []byte(`
contracts: []
services: [shop]
types:
  - {name: store, service: shop, sync: true}
  - {name: reports, service: shop, sync: false}
  - {name: flags, service: shop, sync: false}
resources:
  - {type: store, id: s1}
  - {type: store, id: s2}
  - {type: store, id: s3}
cases:
  - name: every kind of question
    subject: ann
    questions:
      - {type: store, resource: s1, action: read, allowed: true}
      - {type: store, resource: s1, action: write, allowed: false}
      - {type: store, resource: s2, action: read, allowed: true}
      - {type: store, resource: s3, action: read, allowed: false}
      - {type: reports, action: list, allowed: true}
      - {type: reports, action: export, allowed: false}
      - {type: flags, action: on, allowed: true}
      - {type: store, action: read, allowed: false}
      - {type: store, resource: gone, action: read, allowed: false}
      - {type: nowhere, action: read, allowed: false}
`))
	if err != nil {
		t.Fatal(err)
	}
	c := &f.Cases[0]
	answers := []Answer{
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // listed for read: agrees
		{Allowed: false, Decision: policyfix.DecisionNoAccess},         // not listed for write: agrees
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // not listed for read: disagrees
		{Allowed: false, Decision: policyfix.DecisionDenied},           // listed for read: disagrees
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // the levels of reports have list: agrees
		{Allowed: false, Decision: policyfix.DecisionNoAccess},         // the levels of reports have export: disagrees
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // agrees, but the search for flags lists a resource
		{Allowed: false, Decision: policyfix.DecisionNoAccess},         // a type with resources as a whole: not compared
		{Allowed: false, Decision: policyfix.DecisionResourceNotFound}, // listed: disagrees
		{Allowed: false, Decision: policyfix.DecisionTypeNotFound},     // not compared
	}
	found := searched{
		resources: map[search][]string{
			{kind: resourcesOf, typeName: "store", action: "read"}: {"gone", "s1", "s3"},
		},
		levels:     map[string][]string{"reports": {"export", "list"}, "flags": {"on"}},
		unexpected: map[string][]string{"reports": {}, "flags": {"x1"}},
	}
	got := crossCheck(f, c, answers, found)
	want := []string{
		"question 3, read on store/s2: the evaluation says allowed=true (allowed), the search does not list store/s2",
		"question 4, read on store/s3: the evaluation says allowed=false (denied), the search lists store/s3",
		`question 6, export on reports as a whole: the evaluation says allowed=false (no_access), the search gives reports as a whole the levels ["export" "list"]`,
		"question 9, read on store/gone: the evaluation says resource_not_found, the search lists it",
		`the resource search lists ["x1"] for flags, which has no resources`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("crossCheck:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}

	// Searches that agree report nothing; a resource that one search lists twice is a defect of the list.
	answers[2].Allowed, answers[3].Allowed, answers[5].Allowed = false, true, true
	found.unexpected["flags"] = nil
	found.resources[search{kind: resourcesOf, typeName: "store", action: "read"}] = []string{"s1", "s3"}
	if got := crossCheck(f, c, answers, found); len(got) != 0 {
		t.Fatalf("searches that agree: %v", got)
	}
	found.resources[search{kind: resourcesOf, typeName: "store", action: "read"}] = []string{"s1", "s1", "s3"}
	got = crossCheck(f, c, answers, found)
	if !slices.Equal(got, []string{"question 1, read on store/s1: the resource search lists store/s1 2 times"}) {
		t.Fatalf("a resource listed twice: %v", got)
	}
}

// searchesFor asks for each search once, and only for what the answers leave to the searches: a type without resources
// asked as a whole gets an action search and one resource search whatever the number of its actions asked.
func TestSearchesForAreTheOnesTheQuestionsNeed(t *testing.T) {
	f, err := policyfix.Parse("needs", []byte(`
contracts: []
services: [shop]
types:
  - {name: store, service: shop, sync: true}
  - {name: reports, service: shop, sync: false}
resources:
  - {type: store, id: s1}
cases:
  - name: questions
    subject: ann
    questions:
      - {type: store, resource: s1, action: read, allowed: true}
      - {type: store, resource: s1, action: read, allowed: true}
      - {type: store, resource: s1, action: write, allowed: false}
      - {type: reports, action: list, allowed: true}
      - {type: reports, action: export, allowed: false}
      - {type: store, action: read, allowed: false}
      - {type: store, resource: gone, action: audit, allowed: false}
      - {type: nowhere, action: read, allowed: false}
`))
	if err != nil {
		t.Fatal(err)
	}
	answers := make([]Answer, len(f.Cases[0].Questions))
	answers[6].Decision = policyfix.DecisionResourceNotFound
	answers[7].Decision = policyfix.DecisionTypeNotFound
	got := searchesFor(f, &f.Cases[0], answers)
	want := []search{
		{kind: resourcesOf, typeName: "store", action: "read"},
		{kind: resourcesOf, typeName: "store", action: "write"},
		{kind: levelsOf, typeName: "reports"},
		{kind: noResourcesOf, typeName: "reports", action: "list"},
		{kind: resourcesOf, typeName: "store", action: "audit"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("searchesFor:\n got %v\nwant %v", got, want)
	}
}
