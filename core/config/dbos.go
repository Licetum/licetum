package config

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

// DBOSDatabaseMismatch compares database names, permitting different hosts,
// ports, credentials and poolers for the application and DBOS connections.
func DBOSDatabaseMismatch(application, system string) bool {
	if system == "" {
		return false
	}
	normalize := func(raw string) string {
		scheme, rest, ok := strings.Cut(raw, "://")
		if ok {
			base, _, _ := strings.Cut(scheme, "+")
			return base + "://" + rest
		}
		return raw
	}
	a, err := pgx.ParseConfig(normalize(application))
	if err != nil {
		return false
	}
	b, err := pgx.ParseConfig(normalize(system))
	if err != nil {
		return false
	}
	return a.Database != b.Database
}
