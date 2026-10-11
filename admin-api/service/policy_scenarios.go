package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The scenarios of the policy tests that a service keeps. They are test fixtures, not access: writing one changes no
// answer, so it is neither bumping the permission cache nor written to the change log.

func scanScenario(row pgx.Row) (*model.PolicyScenario, error) {
	var s model.PolicyScenario
	var raw []byte
	if err := row.Scan(&s.ID, &raw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.Case); err != nil {
		return nil, err
	}
	return &s, nil
}

func (a *App) ListScenarios(ctx context.Context, serviceID uuid.UUID) ([]model.PolicyScenario, error) {
	if _, err := a.GetExistingService(ctx, a.DB, serviceID); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx,
		`SELECT id, scenario FROM policy_scenario WHERE service_id = $1 ORDER BY name, id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PolicyScenario{}
	for rows.Next() {
		s, err := scanScenario(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (a *App) CreateScenario(ctx context.Context, serviceID uuid.UUID, in *model.PolicyTestCase) (*model.PolicyScenario, error) {
	if _, err := a.GetExistingService(ctx, a.DB, serviceID); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	s, err := scanScenario(a.DB.QueryRow(ctx, `
		INSERT INTO policy_scenario (service_id, name, scenario) VALUES ($1, $2, $3::jsonb)
		RETURNING id, scenario`, serviceID, in.Name, string(raw)))
	if db.IsUniqueViolation(err) {
		return nil, apperr.Conflict(i18n.PolicyScenarioNameTaken, in.Name)
	}
	return s, err
}

func (a *App) UpdateScenario(ctx context.Context, serviceID, id uuid.UUID, in *model.PolicyTestCase) (*model.PolicyScenario, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	s, err := scanScenario(a.DB.QueryRow(ctx, `
		UPDATE policy_scenario SET name = $3, scenario = $4::jsonb, updated_at = now()
		WHERE id = $1 AND service_id = $2
		RETURNING id, scenario`, id, serviceID, in.Name, string(raw)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.PolicyScenarioNotFound, id)
	}
	if db.IsUniqueViolation(err) {
		return nil, apperr.Conflict(i18n.PolicyScenarioNameTaken, in.Name)
	}
	return s, err
}

func (a *App) DeleteScenario(ctx context.Context, serviceID, id uuid.UUID) (uuid.UUID, error) {
	tag, err := a.DB.Exec(ctx, `DELETE FROM policy_scenario WHERE id = $1 AND service_id = $2`, id, serviceID)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() == 0 {
		return uuid.Nil, apperr.NotFound(i18n.PolicyScenarioNotFound, id)
	}
	return id, nil
}
