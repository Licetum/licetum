//go:build integration

package contracts

import (
	"slices"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// crossCheck reports each question on which the permission list and the check disagree, in either direction, and leaves
// out the questions the list cannot answer: a sync type as a whole, an unknown type. A resource the check does not find
// must not be in the list.
func TestCrossCheckComparesTheListWithTheCheck(t *testing.T) {
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
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // listed with read: agrees
		{Allowed: false, Decision: policyfix.DecisionNoAccess},         // listed without write: agrees
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // not listed: disagrees
		{Allowed: false, Decision: policyfix.DecisionDenied},           // listed with read: disagrees
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // not_sync with list: agrees
		{Allowed: false, Decision: policyfix.DecisionNoAccess},         // not_sync with export: disagrees
		{Allowed: true, Decision: policyfix.DecisionAllowed},           // the type is listed as sync: disagrees
		{Allowed: false, Decision: policyfix.DecisionNoAccess},         // a sync type as a whole: not compared
		{Allowed: false, Decision: policyfix.DecisionResourceNotFound}, // listed: disagrees
		{Allowed: false, Decision: policyfix.DecisionTypeNotFound},     // not compared
	}
	list := engine.PermissionResult{
		"store": {Type: model.TypeModeSync, Resources: []model.ResourceAccess{
			{ID: "s1", Name: "s1", Access: []string{"read"}},
			{ID: "s3", Name: "s3", Access: []string{"read", "audit"}},
			{ID: "gone", Name: "gone", Access: []string{"read"}},
		}},
		"reports": {Type: model.TypeModeNotSync, Access: []string{"list", "export"}},
		"flags":   {Type: model.TypeModeSync},
	}
	got := crossCheck(f, c, answers, list)
	want := []string{
		"question 3, read on store/s2: the check says allowed=true (allowed), the permission list does not name store/s2",
		`question 4, read on store/s3: the check says allowed=false (denied), the permission list gives store/s3 the levels ["audit" "read"]`,
		`question 6, export on reports as a whole: the check says allowed=false (no_access), the permission list gives reports as a whole the levels ["export" "list"]`,
		"question 7, on on flags as a whole: the permission list has flags as sync, not not_sync",
		"question 9, read on store/gone: the check does not find it, the permission list names it",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("crossCheck:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}

	// A list that agrees reports nothing; a resource listed twice is a defect of the list.
	answers[2].Allowed, answers[3].Allowed, answers[5].Allowed, answers[6].Allowed = false, true, true, false
	list["flags"] = model.TypePermission{Type: model.TypeModeNotSync}
	list["store"] = model.TypePermission{Type: model.TypeModeSync, Resources: list["store"].Resources[:2]}
	if got := crossCheck(f, c, answers, list); len(got) != 0 {
		t.Fatalf("a list that agrees: %v", got)
	}
	store := list["store"]
	store.Resources = append(store.Resources, model.ResourceAccess{ID: "s1", Name: "s1", Access: []string{"write"}})
	list["store"] = store
	got = crossCheck(f, c, answers, list)
	if !slices.ContainsFunc(got, func(m string) bool { return strings.Contains(m, "names store/s1 more than once") }) {
		t.Fatalf("a resource listed twice: %v", got)
	}
}
