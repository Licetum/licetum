package db_test

import (
	"testing"

	"github.com/licetum/licetum/core/db"
)

func TestNormalizeDSN(t *testing.T) {
	tests := map[string]string{
		"postgresql+asyncpg://u:p@h:5432/db": "postgresql://u:p@h:5432/db",
		"postgresql://u:p@h/db":              "postgresql://u:p@h/db",
		"postgres+psycopg://h/db?x=1":        "postgres://h/db?x=1",
		"host=h dbname=db":                   "host=h dbname=db",
	}
	for in, want := range tests {
		if got := db.NormalizeDSN(in); got != want {
			t.Errorf("NormalizeDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
