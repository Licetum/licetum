//go:build integration

package api_test

// Integration tests of the authentication of PEP clients on the AuthZEN routes (ADR 0001, 7.10): the API keys and the tokens of
// a registered issuer, the one refusal that tells nothing about the services, and the person of a question coming from the body.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	support "github.com/licetum/licetum/core/testsupport"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

const pepAudience = "licetum-pdp-test"

// pepClient registers a client of the service, as the admin routes of P2-16 will, and tells the app of the change.
func (w *world) pepClient(serviceID, name, kind string, operations ...string) db.PEPClient {
	w.t.Helper()
	c := db.PEPClient{ServiceID: uuid.MustParse(serviceID), Name: name, Kind: kind, Operations: operations, Enabled: true}
	return w.registerPEPClient(c)
}

func (w *world) registerPEPClient(c db.PEPClient) db.PEPClient {
	w.t.Helper()
	stored := support.RegisterPEPClient(w.t, w.pool, c)
	w.app.AfterPEPWrite()
	return stored
}

// pepKey issues a key to the client.
func (w *world) pepKey(c db.PEPClient) (key, keyID string) {
	w.t.Helper()
	key, keyID = support.IssueTestAPIKey(w.t, w.pool, c.ID)
	w.app.AfterPEPWrite()
	return key, keyID
}

// pepIssuer registers a provider whose tokens a PEP may present, and returns it.
func (w *world) pepIssuer() *support.IdP {
	w.t.Helper()
	idp := support.NewIdP(w.t)
	support.RegisterPEPIssuer(w.t, w.pool, db.PEPIssuer{
		Issuer: idp.Issuer(), JWKSURL: idp.JWKSURL(), Audiences: []string{pepAudience}, Algs: []string{"RS256"}, RequireTyp: true,
	})
	w.app.AfterPEPWrite()
	return idp
}

// pepToken is the access token of the client application of the provider.
func pepToken(idp *support.IdP, clientID string, opts ...support.TokenOption) string {
	return idp.ClientToken(clientID, append([]support.TokenOption{support.Audience(pepAudience), support.HeaderType("at+jwt")}, opts...)...)
}

// evaluate asks whether the person may read the branch, as the holder of the credential, of the service named by ref.
func (w *world) evaluate(ref, credential, person string) (int, http.Header, []byte) {
	w.t.Helper()
	return w.askWithHeader(authzenreq.Evaluation(ref, person, authzenreq.On("branch", "B1"), "read"), credential)
}

// decisionOf is the decision of a 200 answer of the single evaluation.
func (w *world) decisionOf(status int, raw []byte) bool {
	w.t.Helper()
	if status != http.StatusOK {
		w.t.Fatalf("evaluation = %d (%s), want 200", status, raw)
	}
	return decodeMap(w.t, raw)["decision"].(bool)
}

// TestContract_AZ09_EveryRefusalOfAValidCredentialIsTheSame403: a credential that authenticates but gives nothing on the service gets
// one answer, whatever the reason (no such service, a client of another service, an operation it lacks, a token whose client is not
// registered), so a caller learns nothing about which services exist or whom they have clients (ADR 0001, 7.10).
func TestContract_AZ09_EveryRefusalOfAValidCredentialIsTheSame403(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)

	orgClient := w.pepClient(w.org, "org-evaluator", db.PEPKindAPIKey, engine.PEPEvaluate)
	orgKey, _ := w.pepKey(orgClient)
	appKey, _ := w.pepKey(w.pepClient(w.appSvc, "app-evaluator", db.PEPKindAPIKey, engine.PEPEvaluate))
	searchKey, _ := w.pepKey(w.pepClient(w.org, "org-searcher", db.PEPKindAPIKey, engine.PEPSearchResource))

	idp := w.pepIssuer()
	issuer := idp.Issuer()
	registered := "checkout"
	w.registerPEPClient(db.PEPClient{ServiceID: uuid.MustParse(w.org), Name: "org-jwt", Kind: db.PEPKindJWT, Issuer: &issuer,
		ClientID: &registered, Operations: []string{engine.PEPEvaluate}, Enabled: true})

	if status, _, raw := w.evaluate("org", orgKey, hierarchySubject); !w.decisionOf(status, raw) {
		t.Fatalf("the client of org may read B1 for %s: %s", hierarchySubject, raw)
	}

	type refusal struct{ name, ref, credential string }
	refusals := []refusal{
		{"no such service", "nowhere", orgKey},
		{"no such service, by id", uuid.NewString(), orgKey},
		{"a client of another service", "org", appKey},
		{"the same client on another service", "app", orgKey},
		{"an operation the client lacks", "org", searchKey},
		{"a token whose client is not registered", "org", pepToken(idp, "stranger")},
		{"a token of a client of no service", "app", pepToken(idp, registered)},
	}
	var first struct {
		status int
		code   string
		body   string
	}
	for i, r := range refusals {
		status, header, raw := w.evaluate(r.ref, r.credential, hierarchySubject)
		requireAuthzenError(t, status, header, raw, http.StatusForbidden, "authzen_forbidden")
		if strings.Contains(strings.ToLower(string(raw)), "service") && strings.Contains(string(raw), r.ref) {
			t.Errorf("%s: the answer names the service: %s", r.name, raw)
		}
		if i == 0 {
			first.status, first.code, first.body = status, header.Get("Licetum-Error-Code"), string(raw)
			continue
		}
		if status != first.status || header.Get("Licetum-Error-Code") != first.code || string(raw) != first.body {
			t.Errorf("%s: %d %s %s differs from the first refusal %d %s %s", r.name, status, header.Get("Licetum-Error-Code"), raw,
				first.status, first.code, first.body)
		}
	}
	if status, _, raw := w.evaluate("org", pepToken(idp, registered), hierarchySubject); !w.decisionOf(status, raw) {
		t.Fatalf("the registered client of the provider may read B1: %s", raw)
	}
}

// An API key authenticates its client; the person of the question is the one the body names, never the client.
func TestPEPAPIKeyAuthenticatesAndTheBodyNamesThePerson(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	client := w.pepClient(w.org, "evaluator", db.PEPKindAPIKey, engine.PEPEvaluate)
	key, keyID := w.pepKey(client)

	status, _, raw := w.evaluate("org", key, hierarchySubject)
	if !w.decisionOf(status, raw) {
		t.Fatalf("%s may read B1: %s", hierarchySubject, raw)
	}
	status, _, raw = w.evaluate("org", key, "somebody-else")
	if w.decisionOf(status, raw) {
		t.Fatal("somebody else, who has no rule, may read B1")
	}
	// The slug and the id of the service name the same service.
	status, _, raw = w.evaluate(w.org, key, hierarchySubject)
	if !w.decisionOf(status, raw) {
		t.Fatalf("the service by id: %s", raw)
	}

	// A key whose secret is wrong, one whose id nobody holds, and one that is not a key at all, are not authenticated.
	secret := key[len("lct_")+16+1:] // the secret is base64url, which has underscores of its own
	flipped := "A" + secret[1:]
	if secret[0] == 'A' {
		flipped = "B" + secret[1:]
	}
	for name, credential := range map[string]string{
		"a wrong secret":     "lct_" + keyID + "_" + flipped,
		"an all-zero secret": "lct_" + keyID + "_" + strings.Repeat("A", 43),
		"an unknown key id":  "lct_0000000000000000_" + secret,
		"a truncated key":    key[:10],
		"only the prefix":    "lct_",
	} {
		status, header, raw := w.evaluate("org", credential, hierarchySubject)
		if status != http.StatusUnauthorized {
			t.Errorf("%s: %d (%s), want 401", name, status, raw)
		}
		if got := header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") || !strings.Contains(got, `error="invalid_token"`) {
			t.Errorf("%s: WWW-Authenticate = %q", name, got)
		}
	}

	// A revoked key is refused once the app has read the directory again.
	if err := db.RevokeAPIKey(t.Context(), w.pool, keyID); err != nil {
		t.Fatal(err)
	}
	w.app.AfterPEPWrite()
	if status, _, raw := w.evaluate("org", key, hierarchySubject); status != http.StatusUnauthorized {
		t.Fatalf("a revoked key = %d (%s), want 401", status, raw)
	}
}

// A token of an issuer registered for PEPs authenticates the client it was issued to. The audience is mandatory and the issuer's.
func TestPEPTokenOfARegisteredIssuerAuthenticatesItsClient(t *testing.T) {
	w := newWorld(t)
	w.rule("division", "D1", nil)
	idp := w.pepIssuer()
	issuer := idp.Issuer()
	checkout := "checkout"
	w.registerPEPClient(db.PEPClient{ServiceID: uuid.MustParse(w.org), Name: "checkout", Kind: db.PEPKindJWT, Issuer: &issuer,
		ClientID: &checkout, Operations: []string{engine.PEPEvaluate}, Enabled: true})

	if status, _, raw := w.evaluate("org", pepToken(idp, checkout), hierarchySubject); !w.decisionOf(status, raw) {
		t.Fatalf("the client may read B1: %s", raw)
	}
	if status, _, raw := w.evaluate("org", pepToken(idp, checkout), "somebody-else"); w.decisionOf(status, raw) {
		t.Fatal("somebody else may read B1")
	}

	other := support.NewIdP(t)
	for name, token := range map[string]string{
		"another audience": pepToken(idp, checkout, support.Audience("some-other-service")),
		"no audience":      idp.ClientToken(checkout, support.HeaderType("at+jwt")),
		"not an at+jwt":    pepToken(idp, checkout, support.HeaderType("JWT")),
		"an expired token": pepToken(idp, checkout, support.ValidFor(-time.Hour)),
		"another issuer":   other.ClientToken(checkout, support.Audience(pepAudience), support.HeaderType("at+jwt")),
	} {
		status, header, raw := w.evaluate("org", token, hierarchySubject)
		if status != http.StatusUnauthorized {
			t.Errorf("%s: %d (%s), want 401", name, status, raw)
		}
		if got := header.Get("WWW-Authenticate"); !strings.Contains(got, `error="invalid_token"`) {
			t.Errorf("%s: WWW-Authenticate = %q", name, got)
		}
	}

	// A person's token still works where no PEP client is involved: until the person's way goes (stage 2c) it is the token of the service.
	status, header, raw := w.evaluate("org", w.token(jwt.MapClaims{"preferred_username": hierarchySubject}), hierarchySubject)
	if !w.decisionOf(status, raw) {
		t.Fatalf("the holder of a person's token may read B1: %d %v %s", status, header, raw)
	}
}
