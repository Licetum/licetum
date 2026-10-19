package model

import (
	"regexp"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// slugPattern: lowercase Latin letters, digits, '-' and '_', starting with a letter or a digit.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidSlug reports whether s may be a service slug. A slug is never a UUID, so that nothing that names a service by its
// slug can be taken for one that names it by its id.
func ValidSlug(s string) bool {
	if len(s) > 64 || !slugPattern.MatchString(s) {
		return false
	}
	_, err := uuid.Parse(s)
	return err != nil
}

// ServiceInput is the body of POST /service/. Every field but url and icon is required (checked by the Huma schema).
type ServiceInput struct {
	_    struct{} `additionalProperties:"true"`
	Name string   `json:"name" doc:"Service name" example:"filials"`
	Slug string   `json:"slug" maxLength:"64" pattern:"^[a-z0-9][a-z0-9_-]*$" doc:"Service slug: lowercase Latin letters, digits, '-' and '_', unique among live services. The PDP names the service by it in its paths (/service/<slug>/access/v1/...); it is not a UUID" example:"filials"`
	URL  *string  `json:"url" required:"false" nullable:"true" format:"uri" doc:"Application URL; optional, it is only shown in the interface and is not requested" example:"https://app.example.com"`
	Icon *string  `json:"icon" required:"false" nullable:"true" format:"uri" doc:"Icon URL; optional, it is only shown in the interface and is not requested" example:"https://app.example.com/icon.png"`
}

func (s *ServiceInput) Validate() error {
	if !ValidSlug(s.Slug) {
		return apperr.Unprocessable(i18n.ServiceSlugInvalid, s.Slug)
	}
	// The address and the icon are optional: absent, null or empty means none.
	for _, f := range []struct {
		name  string
		value **string
	}{{"url", &s.URL}, {"icon", &s.Icon}} {
		if *f.value == nil || **f.value == "" {
			*f.value = nil
			continue
		}
		v, err := NormalizeHTTPURL(f.name, **f.value)
		if err != nil {
			return err
		}
		*f.value = &v
	}
	return nil
}

// ServiceUpdate is the body of PATCH /service/{id}. Partial update: a missing field keeps its value.
type ServiceUpdate struct {
	_    struct{} `additionalProperties:"true"`
	Name *string  `json:"name" required:"false" doc:"Service name"`
	Slug *string  `json:"slug" required:"false" maxLength:"64" pattern:"^[a-z0-9][a-z0-9_-]*$" doc:"Service slug (see POST /service)"`
	URL  *string  `json:"url" required:"false" doc:"Application URL (an http or https address); an empty string removes it"`
	Icon *string  `json:"icon" required:"false" doc:"Icon URL (an http or https address); an empty string removes it"`
}

func (s *ServiceUpdate) Validate() error {
	if s.Slug != nil && !ValidSlug(*s.Slug) {
		return apperr.Unprocessable(i18n.ServiceSlugInvalid, *s.Slug)
	}
	if s.Name == nil && s.Slug == nil && s.URL == nil && s.Icon == nil {
		return apperr.Unprocessable(i18n.ServiceUpdateEmpty)
	}
	// "" removes the address or the icon (they are optional).
	for _, f := range []struct {
		name  string
		value **string
	}{{"url", &s.URL}, {"icon", &s.Icon}} {
		if *f.value == nil || **f.value == "" {
			continue
		}
		v, err := NormalizeHTTPURL(f.name, **f.value)
		if err != nil {
			return err
		}
		*f.value = &v
	}
	return nil
}

// Service is a row of the service table.
type Service struct {
	ID   uuid.UUID
	Name string
	Slug string
	URL  string
	Icon string
}

// ServiceResponse is a service as the admin API returns it.
type ServiceResponse struct {
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Service id"`
	Name string    `json:"name" doc:"Service name"`
	Slug string    `json:"slug" doc:"Service slug: the PDP of the service is <PDP__PUBLIC_URL>/service/<slug>"`
	URL  *string   `json:"url" doc:"Application URL"`
	Icon *string   `json:"icon" doc:"Icon URL"`
}

func (s *Service) Response() ServiceResponse {
	return ServiceResponse{ID: s.ID, Name: s.Name, Slug: s.Slug, URL: optionalURL(s.URL), Icon: optionalURL(s.Icon)}
}

// optionalURL is the address of an optional field: empty is none.
func optionalURL(raw string) *string {
	if raw == "" {
		return nil
	}
	return normalizeOutputURL(&raw)
}
