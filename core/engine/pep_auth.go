package engine

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

// ErrNotPEPCredential says that a credential is not one of a PEP: it is neither an API key nor a token of a registered issuer. Until
// the authentication by the token of the service goes (the removals of stage 2c), the caller may be a person with such a token.
var ErrNotPEPCredential = errors.New("the credential is not a PEP credential")

// PEPCaller is a PEP client that authenticated, and may do the operation it asked for on the service.
type PEPCaller struct {
	Client    db.PEPClient
	ServiceID uuid.UUID
}

// AuthenticatePEP authenticates the holder of a credential as a PEP client of the service named by serviceRef (its id or its
// slug), and checks that the client may do the operation.
//
//   - A credential that is not a PEP credential is ErrNotPEPCredential.
//   - A PEP credential that is not valid (a key the directory does not know or whose secret is wrong, a token that does not verify)
//     is a 401, and a token whose issuer cannot be read is a 424.
//   - A valid credential that does not give the operation is the same 403 authzen_forbidden whatever the reason: the service does
//     not exist, the client is not registered for it or is disabled, or the operation is not among its operations. The answer does not
//     tell a caller which services exist (ADR 0001, 7.10).
func (a *App) AuthenticatePEP(ctx context.Context, credential, serviceRef, operation string) (*PEPCaller, error) {
	dir, err := a.PEPDirectory(ctx)
	if err != nil {
		if strings.HasPrefix(credential, apiKeyPrefix) {
			return nil, err
		}
		// A token cannot be told from a person's without the directory, which this process neither holds nor can load: it is taken
		// for a person's, whose check has its own way through a database that does not answer.
		return nil, ErrNotPEPCredential
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
			return nil, ErrNotPEPCredential
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

// apiKeyClient authenticates an API key: the key id names a credential the directory holds, and the sha256 of the secret equals
// the one stored (in constant time). A key that does not authenticate, for whatever reason, is the same 401; an unknown key id still
// costs one comparison, so the answer does not tell a key id that exists from one that does not by its time.
func apiKeyClient(dir *PEPDirectory, key string, now time.Time) (db.PEPClient, error) {
	invalid := apperr.InvalidToken(i18n.TokenInvalid)
	keyID, secret, err := pepauth.ParseAPIKey(key)
	if err != nil {
		pepauth.DummyCompare(key)
		return db.PEPClient{}, invalid
	}
	cred, client, ok := dir.APIKey(keyID, now)
	if !ok {
		pepauth.DummyCompare(secret)
		return db.PEPClient{}, invalid
	}
	if !pepauth.CompareSecret(cred.SHA256, secret) {
		return db.PEPClient{}, invalid
	}
	return client, nil
}

// unverifiedIssuer reads the iss of a token without verifying it, to find the verifier of that issuer. Nothing it returns is
// trusted: the verifier checks the token.
func unverifiedIssuer(token string) (string, bool) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return "", false
	}
	iss, ok := claims["iss"].(string)
	return iss, ok && iss != ""
}
