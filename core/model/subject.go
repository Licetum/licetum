package model

import "github.com/google/uuid"

// SubjectGroup is a group a person is in.
type SubjectGroup struct {
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Group id"`
	Name string    `json:"name" doc:"Group name"`
}
