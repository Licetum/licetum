// Package model holds the JSON request/response schemas of the API and their validation (the
// equivalent of the pydantic schemas of the Python version). Validation errors are apperr 422.
package model

import (
	"net/url"
	"strings"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// Validator is an input schema the HTTP layer validates right after decoding the body.
type Validator interface {
	Validate() error
}

func required(field string) error {
	return apperr.Unprocessable(i18n.FieldRequired, field)
}

// NormalizeHTTPURL mirrors pydantic AnyHttpUrl: http/https only, a host is required, an empty path
// becomes "/" (that is how pydantic serialized URLs in the Python version, so values in the DB
// keep the same format).
func NormalizeHTTPURL(field, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", apperr.Unprocessable(i18n.FieldURLInvalid, field)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" && u.RawPath == "" && u.Opaque == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

// normalizeOutputURL is for responses: normalize if possible, otherwise return as is
// (historical or seeded values must not break the service list).
func normalizeOutputURL(raw *string) *string {
	if raw == nil {
		return nil
	}
	if v, err := NormalizeHTTPURL("", *raw); err == nil {
		return &v
	}
	return raw
}

// ValidatePostgresDSN mirrors pydantic PostgresDsn: the postgres/postgresql scheme
// (optionally with the SQLAlchemy driver suffix, postgresql+asyncpg) and a host.
func ValidatePostgresDSN(field, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", apperr.Unprocessable(i18n.FieldDSNInvalid, field)
	}
	scheme, _, _ := strings.Cut(u.Scheme, "+")
	if scheme != "postgres" && scheme != "postgresql" {
		return "", apperr.Unprocessable(i18n.FieldDSNScheme, field)
	}
	if u.Host == "" {
		return "", apperr.Unprocessable(i18n.FieldDSNHost, field)
	}
	return u.String(), nil
}
