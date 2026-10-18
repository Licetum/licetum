package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/redisx"
)

const (
	DefaultTokenCacheTTL = 300 * time.Second
	pkceKeyPrefix        = "authz:pkce:"
	pkceTTL              = 600 * time.Second
	tokenExchangeTimeout = 10 * time.Second
)

// ---------- state ----------

// OAuthState is the content of the signed (HS256) state parameter: it protects the callback from
// CSRF and carries redirect_to and the nonce (the Redis key of the PKCE code_verifier) through the IdP.
type OAuthState struct {
	ServiceID  uuid.UUID
	RedirectTo *string
	Nonce      string
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *App) CreateState(serviceID uuid.UUID, redirectTo *string, nonce string) (string, error) {
	if nonce == "" {
		nonce = randomToken(16)
	}
	claims := jwt.MapClaims{
		"service_id":  serviceID.String(),
		"redirect_to": redirectTo,
		"nonce":       nonce,
		"exp":         time.Now().Add(a.Cfg.Auth.StateTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(a.Cfg.Auth.StateSecret))
}

func (a *App) VerifyState(state string) (*OAuthState, error) {
	invalid := apperr.BadRequest(i18n.OAuthStateInvalid)
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(state, claims, func(*jwt.Token) (any, error) {
		return []byte(a.Cfg.Auth.StateSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, invalid.Wrap(err)
	}

	rawID, _ := claims["service_id"].(string)
	serviceID, err := uuid.Parse(rawID)
	if err != nil {
		return nil, invalid.Wrap(err)
	}
	nonce, _ := claims["nonce"].(string)
	if nonce == "" {
		return nil, invalid
	}
	out := &OAuthState{ServiceID: serviceID, Nonce: nonce}
	if s, ok := claims["redirect_to"].(string); ok {
		out.RedirectTo = &s
	}
	return out, nil
}

// ---------- PKCE ----------

// GeneratePKCEPair returns (code_verifier, code_challenge) per RFC 7636, method S256.
func GeneratePKCEPair() (verifier, challenge string) {
	verifier = randomToken(64) // 86 characters — within 43..128 required by RFC 7636
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:])
}

// ---------- login / callback ----------

// CheckRedirectTo: redirect_to must point to one of the allowed CORS origins. Unlike the plain
// startswith of the Python version, the origin boundary is checked explicitly:
// "http://localhost:5173.evil.com" and "http://localhost:5173@evil.com" no longer pass.
func (a *App) CheckRedirectTo(redirectTo *string) error {
	if redirectTo == nil {
		return nil
	}
	for _, origin := range a.Cfg.Cors.AllowOrigins {
		if redirectMatchesOrigin(*redirectTo, origin) {
			return nil
		}
	}
	return apperr.BadRequest(i18n.OAuthRedirectNotAllowed, *redirectTo)
}

func redirectMatchesOrigin(target, origin string) bool {
	if origin == "" || !strings.HasPrefix(target, origin) {
		return false
	}
	if strings.HasSuffix(origin, "/") || len(target) == len(origin) {
		return true
	}
	switch target[len(origin)] {
	case '/', '?', '#':
		return true
	}
	return false
}

// StartLogin builds the authorization URL of the service IdP. For a public client (no client_secret)
// PKCE is enabled: the code_verifier never reaches the browser — it is stored in Redis under the
// nonce from the state, not in the state itself.
func (a *App) StartLogin(ctx context.Context, serviceID uuid.UUID, redirectURI string, redirectTo *string) (string, error) {
	if err := a.CheckRedirectTo(redirectTo); err != nil {
		return "", err
	}
	svc, err := a.GetExistingService(ctx, a.DB, serviceID)
	if err != nil {
		return "", err
	}
	if svc.AuthorizationURL == nil || *svc.AuthorizationURL == "" {
		return "", apperr.BadRequest(i18n.OAuthNotConfigured, serviceID)
	}

	nonce := randomToken(16)
	params := url.Values{
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"scope":         {"openid profile"},
	}
	if svc.ClientID != nil {
		params.Set("client_id", *svc.ClientID)
	}
	if svc.ClientSecret == nil || *svc.ClientSecret == "" {
		verifier, challenge := GeneratePKCEPair()
		if err := a.Redis.Set(ctx, pkceKeyPrefix+nonce, verifier, pkceTTL).Err(); err != nil {
			return "", err
		}
		params.Set("code_challenge", challenge)
		params.Set("code_challenge_method", "S256")
	}

	state, err := a.CreateState(serviceID, redirectTo, nonce)
	if err != nil {
		return "", err
	}
	params.Set("state", state)

	sep := "?"
	if strings.Contains(*svc.AuthorizationURL, "?") {
		sep = "&"
	}
	return *svc.AuthorizationURL + sep + params.Encode(), nil
}

// CallbackResult is the IdP response to the code exchange and (optionally) where to send the browser.
type CallbackResult struct {
	Token      map[string]any
	RedirectTo *string
}

func (a *App) FinishLogin(ctx context.Context, serviceID uuid.UUID, code, rawState, redirectURI string) (*CallbackResult, error) {
	state, err := a.VerifyState(rawState)
	if err != nil {
		return nil, err
	}
	if state.ServiceID != serviceID {
		return nil, apperr.BadRequest(i18n.OAuthStateServiceMismatch)
	}
	if err := a.CheckRedirectTo(state.RedirectTo); err != nil {
		return nil, err
	}

	svc, err := a.GetExistingService(ctx, a.DB, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.TokenURL == nil || *svc.TokenURL == "" {
		return nil, apperr.NotFound(i18n.OAuthNotConfigured, serviceID)
	}

	var verifier *string
	if v, err := a.Redis.GetDel(ctx, pkceKeyPrefix+state.Nonce).Result(); err == nil {
		verifier = &v
	} else if !errors.Is(err, redis.Nil) {
		return nil, err
	}

	token, err := a.exchangeCode(ctx, svc, code, redirectURI, verifier)
	if err != nil {
		return nil, err
	}
	return &CallbackResult{Token: token, RedirectTo: state.RedirectTo}, nil
}

func (a *App) exchangeCode(ctx context.Context, svc *model.Service, code, redirectURI string, verifier *string) (map[string]any, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	// Exactly one of the two: a confidential client sends client_secret, a public one sends code_verifier (PKCE).
	if verifier != nil {
		form.Set("code_verifier", *verifier)
	}
	status, body, err := a.postTokenEndpoint(ctx, svc, form)
	if err != nil {
		return nil, err
	}

	breakerKey := tokenBreakerKey(svc)
	if status < 200 || status >= 300 {
		redisx.RecordFailure(ctx, a.Redis, breakerKey)
		return nil, apperr.FailedDependency(i18n.TokenExchangeRejected, status, string(body))
	}
	token, err := decodeTokenBody(body)
	if err != nil {
		redisx.RecordFailure(ctx, a.Redis, breakerKey)
		return nil, err
	}
	redisx.RecordSuccess(ctx, a.Redis, breakerKey)
	return token, nil
}

// RefreshTokens renews the tokens by a refresh token (RFC 6749 §6) without sending the user through
// the IdP again. Like login and callback it needs no Bearer: holding the refresh token is the proof.
//
// invalid_grant is not a dependency failure but "sign in again": a 401 that does not count toward
// the circuit breaker. Anything else the IdP rejects (invalid_client after a secret change,
// throttling, 5xx) is a 424 — a configuration or load problem must not log every user out.
func (a *App) RefreshTokens(ctx context.Context, serviceID uuid.UUID, refreshToken string) (*model.TokenResponse, error) {
	svc, err := a.GetExistingService(ctx, a.DB, serviceID)
	if err != nil {
		return nil, err
	}
	status, body, err := a.postTokenEndpoint(ctx, svc, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		return nil, err
	}

	breakerKey := tokenBreakerKey(svc)
	if status >= 200 && status < 300 {
		token, err := decodeTokenBody(body)
		if err != nil {
			return nil, err
		}
		redisx.RecordSuccess(ctx, a.Redis, breakerKey)
		out, ok := tokenResponseFrom(token)
		if !ok {
			return nil, apperr.FailedDependency(i18n.TokenRefreshBadResponse)
		}
		return out, nil
	}
	if isInvalidGrant(status, body) {
		return nil, apperr.InvalidToken(i18n.RefreshTokenInvalid)
	}
	redisx.RecordFailure(ctx, a.Redis, breakerKey)
	return nil, apperr.FailedDependency(i18n.TokenRefreshRejected, status)
}

func tokenBreakerKey(svc *model.Service) string { return "token_url:" + svc.ID.String() }

// postTokenEndpoint is the shared part of token endpoint requests: the circuit breaker, client_id
// and client_secret (a confidential client; a public one has no secret). A transport failure is a
// 424 and counts toward the breaker; interpreting the answer is up to the caller.
func (a *App) postTokenEndpoint(ctx context.Context, svc *model.Service, form url.Values) (int, []byte, error) {
	breakerKey := tokenBreakerKey(svc)
	if redisx.BreakerOpen(ctx, a.Redis, breakerKey) {
		return 0, nil, apperr.FailedDependency(i18n.TokenEndpointBreakerOpen)
	}
	if svc.TokenURL == nil || *svc.TokenURL == "" {
		return 0, nil, apperr.FailedDependency(i18n.OAuthNotConfigured, svc.ID)
	}
	if svc.ClientID != nil {
		form.Set("client_id", *svc.ClientID)
	}
	if svc.ClientSecret != nil && *svc.ClientSecret != "" {
		form.Set("client_secret", *svc.ClientSecret)
	}

	ctx, cancel := context.WithTimeout(ctx, tokenExchangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *svc.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, apperr.FailedDependency(i18n.TokenEndpointUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.HTTP.Do(req)
	if err != nil {
		redisx.RecordFailure(ctx, a.Redis, breakerKey)
		return 0, nil, apperr.FailedDependency(i18n.TokenEndpointUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

func decodeTokenBody(body []byte) (map[string]any, error) {
	var token map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&token); err != nil {
		return nil, apperr.FailedDependency(i18n.TokenEndpointBadJSON, err)
	}
	if token == nil {
		return nil, apperr.FailedDependency(i18n.TokenEndpointBadJSON, errors.New("not a JSON object"))
	}
	return token, nil
}

// isInvalidGrant: per RFC 6749 §5.2 only error=invalid_grant means "the refresh token expired or was
// revoked".
func isInvalidGrant(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnauthorized {
		return false
	}
	var answer struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(body, &answer) == nil && answer.Error == "invalid_grant"
}

// tokenResponseFrom picks the fields the client needs; ok is false without an access token.
func tokenResponseFrom(token map[string]any) (*model.TokenResponse, bool) {
	access, _ := token["access_token"].(string)
	if access == "" {
		return nil, false
	}
	out := &model.TokenResponse{AccessToken: access, TokenType: "Bearer"}
	if t, ok := token["token_type"].(string); ok && t != "" {
		out.TokenType = t
	}
	if v, ok := claimInt(token["expires_in"]); ok {
		out.ExpiresIn = &v
	}
	if v, ok := token["refresh_token"].(string); ok && v != "" {
		out.RefreshToken = &v
	}
	if v, ok := claimInt(token["refresh_expires_in"]); ok {
		out.RefreshExpiresIn = &v
	}
	return out, true
}

// ---------- verify token ----------

var verifyGroup singleflight.Group

// VerifyToken verifies the token signature against the service JWKS and returns its claims. A success is kept in
// two levels under the service and sha256(token): in this process (the first level, at most a minute) and in Redis, with
// TTL = min(DefaultTokenCacheTTL, exp - now), so that /permission does not hit the JWKS on every request. A token that
// failed verification is remembered for a few seconds (AUTH__INVALID_TOKEN_TTL), so that a bad token sent again and
// again does not reach the identity provider or Redis.
//
// Difference from the Python version: an invalid or expired token is a 401 and does NOT count as a
// JWKS failure for the circuit breaker (previously five garbage tokens opened the breaker and
// blocked /permission for every user of the service for 30 seconds). 424 only when the JWKS is down.
func (a *App) VerifyToken(ctx context.Context, svc *model.Service, token string) (Claims, error) {
	tokenHash := tokenDigest(token)
	// A token is verified against the keys and the issuer of one service, so every level is keyed by the service as
	// well: a token verified for one service must never be accepted from the cache for another.
	scoped := svc.ID.String() + ":" + tokenHash
	cacheKey := "authz:token:" + scoped
	invalidKey := scoped
	now := time.Now()

	if err, ok := a.invalidTokens.Get(invalidKey, now); ok {
		return nil, err
	}
	if claims, ok := a.tokensL1.Get(scoped, now); ok {
		a.metrics.l1Lookup("token", true)
		return maps.Clone(claims), nil
	}
	a.metrics.l1Lookup("token", false)

	if cached, err := a.Redis.Get(ctx, cacheKey).Bytes(); err == nil {
		if claims, err := decodeClaims(cached); err == nil {
			a.keepTokenInMemory(scoped, claims, now)
			return claims, nil
		}
	} else if !errors.Is(err, redis.Nil) {
		logRedis(ctx, slog.LevelWarn, "failed to read the token cache", err)
	}

	claims, err := a.verifyTokenLive(ctx, svc, token, tokenHash)
	if err != nil {
		if apiErr, ok := apperr.As(err); ok && apiErr.Status == http.StatusUnauthorized {
			a.invalidTokens.Add(invalidKey, err, a.tuningOrDefault().invalidTokenTTL, now)
		}
		return nil, err
	}

	if exp, ok := claimInt(claims["exp"]); ok {
		ttl := min(DefaultTokenCacheTTL, time.Until(time.Unix(exp, 0)).Truncate(time.Second))
		if ttl > 0 {
			a.keepTokenInMemory(scoped, claims, now)
			if payload, err := json.Marshal(claims); err == nil {
				a.Background(func(ctx context.Context) { a.Redis.Set(ctx, cacheKey, payload, ttl) })
			}
		}
	}
	return claims, nil
}

// tokenDigest is what the token caches are keyed by (together with the service): the token itself is never stored.
func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// keepTokenInMemory keeps verified claims in the first level of the token cache until the token expires, and at most
// a minute.
func (a *App) keepTokenInMemory(key string, claims Claims, now time.Time) {
	exp, ok := claimInt(claims["exp"])
	if !ok {
		return
	}
	ttl := min(maxTokenL1TTL, time.Unix(exp, 0).Sub(now))
	a.tokensL1.Add(key, maps.Clone(claims), ttl, now)
}

func decodeClaims(raw []byte) (Claims, error) {
	var claims Claims
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	err := dec.Decode(&claims)
	return claims, err
}

func claimInt(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(x), true
	case string:
		// Some IdPs send numbers as strings (pydantic accepted them too).
		if i, err := strconv.ParseInt(x, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// errJWKSUnavailable is a failure that counts toward the circuit breaker (unlike an
// invalid token).
type errJWKSUnavailable struct{ err *apperr.Error }

func (e errJWKSUnavailable) Error() string { return e.err.Error() }
func (e errJWKSUnavailable) Unwrap() error { return e.err }

func (a *App) verifyTokenLive(ctx context.Context, svc *model.Service, token, tokenHash string) (Claims, error) {
	if svc.JwksURL == nil || *svc.JwksURL == "" {
		return nil, apperr.FailedDependency(i18n.JWKSNotConfigured, svc.ID)
	}
	breakerKey := "jwks:" + svc.ID.String()
	if redisx.BreakerOpen(ctx, a.Redis, breakerKey) {
		return nil, apperr.FailedDependency(i18n.JWKSBreakerOpen)
	}

	v, err, _ := verifyGroup.Do("verify:"+svc.ID.String()+":"+tokenHash, func() (any, error) {
		return a.fetchAndVerify(context.WithoutCancel(ctx), svc, token)
	})
	if err != nil {
		var unavailable errJWKSUnavailable
		if errors.As(err, &unavailable) {
			redisx.RecordFailure(ctx, a.Redis, breakerKey)
			return nil, unavailable.err
		}
		return nil, err
	}
	redisx.RecordSuccess(ctx, a.Redis, breakerKey)
	return v.(Claims), nil
}

func (a *App) fetchAndVerify(ctx context.Context, svc *model.Service, token string) (Claims, error) {
	parser := jwt.NewParser(jwt.WithJSONNumber())
	unverified, _, err := parser.ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return nil, apperr.InvalidToken(i18n.TokenMalformed, err)
	}
	kid, hasKid := unverified.Header["kid"]

	key, err := a.keySets().keyFor(ctx, *svc.JwksURL, kid, hasKid)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, apperr.InvalidToken(i18n.TokenKidNotFound)
	}
	jwk, err := key.parsed()
	if err != nil {
		return nil, err
	}
	alg := jwk.Algorithm
	if alg == "" {
		alg = "RS256"
	}

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return jwk.Key, nil },
		jwt.WithValidMethods([]string{alg, "ES256"}),
		jwt.WithJSONNumber(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, apperr.InvalidToken(i18n.TokenInvalid, err)
	}

	iss, _ := claims["iss"].(string)
	if svc.Issuer == nil || strings.TrimRight(iss, "/") != strings.TrimRight(*svc.Issuer, "/") {
		return nil, apperr.InvalidToken(i18n.TokenIssuerMismatch)
	}
	return Claims(claims), nil
}

// SubjectFromClaims uses preferred_username, not sub: with some IdPs (identity brokering to an
// external SSO) sub is not stable across sessions of the same person, while preferred_username is,
// and it is also more convenient for an admin granting access by hand.
func SubjectFromClaims(claims Claims) (string, error) {
	if v := claims["preferred_username"]; PyTruthy(v) {
		return PyStr(v), nil
	}
	if v, ok := claims["sub"]; ok && v != nil {
		return PyStr(v), nil
	}
	return "", apperr.InvalidToken(i18n.TokenNoSubject)
}

// TokenFragment encodes the IdP response as application/x-www-form-urlencoded for a URL fragment.
func TokenFragment(token map[string]any) string {
	values := url.Values{}
	for k, v := range token {
		values.Set(k, PyStr(v))
	}
	return values.Encode()
}

// GetServiceForCheck returns the service for /permission (404 if not found).
// It carries what the check path needs and not the client secret.
func (a *App) GetServiceForCheck(ctx context.Context, serviceID uuid.UUID) (*model.Service, error) {
	if snap := a.policySnapshot(ctx); snap != nil {
		return snap.service(serviceID)
	}
	s, err := ScanServiceCheck(a.reader(ctx).QueryRow(ctx,
		`SELECT `+ServiceCheckColumns+` FROM service WHERE id = $1 AND deleted_at IS NULL`, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.ServiceNotFound, serviceID)
	}
	return s, err
}
