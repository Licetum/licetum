package support

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"
)

// RegisterPEPIssuer creates the issuer of the PEP directory, or replaces its settings.
func RegisterPEPIssuer(tb testing.TB, pool *pgxpool.Pool, i db.PEPIssuer) {
	tb.Helper()
	if err := db.UpsertIssuer(context.Background(), pool, i); err != nil {
		tb.Fatal(err)
	}
}

// RegisterPEPClient adds a client to a service and returns it as stored.
func RegisterPEPClient(tb testing.TB, pool *pgxpool.Pool, c db.PEPClient) db.PEPClient {
	tb.Helper()
	stored, err := db.CreatePEPClient(context.Background(), pool, c)
	if err != nil {
		tb.Fatal(err)
	}
	return stored
}

// IssueTestAPIKey issues a key to an api_key client and returns the key, which is shown once, and its key id.
func IssueTestAPIKey(tb testing.TB, pool *pgxpool.Pool, clientID uuid.UUID) (key, keyID string) {
	tb.Helper()
	key, keyID, err := db.IssueAPIKey(context.Background(), pool, clientID)
	if err != nil {
		tb.Fatal(err)
	}
	return key, keyID
}
