package cli

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/licetum/licetum/core/config"
)

// recorder is a slog.Handler that keeps the records it was given.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	return nil
}
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

func attrs(rec slog.Record) map[string]string {
	out := map[string]string{}
	rec.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

// clearDeprecated makes every variable of the catalog absent for the test: t.Setenv first, so that the runner restores the
// ambient value, then Unsetenv. No test of this package may run in parallel.
func clearDeprecated(t *testing.T) {
	t.Helper()
	for _, d := range config.DeprecatedCatalog() {
		t.Setenv(d.Name, "")
		if err := os.Unsetenv(d.Name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWarnDeprecatedLogsOneRecordPerSettingWithItsReason(t *testing.T) {
	for _, row := range config.DeprecatedCatalog() {
		t.Run(row.Name, func(t *testing.T) {
			clearDeprecated(t)
			t.Setenv(row.Name, "x")
			rec := &recorder{}
			warnDeprecated(slog.New(rec))
			if len(rec.records) != 1 {
				t.Fatalf("got %d records, want 1", len(rec.records))
			}
			got := rec.records[0]
			a := attrs(got)
			if got.Level != slog.LevelWarn || got.Message != "the setting no longer has an effect" {
				t.Errorf("level %v, message %q", got.Level, got.Message)
			}
			if a["setting"] != row.Name || a["reason"] != row.Reason || row.Reason == "" {
				t.Errorf("attributes %v, want setting %s and reason %q", a, row.Name, row.Reason)
			}
		})
	}
}

func TestWarnDeprecatedIsSilentWhenNothingIsSet(t *testing.T) {
	clearDeprecated(t)
	rec := &recorder{}
	warnDeprecated(slog.New(rec))
	if len(rec.records) != 0 {
		t.Errorf("got %d records with nothing set", len(rec.records))
	}
}

func TestWarnDeprecatedLogsEverySetSettingInCatalogOrder(t *testing.T) {
	clearDeprecated(t)
	catalog := config.DeprecatedCatalog()
	for _, row := range catalog {
		t.Setenv(row.Name, "x")
	}
	rec := &recorder{}
	warnDeprecated(slog.New(rec))
	if len(rec.records) != len(catalog) {
		t.Fatalf("got %d records, want %d", len(rec.records), len(catalog))
	}
	for i, row := range catalog {
		if got := attrs(rec.records[i])["setting"]; got != row.Name {
			t.Errorf("record %d is %s, want %s", i, got, row.Name)
		}
	}
}
