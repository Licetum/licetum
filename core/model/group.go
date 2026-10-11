package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// GroupSubjectPrefix: a group is a subject too — AssignmentRule.subject = "group:<uuid>" grants permissions
// to the group through the same mechanism as to a regular user.
const GroupSubjectPrefix = "group:"

func GroupSubject(groupID uuid.UUID) string {
	return GroupSubjectPrefix + groupID.String()
}

type GroupInput struct {
	_    struct{} `additionalProperties:"true"`
	Name string   `json:"name" doc:"Group name (globally unique)" example:"sales"`
}

type Group struct {
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Group id"`
	Name string    `json:"name" doc:"Group name"`
}

// GroupMemberInput: either predicate (AND conditions over claims) or member_group_id
// (a group nested as a whole into another group), but not both.
type GroupMemberInput struct {
	_             struct{}             `additionalProperties:"true"`
	MemberGroupID *uuid.UUID           `json:"member_group_id" format:"uuid" required:"false" doc:"Nest another group as a whole: its members become members of this group (mutually exclusive with predicate)"`
	Predicate     []PredicateCondition `json:"predicate" required:"false" doc:"AND conditions over subject claims; an empty list matches any subject"`
	Enabled       *bool                `json:"enabled" required:"false" default:"true" doc:"Whether the rule is active"`
	ValidFrom     *time.Time           `json:"valid_from" required:"false" nullable:"true" doc:"The membership starts at this moment (RFC 3339); omitted or null — right away"`
	ExpiresAt     *time.Time           `json:"expires_at" required:"false" nullable:"true" doc:"The membership ends at this moment (RFC 3339); omitted or null — never"`
}

func (m *GroupMemberInput) Validate() error {
	if err := validateValidity(m.ValidFrom, m.ExpiresAt); err != nil {
		return err
	}
	if m.Predicate == nil {
		m.Predicate = []PredicateCondition{}
	}
	if m.Enabled == nil {
		t := true
		m.Enabled = &t
	}
	if err := validatePredicate(m.Predicate); err != nil {
		return err
	}
	if m.MemberGroupID != nil && len(m.Predicate) > 0 {
		return apperr.Unprocessable(i18n.GroupMemberExclusive)
	}
	return nil
}

type GroupMember struct {
	ID            uuid.UUID            `json:"id" format:"uuid" doc:"Rule id"`
	GroupID       uuid.UUID            `json:"group_id" format:"uuid" doc:"Id of the group the subject or group belongs to"`
	MemberGroupID *uuid.UUID           `json:"member_group_id" format:"uuid" doc:"Nested group"`
	Predicate     []PredicateCondition `json:"predicate" doc:"AND conditions over subject claims"`
	Enabled       bool                 `json:"enabled" doc:"Whether the rule is active"`
	ValidFrom     *time.Time           `json:"valid_from" nullable:"true" doc:"The membership starts at this moment; null — right away"`
	ExpiresAt     *time.Time           `json:"expires_at" nullable:"true" doc:"The membership ends at this moment; null — never"`
}
