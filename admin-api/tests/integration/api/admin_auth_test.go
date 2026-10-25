//go:build integration

package api_test

// The authentication of administrators (ADR 0001, 8.12): the admin API verifies the bearer token against the static issuer of
// ADMIN_AUTH__* and answers before it parses the body.

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signAdmin signs claims as the mock identity provider does, without the defaults e.token adds: the claims are the token's own.
func (e *env) signAdmin(method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	e.t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(key)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// TestContract_ADM01_ABadAdministratorTokenIsA401WithTheChallengeAndA403ComesBeforeTheBodyIsValidated: a missing token is a 401
// with the Bearer challenge, a token that is malformed, expired, not yet valid, unsigned, signed by another key, issued by another
// provider or for another audience is a 401 invalid_token, and a caller without the right gets a 403 before the body is checked
// (422 for the administrator who sends the same body).
func TestContract_ADM01_ABadAdministratorTokenIsA401WithTheChallengeAndA403ComesBeforeTheBodyIsValidated(t *testing.T) {
	e := setup(t)
	issuer := e.mock.URL + "/realm"
	claims := func(mut func(jwt.MapClaims)) jwt.MapClaims {
		c := jwt.MapClaims{"preferred_username": "admin", "sub": "admin-sub", "iss": issuer, "aud": testAdminAudience,
			"exp": time.Now().Add(time.Hour).Unix()}
		if mut != nil {
			mut(c)
		}
		return c
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, claims(nil))
	noneToken, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}

	bad := map[string]string{
		"not a token":              "not-a-token",
		"a wrong audience":         e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { c["aud"] = "another-application" })),
		"no audience":              e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { delete(c, "aud") })),
		"another provider":         e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { c["iss"] = issuer + "/" })),
		"an expired token":         e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() })),
		"no expiry":                e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { delete(c, "exp") })),
		"not valid yet":            e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() })),
		"another key":              e.signAdmin(jwt.SigningMethodRS256, other, claims(nil)),
		"no signature":             noneToken,
		"a shared secret":          e.signAdmin(jwt.SigningMethodHS256, []byte("secret"), claims(nil)),
		"a token without a person": e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { delete(c, "preferred_username"); delete(c, "sub") })),
	}
	for name, token := range bad {
		status, header, raw := e.doWithHeader(http.MethodGet, e.admin.URL+"/service", nil, "Authorization", "Bearer "+token)
		if status != http.StatusUnauthorized {
			t.Errorf("%s = %d (%s), want 401", name, status, raw)
			continue
		}
		if got := header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") || !strings.Contains(got, `error="invalid_token"`) {
			t.Errorf("%s: WWW-Authenticate = %q", name, got)
		}
	}

	// No token at all, or another scheme: the challenge without an error.
	for name, authorization := range map[string]string{"no header": "", "another scheme": "Basic YWRtaW46YWRtaW4="} {
		status, header, raw := e.doWithHeader(http.MethodGet, e.admin.URL+"/service", nil, "Authorization", authorization)
		if status != http.StatusUnauthorized || header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s = %d %q (%s), want 401 with the Bearer challenge", name, status, header.Get("WWW-Authenticate"), raw)
		}
	}

	// The audience may be one of several.
	several := e.signAdmin(jwt.SigningMethodRS256, e.key, claims(func(c jwt.MapClaims) { c["aud"] = []string{"another-application", testAdminAudience} }))
	if status, raw := e.do(http.MethodGet, e.admin.URL+"/service", nil, "Authorization", "Bearer "+several); status != http.StatusOK {
		t.Errorf("a token whose audience lists ours = %d (%s), want 200", status, raw)
	}

	// Authorization comes before validation: the same bad body is a 403 for a stranger and a 422 for the administrator.
	stranger := e.token(jwt.MapClaims{"preferred_username": "stranger"})
	status, raw := e.do(http.MethodPost, e.admin.URL+"/service", map[string]any{}, "Authorization", "Bearer "+stranger)
	if status != http.StatusForbidden {
		t.Errorf("a stranger's invalid body = %d (%s), want 403", status, raw)
	}
	if status, raw := e.do(http.MethodPost, e.admin.URL+"/service", map[string]any{}); status != http.StatusUnprocessableEntity {
		t.Errorf("the administrator's invalid body = %d (%s), want 422", status, raw)
	}
}
