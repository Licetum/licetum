package harness

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	support "github.com/licetum/licetum/core/testsupport"
)

// PEPAudience is the audience of the tokens of the PEP clients that authenticate with a token of the fake identity provider.
const PEPAudience = "licetum-pdp-test"

// RegisterPEPIssuer registers the fake identity provider as an issuer of PEP tokens (algorithm RS256, audience PEPAudience, the
// typ at+jwt of an access token) and tells the replicas.
func (c *Cluster) RegisterPEPIssuer() {
	c.T.Helper()
	support.RegisterPEPIssuer(c.T, c.Pool, db.PEPIssuer{
		Issuer: c.IdP.Issuer(), JWKSURL: c.IdP.JWKSURL(), Audiences: []string{PEPAudience}, Algs: []string{"RS256"}, RequireTyp: true,
	})
	c.pepChanged()
}

// JWTClient makes a PEP client of the service with the slug that authenticates with the tokens the fake provider issues to
// clientID (PEPToken), and tells the replicas. RegisterPEPIssuer comes first.
func (c *Cluster) JWTClient(serviceSlug, clientID string) {
	c.T.Helper()
	var serviceID uuid.UUID
	if err := c.Pool.QueryRow(context.Background(), `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL`, serviceSlug).Scan(&serviceID); err != nil {
		c.T.Fatalf("the service %s: %v", serviceSlug, err)
	}
	issuer := c.IdP.Issuer()
	support.RegisterPEPClient(c.T, c.Pool, db.PEPClient{
		ServiceID: serviceID, Name: clientID, Kind: db.PEPKindJWT, Issuer: &issuer, ClientID: &clientID,
		Operations: []string{engine.PEPEvaluate, engine.PEPSearchResource, engine.PEPSearchAction, engine.PEPSearchSubject}, Enabled: true,
	})
	c.pepChanged()
}

// PEPToken is an access token of the fake provider for the client. subject tells the tokens of one client apart.
func (c *Cluster) PEPToken(clientID, subject string, opts ...support.TokenOption) string {
	return c.IdP.SignToken(jwt.MapClaims{"azp": clientID, "sub": subject},
		append([]support.TokenOption{support.Audience(PEPAudience), support.HeaderType("at+jwt")}, opts...)...)
}

// pepChanged makes the replicas read the PEP directory again.
func (c *Cluster) pepChanged() {
	for _, r := range c.Replicas {
		r.App.AfterPEPWrite()
	}
}
