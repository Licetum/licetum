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
