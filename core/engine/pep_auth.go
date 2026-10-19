package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/pepauth"
)

// The operations of the PDP that a PEP client may be allowed (pep_client.operations, ADR 0001, 7.10). explain and the three searches
// are off by default.
const (
	PEPEvaluate       = "evaluate"
	PEPSearchResource = "search_resource"
	PEPSearchAction   = "search_action"
	PEPSearchSubject  = "search_subject"
	PEPExplain        = "explain"
)

// apiKeyPrefix starts every API key of a PEP (lct_<16 hex digits>_<43 characters>, see pepauth.NewAPIKey).
const apiKeyPrefix = "lct_"

// PEPCaller is a PEP client that authenticated, and may do the operation it asked for on the service.
type PEPCaller struct {
	Client    db.PEPClient
	ServiceID uuid.UUID
}

// AuthenticatePEP authenticates the holder of a credential as a PEP client of the service named by serviceRef (its slug), and
// checks that the client may do the operation.
//
//   - A credential that is not valid is a 401: a key the directory does not know or whose secret is wrong, a token that does not
//     verify, one of an issuer that is not registered, and anything that is neither an API key nor a token. A token whose issuer
//     cannot be read is a 424.
//   - A valid credential that does not give the operation is the same 403 authzen_forbidden whatever the reason: the service does
//     not exist, the client is not registered for it or is disabled, or the operation is not among its operations. The answer does not
//     tell a caller which services exist (ADR 0001, 7.10).
func (a *App) AuthenticatePEP(ctx context.Context, credential, serviceRef, operation string) (*PEPCaller, error) {
	dir, err := a.PEPDirectory(ctx)
	if err != nil {
		return nil, err
	}

	var client db.PEPClient
	var issuer string // for a token: the issuer and the client it was issued to, to find the client row
	var viaClient string
	switch {
	case strings.HasPrefix(credential, apiKeyPrefix):
		c, err := apiKeyClient(dir, credential, a.now())
		if err != nil {
			return nil, err
		}
		client = c
	default:
		iss, ok := unverifiedIssuer(credential)
		verifier := dir.Verifiers[iss]
		if !ok || verifier == nil {
			return nil, apperr.InvalidToken(i18n.TokenInvalid)
		}
		verified, err := verifier.Verify(ctx, credential)
		if err != nil {
			return nil, verifyFailure(err)
		}
		issuer, viaClient = verified.Issuer, verified.Client
	}

	// The credential is good; from here on every refusal is the same.
	forbidden := apperr.Forbidden(i18n.AuthzenForbidden)
	serviceID, err := a.ServiceIDByRef(ctx, serviceRef)
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Status == 404 {
			return nil, forbidden
		}
		return nil, err
	}
	if client.ID == uuid.Nil {
		// A token: the client row of the service that the issuer and the client of the token name.
		i := slices.IndexFunc(dir.ClientsByService[serviceID], func(c db.PEPClient) bool {
			return c.Kind == db.PEPKindJWT && c.Issuer != nil && *c.Issuer == issuer && c.ClientID != nil && *c.ClientID == viaClient
		})
		if i < 0 {
			return nil, forbidden
		}
		client = dir.ClientsByService[serviceID][i]
	}
	if client.ServiceID != serviceID || !client.Enabled || !slices.Contains(client.Operations, operation) {
		return nil, forbidden
	}
	return &PEPCaller{Client: client, ServiceID: serviceID}, nil
}

// verifyFailure is the answer to a token the verifier did not accept: a key set that could not be read is a 424 (the token is
// neither accepted nor refused), anything else a 401 invalid_token.
func verifyFailure(err error) error {
	if errors.Is(err, pepauth.ErrJWKSUnavailable) {
		var unavailable *apperr.Error
		if errors.As(err, &unavailable) {
			return unavailable
		}
		return err
	}
	return apperr.InvalidToken(i18n.TokenInvalid)
}

// apiKeyClient authenticates an API key: the key id names a credential the directory holds, and the hash of the secret equals
// the one stored (in constant time). A key that does not authenticate, for whatever reason, is the same 401; an unknown key id still
// costs one comparison, so the answer does not tell a key id that exists from one that does not by its time.
func apiKeyClient(dir *PEPDirectory, key string, now time.Time) (db.PEPClient, error) {
	invalid := apperr.InvalidToken(i18n.TokenInvalid)
	keyID, random, err := pepauth.ParseAPIKey(key)
	if err != nil {
		pepauth.DummyCompare(key)
		return db.PEPClient{}, invalid
	}
	cred, client, ok := dir.APIKey(keyID, now)
	if !ok {
		pepauth.DummyCompare(random)
		return db.PEPClient{}, invalid
	}
	if !pepauth.CompareSecret(cred.SHA256, random) {
		return db.PEPClient{}, invalid
	}
	return client, nil
}

// unverifiedIssuer reads the iss of a token without verifying it, to find the verifier of that issuer. Nothing it returns is
// trusted: it only chooses the verifier, which checks the signature, the issuer, the audience and the times of the token. The payload
// is read by hand, so that no parser that skips the signature check is in the path of a credential.
func unverifiedIssuer(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", false
	}
	return claims.Iss, claims.Iss != ""
}
