package server

// The caller of an AuthZEN request: a PEP client of the service, which asks about the people of its application. The person of a
// question is the subject of the body and the claims the rules read are its properties; the credential names the client and never
// the person.

import (
	"context"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

// authzenCaller is the PEP client that asks AuthZEN questions about a service.
type authzenCaller struct {
	app       pdpEngine
	cfg       *config.Config
	serviceID uuid.UUID
	// simulated is set for the simulation of an administrator: nothing is recorded, not even the decisions.
	simulated bool
	// loggedOnBehalf is set once the access log line names the first person asked about.
	loggedOnBehalf bool
}

// speaker is the person a question is about, with the claims the rules read.
type speaker struct {
	subject string
	claims  engine.Claims
}

// speaker says who a subject object stands for: the person named in the body, with the claims given as its properties.
func (c *authzenCaller) speaker(ctx context.Context, s *model.AuthzenSubject) (speaker, *apperr.Error) {
	claims, err := subjectClaims(s.ID, s.Properties)
	if err != nil {
		return speaker{}, badRequest(err)
	}
	if !c.loggedOnBehalf {
		c.loggedOnBehalf = true
		SetOnBehalfOf(ctx, s.ID)
	}
	return speaker{subject: s.ID, claims: claims}, nil
}

// subjectClaims checks the person a question names and builds the claims the decision reads: those the caller sends, with
// preferred_username set to the subject unless the caller sent one (the rules that name a person read it). The checks are those of
// model.PrepareSubject, which the AuthZEN requests share.
func subjectClaims(subject string, properties map[string]any) (engine.Claims, error) {
	prepared, err := model.PrepareSubject("", subject, properties)
	if err != nil {
		return nil, err
	}
	return engine.Claims(prepared.Claims), nil
}
