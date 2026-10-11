package model_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// A rule's name and description are optional texts: the surrounding spaces go, and a blank one is no name at all.
func TestRuleNameAndDescriptionAreTrimmedOrNull(t *testing.T) {
	typeID := uuid.New()
	subject := "alice"
	name := "  Sales can read  "
	blank := "   "
	in := &model.AssignmentRuleInput{
		TypeResourceID: &typeID,
		Access:         []string{"read"},
		Subject:        &subject,
		Name:           &name,
		Description:    &blank,
	}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	if in.Name == nil || *in.Name != "Sales can read" {
		t.Fatalf("the name must be trimmed: %v", in.Name)
	}
	if in.Description != nil {
		t.Fatalf("a blank description must be null: %q", *in.Description)
	}
}

func TestRuleWithoutNameIsValid(t *testing.T) {
	typeID := uuid.New()
	subject := "alice"
	in := &model.AssignmentRuleInput{TypeResourceID: &typeID, Access: []string{"read"}, Subject: &subject}
	if err := in.Validate(); err != nil {
		t.Fatalf("a rule needs no name: %v", err)
	}
	if in.Name != nil || in.Description != nil {
		t.Fatal("an absent name stays absent")
	}
}
