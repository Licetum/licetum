package server

// The authentication of the callers of the AuthZEN routes: PEP clients, with an API key or the token of an issuer registered for
// PEPs. The token of a person is not accepted.

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
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

// realm is the PDP identifier of the service the request is about (PDP__PUBLIC_URL/service/<ref>, ADR 0001 7.8), or a fixed name
// when the public URL is not set. The reference is the one the caller put in the path.
func (au *authzenAuth) realm(serviceRef string) string {
	if au.cfg.PDP.PublicURL == "" {
		return "licetum"
	}
	return strings.TrimSuffix(au.cfg.PDP.PublicURL, "/") + "/service/" + serviceRef
}

// caller authenticates the caller of an operation on the service named by serviceRef (its slug), from the Authorization header.
//
// A PEP client needs the PEP operation (engine.PEPEvaluate and the others), and the explain operation as well when the request asks
// for an explanation. Whatever it lacks, an unknown service included, the answer is the same 403. A client over its rate limit is
// a 429 with Retry-After. The person a question is about comes from the body, never from the credential.
//
// A missing header, and a scheme other than Bearer, are a 401 with the Bearer challenge (RFC 6750); so is a credential that is not
// a PEP credential.
func (au *authzenAuth) caller(ctx context.Context, serviceRef, authorization, operation string, explain bool) (*authzenCaller, error) {
	if authorization == "" {
		return nil, apperr.Unauthorized(i18n.AuthHeaderRequired).WithRealm(au.realm(serviceRef))
	}
	token, isBearer := httpx.BearerToken(authorization)
	if !isBearer {
		return nil, apperr.Unauthorized(i18n.AuthHeaderRequired).WithRealm(au.realm(serviceRef))
	}
	pep, err := au.app.AuthenticatePEP(ctx, token, serviceRef, operation)
	if err != nil {
		var challenged *apperr.Error
		if errors.As(err, &challenged) {
			return nil, challenged.WithRealm(au.realm(serviceRef))
		}
		return nil, err
	}
	if explain && !slices.Contains(pep.Client.Operations, engine.PEPExplain) {
		return nil, apperr.Forbidden(i18n.AuthzenForbidden)
	}
	if au.limits != nil {
		if ok, wait := au.limits.allow(pep.Client.ID); !ok {
			return nil, apperr.New(429, i18n.AuthzenRateLimited).WithHeader("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
		}
	}
	SetRequestUser(ctx, "pep:"+pep.Client.Name)
	return &authzenCaller{app: au.app, cfg: au.cfg, serviceID: pep.ServiceID}, nil
}
