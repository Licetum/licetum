// Package apperr defines API errors serialized in a single shape:
//
//	{"detail": "...", "extra": {"type": "generic" | "missing" | "blocked_by", "code": "...", ...}}
//
// An error is created from an i18n catalog key; detail is rendered in the request language
// (see Localize) and the key itself is returned in extra.code, so clients can branch on it
// without parsing localized text.
//
// *Error implements huma.StatusError (GetStatus), so Huma writes it as the response body and
// the Error struct doubles as the error schema in OpenAPI. Any other error is treated as
// internal and returned as a 500 without details.
package apperr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/licetum/licetum/core/i18n"
)

const (
	TypeGeneric             = "generic"
	TypeMissing             = "missing"
	TypeProviderUnavailable = "provider_unavailable"
	TypeBlockedBy           = "blocked_by"
)

// Extra is a union discriminated by the type field (as in the legacy Python version).
type Extra struct {
	Type      string   `json:"type" enum:"generic,missing,provider_unavailable,blocked_by" doc:"Kind of error"`
	Code      string   `json:"code,omitempty" doc:"Stable machine-readable error code, independent of the response language" example:"service_not_found"`
	Missing   []string `json:"missing,omitempty" doc:"For type=missing: which fields or sources are unavailable" example:"[\"url\"]"`
	BlockedBy []string `json:"blocked_by,omitempty" doc:"For type=blocked_by: which entities prevent the deletion" example:"[\"assignment_rule\"]"`
	Operation *int     `json:"operation,omitempty" doc:"For a batch: the index of the operation that failed; nothing of the batch was applied" example:"2"`
}

// Error is the body of every error response.
type Error struct {
	Status int    `json:"-"`
	Detail string `json:"detail" doc:"Human-readable error description in the language negotiated via Accept-Language"`
	Extra  Extra  `json:"extra" doc:"Machine-readable details"`

	key     i18n.Key
	args    []any
	suffix  string
	cause   error
	headers http.Header
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%d %s: %v", e.Status, e.Detail, e.cause)
	}
	return fmt.Sprintf("%d %s", e.Status, e.Detail)
}

// GetStatus implements huma.StatusError.
func (e *Error) GetStatus() int { return e.Status }

func (e *Error) Unwrap() error { return e.cause }

// GetHeaders implements huma.HeadersError: the headers are set on the error response.
func (e *Error) GetHeaders() http.Header { return e.headers }

// WithHeader adds a response header (e.g. WWW-Authenticate) to the error.
func (e *Error) WithHeader(name, value string) *Error {
	if e.headers == nil {
		e.headers = http.Header{}
	}
	e.headers.Add(name, value)
	return e
}

// Key returns the catalog key (also available to clients as extra.code).
func (e *Error) Key() i18n.Key { return e.key }

// Wrap keeps the original cause for logs without exposing it to the client.
func (e *Error) Wrap(cause error) *Error {
	e.cause = cause
	return e
}

// WithSuffix appends untranslated details (e.g. validator messages) after the localized text.
func (e *Error) WithSuffix(suffix string) *Error {
	e.suffix = suffix
	e.render(i18n.Default)
	return e
}

// Localize returns a copy with detail rendered in the given language. Errors are created in
// English and localized right before being written to the response. It copies rather than
// mutates because one error value may be shared by concurrent requests (singleflight).
func (e *Error) Localize(l i18n.Lang) *Error {
	c := *e
	c.render(l)
	return &c
}

func (e *Error) render(l i18n.Lang) {
	if e.key == "" {
		return
	}
	e.Detail = i18n.T(l, e.key, e.args...)
	if e.suffix != "" {
		e.Detail += ": " + e.suffix
	}
}

// New creates an error with an arbitrary status and type=generic.
// AtOperation returns the error with the index of the operation of a batch that failed (see Extra.Operation).
func (e *Error) AtOperation(index int) *Error {
	c := *e
	c.Extra.Operation = &index
	return &c
}

func New(status int, key i18n.Key, args ...any) *Error {
	e := &Error{Status: status, Extra: Extra{Type: TypeGeneric, Code: string(key)}, key: key, args: args}
	e.render(i18n.Default)
	return e
}

func BadRequest(key i18n.Key, args ...any) *Error { return New(http.StatusBadRequest, key, args...) }

// Unauthorized — 401 for a request without usable credentials. RFC 6750 §3: the response carries
// the WWW-Authenticate challenge.
func Unauthorized(key i18n.Key, args ...any) *Error {
	return New(http.StatusUnauthorized, key, args...).WithHeader("WWW-Authenticate", "Bearer")
}

// InvalidToken — 401 for a token that is malformed, expired or not accepted (RFC 6750 §3.1
// error="invalid_token"): the client should refresh the token or sign in again.
func InvalidToken(key i18n.Key, args ...any) *Error {
	return New(http.StatusUnauthorized, key, args...).WithHeader("WWW-Authenticate", `Bearer error="invalid_token"`)
}

func Forbidden(key i18n.Key, args ...any) *Error { return New(http.StatusForbidden, key, args...) }

func NotFound(key i18n.Key, args ...any) *Error { return New(http.StatusNotFound, key, args...) }

func Conflict(key i18n.Key, args ...any) *Error { return New(http.StatusConflict, key, args...) }

func Unprocessable(key i18n.Key, args ...any) *Error {
	return New(http.StatusUnprocessableEntity, key, args...)
}

// FailedDependency — 424: an upstream is unavailable (IdP, JWKS, synchronizer).
func FailedDependency(key i18n.Key, args ...any) *Error {
	return New(http.StatusFailedDependency, key, args...)
}

// ServiceUnavailable — 503: the instance is not set up to serve the request yet.
func ServiceUnavailable(key i18n.Key, args ...any) *Error {
	return New(http.StatusServiceUnavailable, key, args...)
}

func Internal() *Error { return New(http.StatusInternalServerError, i18n.InternalError) }

// BlockedBy — 409 listing the entities that prevent a deletion.
func BlockedBy(blockedBy []string, key i18n.Key, args ...any) *Error {
	e := New(http.StatusConflict, key, args...)
	e.Extra.Type = TypeBlockedBy
	e.Extra.BlockedBy = blockedBy
	return e
}

// Missing — 424 listing the unavailable fields (e.g. a service url/icon).
func Missing(missing []string, key i18n.Key, args ...any) *Error {
	e := New(http.StatusFailedDependency, key, args...)
	e.Extra.Type = TypeMissing
	e.Extra.Missing = missing
	return e
}

// As extracts *Error from an error chain.
func As(err error) (*Error, bool) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}
