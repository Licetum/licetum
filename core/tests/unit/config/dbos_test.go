package config_test

import (
	"testing"

	"github.com/licetum/licetum/core/config"
)

func TestDBOSMustShareTheApplicationDatabase(t *testing.T) {
	for _, tc := range []struct {
		app, system string
		different   bool
	}{
		{"postgres://app@primary:5432/licetum", "", false},
		{"postgres://app@pooler:6432/licetum", "postgres://worker@primary:5432/licetum", false},
		{"postgresql+asyncpg://app@primary/licetum", "postgres://worker@primary/licetum", false},
		{"host=primary dbname=licetum", "host=pooler dbname=licetum", false},
		{"postgres://app@primary/licetum", "postgres://worker@primary/other", true},
	} {
		if got := config.DBOSDatabaseMismatch(tc.app, tc.system); got != tc.different {
			t.Errorf("database comparison = %v, want %v", got, tc.different)
		}
	}
}
