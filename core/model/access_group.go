package model

import (
	"strings"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// AccessGroupInput is the body of POST /type_resource/{id}/access_group and of PATCH on one of its groups.
type AccessGroupInput struct {
	_      struct{} `additionalProperties:"true"`
	Name   string   `json:"name" minLength:"1" maxLength:"64" doc:"The name: a rule or a role item of this resource type refers to the group as @name. Letters, digits, _ and -" example:"editor"`
	Levels []string `json:"levels" minItems:"1" nullable:"false" doc:"The access levels the group stands for. A level is neither * nor a group reference" example:"[\"read\",\"write\"]"`
}

// Validate checks the name and the levels of a group.
func (g *AccessGroupInput) Validate() error {
	if !ValidAccessGroupName(g.Name) {
		return apperr.Unprocessable(i18n.AccessGroupNameInvalid, g.Name)
	}
	if len(g.Levels) == 0 {
		return apperr.Unprocessable(i18n.AccessGroupEmpty)
	}
	for _, level := range g.Levels {
		if level == "" || level == AllLevels || strings.HasPrefix(level, AccessGroupPrefix) {
			return apperr.Unprocessable(i18n.AccessLevelInvalid, "levels")
		}
	}
	return nil
}

// ValidAccessGroupName reports whether a name may name an access group: letters, digits, '_' and '-', up to 64
// characters.
func ValidAccessGroupName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		ok := c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			return false
		}
	}
	return true
}

// AccessGroup is a named set of access levels of one resource type: a rule or a role item lists @name in its access
// and gets the levels of the group.
type AccessGroup struct {
	ID             uuid.UUID `json:"id" format:"uuid" doc:"Access group id"`
	TypeResourceID uuid.UUID `json:"type_resource_id" format:"uuid" doc:"Id of the resource type the group belongs to"`
	Name           string    `json:"name" doc:"The name; a rule or an item refers to it as @name"`
	Levels         []string  `json:"levels" doc:"The access levels the group stands for"`
}
