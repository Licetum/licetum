package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The PEP directory: the issuers, the clients of the services and their API keys. Each write runs in its own transaction (see
// core/db/pep.go); afterwards the process is told of the change, so the next request of this process reads the directory again.

// PutPEPIssuer creates the issuer, or replaces its settings.
func (a *App) PutPEPIssuer(ctx context.Context, in *model.PEPIssuer) (*model.PEPIssuer, error) {
	issuer := db.PEPIssuer{Issuer: in.Issuer, JWKSURL: in.JWKSURL, Audiences: in.Audiences, Algs: in.Algs, RequireTyp: in.RequireTyp}
	if err := db.UpsertIssuer(ctx, a.DB, issuer); err != nil {
		return nil, pepError(err, in.Issuer)
	}
	a.AfterPEPWrite()
	return in, nil
}

// DeletePEPIssuer deletes an issuer that no client uses.
func (a *App) DeletePEPIssuer(ctx context.Context, issuer string) error {
	if err := db.DeleteIssuer(ctx, a.DB, issuer); err != nil {
		return pepError(err, issuer)
	}
	a.AfterPEPWrite()
	return nil
}

// ListPEPClients lists the clients of a live service, by name.
func (a *App) ListPEPClients(ctx context.Context, serviceID uuid.UUID) ([]model.PEPClient, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	clients, err := db.ListPEPClients(ctx, a.DB, serviceID)
	if err != nil {
		return nil, err
	}
	out := make([]model.PEPClient, len(clients))
	for i, c := range clients {
		out[i] = pepClientResponse(c)
	}
	return out, nil
}

// GetPEPClient returns a client of the service.
func (a *App) GetPEPClient(ctx context.Context, serviceID, clientID uuid.UUID) (*model.PEPClient, error) {
	c, err := db.GetPEPClient(ctx, a.DB, serviceID, clientID)
	if err != nil {
		return nil, pepError(err, clientID.String())
	}
	out := pepClientResponse(c)
	return &out, nil
}

// CreatePEPClient adds a client to a service, which must be live. The body is validated.
func (a *App) CreatePEPClient(ctx context.Context, serviceID uuid.UUID, in *model.PEPClientInput) (*model.PEPClient, error) {
	if err := a.requireLiveService(ctx, serviceID); err != nil {
		return nil, err
	}
	c := db.PEPClient{ServiceID: serviceID, Name: in.Name, Kind: in.Kind, Issuer: in.Issuer, ClientID: in.ClientID,
		Operations: in.Operations, Enabled: in.Enabled == nil || *in.Enabled}
	stored, err := db.CreatePEPClient(ctx, a.DB, c)
	if err != nil {
		return nil, pepError(err, c.Name)
	}
	a.AfterPEPWrite()
	out := pepClientResponse(stored)
	return &out, nil
}

// UpdatePEPClient changes the fields of a client of the service that the patch carries. The kind is not changed.
func (a *App) UpdatePEPClient(ctx context.Context, serviceID, clientID uuid.UUID, p *model.PEPClientPatch) (*model.PEPClient, error) {
	stored, err := db.ModifyPEPClient(ctx, a.DB, serviceID, clientID, func(c *db.PEPClient) error {
		if p.Name != nil {
			c.Name = *p.Name
		}
		if p.Issuer != nil {
			c.Issuer = p.Issuer
		}
		if p.ClientID != nil {
			c.ClientID = p.ClientID
		}
		if p.Operations != nil {
			c.Operations = *p.Operations
		}
		if p.Enabled != nil {
			c.Enabled = *p.Enabled
		}
		return nil
	})
	if err != nil {
		return nil, pepError(err, clientID.String())
	}
	a.AfterPEPWrite()
	out := pepClientResponse(stored)
	return &out, nil
}

// DeletePEPClient deletes a client of the service, and revokes its keys.
func (a *App) DeletePEPClient(ctx context.Context, serviceID, clientID uuid.UUID) error {
	if err := db.DeletePEPClient(ctx, a.DB, serviceID, clientID); err != nil {
		return pepError(err, clientID.String())
	}
	a.AfterPEPWrite()
	return nil
}

// ListPEPKeys lists the keys of a client of the service, the revoked ones too, the newest first. It never carries a secret.
func (a *App) ListPEPKeys(ctx context.Context, serviceID, clientID uuid.UUID) ([]model.PEPKey, error) {
	keys, err := db.ListPEPKeys(ctx, a.DB, serviceID, clientID)
	if err != nil {
		return nil, pepError(err, clientID.String())
	}
	now := time.Now()
	out := make([]model.PEPKey, len(keys))
	for i, k := range keys {
		out[i] = pepKeyResponse(k, now)
	}
	return out, nil
}

// IssuePEPKey issues a key to an api_key client of the service. The key is in the answer and nowhere else: it is not stored, so a
// key that was lost is revoked and issued again.
func (a *App) IssuePEPKey(ctx context.Context, serviceID, clientID uuid.UUID) (*model.PEPKeyIssued, error) {
	if _, err := db.GetPEPClient(ctx, a.DB, serviceID, clientID); err != nil {
		return nil, pepError(err, clientID.String())
	}
	key, keyID, err := db.IssueAPIKey(ctx, a.DB, clientID)
	if err != nil {
		return nil, pepError(err, clientID.String())
	}
	a.AfterPEPWrite()
	keys, err := db.ListPEPKeys(ctx, a.DB, serviceID, clientID)
	if err != nil {
		return nil, pepError(err, clientID.String())
	}
	for _, k := range keys {
		if k.KeyID == keyID {
			return &model.PEPKeyIssued{PEPKey: pepKeyResponse(k, time.Now()), Key: key}, nil
		}
	}
	return nil, apperr.NotFound(i18n.PEPNotFound, keyID)
}

// RevokePEPKey revokes a key of a client of the service; a key that is revoked already stays so. A key of another client, or of
// another service, does not exist for this route.
func (a *App) RevokePEPKey(ctx context.Context, serviceID, clientID uuid.UUID, keyID string) error {
	owned, err := engine.Exists(ctx, a.DB, `
		SELECT 1 FROM pep_credential k JOIN pep_client c ON c.id = k.client_id
		WHERE k.key_id = $1 AND c.id = $2 AND c.service_id = $3 AND c.deleted_at IS NULL`, keyID, clientID, serviceID)
	if err != nil {
		return err
	}
	if !owned {
		return apperr.NotFound(i18n.PEPNotFound, keyID)
	}
	if err := db.RevokeAPIKey(ctx, a.DB, keyID); err != nil {
		return pepError(err, keyID)
	}
	a.AfterPEPWrite()
	return nil
}

// ListPEPIssuers lists the registered issuers, by name.
func (a *App) ListPEPIssuers(ctx context.Context) ([]model.PEPIssuer, error) {
	issuers, err := db.ListIssuers(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	out := make([]model.PEPIssuer, len(issuers))
	for i, in := range issuers {
		out[i] = model.PEPIssuer{Issuer: in.Issuer, JWKSURL: in.JWKSURL, Audiences: in.Audiences, Algs: in.Algs, RequireTyp: in.RequireTyp}
	}
	return out, nil
}

// requireLiveService is a 404 for a service that does not exist or is deleted.
func (a *App) requireLiveService(ctx context.Context, serviceID uuid.UUID) error {
	live, err := engine.Exists(ctx, a.DB, `SELECT 1 FROM service WHERE id = $1 AND deleted_at IS NULL`, serviceID)
	if err != nil {
		return err
	}
	if !live {
		return apperr.NotFound(i18n.ServiceNotFound, serviceID)
	}
	return nil
}

func pepClientResponse(c db.PEPClient) model.PEPClient {
	return model.PEPClient{ID: c.ID, ServiceID: c.ServiceID, Name: c.Name, Kind: c.Kind, Issuer: c.Issuer, ClientID: c.ClientID,
		Operations: c.Operations, Enabled: c.Enabled, CreatedAt: c.CreatedAt}
}

func pepKeyResponse(k db.PEPKey, now time.Time) model.PEPKey {
	active := k.RevokedAt == nil && (k.ExpiresAt == nil || k.ExpiresAt.After(now))
	return model.PEPKey{KeyID: k.KeyID, CreatedAt: k.CreatedAt, ExpiresAt: k.ExpiresAt, RevokedAt: k.RevokedAt, Active: active}
}

// pepError maps the errors of the PEP directory to the answers of the API; any other error is returned as it is.
func pepError(err error, what string) error {
	switch {
	case errors.Is(err, db.ErrPEPKeyLimit):
		return apperr.Conflict(i18n.PEPKeyLimit, db.PEPKeyLimit)
	case errors.Is(err, db.ErrPEPClientInvalid):
		return apperr.Unprocessable(i18n.PEPClientInvalid)
	case errors.Is(err, db.ErrPEPIssuerInUse):
		return apperr.Conflict(i18n.PEPIssuerInUse, what)
	case errors.Is(err, db.ErrPEPNotFound):
		return apperr.NotFound(i18n.PEPNotFound, what)
	}
	return err
}
