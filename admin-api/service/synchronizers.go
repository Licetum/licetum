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

// liveSynchronizerExists is the single SQL definition of a type's mode: a type with a live (not
// deleted) synchronizer is sync, otherwise it is not_sync. typeIDExpr is the SQL expression of the
// type id being checked.
func liveSynchronizerExists(typeIDExpr string) string {
	return `EXISTS (SELECT 1 FROM synchronizer s WHERE s.type_resource_id = ` + typeIDExpr + ` AND s.deleted_at IS NULL)`
}

// hasSynchronizer reports whether the type has a live synchronizer. The type's mode depends on it:
// with one it is sync (the type has resources), without one it is not_sync (access to the type as a
// whole, a "capability").
func (a *App) hasSynchronizer(ctx context.Context, q db.Querier, typeResourceID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT `+liveSynchronizerExists("$1"), typeResourceID).Scan(&ok)
	return ok, err
}

func (a *App) getExistingSynchronizer(ctx context.Context, q db.Querier, typeResourceID, synchronizerID uuid.UUID) (*model.Synchronizer, error) {
	s, err := engine.ScanSynchronizer(q.QueryRow(ctx,
		`SELECT `+engine.SynchronizerColumns+` FROM synchronizer
		 WHERE id = $1 AND type_resource_id = $2 AND deleted_at IS NULL`, synchronizerID, typeResourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.SynchronizerNotFound, synchronizerID)
	}
	return s, err
}

func (a *App) CreateSynchronizer(ctx context.Context, serviceID, typeResourceID uuid.UUID, in *model.SynchronizerInput) (*model.Synchronizer, error) {
	if _, err := a.GetExistingTypeResource(ctx, a.DB, serviceID, typeResourceID); err != nil {
		return nil, err
	}

	// A type has one source (uq_synchronizer_live_type): say so before probing the new one.
	if has, err := a.hasSynchronizer(ctx, a.DB, typeResourceID); err != nil {
		return nil, err
	} else if has {
		return nil, apperr.Conflict(i18n.SynchronizerExists, typeResourceID)
	}

	if a.Sync.FetchOne(ctx, in.Config, in.ProbeResourceID, "") == nil {
		return nil, apperr.FailedDependency(i18n.SynchronizerProbeFailed, in.ProbeResourceID)
	}

	var out *model.Synchronizer
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		// The probe only proves the source works; the resources reach the cache through the synchronization.
		s, err := engine.ScanSynchronizer(q.QueryRow(ctx, `
			INSERT INTO synchronizer (type_resource_id, kind, config)
			VALUES ($1, $2::synchronizer_kind, $3::jsonb)
			RETURNING `+engine.SynchronizerColumns,
			typeResourceID, model.KindToDB(in.Config.Kind), engine.MustJSON(in.Config.StoredConfig()),
		))
		if db.IsUniqueViolation(err) {
			// Two creations racing for the same type: the second loses.
			return change{}, apperr.Conflict(i18n.SynchronizerExists, typeResourceID)
		}
		if err != nil {
			return change{}, err
		}
		s.Config = s.Config.Masked()
		out = s
		return change{entity: "synchronizer", entityID: s.ID, serviceID: &serviceID, action: actionCreate, after: s}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a *App) ListSynchronizers(ctx context.Context, serviceID, typeResourceID uuid.UUID) ([]model.Synchronizer, error) {
	if _, err := a.GetExistingTypeResource(ctx, a.DB, serviceID, typeResourceID); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx,
		`SELECT `+engine.SynchronizerColumns+` FROM synchronizer WHERE type_resource_id = $1 AND deleted_at IS NULL`,
		typeResourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Synchronizer{}
	for rows.Next() {
		s, err := engine.ScanSynchronizer(rows)
		if err != nil {
			return nil, err
		}
		s.Config = s.Config.Masked()
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (a *App) UpdateSynchronizer(ctx context.Context, serviceID, typeResourceID, synchronizerID uuid.UUID, in *model.SynchronizerInput) (*model.Synchronizer, error) {
	if _, err := a.GetExistingTypeResource(ctx, a.DB, serviceID, typeResourceID); err != nil {
		return nil, err
	}
	existing, err := a.getExistingSynchronizer(ctx, a.DB, typeResourceID, synchronizerID)
	if err != nil {
		return nil, err
	}
	// The client sees the secrets masked: the mask in the request means "do not change".
	in.Config = in.Config.RestoreSecrets(existing.Config)

	if a.Sync.FetchOne(ctx, in.Config, in.ProbeResourceID, "") == nil {
		return nil, apperr.FailedDependency(i18n.SynchronizerProbeFailed, in.ProbeResourceID)
	}

	var out *model.Synchronizer
	err = a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingSynchronizer(ctx, q, typeResourceID, synchronizerID)
		if err != nil {
			return change{}, err
		}
		s, err := engine.ScanSynchronizer(q.QueryRow(ctx, `
			UPDATE synchronizer SET kind = $2::synchronizer_kind, config = $3::jsonb
			WHERE id = $1
			RETURNING `+engine.SynchronizerColumns,
			synchronizerID, model.KindToDB(in.Config.Kind), engine.MustJSON(in.Config.StoredConfig()),
		))
		if err != nil {
			return change{}, err
		}
		// the change log keeps the secrets masked, before and after
		s.Config = s.Config.Masked()
		out = s
		return change{entity: "synchronizer", entityID: synchronizerID, serviceID: &serviceID, action: actionUpdate,
			before: old.Config.Masked(), after: s}, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a *App) DeleteSynchronizer(ctx context.Context, serviceID, typeResourceID, synchronizerID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		if _, err := a.GetExistingTypeResource(ctx, q, serviceID, typeResourceID); err != nil {
			return change{}, err
		}
		old, err := a.getExistingSynchronizer(ctx, q, typeResourceID, synchronizerID)
		if err != nil {
			return change{}, err
		}
		if _, err := q.Exec(ctx, `UPDATE synchronizer SET deleted_at = now() WHERE id = $1`, synchronizerID); err != nil {
			return change{}, err
		}
		return change{entity: "synchronizer", entityID: synchronizerID, serviceID: &serviceID, action: actionDelete,
			before: old.Config.Masked()}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return synchronizerID, nil
}
