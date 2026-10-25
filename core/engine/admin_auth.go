package engine

import (
	"context"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/pepauth"
)

// VerifyAdminToken verifies the bearer token of an administrator against the static issuer of ADMIN_AUTH__* (ADR 0001, 8.12): the
// signature against the keys of the issuer (one key set per process, shared with the PEP verifiers), an algorithm of the
// allowlist, iss compared exactly, an aud that contains ADMIN_AUTH__AUDIENCE, exp and nbf with a minute of leeway. A token the
// verifier does not accept is a 401 invalid_token; keys that cannot be read are a 424; an installation without ADMIN_AUTH__* is a
// 503, since nobody can authenticate.
func (a *App) VerifyAdminToken(ctx context.Context, token string) (pepauth.Verified, error) {
	cfg := a.Cfg.AdminAuth
	if !cfg.IsConfigured() {
		return pepauth.Verified{}, apperr.ServiceUnavailable(i18n.AdminAuthNotConfigured)
	}
	a.adminVerifierOnce.Do(func() {
		a.adminVerifier = pepauth.NewVerifier(pepauth.IssuerConfig{
			Issuer: cfg.Issuer, JWKSURL: cfg.JWKSURL, Audiences: []string{cfg.Audience},
		}, a.keySets(), "")
	})
	verified, err := a.adminVerifier.Verify(ctx, token)
	if err != nil {
		return pepauth.Verified{}, verifyFailure(ctx, err)
	}
	return verified, nil
}

// AdminSubject names the administrator whose token was verified: the claim of ADMIN_AUTH__SUBJECT_CLAIM when the token has it,
// else its sub. A token with neither is a 401.
func (a *App) AdminSubject(claims Claims) (string, error) {
	if name := a.Cfg.AdminAuth.SubjectClaim; name != "" && name != "sub" {
		if v := claims[name]; PyTruthy(v) {
			return PyStr(v), nil
		}
	}
	if v, ok := claims["sub"]; ok && v != nil {
		return PyStr(v), nil
	}
	return "", apperr.InvalidToken(i18n.TokenNoSubject)
}
