package model

import (
	"time"

	"github.com/google/uuid"
)

// KnownSubject is a subject Licetum has seen in a verified token.
type KnownSubject struct {
	Subject   string    `json:"subject" doc:"The subject as the rules name it (preferred_username, or sub)" example:"alice"`
	Name      *string   `json:"name" doc:"The display name from the token (name, or given and family name); null when the token had none"`
	FirstSeen time.Time `json:"first_seen" doc:"When a token of the subject was first verified"`
	LastSeen  time.Time `json:"last_seen" doc:"When a token of the subject was last verified (noted at most every ten minutes)"`
	SeenCount int64     `json:"seen_count" doc:"How many times it was noted"`
}

// KnownSubjectList is a page of the registry.
type KnownSubjectList struct {
	Enabled       bool           `json:"enabled" doc:"Whether the registry is on (SUBJECTS__ENABLED); when it is off the list stays as it was and does not grow"`
	RetentionDays int            `json:"retention_days" doc:"Days a subject that is not seen again is kept (SUBJECTS__RETENTION_DAYS); 0 — for good"`
	Total         int            `json:"total" doc:"How many subjects match"`
	Items         []KnownSubject `json:"items" nullable:"false" doc:"The page, the most recently seen first"`
}

// SubjectGroup is a group a subject is in.
type SubjectGroup struct {
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Group id"`
	Name string    `json:"name" doc:"Group name"`
}

// KnownSubjectDetail is a known subject with the claims the policy reads and the groups it is in.
type KnownSubjectDetail struct {
	KnownSubject
	Claims map[string]any `json:"claims" nullable:"false" doc:"The claims kept for the subject: only those the policy reads (strings or lists of strings)"`
	Groups []SubjectGroup `json:"groups" nullable:"false" doc:"The groups the subject is in now, nested groups included, as of the claims above"`
}
