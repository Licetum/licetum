package support

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"
)

// pepOperations are what the PEP client of a test may do: every operation of the PDP.
var pepOperations = []string{"evaluate", "search_resource", "search_action", "search_subject", "explain"}

// PEPCallers lets the tests ask the PDP the way a PEP does. A test names who asks by the access token of a person (the token of
// the mock identity provider, with the claims the rules read): the PDP does not take such a token, so a request that carries one
// is sent as the PEP client of the service it is about, with the person as the subject of the body and the claims of the token as
// the properties of the subject. One client, with an API key and every operation, is made per service when it is first needed.
type PEPCallers struct {
	tb   testing.TB
	pool *pgxpool.Pool
	// changed tells the apps of the process that the directory of PEPs changed (App.AfterPEPWrite of each).
	changed func()

	mu   sync.Mutex
	keys map[uuid.UUID]string
}

// NewPEPCallers makes the callers of the tests on the database of pool. changed is called after a client is made, so that the apps
// read the directory at once instead of at their next poll of the revisions.
func NewPEPCallers(tb testing.TB, pool *pgxpool.Pool, changed func()) *PEPCallers {
	return &PEPCallers{tb: tb, pool: pool, changed: changed, keys: map[uuid.UUID]string{}}
}

// credentialClaims are the claims that identify a token and not a person: they are not sent as properties.
var credentialClaims = map[string]bool{"iss": true, "aud": true, "exp": true, "nbf": true, "iat": true, "jti": true}

// Prepare is what a request of a test is sent as: the credential of the PEP client and the body with the person's claims. The
// path decides whether the request is a question to the AuthZEN API of a service (/service/<ref>/access/v1/...); anything else,
// and a request without a token, is sent as it is.
func (p *PEPCallers) Prepare(path, token string, body any) (credential string, prepared any) {
	if token == "" {
		return "", body
	}
	ref, ok := serviceOfAuthzenPath(path)
	if !ok {
		return token, body
	}
	claims, ok := claimsOf(token)
	if !ok {
		// Not a token of the mock provider: whatever it is, it is the PDP's to refuse.
		return token, body
	}
	key, ok := p.credential(ref)
	if !ok {
		return token, body
	}
	return key, withClaims(body, claims)
}

// serviceOfAuthzenPath is the reference of the service (slug or id) of a path below /service/<ref>/access/v1/.
func serviceOfAuthzenPath(path string) (string, bool) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	rest, ok := strings.CutPrefix(path, "/service/")
	if !ok {
		return "", false
	}
	segment, tail, ok := strings.Cut(rest, "/")
	if !ok || !strings.HasPrefix(tail, "access/v1/") {
		return "", false
	}
	ref, err := url.PathUnescape(segment)
	if err != nil || ref == "" {
		return "", false
	}
	return ref, true
}

// credential is the API key of the client of the service named by ref, made when it is first asked for. A service nobody has
// (an unknown reference) has none: the request is sent with its token, and the PDP refuses it.
func (p *PEPCallers) credential(ref string) (string, bool) {
	ctx := context.Background()
	var id uuid.UUID
	if err := p.pool.QueryRow(ctx, `SELECT id FROM service WHERE deleted_at IS NULL AND (slug = $1 OR id::text = $1)`, ref).Scan(&id); err != nil {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if key, ok := p.keys[id]; ok {
		return key, true
	}
	client := RegisterPEPClient(p.tb, p.pool, db.PEPClient{
		ServiceID: id, Name: "test-pep", Kind: db.PEPKindAPIKey, Operations: pepOperations, Enabled: true,
	})
	key, _ := IssueTestAPIKey(p.tb, p.pool, client.ID)
	p.keys[id] = key
	if p.changed != nil {
		p.changed()
	}
	return key, true
}

// subjectOfClaims is the person the claims stand for, as the check API read it: preferred_username, else sub.
func subjectOfClaims(claims map[string]any) string {
	for _, name := range []string{"preferred_username", "sub"} {
		switch v := claims[name].(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

// claimsOf reads the payload of a token without verifying it, and returns the claims of the person.
func claimsOf(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, false
	}
	var all map[string]any
	if json.Unmarshal(raw, &all) != nil {
		return nil, false
	}
	claims := map[string]any{}
	for k, v := range all {
		if !credentialClaims[k] {
			claims[k] = v
		}
	}
	return claims, true
}

// withClaims returns the body of a question with the claims as the properties of its subject, unless the request brought
// properties of its own. A body that is not a question about a person (a subject search names no person) is returned as it is.
func withClaims(body any, claims map[string]any) any {
	question, ok := body.(map[string]any)
	if !ok {
		return body
	}
	subject, ok := question["subject"].(map[string]any)
	if !ok || subject["id"] == nil || subject["id"] == "" {
		return body
	}
	if _, given := subject["properties"]; given || len(claims) == 0 {
		return body
	}
	// The claims are the holder's: a question about somebody else (a client asking on behalf of a person) brings the claims of that
	// person itself.
	if id, _ := subject["id"].(string); id != subjectOfClaims(claims) {
		return body
	}
	withProperties := make(map[string]any, len(subject)+1)
	for k, v := range subject {
		withProperties[k] = v
	}
	withProperties["properties"] = claims
	out := make(map[string]any, len(question))
	for k, v := range question {
		out[k] = v
	}
	out["subject"] = withProperties
	return out
}
