package support

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

// IdP is a fake identity provider for the tests: it serves the JWKS of its signing keys, counts how often it is
// asked, rotates its keys (a new kid) and signs the tokens the tests present. A service registered with
// Issuer(), JWKSURL() and the other addresses verifies its tokens against it.
type IdP struct {
	server *httptest.Server

	mu       sync.Mutex
	keys     []*idpKey // the last one signs
	rotation int
	delay    time.Duration
	status   int

	jwksRequests atomic.Int64
}

type idpKey struct {
	kid string
	key *rsa.PrivateKey
}

// NewIdP starts a fake provider with one key (kid "k1"); it stops with the test.
func NewIdP(t testing.TB) *IdP {
	t.Helper()
	i := &IdP{}
	i.addKey()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jwks", i.serveJWKS)
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	i.server = httptest.NewServer(mux)
	t.Cleanup(i.server.Close)
	return i
}

func (i *IdP) addKey() *idpKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err) // the test cannot go on without a key
	}
	i.rotation++
	k := &idpKey{kid: fmt.Sprintf("k%d", i.rotation), key: key}
	i.keys = append(i.keys, k)
	return k
}

// URL is the base address of the provider.
func (i *IdP) URL() string { return i.server.URL }

// Issuer is the iss of the tokens; AuthorizationURL, TokenURL and JWKSURL are the addresses a service is registered with.
func (i *IdP) Issuer() string           { return i.server.URL + "/realm" }
func (i *IdP) AuthorizationURL() string { return i.server.URL + "/realm/auth" }
func (i *IdP) TokenURL() string         { return i.server.URL + "/realm/token" }
func (i *IdP) JWKSURL() string          { return i.server.URL + "/jwks" }

// AppURL and IconURL answer 200, for a service that is registered with an address and an icon.
func (i *IdP) AppURL() string  { return i.server.URL + "/app" }
func (i *IdP) IconURL() string { return i.server.URL + "/icon.png" }

// Token signs a token with the current key. The issuer and a validity of an hour are added when claims has none.
func (i *IdP) Token(claims jwt.MapClaims) string { return i.TokenValidFor(claims, time.Hour) }

// TokenValidFor is Token with the given validity (a negative one makes an expired token).
func (i *IdP) TokenValidFor(claims jwt.MapClaims, valid time.Duration) string {
	i.mu.Lock()
	k := i.keys[len(i.keys)-1]
	i.mu.Unlock()
	c := jwt.MapClaims{}
	for name, v := range claims {
		c[name] = v
	}
	if _, ok := c["iss"]; !ok {
		c["iss"] = i.Issuer()
	}
	if _, ok := c["exp"]; !ok {
		c["exp"] = time.Now().Add(valid).Unix()
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = k.kid
	signed, err := tok.SignedString(k.key)
	if err != nil {
		panic(err)
	}
	return signed
}

// RotateKey makes a new key with a new kid the one that signs from now on, and returns its kid. With keepOld the
// previous keys stay in the JWKS (as a provider does while a rotation settles); without it they are dropped and the
// tokens they signed stop verifying.
func (i *IdP) RotateKey(keepOld bool) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !keepOld {
		i.keys = nil
	}
	return i.addKey().kid
}

// KeyID is the kid of the key that signs now.
func (i *IdP) KeyID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.keys[len(i.keys)-1].kid
}

// JWKSRequests is how many times the JWKS was asked for since the start or the last ResetCounters.
func (i *IdP) JWKSRequests() int { return int(i.jwksRequests.Load()) }

// ResetCounters forgets the requests counted so far.
func (i *IdP) ResetCounters() { i.jwksRequests.Store(0) }

// SetJWKSDelay makes the JWKS answer wait d; SetJWKSStatus makes it fail with the status (0 turns it off).
func (i *IdP) SetJWKSDelay(d time.Duration) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.delay = d
}

func (i *IdP) SetJWKSStatus(code int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.status = code
}

func (i *IdP) serveJWKS(w http.ResponseWriter, r *http.Request) {
	i.jwksRequests.Add(1)
	i.mu.Lock()
	delay, status := i.delay, i.status
	var set jose.JSONWebKeySet
	for _, k := range i.keys {
		set.Keys = append(set.Keys, jose.JSONWebKey{Key: &k.key.PublicKey, KeyID: k.kid, Algorithm: "RS256", Use: "sig"})
	}
	i.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(set)
}
