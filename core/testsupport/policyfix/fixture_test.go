package policyfix

import (
	"strings"
	"testing"
)

func TestTheDecisionFixturesParse(t *testing.T) {
	fixtures, err := Decisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range fixtures {
		if len(f.Contracts) == 0 {
			t.Errorf("%s names no contract", f.Name)
		}
	}
}

func TestValidateNamesWhatIsUndefined(t *testing.T) {
	_, err := Parse("bad", []byte(`
contracts: [DEC01]
services: [s]
types: [{name: a, service: s, sync: false}]
resources: [{type: a, id: X}]
rules:
  - {name: r, subject: u, type: nope, access: [read]}
  - {name: r, subject: u, member_of: g, type: a, access: [read]}
cases: [{name: c, subject: u, questions: [{type: a, action: read, allowed: false, granted_by: [missing]}]}]
`))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"has no synchronizer", `unknown type "nope"`, `rule "r" twice`, `unknown group "g"`, `unknown rule "missing"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestParseRefusesUnknownFields(t *testing.T) {
	_, err := Parse("typo", []byte(`
contracts: [DEC01]
services: [s]
types: [{name: a, service: s, sync: true}]
cases: [{name: c, subject: u, questions: [{type: a, action: read, permitted: true}]}]
`))
	if err == nil || !strings.Contains(err.Error(), "permitted") {
		t.Fatalf("an unknown field must be refused: %v", err)
	}
}

// Before describes the state at the first synchronization: what it may change, and what it may name.
func TestValidateBefore(t *testing.T) {
	const graph = `
contracts: [DEC01]
services: [s]
types:
  - {name: top, service: s, sync: true}
  - {name: leaf, service: s, sync: true}
  - {name: gone, service: s, sync: false}
resources: [{type: top, id: T1}, {type: leaf, id: L1}]
access_groups: [{type: leaf, name: editor, levels: [read]}]
cases: [{name: c, subject: u, questions: [{type: leaf, resource: L1, action: read, allowed: false}]}]
`
	f, err := Parse("good", []byte(graph+`
rules: [{name: dead, subject: u, type: gone, resource: G1, access: [read]}]
before:
  types:
    - {name: middle, service: s, sync: true, parents: [{type: top}]}
    - {name: leaf, service: s, sync: true, parents: [{type: middle}]}
    - {name: gone, service: s, sync: true}
  resources:
    - {type: middle, id: M1, parents: [{type: top, id: T1}]}
    - {type: leaf, id: L1, parents: [{type: middle, id: M1}]}
    - {type: leaf, id: L2}
    - {type: gone, id: G1}
  access_groups: [{type: leaf, name: editor, levels: [read, write]}]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.DeletedTypes(); len(got) != 1 || got[0] != "middle" {
		t.Errorf("deleted types: %v", got)
	}
	if got := f.ResourcesAtFirstSync(); len(got) != 5 || len(got[1].Parents) != 1 {
		t.Errorf("resources at the first synchronization: %+v", got)
	}

	_, err = Parse("bad", []byte(graph+`
rules: [{name: point, subject: u, type: gone, resource: G1, access: [read]}]
before:
  types:
    - {name: top, service: s, sync: false}
    - {name: leaf, service: s, sync: true}
  resources:
    - {type: nowhere, id: X}
    - {type: leaf, id: L2, parents: [{type: top, id: T1}]}
  access_groups: [{type: leaf, name: ghost, levels: [read]}]
`))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		`type "top": a synchronizer added afterwards`, `type "leaf": it does not differ`, "resource nowhere/X: unknown type",
		`"top" was not a parent type of "leaf"`, "access group leaf/ghost", `type "gone" has no synchronizer`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}
