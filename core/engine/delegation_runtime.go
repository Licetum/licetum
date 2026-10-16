package engine

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

const (
	delegatedSubjectMax = 256
	delegatedClaimsMax  = 100
	delegatedClaimsSize = 16 * 1024
)

// ClientOfClaims is the client application a token was issued to: azp (OpenID Connect), or client_id (RFC 9068).
func ClientOfClaims(claims Claims) string {
	for _, name := range []string{"azp", "client_id"} {
		if v := claims[name]; PyTruthy(v) {
			return strings.TrimSpace(PyStr(v))
		}
	}
	return ""
}

// AuthorizeDelegate says whether the client of a verified token may ask on behalf of a person in the service, and returns
// the client. The service has to name it among its trusted clients.
func AuthorizeDelegate(svc *model.Service, claims Claims) (string, error) {
	client := ClientOfClaims(claims)
	if client == "" {
		return "", apperr.Forbidden(i18n.DelegationNoClient)
	}
	if !slices.Contains(svc.TrustedClients, client) {
		return "", apperr.Forbidden(i18n.DelegationNotTrusted, client)
	}
	return client, nil
}

// PrepareDelegated checks the person a call names and builds the claims the decision reads: those the caller sends, with
// preferred_username set to the subject unless the caller sent one (the rules that name a person read it).
func PrepareDelegated(subject string, raw map[string]any) (Claims, error) {
	if subject == "" || len(subject) > delegatedSubjectMax || strings.HasPrefix(subject, model.GroupSubjectPrefix) ||
		strings.ContainsFunc(subject, unicode.IsControl) {
		return nil, apperr.Unprocessable(i18n.DelegationSubjectInvalid)
	}
	if len(raw) > delegatedClaimsMax {
		return nil, apperr.Unprocessable(i18n.DelegationClaimsInvalid)
	}
	if len(raw) > 0 {
		encoded, err := json.Marshal(raw)
		if err != nil || len(encoded) > delegatedClaimsSize {
			return nil, apperr.Unprocessable(i18n.DelegationClaimsInvalid)
		}
	}
	claims := Claims{}
	for k, v := range raw {
		claims[k] = v
	}
	if !PyTruthy(claims["preferred_username"]) {
		claims["preferred_username"] = subject
	}
	return claims, nil
}

// DelegatedCall is what the audit keeps of a call.
type DelegatedCall struct {
	CallerClient  string
	CallerSubject string
	Subject       string
	Operation     string
	Questions     int
	Allowed       int
}

// RecordDelegatedCall keeps a call. In strict mode (the default) it is written before the answer is given: a call that
// cannot be recorded is not answered. In async mode the record is buffered and written in batches (see auditSink).
func (a *App) RecordDelegatedCall(ctx context.Context, serviceID uuid.UUID, c DelegatedCall) error {
	requestID := ""
	if t := TraceFrom(ctx); t != nil {
		requestID = t.Request()
	}
	if sink := a.audit(); sink != nil {
		sink.add(auditRecord{at: time.Now(), serviceID: serviceID, call: c, requestID: requestID})
		return nil
	}
	_, err := a.DB.Exec(ctx, `
		INSERT INTO delegated_call (service_id, caller_client, caller_subject, subject, operation, questions, allowed, request_id)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, NULLIF($8, ''))`,
		serviceID, c.CallerClient, c.CallerSubject, c.Subject, c.Operation, c.Questions, c.Allowed, requestID)
	return err
}
