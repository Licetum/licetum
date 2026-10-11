package model

import "github.com/google/uuid"

// The entities a batch operation can write. Each one is written through the function the admin API uses for it.
const (
	BatchAssignmentRule = "assignment_rule"
	BatchGroup          = "group"
	BatchGroupMember    = "group_member"
	BatchRole           = "role"
	BatchRolePermission = "role_permission"
	BatchAccessGroup    = "access_group"
)

// The operations of a batch.
const (
	BatchCreate = "create"
	BatchUpdate = "update"
	BatchDelete = "delete"
)

// BatchOperation is one write of a batch: a create, an update or a delete of an entity of the policy.
type BatchOperation struct {
	Op     string     `json:"op" enum:"create,update,delete" doc:"create makes the entity, update changes it with its body, delete removes it"`
	Entity string     `json:"entity" enum:"assignment_rule,group,group_member,role,role_permission,access_group" doc:"The kind of entity: an assignment_rule, a group, a group_member (of the group in parent), a role, a role_permission (an item of the role in parent) or an access_group (of the resource type in parent)"`
	ID     *uuid.UUID `json:"id,omitempty" format:"uuid" required:"false" doc:"The entity of an update or a delete. For a create: the id the entity gets; absent — a new id"`
	Parent *uuid.UUID `json:"parent,omitempty" format:"uuid" required:"false" doc:"The entity the operation belongs to: the group of a group_member, the role of a role_permission, the resource type of an access_group"`
	Body   any        `json:"body,omitempty" required:"false" doc:"The input of the entity, as its endpoint takes it: for a create and an update"`
}

// BatchInput is the body of POST /batch.
type BatchInput struct {
	_          struct{}         `additionalProperties:"true"`
	Operations []BatchOperation `json:"operations" minItems:"1" maxItems:"100" doc:"The operations, applied in order in one transaction: all of them take effect, or none. An operation sees the writes of the ones before it. An entity created by an operation is named by its id in the next ones, so give a create an id when later operations need it"`
}

// BatchResult is what one operation of a batch did.
type BatchResult struct {
	Index  int       `json:"index" doc:"Position of the operation in the batch"`
	Op     string    `json:"op" enum:"create,update,delete" doc:"What the operation did"`
	Entity string    `json:"entity" doc:"The kind of entity"`
	ID     uuid.UUID `json:"id" format:"uuid" doc:"The entity the operation wrote"`
}

// BatchResponse is the body of the answer of POST /batch.
type BatchResponse struct {
	Results []BatchResult `json:"results" nullable:"false" doc:"One result per operation, in order"`
}

// RevertResponse is the body of the answer of a revert or a rollback: the operations that undo the changes, and
// what they did. A dry run carries the operations only.
type RevertResponse struct {
	DryRun     bool             `json:"dry_run" doc:"Whether nothing was applied"`
	Operations []BatchOperation `json:"operations" nullable:"false" doc:"The operations that undo the changes, in the order they run"`
	Results    []BatchResult    `json:"results" nullable:"false" doc:"What the operations did; empty for a dry run"`
}

// RollbackInput is the body of POST /changes/rollback.
type RollbackInput struct {
	_      struct{} `additionalProperties:"true"`
	To     int64    `json:"to" minimum:"0" doc:"The position of the change log to go back to: every change after it is undone, newest first. Take it from the id of a record of GET /changes"`
	DryRun bool     `json:"dry_run" required:"false" doc:"Return the operations that would undo the changes, and apply nothing"`
}

// RevertInput is the body of POST /changes/{change_id}/revert.
type RevertInput struct {
	_      struct{} `additionalProperties:"true"`
	DryRun bool     `json:"dry_run" required:"false" doc:"Return the operation that would undo the change, and apply nothing"`
}
