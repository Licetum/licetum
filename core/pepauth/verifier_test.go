package pepauth_test

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/pepauth"
	support "github.com/licetum/licetum/core/testsupport"
)

// testPDP is the audience of the PDP in these tests.
const testPDP = "licetum-pdp"

// idpWithKeys is the fake provider of the tests: an RSA key, an ES256 key, an EdDSA key and, last, another RSA key, which
// signs by default.
func idpWithKeys(t *testing.T) *support.IdP {
	t.Helper()
	idp := support.NewIdP(t)
	idp.AddKey("ES256")
	idp.AddKey("EdDSA")
	idp.AddKey("RS256")
	return idp
}

// keySetsOf is the key set of a test process: the provider's keys are fetched at once and held for an hour.
func keySetsOf(minRefresh time.Duration) *pepauth.KeySets {
	return &pepauth.KeySets{HTTP: &http.Client{}, TTL: time.Hour, MinRefresh: minRefresh}
}

// verifierOf is the verifier of the provider's tokens with the configuration given; the issuer and the JWKS are the
// provider's.
func verifierOf(idp *support.IdP, cfg pepauth.IssuerConfig) *pepauth.Verifier {
	cfg.Issuer = idp.Issuer()
	cfg.JWKSURL = idp.JWKSURL()
	return pepauth.NewVerifier(cfg, keySetsOf(time.Minute), testPDP)
}

// pdpClaims are the claims of a token the PDP accepts, with the changes given (a nil value leaves the claim out).
func pdpClaims(changes jwt.MapClaims) jwt.MapClaims {
	c := jwt.MapClaims{"sub": "pep-service", "aud": testPDP}
	maps.Copy(c, changes)
	return c
}

// rsToken signs the claims with the RSA key of the provider, RS256, and the options given.
func rsToken(idp *support.IdP, claims jwt.MapClaims, opts ...support.TokenOption) string {
	return idp.SignToken(claims, append([]support.TokenOption{support.SignedWith("RS256")}, opts...)...)
}

// encodeJSON is the base64url of the JSON of v, as a part of a token.
func encodeJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// craftToken assembles a token from its header and claims without signing it: the signature is the one given.
func craftToken(t *testing.T, header, claims map[string]any, signature string) string {
	t.Helper()
	return encodeJSON(t, header) + "." + encodeJSON(t, claims) + "." + signature
}

// TestContract_PEP01_AcceptsOnlyAllowedJWTs runs the rules of a PEP token against the provider. The times are made when a case
// runs, so that the leeway boundaries are checked at the tolerance and not at the time the table was written.
func TestContract_PEP01_AcceptsOnlyAllowedJWTs(t *testing.T) {
	idp := idpWithKeys(t)
	at := func(d time.Duration) int64 { return time.Now().Add(d).Unix() }
	none := func(alg string) string {
		return craftToken(t, map[string]any{"alg": alg, "typ": "JWT"},
			map[string]any{"iss": idp.Issuer(), "sub": "pep-service", "aud": testPDP, "exp": at(time.Hour)}, "")
	}
	// hmacWithPublicKey is the classic confusion attack: the public key of the provider used as an HMAC secret.
	hmacWithPublicKey := func(alg string) string {
		der, err := x509.MarshalPKIXPublicKey(idp.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		secret := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
		hs := jwt.NewWithClaims(jwt.GetSigningMethod(alg), pdpClaims(jwt.MapClaims{"iss": idp.Issuer(), "exp": at(time.Hour)}))
		signed, err := hs.SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}

	cases := []struct {
		name     string
		cfg      pepauth.IssuerConfig
		token    func() string
		accepted bool
	}{
		{"a valid token", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"iss": idp.Issuer()}))
		}, true},

		// The issuer is compared exactly.
		{"another issuer", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"iss": "https://elsewhere.example/realm"}))
		}, false},
		{"the issuer with a trailing slash", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"iss": idp.Issuer() + "/"}))
		}, false},
		{"no issuer", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"iss": nil}))
		}, false},

		// The audience is always required; the PDP's own is the one when the configuration names none.
		{"no aud", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"aud": nil}))
		}, false},
		{"another aud", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"aud": "someone-else"}))
		}, false},
		{"the PDP's aud when the configuration names others", pepauth.IssuerConfig{Audiences: []string{"pep-api"}}, func() string {
			return rsToken(idp, pdpClaims(nil))
		}, false},
		{"a configured aud as a string", pepauth.IssuerConfig{Audiences: []string{"pep-api", "pep-admin"}}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"aud": "pep-admin"}))
		}, true},
		{"a configured aud in a list", pepauth.IssuerConfig{Audiences: []string{"pep-api"}}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"aud": []string{"other", "pep-api"}}))
		}, true},
		{"a list of audiences none of them configured", pepauth.IssuerConfig{Audiences: []string{"pep-api"}}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"aud": []string{"x", "y"}}))
		}, false},
		{"a list of audiences with a value that is not a string", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"aud": []any{testPDP, 7}}))
		}, false},

		// exp is required, nbf is checked, both with a leeway of a minute.
		{"an expired token", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"exp": at(-time.Hour)}))
		}, false},
		{"a token that expired 59 seconds ago", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"exp": at(-59 * time.Second)}))
		}, true},
		{"a token that expired 61 seconds ago", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"exp": at(-61 * time.Second)}))
		}, false},
		{"no exp", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"exp": nil}))
		}, false},
		{"nbf in the future by 59 seconds", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"nbf": at(59 * time.Second)}))
		}, true},
		{"nbf in the future by 61 seconds", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(jwt.MapClaims{"nbf": at(61 * time.Second)}))
		}, false},

		// The algorithms: none and HMAC are never accepted, the others only when the configuration lists them.
		{"alg none", pepauth.IssuerConfig{}, func() string { return none("none") }, false},
		{"alg none listed in the configuration", pepauth.IssuerConfig{Algs: []string{"none", "RS256"}}, func() string {
			return none("none")
		}, false},
		{"HS256 signed with the public key of the provider", pepauth.IssuerConfig{}, func() string {
			return hmacWithPublicKey("HS256")
		}, false},
		{"HS256 listed in the configuration", pepauth.IssuerConfig{Algs: []string{"HS256", "RS256"}}, func() string {
			return hmacWithPublicKey("HS256")
		}, false},
		{"ES256 when the configuration lists only RS256", pepauth.IssuerConfig{Algs: []string{"RS256"}}, func() string {
			return idp.SignToken(pdpClaims(nil), support.SignedWith("ES256"))
		}, false},
		{"RS256 when the configuration lists only ES256", pepauth.IssuerConfig{Algs: []string{"ES256"}}, func() string {
			return rsToken(idp, pdpClaims(nil))
		}, false},
		{"RS256 by default", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(nil))
		}, true},
		{"ES256 by default", pepauth.IssuerConfig{}, func() string {
			return idp.SignToken(pdpClaims(nil), support.SignedWith("ES256"))
		}, true},
		{"EdDSA by default", pepauth.IssuerConfig{}, func() string {
			return idp.SignToken(pdpClaims(nil), support.SignedWith("EdDSA"))
		}, true},

		// The typ is read only when the configuration requires an access token.
		{"typ is required and missing", pepauth.IssuerConfig{RequireTyp: true}, func() string {
			return rsToken(idp, pdpClaims(nil), support.HeaderType(""))
		}, false},
		{"typ is required and JWT", pepauth.IssuerConfig{RequireTyp: true}, func() string {
			return rsToken(idp, pdpClaims(nil), support.HeaderType("JWT"))
		}, false},
		{"typ is required and at+jwt", pepauth.IssuerConfig{RequireTyp: true}, func() string {
			return rsToken(idp, pdpClaims(nil), support.HeaderType("at+jwt"))
		}, true},
		{"typ is required and the media type of at+jwt", pepauth.IssuerConfig{RequireTyp: true}, func() string {
			return rsToken(idp, pdpClaims(nil), support.HeaderType("application/at+jwt"))
		}, true},
		{"typ is not read when it is not required", pepauth.IssuerConfig{}, func() string {
			return rsToken(idp, pdpClaims(nil), support.HeaderType(""))
		}, true},

		// The key: a kid the provider does not publish, and a signature over other claims than the ones sent.
		{"a kid the provider does not publish", pepauth.IssuerConfig{}, func() string {
			return craftToken(t, map[string]any{"alg": "RS256", "kid": "k999", "typ": "JWT"},
				map[string]any{"iss": idp.Issuer(), "sub": "pep-service", "aud": testPDP, "exp": at(time.Hour)}, "c2ln")
		}, false},
		{"claims changed after signing", pepauth.IssuerConfig{}, func() string {
			parts := strings.Split(rsToken(idp, pdpClaims(nil)), ".")
			forged := encodeJSON(t, map[string]any{"iss": idp.Issuer(), "sub": "admin", "aud": testPDP, "exp": at(time.Hour)})
			return parts[0] + "." + forged + "." + parts[2]
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := verifierOf(idp, c.cfg).Verify(t.Context(), c.token())
			if c.accepted {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got.Issuer != idp.Issuer() {
					t.Fatalf("issuer %q, want the provider's", got.Issuer)
				}
				return
			}
			if !errors.Is(err, pepauth.ErrTokenInvalid) {
				t.Fatalf("err = %v, want ErrTokenInvalid", err)
			}
		})
	}
}

// TestContract_PEP01_ClientAndSubjectOfAToken reads who presents a token the way the check API reads a person and a client.
func TestContract_PEP01_ClientAndSubjectOfAToken(t *testing.T) {
	idp := idpWithKeys(t)
	cases := []struct {
		name        string
		claims      jwt.MapClaims
		wantSubject string
		wantClient  string
		accepted    bool
	}{
		{"the client is the azp", pdpClaims(jwt.MapClaims{"azp": "pep-a", "client_id": "pep-b", "sub": "s1"}), "s1", "pep-a", true},
		{"the client is the client_id without an azp", pdpClaims(jwt.MapClaims{"client_id": "pep-b", "sub": "s1"}), "s1", "pep-b", true},
		{"the client is the sub without an azp or a client_id", pdpClaims(jwt.MapClaims{"sub": "pep-c"}), "pep-c", "pep-c", true},
		{"the subject is the preferred_username when it is set", pdpClaims(jwt.MapClaims{"sub": "s1", "preferred_username": "alice"}), "alice", "s1", true},
		{"a token with no subject is refused", pdpClaims(jwt.MapClaims{"sub": nil}), "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := verifierOf(idp, pepauth.IssuerConfig{}).Verify(t.Context(), rsToken(idp, c.claims))
			if !c.accepted {
				if !errors.Is(err, pepauth.ErrTokenInvalid) {
					t.Fatalf("err = %v, want ErrTokenInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got.Subject != c.wantSubject || got.Client != c.wantClient {
				t.Fatalf("subject %q, client %q; want %q, %q", got.Subject, got.Client, c.wantSubject, c.wantClient)
			}
		})
	}
}

// TestVerifyReturnsACopyOfTheClaims: the claims kept for the next verification of a token are not changed by a caller.
func TestVerifyReturnsACopyOfTheClaims(t *testing.T) {
	idp := idpWithKeys(t)
	v := verifierOf(idp, pepauth.IssuerConfig{})
	token := rsToken(idp, pdpClaims(jwt.MapClaims{
		"groups":  []any{"sales"},
		"context": map[string]any{"region": "eu"},
	}))
	first, err := v.Verify(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	// The top level and the nested values: a caller may change any of them, the cache must keep the token as it was verified.
	first.Claims["sub"] = "changed by a caller"
	first.Claims["groups"].([]any)[0] = "changed by a caller"
	first.Claims["context"].(map[string]any)["region"] = "changed by a caller"
	second, err := v.Verify(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if second.Subject != "pep-service" || second.Claims["sub"] != "pep-service" {
		t.Fatalf("the second verification sees %q, %v; the first caller changed the cache", second.Subject, second.Claims["sub"])
	}
	if got := second.Claims["groups"].([]any)[0]; got != "sales" {
		t.Errorf("the nested list is %v after a caller changed it, want sales", got)
	}
	if got := second.Claims["context"].(map[string]any)["region"]; got != "eu" {
		t.Errorf("the nested object is %v after a caller changed it, want eu", got)
	}
}
