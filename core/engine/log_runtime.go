package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/model"
)

// LogOptions turns the stored settings into the configuration of the logger, and checks it the way the logger does.
func LogOptions(s model.LogSettings, salt string) (logx.Options, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(s.Level))); err != nil {
		return logx.Options{}, err
	}
	fields := make([]logx.Field, len(s.Fields))
	for i, f := range s.Fields {
		fields[i] = logx.Field{Name: f.Name, As: f.As}
	}
	opts := logx.Options{
		Format:  s.Format,
		Level:   level,
		Fields:  fields,
		Events:  map[string]bool{"access": s.Events.Access, "decision": s.Events.Decision, "sync": s.Events.Sync},
		Subject: s.Subject,
		Salt:    salt,

		SampleAllowed: s.SampleAllowed,
	}
	return opts, opts.Validate()
}

// LoadLogSettings applies the stored log settings. It is a no-op when the process already applied this version.
func (a *App) LoadLogSettings(ctx context.Context) error {
	var version int64
	var raw []byte
	var salt string
	if err := a.DB.QueryRow(ctx, `SELECT version, settings, salt FROM log_settings WHERE id = 1`).
		Scan(&version, &raw, &salt); err != nil {
		return err
	}
	if version == a.logVersion.Load() {
		return nil
	}
	var s model.LogSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	return a.ApplyLogSettings(s, salt, version)
}

// ApplyLogSettings applies a configuration this process has stored or read. A configuration the logger refuses keeps
// the one in force.
func (a *App) ApplyLogSettings(s model.LogSettings, salt string, version int64) error {
	opts, err := LogOptions(s, salt)
	if err != nil {
		return err
	}
	if a.Logs != nil {
		if err := a.Logs.Apply(opts); err != nil {
			return err
		}
	}
	a.logVersion.Store(version)
	return nil
}

// noteLogSettingsRevision loads the log settings when their revision (revision_state, scope log_settings) is not the one
// this process applied. It is called by every reading of the revisions, so the poll of WatchRevision is what carries a
// change to the replicas: there is no ticker of its own. A failed load is tried again by the next reading.
func (a *App) noteLogSettingsRevision(revision int64) {
	if revision == a.logRevision.Load() || !a.logLoading.CompareAndSwap(false, true) {
		return
	}
	if !a.Background(func(ctx context.Context) {
		defer a.logLoading.Store(false)
		if err := a.LoadLogSettings(ctx); err != nil {
			slog.WarnContext(ctx, "failed to load the log settings", "error", err)
			return
		}
		a.logRevision.Store(revision)
	}) {
		a.logLoading.Store(false)
	}
}
