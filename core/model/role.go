package model

import (
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// A role is a named set of permissions: its items (RolePermission) grant access levels on targets of
// resource types, just like inline rules. A role is not a subject and is not a member of anything —
// it is assigned by an AssignmentRule with role_id, to the same three kinds of subjects as an inline
// rule (see AssignmentRuleInput). A group answers "who", a role answers "what".

type RoleInput struct {
	_    struct{} `additionalProperties:"true"`
	Name string   `json:"name" doc:"Role name (globally unique)" example:"sales_manager"`
}

type Role struct {
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Role id"`
	Name string    `json:"name" doc:"Role name"`
}

// RolePermissionInput is one item of a role: access levels on a target inside a resource type. The
// target is the same as for an inline rule — the whole type (no resource_id and no resource_from_claim),
// a resource, or a resource computed from a claim of the subject.
type RolePermissionInput struct {
	_                 struct{}             `additionalProperties:"true"`
	TypeResourceID    uuid.UUID            `json:"type_resource_id" format:"uuid" doc:"Id of the resource type the item applies to (of any service)"`
	ResourceID        *string              `json:"resource_id" required:"false" doc:"Resource id; null means the item covers the whole resource type (if resource_from_claim and resource_where are not set either)"`
	ResourceFromClaim *ResourceFromClaim   `json:"resource_from_claim" required:"false" doc:"Compute resource_id dynamically from a claim of the subject; mutually exclusive with resource_id and resource_where"`
	ResourceWhere     []PredicateCondition `json:"resource_where" required:"false" nullable:"true" doc:"Covers the resources of the type whose attributes (as the synchronizer reported them) match these conditions; mutually exclusive with resource_id and resource_from_claim; needs a synchronizer"`
	Access            []string             `json:"access" minItems:"1" nullable:"false" doc:"Access levels (read, write, ...) or access groups (@name, see AccessGroup). '*' is not allowed here: denying everything is possible only with an inline deny rule" example:"[\"read\",\"write\"]"`
}

func (p *RolePermissionInput) Validate() error {
	if len(p.Access) == 0 {
		return apperr.Unprocessable(i18n.AccessEmpty)
	}
	for _, level := range p.Access {
		if level == "" || level == AllLevels || level == AccessGroupPrefix {
			return apperr.Unprocessable(i18n.AccessLevelInvalid, "access")
		}
	}
	return validateTarget(p.ResourceID, p.ResourceFromClaim, p.ResourceWhere)
}

// RolePermission is an item of a role.
type RolePermission struct {
	ID                uuid.UUID            `json:"id" format:"uuid" doc:"Item id"`
	RoleID            uuid.UUID            `json:"role_id" format:"uuid" doc:"Id of the role the item belongs to"`
	TypeResourceID    uuid.UUID            `json:"type_resource_id" format:"uuid" doc:"Resource type id"`
	ResourceID        *string              `json:"resource_id" doc:"Resource id; null means the whole type"`
	ResourceFromClaim *ResourceFromClaim   `json:"resource_from_claim" doc:"Dynamic resource_id computed from a claim"`
	ResourceWhere     []PredicateCondition `json:"resource_where" nullable:"true" doc:"The conditions over the resource attributes the item covers; null for the other targets"`
	Access            []string             `json:"access" doc:"Access levels"`
}

// RolePermissionWithContext is an item with the names of its type and service, for the admin UI.
type RolePermissionWithContext struct {
	RolePermission
	ServiceID        uuid.UUID `json:"service_id" format:"uuid" doc:"Id of the service that owns the resource type"`
	ServiceName      string    `json:"service_name" doc:"Name of the service that owns the resource type"`
	TypeResourceName string    `json:"type_resource_name" doc:"Resource type name"`
	TargetValid      bool      `json:"target_valid" doc:"Whether the item's target can take effect: false for an item on a specific resource of a type that has no synchronizer, or that looks a resource up in, or climbs through, such a type (see AssignmentRule.target_valid)"`
}
