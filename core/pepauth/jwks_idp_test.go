package pepauth_test

import (
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/licetum/licetum/core/pepauth"
	support "github.com/licetum/licetum/core/testsupport"
)

// TestContract_PEP03_JunkTokensDoNotMultiplyFetches sends junk tokens with random key ids for a while: the provider is asked
// once per AUTH__JWKS_MIN_REFRESH at most, however many tokens come.
func TestContract_PEP03_JunkTokensDoNotMultiplyFetches(t *testing.T) {
	idp := support.NewIdP(t)
	const minRefresh = 100 * time.Millisecond
	v := pepauth.NewVerifier(pepauth.IssuerConfig{Issuer: idp.Issuer(), JWKSURL: idp.JWKSURL()}, keySetsOf(minRefresh), testPDP)

	start := time.Now()
	// A valid token fills the key set: that is the first fetch.
	if _, err := v.Verify(t.Context(), rsToken(idp, pdpClaims(nil))); err != nil {
		t.Fatal(err)
	}
	junk := 0
	for time.Since(start) < 350*time.Millisecond {
		token := craftToken(t,
			map[string]any{"alg": "RS256", "kid": rand.Text(), "typ": "JWT"},
			map[string]any{"iss": idp.Issuer(), "sub": "junk", "aud": testPDP, "exp": time.Now().Add(time.Hour).Unix()},
			"c2ln")
		if _, err := v.Verify(t.Context(), token); !errors.Is(err, pepauth.ErrTokenInvalid) {
			t.Fatalf("junk token: err = %v, want ErrTokenInvalid", err)
		}
		junk++
	}
	// Two fetches are at least minRefresh apart, so the refetches after the first one number at most elapsed / minRefresh.
	elapsed := time.Since(start)
	refetches := idp.JWKSRequests() - 1
	if limit := int(elapsed / minRefresh); refetches > limit {
		t.Fatalf("%d refetches for %d junk tokens in %v, want at most %d", refetches, junk, elapsed, limit)
	}
}
