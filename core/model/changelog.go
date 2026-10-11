package model

import (
	"time"

	"github.com/google/uuid"
)

// ChangeRecord is one entry of the change log: a write of the admin API, who made it, and the entity before and
// after the change. Secrets are masked in the entities (see Synchronizer).
type ChangeRecord struct {
	ID        int64      `json:"id" doc:"Position in the log; it grows with every change. Pass it as after to read the records that follow"`
	At        time.Time  `json:"at" doc:"When the change was made"`
	Actor     string     `json:"actor" doc:"The subject that made the change; empty for a change made outside a request"`
	ServiceID *uuid.UUID `json:"service_id" format:"uuid" nullable:"true" doc:"The service the entity belongs to; null for the platform-wide entities"`
	Entity    string     `json:"entity" doc:"The kind of entity: service, type_resource, synchronizer, group, group_member, role, role_permission, assignment_rule or log_settings"`
	EntityID  *uuid.UUID `json:"entity_id" format:"uuid" nullable:"true" doc:"The id of the entity; null for the log settings"`
	Action    string     `json:"action" enum:"create,update,delete" doc:"What was done"`
	Before    any        `json:"before" nullable:"true" doc:"The entity before the change; null for a create"`
	After     any        `json:"after" nullable:"true" doc:"The entity after the change; null for a delete"`
}
