package db_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

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

func TestIsUnavailableTellsAnUnreachableDatabaseFromAnErrorOfTheStatement(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, true},
		{fmt.Errorf("query: %w", io.ErrUnexpectedEOF), true},
		{fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{&pgconn.PgError{Code: "57P01"}, true},  // admin_shutdown
		{&pgconn.PgError{Code: "53300"}, true},  // too_many_connections
		{&pgconn.PgError{Code: "08006"}, true},  // connection_failure
		{&pgconn.PgError{Code: "23505"}, false}, // unique_violation: the statement's own error
		{&pgconn.PgError{Code: "42P01"}, false}, // undefined_table
		{context.DeadlineExceeded, false},       // the caller's own deadline is not a sign of the database
		{context.Canceled, false},
		{errors.New("something else"), false},
	} {
		if got := db.IsUnavailable(c.err); got != c.want {
			t.Errorf("IsUnavailable(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
