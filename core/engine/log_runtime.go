package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/model"
)

// logSettingsWatchInterval is how often a replica checks whether another one changed the log settings.
const logSettingsWatchInterval = 15 * time.Second

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

// WatchLogSettings applies the settings another replica changed: every logSettingsWatchInterval it reads the version of
// the settings and loads them when it moved. It returns when ctx ends.
func (a *App) WatchLogSettings(ctx context.Context) {
	ticker := time.NewTicker(logSettingsWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var version int64
		if err := a.DB.QueryRow(ctx, `SELECT version FROM log_settings WHERE id = 1`).Scan(&version); err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "failed to read the log settings version", "error", err)
			}
			continue
		}
		if version != a.logVersion.Load() {
			if err := a.LoadLogSettings(ctx); err != nil {
				slog.WarnContext(ctx, "failed to load the log settings", "error", err)
			}
		}
	}
}
