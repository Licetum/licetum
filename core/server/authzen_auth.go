package server

// The authentication of the callers of the AuthZEN routes. A PEP client (an API key, or a token of an issuer registered for PEPs)
// is authenticated first; any other bearer token is the token of a person, checked against the service as before, until the
// removals of stage 2c take that way out.

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/httpx"
	"github.com/licetum/licetum/core/i18n"
)

// authzenAuth is what the routes of one PDP share for the authentication of their callers.
type authzenAuth struct {
	app    pdpEngine
	cfg    *config.Config
	limits *clientLimits
}

func newAuthzenAuth(app pdpEngine, cfg *config.Config) *authzenAuth {
	return &authzenAuth{app: app, cfg: cfg, limits: newClientLimits(cfg.PEP, time.Now)}
}

// caller authenticates the caller of an operation on the service named by serviceRef, from the Authorization header.
//
// A PEP client needs the PEP operation (engine.PEPEvaluate and the others), and the explain operation as well when the request asks
// for an explanation. Whatever it lacks, an unknown service included, the answer is the same 403. A client over its rate limit is
// a 429 with Retry-After. The person a question is about comes from the body, never from the credential.
//
// A missing header, and a scheme other than Bearer, are a 401 with the Bearer challenge (RFC 6750). Any other bearer token is
// the token of a person: the service must exist, and the token is verified against it.
func (au *authzenAuth) caller(ctx context.Context, serviceRef, authorization, operation string, explain bool) (*authzenCaller, error) {
	if authorization == "" {
		return nil, apperr.Unauthorized(i18n.AuthHeaderRequired)
	}
	token, isBearer := httpx.BearerToken(authorization)
	if !isBearer {
		return nil, apperr.Unauthorized(i18n.AuthHeaderRequired)
	}
	pep, err := au.app.AuthenticatePEP(ctx, token, serviceRef, operation)
	switch {
	case err == nil:
		if explain && !slices.Contains(pep.Client.Operations, engine.PEPExplain) {
			return nil, apperr.Forbidden(i18n.AuthzenForbidden)
		}
		if au.limits != nil {
			if ok, wait := au.limits.allow(pep.Client.ID); !ok {
				return nil, apperr.New(429, i18n.AuthzenRateLimited).WithHeader("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			}
		}
		SetRequestUser(ctx, "pep:"+pep.Client.Name)
		return &authzenCaller{app: au.app, cfg: au.cfg, serviceID: pep.ServiceID, pep: pep, tally: map[string]*delegatedTally{}}, nil
	case errors.Is(err, engine.ErrNotPEPCredential):
		serviceID, err := au.app.ServiceIDByRef(ctx, serviceRef)
		if err != nil {
			return nil, err
		}
		return newAuthzenCaller(ctx, au.app, au.cfg, serviceID, authorization)
	default:
		return nil, err
	}
}
