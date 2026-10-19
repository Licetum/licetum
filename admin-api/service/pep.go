package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
)

// The PEP directory: the issuers, the clients of the services and their API keys. Each write runs in its own transaction (see
// core/db/pep.go); afterwards the process is told of the change, so the next request of this process reads the directory again.

// PutPEPIssuer creates the issuer, or replaces its settings.
func (a *App) PutPEPIssuer(ctx context.Context, in db.PEPIssuer) error {
	if err := db.UpsertIssuer(ctx, a.DB, in); err != nil {
		return pepError(err, in.Issuer)
	}
	a.AfterPEPWrite()
	return nil
}

// DeletePEPIssuer deletes an issuer that no client uses.
func (a *App) DeletePEPIssuer(ctx context.Context, issuer string) error {
	if err := db.DeleteIssuer(ctx, a.DB, issuer); err != nil {
		return pepError(err, issuer)
	}
	a.AfterPEPWrite()
	return nil
}

// CreatePEPClient adds a client to a service, which must be live.
func (a *App) CreatePEPClient(ctx context.Context, serviceID uuid.UUID, c db.PEPClient) (db.PEPClient, error) {
	live, err := engine.Exists(ctx, a.DB, `SELECT 1 FROM service WHERE id = $1 AND deleted_at IS NULL`, serviceID)
	if err != nil {
		return db.PEPClient{}, err
	}
	if !live {
		return db.PEPClient{}, apperr.NotFound(i18n.ServiceNotFound, serviceID)
	}
	c.ServiceID = serviceID
	stored, err := db.CreatePEPClient(ctx, a.DB, c)
	if err != nil {
		return db.PEPClient{}, pepError(err, c.Name)
	}
	a.AfterPEPWrite()
	return stored, nil
}

// UpdatePEPClient changes a client of the service. The kind is not changed.
func (a *App) UpdatePEPClient(ctx context.Context, serviceID uuid.UUID, c db.PEPClient) (db.PEPClient, error) {
	c.ServiceID = serviceID
	stored, err := db.UpdatePEPClient(ctx, a.DB, c)
	if err != nil {
		return db.PEPClient{}, pepError(err, c.ID.String())
	}
	a.AfterPEPWrite()
	return stored, nil
}

// DeletePEPClient deletes a client of the service, and revokes its keys.
func (a *App) DeletePEPClient(ctx context.Context, serviceID, clientID uuid.UUID) error {
	if err := db.DeletePEPClient(ctx, a.DB, serviceID, clientID); err != nil {
		return pepError(err, clientID.String())
	}
	a.AfterPEPWrite()
	return nil
}

// IssuePEPKey issues a key to an api_key client of the service. The key is returned once: it is not stored.
func (a *App) IssuePEPKey(ctx context.Context, serviceID, clientID uuid.UUID) (string, string, error) {
	owned, err := engine.Exists(ctx, a.DB,
		`SELECT 1 FROM pep_client WHERE id = $1 AND service_id = $2 AND deleted_at IS NULL`, clientID, serviceID)
	if err != nil {
		return "", "", err
	}
	if !owned {
		return "", "", apperr.NotFound(i18n.PEPNotFound, clientID.String())
	}
	key, keyID, err := db.IssueAPIKey(ctx, a.DB, clientID)
	if err != nil {
		return "", "", pepError(err, clientID.String())
	}
	a.AfterPEPWrite()
	return key, keyID, nil
}

// RevokePEPKey revokes a key of a client of the service.
func (a *App) RevokePEPKey(ctx context.Context, serviceID uuid.UUID, keyID string) error {
	owned, err := engine.Exists(ctx, a.DB, `
		SELECT 1 FROM pep_credential k JOIN pep_client c ON c.id = k.client_id
		WHERE k.key_id = $1 AND c.service_id = $2`, keyID, serviceID)
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
