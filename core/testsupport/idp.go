package support

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net"
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
// Issuer(), JWKSURL() and the other addresses verifies its tokens against it. Its first key is RSA (RS256); AddKey adds
// an ECDSA P-256 key (ES256) or an Ed25519 key (EdDSA).
type IdP struct {
	server *httptest.Server
	// base is the address the provider is reached at: its own, or the one containers use to reach the host (NewIdPForContainers).
	base string

	mu       sync.Mutex
	keys     []*idpKey // the last one signs; a token signed with an algorithm takes the last key of that algorithm
	rotation int
	delay    time.Duration
	status   int

	jwksRequests atomic.Int64
}

// idpKey is one signing key of the provider.
type idpKey struct {
	kid    string
	alg    string
	method jwt.SigningMethod
	signer crypto.Signer
}

// NewIdP starts a fake provider with one key (kid "k1"); it stops with the test.
func NewIdP(t testing.TB) *IdP { return newIdP(t, "") }

// NewIdPForContainers starts the provider on all interfaces and names itself by publicHost (for example host.docker.internal), so
// that services that run in containers reach it at the addresses it gives out.
func NewIdPForContainers(t testing.TB, publicHost string) *IdP { return newIdP(t, publicHost) }

func newIdP(t testing.TB, publicHost string) *IdP {
	t.Helper()
	i := &IdP{}
	i.addKey("RS256")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jwks", i.serveJWKS)
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	if publicHost == "" {
		i.server = httptest.NewServer(mux)
		i.base = i.server.URL
	} else {
		ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp4", "0.0.0.0:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		i.server = httptest.NewUnstartedServer(mux)
		i.server.Listener = ln
		i.server.Start()
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		i.base = "http://" + net.JoinHostPort(publicHost, port)
	}
	t.Cleanup(i.server.Close)
	return i
}

// addKey adds a key of the algorithm under the next kid (k1, k2, ...). The caller holds mu, except while the provider is made.
func (i *IdP) addKey(alg string) *idpKey {
	i.rotation++
	k := newKey(fmt.Sprintf("k%d", i.rotation), alg)
	i.keys = append(i.keys, k)
	return k
}

// newKey makes a signing key of the algorithm: RS256 (2048-bit RSA), ES256 (P-256) or EdDSA (Ed25519).
func newKey(kid, alg string) *idpKey {
	var (
		signer crypto.Signer
		method jwt.SigningMethod
		err    error
	)
	switch alg {
	case "RS256":
		signer, err = rsa.GenerateKey(rand.Reader, 2048)
		method = jwt.SigningMethodRS256
	case "ES256":
		signer, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		method = jwt.SigningMethodES256
	case "EdDSA":
		_, signer, err = ed25519.GenerateKey(rand.Reader)
		method = jwt.SigningMethodEdDSA
	default:
		panic("the fake IdP has no key of algorithm " + alg)
	}
	if err != nil {
		panic(err) // the test cannot go on without a key
	}
	return &idpKey{kid: kid, alg: alg, method: method, signer: signer}
}

// URL is the base address of the provider.
func (i *IdP) URL() string { return i.base }

// Issuer is the iss of the tokens; JWKSURL is the address of its key set.
func (i *IdP) Issuer() string  { return i.base + "/realm" }
func (i *IdP) JWKSURL() string { return i.base + "/jwks" }

// AppURL and IconURL answer 200, for a service that is registered with an address and an icon.
func (i *IdP) AppURL() string  { return i.base + "/app" }
func (i *IdP) IconURL() string { return i.base + "/icon.png" }

// Token signs a token with the current key. The issuer and a validity of an hour are added when claims has none.
func (i *IdP) Token(claims jwt.MapClaims) string { return i.SignToken(claims) }

// TokenValidFor is Token with the given validity (a negative one makes an expired token).
func (i *IdP) TokenValidFor(claims jwt.MapClaims, valid time.Duration) string {
	return i.SignToken(claims, ValidFor(valid))
}

// ClientToken signs the token of a client-credentials grant: the client is the azp, and sub names its service account, so
// the token names no person.
func (i *IdP) ClientToken(clientID string, opts ...TokenOption) string {
	return i.SignToken(jwt.MapClaims{"azp": clientID, "sub": "service-account-" + clientID}, opts...)
}

// TokenOption shapes a token that the provider signs.
type TokenOption func(*tokenShape)

// tokenShape is what the options of a token set.
type tokenShape struct {
	valid time.Duration
	alg   string
	typ   string
	aud   []string
}

// ValidFor is the lifetime of a token whose claims carry no exp (an hour by default).
func ValidFor(valid time.Duration) TokenOption {
	return func(s *tokenShape) { s.valid = valid }
}

// SignedWith signs the token with the last key of the algorithm (AddKey adds one) instead of the last key of the set.
func SignedWith(alg string) TokenOption {
	return func(s *tokenShape) { s.alg = alg }
}

// HeaderType sets the typ of the header, at+jwt for an access token (RFC 9068); "" leaves the header without a typ.
func HeaderType(typ string) TokenOption {
	return func(s *tokenShape) { s.typ = typ }
}

// Audience sets the aud of the token: a string for one value, a list for several. It replaces the aud of the claims.
func Audience(aud ...string) TokenOption {
	return func(s *tokenShape) { s.aud = aud }
}

// SignToken signs the claims. The issuer and the expiry (ValidFor, an hour by default) are added when the claims have
// none; a claim set to nil is left out of the token, which is how a token without exp or aud is made.
func (i *IdP) SignToken(claims jwt.MapClaims, opts ...TokenOption) string {
	shape := tokenShape{valid: time.Hour, typ: "JWT"}
	for _, opt := range opts {
		opt(&shape)
	}
	k := i.signerOf(shape.alg)
	c := jwt.MapClaims{}
	for name, v := range claims {
		c[name] = v
	}
	if _, ok := c["iss"]; !ok {
		c["iss"] = i.Issuer()
	}
	if _, ok := c["exp"]; !ok {
		c["exp"] = time.Now().Add(shape.valid).Unix()
	}
	if shape.aud != nil {
		if len(shape.aud) == 1 {
			c["aud"] = shape.aud[0]
		} else {
			c["aud"] = shape.aud
		}
	}
	for name, v := range c {
		if v == nil {
			delete(c, name)
		}
	}
	tok := jwt.NewWithClaims(k.method, c)
	if shape.typ == "" {
		delete(tok.Header, "typ")
	} else {
		tok.Header["typ"] = shape.typ
	}
	tok.Header["kid"] = k.kid
	signed, err := tok.SignedString(k.signer)
	if err != nil {
		panic(err)
	}
	return signed
}

// signerOf is the last key of the algorithm, or the last key of the set when alg is "".
func (i *IdP) signerOf(alg string) *idpKey {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := len(i.keys) - 1; n >= 0; n-- {
		if alg == "" || i.keys[n].alg == alg {
			return i.keys[n]
		}
	}
	panic("the fake IdP has no key to sign with " + alg + ": AddKey it first")
}

// AddKey adds a key of the algorithm (RS256, ES256 or EdDSA) to the key set under the next kid, makes it the last key and
// returns its kid. The provider publishes it at once.
func (i *IdP) AddKey(alg string) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.addKey(alg).kid
}

// RotateKey makes a new RSA key with a new kid the one that signs from now on, and returns its kid. With keepOld the
// previous keys stay in the JWKS (as a provider does while a rotation settles); without it they are dropped and the
// tokens they signed stop verifying.
func (i *IdP) RotateKey(keepOld bool) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !keepOld {
		i.keys = nil
	}
	return i.addKey("RS256").kid
}

// KeyID is the kid of the key that signs now.
func (i *IdP) KeyID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.keys[len(i.keys)-1].kid
}

// PublicKey is the public part of the key that signs now: an *rsa.PublicKey while the default key is the RSA one.
func (i *IdP) PublicKey() crypto.PublicKey {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.keys[len(i.keys)-1].signer.Public()
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
		set.Keys = append(set.Keys, jose.JSONWebKey{Key: k.signer.Public(), KeyID: k.kid, Algorithm: k.alg, Use: "sig"})
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
