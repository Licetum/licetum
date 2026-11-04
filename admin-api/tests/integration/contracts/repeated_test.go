//go:build integration

package contracts

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// An inline rule lists a denial once per anchor, level and assumption, and an exception once per anchor among those that
// lift it; a role assignment may list an entry once per item of the role.
func TestRepeatedNamesWhatAnInlineRuleListsTwice(t *testing.T) {
	deny, role, except := uuid.New(), uuid.New(), uuid.New()
	l := &Loaded{policy: writtenPolicy{
		ruleNames: map[uuid.UUID]string{deny: "deny-R1", role: "role-deny", except: "except-B2"},
		roleRules: map[uuid.UUID]bool{role: true},
	}}
	r1, b2 := "R1", "B2"
	at := func(id uuid.UUID, typ string, res *string) model.RuleRef {
		return model.RuleRef{RuleID: id, Anchor: model.Anchor{Type: typ, ResourceID: res}}
	}
	lift := []model.RuleRef{at(except, "branch", &b2)}
	got := l.repeated([]model.DenialRef{
		{Rule: at(deny, "rrs", &r1), Level: "read", LiftedBy: append(lift, lift...)},
		{Rule: at(deny, "rrs", &r1), Level: "read"},
		{Rule: at(deny, "rrs", &r1), Level: "read", UnresolvedParent: true},
		{Rule: at(deny, "rrs", &r1), Level: "write"},
		{Rule: at(role, "rrs", &r1), Level: "read"},
		{Rule: at(role, "rrs", &r1), Level: "read"},
	})
	want := []string{
		"the exception except-B2 at branch/B2 among those that lift deny-R1",
		"the denial deny-R1 at rrs/R1 of read",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repeated:\n got %q\nwant %q", got, want)
	}
}
