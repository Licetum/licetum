//go:build integration

package contracts

import (
	"reflect"
	"testing"
)

// The engine lists a denial per anchor and level; the contracts name a rule once: standing when one of its denials stands,
// else lifted by every exception that lifts one, and assumed for an unresolved parent only when every deciding denial is.
func TestByRuleMergesTheDenialsOfARule(t *testing.T) {
	got := byRule([]Denial{
		{Rule: "role", Level: "read", LiftedBy: []string{"x"}},
		{Rule: "role", Level: "read", UnresolvedParent: true},
		{Rule: "twice", Level: "read", LiftedBy: []string{"y"}, UnresolvedParent: true},
		{Rule: "twice", Level: "write", LiftedBy: []string{"x"}},
		{Rule: "assumed", Level: "*", UnresolvedParent: true},
		{Rule: "assumed", Level: "read", UnresolvedParent: true, MissingAttribute: true},
	})
	want := []Denial{
		{Rule: "role", Level: "read", UnresolvedParent: true},
		{Rule: "twice", Level: "read", LiftedBy: []string{"x", "y"}},
		{Rule: "assumed", Level: "*", UnresolvedParent: true, MissingAttribute: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("byRule:\n got %+v\nwant %+v", got, want)
	}
}
