package model

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// slugPattern: lowercase Latin letters, digits, '-' and '_', starting with a letter or a digit.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidSlug reports whether s may be a service slug. A slug is never a UUID: the check API takes the
// service as one path segment, either its id or its slug, and a UUID-like slug would be ambiguous.
func ValidSlug(s string) bool {
	if len(s) > 64 || !slugPattern.MatchString(s) {
		return false
	}
	_, err := uuid.Parse(s)
	return err != nil
}

// ServiceInput is the body of POST /service/. Every field but url, icon and client_secret is required (checked by the Huma schema).
type ServiceInput struct {
	_                struct{} `additionalProperties:"true"`
	Name             string   `json:"name" doc:"Service name" example:"filials"`
	Slug             string   `json:"slug" maxLength:"64" pattern:"^[a-z0-9][a-z0-9_-]*$" doc:"Service slug: lowercase Latin letters, digits, '-' and '_', unique among live services. The check API accepts it in place of the id; it is not a UUID" example:"filials"`
	URL              *string  `json:"url" required:"false" nullable:"true" format:"uri" doc:"Application URL; optional, it is only shown in the interface and is not requested" example:"https://app.example.com"`
	Icon             *string  `json:"icon" required:"false" nullable:"true" format:"uri" doc:"Icon URL; optional, it is only shown in the interface and is not requested" example:"https://app.example.com/icon.png"`
	AuthorizationURL string   `json:"authorization_url" format:"uri" doc:"OAuth2 authorization endpoint of the service"`
	TokenURL         string   `json:"token_url" format:"uri" doc:"OAuth2 token endpoint of the service"`
	JwksURL          string   `json:"jwks_url" format:"uri" doc:"JWKS endpoint used to verify the service token signatures"`
	Issuer           string   `json:"issuer" format:"uri" doc:"OAuth2 issuer (the iss value in the service tokens)"`
	ClientID         string   `json:"client_id" doc:"OAuth2 client_id issued to Licetum by this service"`
	ClientSecret     *string  `json:"client_secret" required:"false" nullable:"true" doc:"OAuth2 client_secret; omit it, send null or an empty string for a public client (PKCE is used then)"`
	TrustedClients   []string `json:"trusted_clients" required:"false" nullable:"true" doc:"The client applications (the azp of their tokens) trusted to ask on behalf of a person; see POST /service/{id}/on_behalf/check. Their tokens must carry the claims the rules of the service read" example:"[\"docs-backend\"]"`
}

func (s *ServiceInput) Validate() error {
	if !ValidSlug(s.Slug) {
		return apperr.Unprocessable(i18n.ServiceSlugInvalid, s.Slug)
	}
	clients, err := NormalizeTrustedClients(s.TrustedClients)
	if err != nil {
		return err
	}
	s.TrustedClients = clients
	for _, f := range []struct {
		name  string
		value *string
	}{
		{"authorization_url", &s.AuthorizationURL},
		{"token_url", &s.TokenURL}, {"jwks_url", &s.JwksURL}, {"issuer", &s.Issuer},
	} {
		v, err := NormalizeHTTPURL(f.name, *f.value)
		if err != nil {
			return err
		}
		*f.value = v
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
	_                struct{}        `additionalProperties:"true"`
	Name             *string         `json:"name" required:"false" doc:"Service name"`
	Slug             *string         `json:"slug" required:"false" maxLength:"64" pattern:"^[a-z0-9][a-z0-9_-]*$" doc:"Service slug (see POST /service)"`
	URL              *string         `json:"url" required:"false" doc:"Application URL (an http or https address); an empty string removes it"`
	Icon             *string         `json:"icon" required:"false" doc:"Icon URL (an http or https address); an empty string removes it"`
	AuthorizationURL *string         `json:"authorization_url" required:"false" format:"uri" doc:"OAuth2 authorization endpoint of the service"`
	TokenURL         *string         `json:"token_url" required:"false" format:"uri" doc:"OAuth2 token endpoint of the service"`
	JwksURL          *string         `json:"jwks_url" required:"false" format:"uri" doc:"JWKS endpoint of the service"`
	Issuer           *string         `json:"issuer" required:"false" format:"uri" doc:"OAuth2 issuer"`
	ClientID         *string         `json:"client_id" required:"false" doc:"OAuth2 client_id"`
	ClientSecret     ClearableString `json:"client_secret" required:"false" doc:"OAuth2 client_secret: omit it to keep the current one; null or an empty string removes it — the client becomes public (PKCE)"`
	TrustedClients   *[]string       `json:"trusted_clients" required:"false" doc:"The client applications (the azp of their tokens) trusted to ask on behalf of a person. Omit it to keep the list; send the whole list to replace it"`
}

// ClearableString is a PATCH field that tells "absent" (keep the value) from null (clear it):
// UnmarshalJSON is only called when the key is present, including for null.
type ClearableString struct {
	Set   bool
	Value *string
}

func (c *ClearableString) UnmarshalJSON(data []byte) error {
	c.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		c.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	c.Value = &s
	return nil
}

// Schema: a nullable string for OpenAPI.
func (ClearableString) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Nullable: true}
}

// Cleared reports a field that was sent as null or an empty string.
func (c ClearableString) Cleared() bool {
	return c.Set && (c.Value == nil || *c.Value == "")
}

func (s *ServiceUpdate) Validate() error {
	urls := []struct {
		name  string
		value **string
	}{
		{"url", &s.URL}, {"icon", &s.Icon}, {"authorization_url", &s.AuthorizationURL},
		{"token_url", &s.TokenURL}, {"jwks_url", &s.JwksURL}, {"issuer", &s.Issuer},
	}
	// "" removes the address or the icon (they are optional); it is the only URL field that may be emptied.
	optional := map[string]bool{"url": true, "icon": true}
	if s.Slug != nil && !ValidSlug(*s.Slug) {
		return apperr.Unprocessable(i18n.ServiceSlugInvalid, *s.Slug)
	}
	empty := s.Name == nil && s.Slug == nil && s.ClientID == nil && !s.ClientSecret.Set && s.TrustedClients == nil
	if s.TrustedClients != nil {
		clients, err := NormalizeTrustedClients(*s.TrustedClients)
		if err != nil {
			return err
		}
		s.TrustedClients = &clients
	}
	for _, f := range urls {
		if *f.value == nil {
			continue
		}
		empty = false
		if optional[f.name] && **f.value == "" {
			continue
		}
		v, err := NormalizeHTTPURL(f.name, **f.value)
		if err != nil {
			return err
		}
		*f.value = &v
	}
	if empty {
		return apperr.Unprocessable(i18n.ServiceUpdateEmpty)
	}
	return nil
}

// Service is a row of the service table.
type Service struct {
	ID               uuid.UUID
	Name             string
	Slug             string
	URL              string
	Icon             string
	AuthorizationURL *string
	TokenURL         *string
	JwksURL          *string
	Issuer           *string
	ClientID         *string
	ClientSecret     *string
	TrustedClients   []string
}

// ServiceResponse deliberately does not expose client_secret.
type ServiceResponse struct {
	ID               uuid.UUID `json:"id" format:"uuid" doc:"Service id"`
	Name             string    `json:"name" doc:"Service name"`
	Slug             string    `json:"slug" doc:"Service slug, accepted in the check API paths in place of the id"`
	URL              *string   `json:"url" doc:"Application URL"`
	Icon             *string   `json:"icon" doc:"Icon URL"`
	AuthorizationURL *string   `json:"authorization_url" doc:"OAuth2 authorization endpoint of the service"`
	TokenURL         *string   `json:"token_url" doc:"OAuth2 token endpoint of the service"`
	JwksURL          *string   `json:"jwks_url" doc:"JWKS endpoint of the service"`
	Issuer           *string   `json:"issuer" doc:"OAuth2 issuer"`
	ClientID         *string   `json:"client_id" doc:"OAuth2 client_id"`
	TrustedClients   []string  `json:"trusted_clients" nullable:"false" doc:"The client applications (the azp of their tokens) trusted to ask on behalf of a person"`
	HasClientSecret  bool      `json:"has_client_secret" doc:"Whether a client secret is stored (the secret itself is never returned); false means a public client (PKCE)"`
}

func (s *Service) Response() ServiceResponse {
	return ServiceResponse{
		ID:               s.ID,
		Name:             s.Name,
		Slug:             s.Slug,
		URL:              optionalURL(s.URL),
		Icon:             optionalURL(s.Icon),
		AuthorizationURL: normalizeOutputURL(s.AuthorizationURL),
		TokenURL:         normalizeOutputURL(s.TokenURL),
		JwksURL:          normalizeOutputURL(s.JwksURL),
		Issuer:           normalizeOutputURL(s.Issuer),
		ClientID:         s.ClientID,
		HasClientSecret:  s.ClientSecret != nil && *s.ClientSecret != "",
		TrustedClients:   append([]string{}, s.TrustedClients...),
	}
}

// Bounds of the list of trusted clients.
const (
	maxTrustedClients   = 50
	maxTrustedClientLen = 256
)

// NormalizeTrustedClients trims the client ids, drops the repeats and checks the bounds: a client id is a printable
// string without spaces inside (as the azp of a token is).
func NormalizeTrustedClients(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" || len(id) > maxTrustedClientLen || strings.ContainsFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return nil, apperr.Unprocessable(i18n.ServiceTrustedClientInvalid, raw)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > maxTrustedClients {
		return nil, apperr.Unprocessable(i18n.ServiceTrustedClientsTooMany, maxTrustedClients)
	}
	return out, nil
}

// optionalURL is the address of an optional field: empty is none.
func optionalURL(raw string) *string {
	if raw == "" {
		return nil
	}
	return normalizeOutputURL(&raw)
}
