package pepauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrTokenInvalid is the rejection of a token: its algorithm, its typ, its key, its issuer, its audience, its times or its
// signature. It says nothing about the provider; a key set that cannot be read is an Unavailable (ErrJWKSUnavailable).
var ErrTokenInvalid = errors.New("pepauth: token invalid")

// clockLeeway is the tolerance of exp and nbf against the clocks of the provider and of the PDP.
const clockLeeway = time.Minute

// defaultAlgs are the algorithms accepted when the configuration names none.
var defaultAlgs = []string{"RS256", "ES256", "EdDSA"}

// supportedAlgs are the algorithms a verifier can check. none and the HMAC algorithms are not among them: a token signed
// with a shared secret is never a PEP's token.
var supportedAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

// ecCurves are the curves the keys of the ECDSA algorithms are on.
var ecCurves = map[string]string{"ES256": "P-256", "ES384": "P-384", "ES512": "P-521"}

// IssuerConfig is what a Verifier needs to know of one issuer.
type IssuerConfig struct {
	// Issuer is the iss the tokens carry, compared exactly.
	Issuer string
	// JWKSURL is where the keys of the issuer are read.
	JWKSURL string
	// Audiences are the values one of which the aud of a token must contain. When it is empty, the audience of the PDP is
	// the one.
	Audiences []string
	// Algs are the signature algorithms accepted. When it is empty, RS256, ES256 and EdDSA are. An algorithm the verifier
	// does not know (none and the HMAC algorithms among them) is dropped from the list, whatever the configuration says.
	Algs []string
	// RequireTyp refuses a token whose typ header is not at+jwt (RFC 9068). Without it the header is not read.
	RequireTyp bool
}

// Verified is the result of a successful verification.
type Verified struct {
	// Subject is the preferred_username of the token when it has one, else its sub.
	Subject string
	// Client is the client application the token was issued to: its azp, else its client_id, else its sub.
	Client string
	// Issuer is the iss of the token, the issuer the verifier was made for.
	Issuer string
	// Claims are the claims of the token. The verifier returns a copy each time: the caller may change it.
	Claims map[string]any
}

// Verifier verifies the tokens of one issuer against the key set of the issuer. It keeps the tokens it verified for a short
// time; it is safe for concurrent use.
type Verifier struct {
	issuer     string
	jwksURL    string
	audiences  []string
	algs       []string
	requireTyp bool
	keys       *KeySets
	cache      tokenCache
}

// NewVerifier makes the verifier of one issuer. keys is the key set of the process, shared by the verifiers, and must not be
// nil. pdpAudience is the audience a token must carry when cfg names none.
func NewVerifier(cfg IssuerConfig, keys *KeySets, pdpAudience string) *Verifier {
	audiences := slices.Clone(cfg.Audiences)
	if len(audiences) == 0 && pdpAudience != "" {
		audiences = []string{pdpAudience}
	}
	audiences = slices.DeleteFunc(audiences, func(s string) bool { return s == "" })

	algs := cfg.Algs
	if len(algs) == 0 {
		algs = defaultAlgs
	}
	var accepted []string
	for _, alg := range algs {
		if slices.Contains(supportedAlgs, alg) && !slices.Contains(accepted, alg) {
			accepted = append(accepted, alg)
		}
	}
	return &Verifier{
		issuer:     cfg.Issuer,
		jwksURL:    cfg.JWKSURL,
		audiences:  audiences,
		algs:       accepted,
		requireTyp: cfg.RequireTyp,
		keys:       keys,
	}
}

// Verify checks a token: its signature with the key the issuer published under its kid, its algorithm, its typ, its issuer,
// its audience, its exp (required) and its nbf, each with a leeway of a minute. A rejection of the token is an error that
// matches ErrTokenInvalid; a key set that cannot be read is an Unavailable, and the token is neither accepted nor refused.
func (v *Verifier) Verify(ctx context.Context, token string) (Verified, error) {
	now := time.Now()
	digest := sha256.Sum256([]byte(token))
	if hit, ok := v.cache.get(digest, now); ok {
		hit.Claims = cloneClaims(hit.Claims)
		return hit, nil
	}
	out, exp, err := v.verify(ctx, token)
	if err != nil {
		return Verified{}, err
	}
	if ttl := min(verifiedTTL, exp.Sub(now)); ttl > 0 {
		kept := out
		kept.Claims = cloneClaims(out.Claims)
		v.cache.put(digest, kept, now.Add(ttl), now)
	}
	return out, nil
}

// verify is Verify without the cache: it returns the token's verification and its exp.
func (v *Verifier) verify(ctx context.Context, token string) (Verified, time.Time, error) {
	if len(v.algs) == 0 {
		// Nothing is accepted: a nil list would accept every algorithm of the parser, so this is refused before it.
		return Verified{}, time.Time{}, invalid("no signature algorithm is accepted")
	}
	var keyErr error
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(v.algs),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithJSONNumber(),
	)
	_, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		key, err := v.keyFor(ctx, t)
		keyErr = err
		return key, err
	})
	if keyErr != nil {
		return Verified{}, time.Time{}, keyErr
	}
	if err != nil {
		return Verified{}, time.Time{}, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	iss, _ := claims["iss"].(string)
	if v.issuer == "" || iss != v.issuer {
		return Verified{}, time.Time{}, invalid("the issuer is not the configured one")
	}
	if !audienceIn(claims["aud"], v.audiences) {
		return Verified{}, time.Time{}, invalid("the audience is not one of the PDP's")
	}
	subject, client := identities(claims)
	if subject == "" {
		return Verified{}, time.Time{}, invalid("the token names no subject")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return Verified{}, time.Time{}, invalid("the token has no usable exp")
	}
	return Verified{Subject: subject, Client: client, Issuer: iss, Claims: claims}, exp.Time, nil
}

// keyFor is the key that verifies the token: the typ the configuration requires, then the key of the issuer under the kid the
// header names, checked against the algorithm. It runs before the signature is checked, and its failures are returned as
// they are: an Unavailable stays one.
func (v *Verifier) keyFor(ctx context.Context, t *jwt.Token) (any, error) {
	if v.requireTyp && !atJWT(t.Header["typ"]) {
		return nil, invalid("the typ is not at+jwt")
	}
	kid, hasKid := t.Header["kid"]
	key, err := v.keys.KeyFor(ctx, v.jwksURL, kid, hasKid)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, invalid("the issuer publishes no key with this kid")
	}
	jwk, err := key.Parsed()
	if err != nil {
		return nil, err
	}
	alg := t.Method.Alg()
	if jwk.Algorithm != "" && jwk.Algorithm != alg {
		return nil, invalid("the key is for another algorithm")
	}
	if jwk.Use != "" && jwk.Use != "sig" {
		return nil, invalid("the key is not for signatures")
	}
	if !keyFits(alg, jwk.Key) {
		return nil, invalid("the key is not of the type of the algorithm")
	}
	return jwk.Key, nil
}

// keyFits reports whether the public key is of the type the algorithm signs with: RSA for RS and PS, the curve of the
// algorithm for ES, Ed25519 for EdDSA.
func keyFits(alg string, key any) bool {
	switch {
	case alg == "EdDSA":
		_, ok := key.(ed25519.PublicKey)
		return ok
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		_, ok := key.(*rsa.PublicKey)
		return ok
	case strings.HasPrefix(alg, "ES"):
		k, ok := key.(*ecdsa.PublicKey)
		return ok && k.Curve != nil && k.Curve.Params().Name == ecCurves[alg]
	}
	return false
}

// atJWT reports whether a typ header is the one of an access token (RFC 9068): at+jwt, or its media type.
func atJWT(typ any) bool {
	s, _ := typ.(string)
	return strings.EqualFold(s, "at+jwt") || strings.EqualFold(s, "application/at+jwt")
}

// audienceIn reports whether aud, a string or a list of strings, contains one of the audiences. A list with a value that is
// not a string is malformed and matches nothing.
func audienceIn(aud any, audiences []string) bool {
	switch a := aud.(type) {
	case string:
		return slices.Contains(audiences, a)
	case []any:
		hit := false
		for _, x := range a {
			s, ok := x.(string)
			if !ok {
				return false
			}
			hit = hit || slices.Contains(audiences, s)
		}
		return hit
	}
	return false
}

// identities reads the subject and the client of a verified token as the AuthZEN API reads them: the subject is preferred_username when it is set, else sub; the client is azp, else client_id, else sub.
func identities(claims jwt.MapClaims) (subject, client string) {
	sub := stringClaim(claims, "sub")
	subject = stringClaim(claims, "preferred_username")
	if subject == "" {
		subject = sub
	}
	client = stringClaim(claims, "azp")
	if client == "" {
		client = stringClaim(claims, "client_id")
	}
	if client == "" {
		client = sub
	}
	return subject, client
}

// stringClaim is the value of a claim that is a string, "" otherwise.
func stringClaim(claims jwt.MapClaims, name string) string {
	s, _ := claims[name].(string)
	return s
}

// cloneClaims copies the claims down to the nested objects and lists, so that a caller that changes them changes neither the
// cache nor the claims of another caller. The other values are immutable (strings, numbers, booleans and nil).
func cloneClaims(claims map[string]any) map[string]any {
	if claims == nil {
		return nil
	}
	out := make(map[string]any, len(claims))
	for k, v := range claims {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		return cloneClaims(v)
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return v
	}
}

// invalid is the rejection of a token for the reason given.
func invalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrTokenInvalid, reason)
}
