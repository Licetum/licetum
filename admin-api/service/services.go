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
			INSERT INTO service (name, slug, url, icon)
			VALUES ($1, $2, $3, $4)
			RETURNING `+engine.ServiceColumns,
			in.Name, in.Slug, derefString(in.URL), derefString(in.Icon),
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
				name = COALESCE($2, name),
				url  = COALESCE($3, url),
				icon = COALESCE($4, icon),
				slug = COALESCE($5, slug)
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING `+engine.ServiceColumns,
			serviceID, in.Name, in.URL, in.Icon, in.Slug,
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
		// services are the entities of the platform "service" policy
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
		// The PEP directory holds the clients of live services only, so the delete moves the pep scope too (PEPDirectory).
		if _, err := db.BumpRevision(ctx, q, db.ScopePEP); err != nil {
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

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
