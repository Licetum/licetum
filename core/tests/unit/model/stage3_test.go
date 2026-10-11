package model_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// The names of access groups and of request attributes are plain identifiers: no spaces, no "@" prefix, no
// punctuation a reference could not carry.
func TestNamesOfAccessGroupsAndContextAttributesAreIdentifiers(t *testing.T) {
	for name, want := range map[string]bool{"editor": true, "read_only-2": true, "": false, "has space": false,
		"@editor": false, strings.Repeat("a", 64): true, strings.Repeat("a", 65): false} {
		if got := model.ValidAccessGroupName(name); got != want {
			t.Errorf("ValidAccessGroupName(%q) = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{"ip": true, "geo.country": true, "edge:region": true, "bad name": false, "": false} {
		if got := model.ValidContextName(name); got != want {
			t.Errorf("ValidContextName(%q) = %v, want %v", name, got, want)
		}
	}
	tooMany := model.RequestContext{}
	for i := 0; i <= 64; i++ {
		tooMany[strings.Repeat("k", 1)+string(rune('a'+i%26))+strings.Repeat("x", i/26)] = i
	}
	if err := tooMany.Validate(); err == nil {
		t.Fatal("65 request attributes are accepted")
	}
	if err := (model.RequestContext{"ip": "10.0.0.1"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// An access group lists levels only: neither the all-levels wildcard nor a reference to another group.
func TestAccessGroupLevelsAreNotWildcardsOrReferences(t *testing.T) {
	for _, levels := range [][]string{{}, {"*"}, {"@other"}, {"read", ""}} {
		in := &model.AccessGroupInput{Name: "editor", Levels: levels}
		if err := in.Validate(); err == nil {
			t.Errorf("levels %v accepted", levels)
		}
	}
	if err := (&model.AccessGroupInput{Name: "editor", Levels: []string{"read", "write"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// The conditions of the rule language: a missing attribute applies only to a denial, the clock reads hour and weekday,
// the networks are CIDR, the numbers are numbers, and a resource condition takes no source.
func TestRuleConditionsAndTheirTargetsAreChecked(t *testing.T) {
	typeID := uuid.New()
	// No subject: a rule that names one target (a subject, a group or a predicate) is checked by the cases below.
	base := func() *model.AssignmentRuleInput {
		return &model.AssignmentRuleInput{TypeResourceID: &typeID, Access: []string{"read"}}
	}
	deny := "apply"
	cases := map[string]func(in *model.AssignmentRuleInput){
		"apply on an allow rule": func(in *model.AssignmentRuleInput) { in.OnMissingAttribute = &deny },
		"a clock attribute other than hour and weekday": func(in *model.AssignmentRuleInput) {
			in.Predicate = []model.PredicateCondition{{Attr: "minute", Op: model.OpEq, Value: model.PredicateValue{Str: "1"}, Source: model.SourceNow}}
		},
		"a regular expression on the clock": func(in *model.AssignmentRuleInput) {
			in.Predicate = []model.PredicateCondition{{Attr: "hour", Op: model.OpRegex, Value: model.PredicateValue{Str: "1."}, Source: model.SourceNow}}
		},
		"a non-number compared as a number": func(in *model.AssignmentRuleInput) {
			in.Predicate = []model.PredicateCondition{{Attr: "level", Op: model.OpGt, Value: model.PredicateValue{Str: "high"}}}
		},
		"a network that is not CIDR": func(in *model.AssignmentRuleInput) {
			in.Predicate = []model.PredicateCondition{{Attr: "ip", Op: model.OpInCIDR, Value: model.PredicateValue{Str: "10.0.0.1"}}}
		},
		"a source on a resource condition": func(in *model.AssignmentRuleInput) {
			in.ResourceWhere = []model.PredicateCondition{{Attr: "owner", Op: model.OpEq, Value: model.PredicateValue{Str: "a"}, Source: model.SourceClaim}}
		},
		"a reserved attribute in resource_where": func(in *model.AssignmentRuleInput) {
			in.ResourceWhere = []model.PredicateCondition{{Attr: "id", Op: model.OpEq, Value: model.PredicateValue{Str: "a"}}}
		},
		"resource_where with a resource id": func(in *model.AssignmentRuleInput) {
			resource := "P1"
			in.ResourceID = &resource
			in.ResourceWhere = []model.PredicateCondition{{Attr: "owner", Op: model.OpEq, Value: model.PredicateValue{Str: "a"}}}
		},
	}
	for name, mutate := range cases {
		in := base()
		mutate(in)
		if err := in.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// A denial may apply on a missing attribute; the operators that compare numbers and networks are accepted.
	denial := base()
	denial.Effect = model.EffectDeny
	denial.OnMissingAttribute = &deny
	denial.Predicate = []model.PredicateCondition{
		{Attr: "ip", Source: model.SourceContext, Op: model.OpInCIDR, Value: model.PredicateValue{List: []string{"10.0.0.0/8"}, IsList: true}},
		{Attr: "hour", Source: model.SourceNow, Op: model.OpGe, Value: model.PredicateValue{Str: "9"}},
		{Attr: "level", Op: model.OpLe, Value: model.PredicateValue{Str: "3.5"}},
	}
	if err := denial.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A permission targets one place: a resource id, a resource found by a claim, or the resources matching conditions.
func TestAPermissionTargetsOnePlace(t *testing.T) {
	typeID := uuid.New()
	subject := "alice"
	resource := "P1"
	in := &model.AssignmentRuleInput{TypeResourceID: &typeID, Access: []string{"read"}, Subject: &subject,
		ResourceID: &resource, ResourceWhere: []model.PredicateCondition{{Attr: "owner", Op: model.OpEq, Value: model.PredicateValue{Str: "a"}}}}
	if err := in.Validate(); err == nil {
		t.Fatal("a resource id and resource_where together are accepted")
	}
}

// A request carries a bounded context: a string of at most 1024 characters, and a list of at most 64 items.
func TestContextValuesAreBounded(t *testing.T) {
	if err := (model.RequestContext{"path": strings.Repeat("a", 1025)}).Validate(); err == nil {
		t.Error("a string of 1025 characters is accepted")
	}
	long := make([]any, 65)
	for i := range long {
		long[i] = "x"
	}
	if err := (model.RequestContext{"tags": long}).Validate(); err == nil {
		t.Error("a list of 65 items is accepted")
	}
	if err := (model.RequestContext{"path": strings.Repeat("a", 1024), "tags": long[:64], "n": 7}).Validate(); err != nil {
		t.Fatal(err)
	}
}
