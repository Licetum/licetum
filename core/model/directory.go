package model

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// The subject directory (ADR 0001, 8.8): the people of a service as the application sent them.
const (
	// DirectoryBatchMax is how many entries one PUT carries.
	DirectoryBatchMax = 1000
	// DirectoryDefaultType is the type of an entry that names none.
	DirectoryDefaultType = "user"

	directoryIDMax         = 512
	directoryNameMax       = 256
	directoryPropertiesMax = 100
	directoryPropertyBytes = 16 << 10
)

var directoryType = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// DirectoryEntryInput is a person of the service as the application sends it.
type DirectoryEntryInput struct {
	_          struct{}       `additionalProperties:"true"`
	Type       string         `json:"type,omitempty" required:"false" maxLength:"64" doc:"The type of the subject (the subject.type of AuthZEN); absent: user" example:"user"`
	ID         string         `json:"id" minLength:"1" maxLength:"512" doc:"The id of the subject as the rules name it and the PEPs send it (subject.id)" example:"alice"`
	Name       string         `json:"name,omitempty" required:"false" maxLength:"256" doc:"The name to show for the subject; it is what a subject search returns as properties.name" example:"Alice Liddell"`
	Properties map[string]any `json:"properties,omitempty" required:"false" doc:"The properties of the subject (subject.properties): the claims the rules read, stored as sent, at most 100 of them and 16 KiB in all"`
}

// Validate checks the entry and gives it its type.
func (e *DirectoryEntryInput) Validate() error {
	if e.Type == "" {
		e.Type = DirectoryDefaultType
	}
	if !directoryType.MatchString(e.Type) {
		return apperr.Unprocessable(i18n.FieldInvalid, "type")
	}
	// The id is kept exactly as sent (a subject is its id); a group would be taken for the group of that name.
	if e.ID == "" || len(e.ID) > directoryIDMax || !utf8.ValidString(e.ID) || strings.ContainsRune(e.ID, 0) ||
		strings.HasPrefix(e.ID, GroupSubjectPrefix) {
		return apperr.Unprocessable(i18n.FieldInvalid, "id")
	}
	e.Name = strings.TrimSpace(e.Name)
	if len(e.Name) > directoryNameMax || !utf8.ValidString(e.Name) || strings.ContainsRune(e.Name, 0) {
		return apperr.Unprocessable(i18n.FieldInvalid, "name")
	}
	if len(e.Properties) > directoryPropertiesMax {
		return apperr.Unprocessable(i18n.FieldInvalid, "properties")
	}
	for name := range e.Properties {
		if name == "" || strings.ContainsRune(name, 0) {
			return apperr.Unprocessable(i18n.FieldInvalid, "properties")
		}
	}
	if raw, err := json.Marshal(e.Properties); err != nil || len(raw) > directoryPropertyBytes || strings.Contains(string(raw), `\u0000`) {
		return apperr.Unprocessable(i18n.FieldInvalid, "properties")
	}
	return nil
}

// DirectoryPut is the body that writes people to the directory of a service.
type DirectoryPut struct {
	_        struct{}              `additionalProperties:"true"`
	Subjects []DirectoryEntryInput `json:"subjects" minItems:"1" maxItems:"1000" doc:"The people to write, at most 1000 per call, each once. An entry that is already as sent is left untouched; the others are created or replaced as a whole"`
}

// Validate checks the entries and that none is sent twice.
func (p *DirectoryPut) Validate() error {
	if len(p.Subjects) == 0 || len(p.Subjects) > DirectoryBatchMax {
		return apperr.Unprocessable(i18n.FieldInvalid, "subjects")
	}
	seen := make(map[[2]string]bool, len(p.Subjects))
	for i := range p.Subjects {
		if err := p.Subjects[i].Validate(); err != nil {
			return err
		}
		key := [2]string{p.Subjects[i].Type, p.Subjects[i].ID}
		if seen[key] {
			return apperr.Unprocessable(i18n.DirectoryEntryRepeated, key[0], key[1])
		}
		seen[key] = true
	}
	return nil
}

// DirectoryWriteResult says what a write did.
type DirectoryWriteResult struct {
	Created   int `json:"created" doc:"Entries that were new"`
	Updated   int `json:"updated" doc:"Entries whose name or properties changed"`
	Unchanged int `json:"unchanged" doc:"Entries that were already as sent: nothing was written to them, and the time they were last sent moved"`
}

// DirectoryEntry is a person of the service as the directory holds it.
type DirectoryEntry struct {
	Type       string         `json:"type" doc:"The type of the subject"`
	ID         string         `json:"id" doc:"The id of the subject"`
	Name       string         `json:"name" doc:"The name to show; empty when none was sent"`
	Properties map[string]any `json:"properties" nullable:"false" doc:"The properties of the subject, as they were sent"`
	UpdatedAt  time.Time      `json:"updated_at" doc:"When the name or the properties last changed"`
	SentAt     time.Time      `json:"sent_at" doc:"When the entry was last sent (refreshed at most hourly): what the retention counts from"`
}

// DirectoryEntryDetail is an entry with the groups the person is in.
type DirectoryEntryDetail struct {
	DirectoryEntry
	Groups []SubjectGroup `json:"groups" nullable:"false" doc:"The groups the person is in now, nested groups included, decided with the stored properties"`
}

// DirectoryList is a page of the directory of a service.
type DirectoryList struct {
	Total int              `json:"total" doc:"How many entries match"`
	Items []DirectoryEntry `json:"items" nullable:"false" doc:"The page, by type and then by id"`
}

// DirectorySettings are the settings of the directory of a service.
type DirectorySettings struct {
	_             struct{} `additionalProperties:"true"`
	RetentionDays *int     `json:"retention_days" minimum:"1" maximum:"36500" doc:"Days an entry that is not sent again is kept; null: until it is deleted"`
}

// Validate checks the retention.
func (s *DirectorySettings) Validate() error {
	if s.RetentionDays != nil && (*s.RetentionDays < 1 || *s.RetentionDays > 36500) {
		return apperr.Unprocessable(i18n.FieldInvalid, "retention_days")
	}
	return nil
}
