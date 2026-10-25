package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/licetum/licetum/core/engine"

	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// GetLogSettings returns the structure of the process logs.
func (a *App) GetLogSettings(ctx context.Context) (model.LogSettings, error) {
	var raw []byte
	if err := a.DB.QueryRow(ctx, `SELECT settings FROM log_settings WHERE id = 1`).Scan(&raw); err != nil {
		return model.LogSettings{}, err
	}
	var s model.LogSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return model.LogSettings{}, err
	}
	if s.Fields == nil {
		s.Fields = []model.LogField{}
	}
	return s, nil
}

// PutLogSettings replaces the structure of the process logs. The settings are checked the way the logger applies them,
// stored together with a change-log record, and applied to this process at once. The other replicas apply them when
// the next poll of the revisions (see WatchRevision).
func (a *App) PutLogSettings(ctx context.Context, in model.LogSettings) (model.LogSettings, error) {
	if in.Fields == nil {
		in.Fields = []model.LogField{}
	}
	var version int64
	var salt string
	err := db.InTx(ctx, a.DB, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT settings, salt FROM log_settings WHERE id = 1 FOR UPDATE`).Scan(&raw, &salt); err != nil {
			return err
		}
		var before model.LogSettings
		if err := json.Unmarshal(raw, &before); err != nil {
			return err
		}
		if _, err := engine.LogOptions(in, salt); err != nil {
			return apperr.Unprocessable(i18n.LogSettingInvalid, err.Error())
		}
		if err := tx.QueryRow(ctx, `
			UPDATE log_settings SET settings = $1::jsonb, version = version + 1, updated_at = now(), updated_by = $2
			WHERE id = 1
			RETURNING version`, engine.MustJSON(in), engine.ActorFrom(ctx)).Scan(&version); err != nil {
			return err
		}
		// The revision commits with the settings; the poll of every process sees it move and loads them.
		if _, err := db.BumpRevision(ctx, tx, db.ScopeLogSettings); err != nil {
			return err
		}
		return recordChange(ctx, tx, change{entity: "log_settings", action: actionUpdate, before: before, after: in})
	})
	if err != nil {
		return model.LogSettings{}, err
	}
	if err := a.ApplyLogSettings(in, salt, version); err != nil {
		slog.WarnContext(ctx, "the stored log settings cannot be applied", "error", err)
	}
	return in, nil
}
