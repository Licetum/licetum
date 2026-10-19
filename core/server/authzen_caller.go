package server

// The caller of an AuthZEN request: the holder of a verified token, who asks about themselves or, for a client
// application the service trusts, about other people. note and flush are the audit of those calls.

import (
	"context"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// authzenCaller is the holder of a verified token, who asks AuthZEN questions: about themselves, or — when the service
// trusts their client application — about other people, whose claims they give as subject.properties.
type authzenCaller struct {
	app       pdpEngine
	cfg       *config.Config
	serviceID uuid.UUID
	svc       *model.Service
	subject   string
	claims    engine.Claims
	// pep is set when the caller is a PEP client: the people it asks about come from the body, never from the credential.
	pep *engine.PEPCaller

	// The client application of the token, when the service trusts it (see engine.AuthorizeDelegate); asked once.
	trustChecked bool
	client       string
	trustErr     error
	// loggedOnBehalf is set once the access log line names the first person asked about.
	loggedOnBehalf bool

	// The people asked about on behalf, in the order first met, with what was asked (the audit of delegated calls).
	delegated []string
	tally     map[string]*delegatedTally
}

type delegatedTally struct{ questions, allowed int }

// speaker is the person a question is about, with the claims the rules read.
type speaker struct {
	subject   string
	claims    engine.Claims
	delegated bool
}

func newAuthzenCaller(ctx context.Context, app pdpEngine, cfg *config.Config, serviceID uuid.UUID, authorization string) (*authzenCaller, error) {
	svc, claims, err := verifiedCaller(ctx, app, serviceID, authorization)
	if err != nil {
		return nil, err
	}
	subject, err := engine.SubjectFromClaims(claims)
	if err != nil {
		return nil, err
	}
	SetRequestUser(ctx, subject)
	c := &authzenCaller{app: app, cfg: cfg, serviceID: serviceID, svc: svc, subject: subject, claims: claims, tally: map[string]*delegatedTally{}}
	return c, nil
}

// trusted returns the client application of the token when the service trusts it.
func (c *authzenCaller) trusted() (string, error) {
	if !c.trustChecked {
		c.client, c.trustErr = engine.AuthorizeDelegate(c.svc, c.claims)
		c.trustChecked = true
	}
	return c.client, c.trustErr
}

// speaker says who a subject object stands for. The holder of the token speaks for themselves with the claims of the
// token; any other subject needs a trusted client, and brings the claims in its properties.
func (c *authzenCaller) speaker(ctx context.Context, s *model.AuthzenSubject) (speaker, *apperr.Error) {
	if c.pep != nil {
		// A PEP client speaks for the people it asks about: the subject is the one of the body, and the claims the rules read are
		// its properties.
		claims, err := engine.PrepareDelegated(s.ID, s.Properties)
		if err != nil {
			return speaker{}, badRequest(err)
		}
		if !c.loggedOnBehalf {
			c.loggedOnBehalf = true
			SetOnBehalfOf(ctx, s.ID)
		}
		return speaker{subject: s.ID, claims: claims}, nil
	}
	if s.ID == c.subject {
		return speaker{subject: c.subject, claims: c.claims}, nil
	}
	client, err := c.trusted()
	if err != nil {
		return speaker{}, apperr.Forbidden(i18n.AuthzenSubjectMismatch)
	}
	claims, err := engine.PrepareDelegated(s.ID, s.Properties)
	if err != nil {
		return speaker{}, badRequest(err)
	}
	if !c.loggedOnBehalf {
		c.loggedOnBehalf = true
		SetRequestUser(ctx, "client:"+client)
		SetOnBehalfOf(ctx, s.ID)
	}
	return speaker{subject: s.ID, claims: claims, delegated: true}, nil
}

// note adds what was asked about a person to the audit of the request (a person the token holder asked about is not audited).
func (c *authzenCaller) note(sp speaker, questions, allowed int) {
	if !sp.delegated {
		return
	}
	t := c.tally[sp.subject]
	if t == nil {
		t = &delegatedTally{}
		c.tally[sp.subject] = t
		c.delegated = append(c.delegated, sp.subject)
	}
	t.questions += questions
	t.allowed += allowed
}

// flush writes the audit of the calls made on behalf of people: a call that cannot be recorded is not answered.
func (c *authzenCaller) flush(ctx context.Context, operation string) error {
	for _, subject := range c.delegated {
		t := c.tally[subject]
		if err := c.app.RecordDelegatedCall(ctx, c.serviceID, engine.DelegatedCall{
			CallerClient: c.client, CallerSubject: c.subject, Subject: subject, Operation: operation,
			Questions: t.questions, Allowed: t.allowed,
		}); err != nil {
			return err
		}
	}
	return nil
}
