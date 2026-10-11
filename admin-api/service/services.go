package service

import (
	"context"
	"errors"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

func (a *App) CreateService(ctx context.Context, in *model.ServiceInput) (*model.ServiceResponse, error) {
	var resp model.ServiceResponse
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		s, err := engine.ScanService(q.QueryRow(ctx, `
			INSERT INTO service (name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING `+engine.ServiceColumns,
			in.Name, in.Slug, derefString(in.URL), derefString(in.Icon), in.AuthorizationURL, in.TokenURL, in.JwksURL, in.Issuer, in.ClientID,
			// An empty secret means a public client (PKCE): stored as NULL.
			emptyToNil(in.ClientSecret), nonNilStrings(in.TrustedClients),
		))
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.ServiceSlugTaken, in.Slug)
		}
		if err != nil {
			return change{}, err
		}
		resp = s.Response()
		// services are the entities of the platform "service" policy
		return change{entity: "service", entityID: s.ID, serviceID: &s.ID, action: actionCreate, after: resp}, nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *App) ListServices(ctx context.Context) ([]model.ServiceResponse, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+engine.ServiceColumns+` FROM service WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ServiceResponse{}
	for rows.Next() {
		s, err := engine.ScanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s.Response())
	}
	return out, rows.Err()
}

func (a *App) UpdateService(ctx context.Context, serviceID uuid.UUID, in *model.ServiceUpdate) (*model.ServiceResponse, error) {
	if _, err := a.GetExistingService(ctx, a.DB, serviceID); err != nil {
		return nil, err
	}

	var resp model.ServiceResponse
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingService(ctx, q, serviceID)
		if err != nil {
			return change{}, err
		}
		// COALESCE: nil PATCH fields keep their current value.
		s, err := engine.ScanService(q.QueryRow(ctx, `
			UPDATE service SET
				name              = COALESCE($2, name),
				url               = COALESCE($3, url),
				icon              = COALESCE($4, icon),
				authorization_url = COALESCE($5, authorization_url),
				token_url         = COALESCE($6, token_url),
				jwks_url          = COALESCE($7, jwks_url),
				issuer            = COALESCE($8, issuer),
				client_id         = COALESCE($9, client_id),
				slug              = COALESCE($12, slug),
				trusted_clients   = COALESCE($13, trusted_clients),
				-- not sent: keep; null or empty: remove (a public client, PKCE); otherwise replace
				client_secret     = CASE WHEN $11 THEN NULLIF($10, '') ELSE client_secret END
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING `+engine.ServiceColumns,
			serviceID, in.Name, in.URL, in.Icon, in.AuthorizationURL, in.TokenURL, in.JwksURL, in.Issuer, in.ClientID,
			in.ClientSecret.Value, in.ClientSecret.Set, in.Slug, in.TrustedClients,
		))
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.ServiceSlugTaken, derefString(in.Slug))
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return change{}, apperr.NotFound(i18n.ServiceNotFound, serviceID)
		}
		if err != nil {
			return change{}, err
		}
		resp = s.Response()
		// services are the entities of the platform "service" policy; the change log keeps no client secret
		return change{entity: "service", entityID: serviceID, serviceID: &serviceID, action: actionUpdate,
			before: old.Response(), after: resp}, nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *App) DeleteService(ctx context.Context, serviceID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingService(ctx, q, serviceID)
		if err != nil {
			return change{}, err
		}

		hasTypeResource, err := engine.Exists(ctx, q,
			`SELECT 1 FROM type_resource WHERE service_id = $1 AND deleted_at IS NULL`, serviceID)
		if err != nil {
			return change{}, err
		}
		if hasTypeResource {
			return change{}, apperr.BlockedBy([]string{"type_resource"}, i18n.ServiceDeleteBlocked, serviceID)
		}

		if _, err := q.Exec(ctx, `UPDATE service SET deleted_at = now() WHERE id = $1`, serviceID); err != nil {
			return change{}, err
		}
		// services are the entities of the platform "service" policy
		return change{entity: "service", entityID: serviceID, serviceID: &serviceID, action: actionDelete,
			before: old.Response()}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return serviceID, nil
}

// nonNilStrings is the list for a text[] column: an empty list, never NULL.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func emptyToNil(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}
