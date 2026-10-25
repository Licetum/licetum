package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/apperr"
)

// authzenError is an error of an AuthZEN operation. The specification gives an error no body of its own, so the body is the
// localized message as a JSON string, and the machine-readable code goes into the Licetum-Error-Code header (ADR 0001, 7.8).
// The headers of the error, WWW-Authenticate among them, go out as they are. The admin API keeps the error model of apperr.
type authzenError struct {
	err *apperr.Error
}

func authzenErrorOf(err *apperr.Error) *authzenError {
	return &authzenError{err: err}
}

// Error implements error.
func (e *authzenError) Error() string { return e.err.Error() }

// GetStatus implements huma.StatusError.
func (e *authzenError) GetStatus() int { return e.err.Status }

// MarshalJSON is the body of the error: the localized message, as a JSON string.
func (e *authzenError) MarshalJSON() ([]byte, error) { return json.Marshal(e.err.Detail) }

// GetHeaders implements huma.HeadersError. It returns a copy: the error may be shared by concurrent requests.
func (e *authzenError) GetHeaders() http.Header {
	h := http.Header{}
	for name, values := range e.err.GetHeaders() {
		h[name] = slices.Clone(values)
	}
	h.Set(licetumErrorCodeHeader, e.err.Extra.Code)
	return h
}

// documentAuthzenErrors describes the error responses of an AuthZEN operation as they are sent: a JSON string, with the code in
// the Licetum-Error-Code header. Huma documents every error with the error model of apperr, which an AuthZEN error does not have.
func documentAuthzenErrors(api huma.API, op huma.Operation) {
	item := api.OpenAPI().Paths[op.Path]
	if item == nil {
		return
	}
	var doc *huma.Operation
	switch op.Method {
	case http.MethodGet:
		doc = item.Get
	case http.MethodPost:
		doc = item.Post
	}
	if doc == nil {
		return
	}
	for _, code := range op.Errors {
		resp := doc.Responses[strconv.Itoa(code)]
		if resp == nil {
			continue
		}
		for _, media := range resp.Content {
			media.Schema = &huma.Schema{Type: "string"}
		}
		resp.Headers = map[string]*huma.Param{
			licetumErrorCodeHeader: {Schema: &huma.Schema{Type: "string"}},
		}
	}
}
