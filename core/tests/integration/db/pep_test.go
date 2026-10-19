//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	support "github.com/licetum/licetum/core/testsupport"
)

// newPEPPool starts PostgreSQL, migrates it to the current schema and makes one live service to register PEP clients for.
func newPEPPool(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infrastructure.Terminate(ctx) })
	pool, err := db.Connect(ctx, config.Database{URI: infrastructure.DBURI, PoolSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateUpTo(ctx, pool, db.LatestMigration()); err != nil {
		t.Fatal(err)
	}
	service := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO service (id, name, url, icon, slug) VALUES ($1, 'pep', '', '', 'pep')`, service); err != nil {
		t.Fatal(err)
	}
	return pool, service
}

// apiKeyClient registers an api_key client of the service.
func apiKeyClient(t *testing.T, pool *pgxpool.Pool, service uuid.UUID, name string) db.PEPClient {
	t.Helper()
	return support.RegisterPEPClient(t, pool, db.PEPClient{ServiceID: service, Name: name, Kind: db.PEPKindAPIKey, Enabled: true})
}

func TestPEPKeyLimitAllowsTwoActiveKeys(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	client := apiKeyClient(t, pool, service, "gateway")

	support.IssueTestAPIKey(t, pool, client.ID)
	support.IssueTestAPIKey(t, pool, client.ID)
	if _, _, err := db.IssueAPIKey(ctx, pool, client.ID); !errors.Is(err, db.ErrPEPKeyLimit) {
		t.Fatalf("a third active key: got %v, want ErrPEPKeyLimit", err)
	}
}

func TestPEPRevokingAKeyFreesASlot(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	client := apiKeyClient(t, pool, service, "gateway")

	_, first := support.IssueTestAPIKey(t, pool, client.ID)
	support.IssueTestAPIKey(t, pool, client.ID)
	if err := db.RevokeAPIKey(ctx, pool, first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.IssueAPIKey(ctx, pool, client.ID); err != nil {
		t.Fatalf("a key after a revocation: %v", err)
	}
	// Revoking again changes nothing and is not an error; a key that does not exist is.
	if err := db.RevokeAPIKey(ctx, pool, first); err != nil {
		t.Fatalf("revoking a revoked key: %v", err)
	}
	if err := db.RevokeAPIKey(ctx, pool, "no-such-key"); !errors.Is(err, db.ErrPEPNotFound) {
		t.Fatalf("revoking an unknown key: got %v, want ErrPEPNotFound", err)
	}
}

func TestPEPWritesMoveTheScopePEPAndNotTheRules(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	client := apiKeyClient(t, pool, service, "gateway")

	before, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	support.IssueTestAPIKey(t, pool, client.ID)
	after, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if after.PEP != before.PEP+1 {
		t.Errorf("a key issue moved the pep scope from %d to %d, want one step", before.PEP, after.PEP)
	}
	if after.Rules != before.Rules {
		t.Errorf("a key issue moved the rules scope from %d to %d, want no change", before.Rules, after.Rules)
	}

	// The other way round: a rules write leaves the pep scope where it is.
	if _, err := db.BumpRevision(ctx, pool, db.ScopeRules); err != nil {
		t.Fatal(err)
	}
	again, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if again.PEP != after.PEP {
		t.Errorf("a rules write moved the pep scope from %d to %d", after.PEP, again.PEP)
	}
}

func TestPEPDeletedServiceTakesItsClientsAndKeys(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	client := apiKeyClient(t, pool, service, "gateway")
	support.IssueTestAPIKey(t, pool, client.ID)

	if _, err := pool.Exec(ctx, `DELETE FROM service WHERE id = $1`, service); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM pep_client`,
		`SELECT count(*) FROM pep_credential`,
	} {
		var n int
		if err := pool.QueryRow(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s = %d after the service was deleted, want 0", query, n)
		}
	}
}

// TestPEPSoftDeletedServiceLeavesTheDirectory: a service is soft-deleted, so its clients and keys stay in their tables. The
// directory must not hold them, or a deleted service would keep authenticating its PEPs.
func TestPEPSoftDeletedServiceLeavesTheDirectory(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	client := apiKeyClient(t, pool, service, "gateway")
	support.IssueTestAPIKey(t, pool, client.ID)

	data, err := db.LoadPEPDirectory(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Clients) != 1 || len(data.Credentials) != 1 {
		t.Fatalf("before the soft delete: %d clients and %d credentials, want 1 and 1", len(data.Clients), len(data.Credentials))
	}

	if _, err := pool.Exec(ctx, `UPDATE service SET deleted_at = now() WHERE id = $1`, service); err != nil {
		t.Fatal(err)
	}
	data, err = db.LoadPEPDirectory(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Clients) != 0 || len(data.Credentials) != 0 {
		t.Errorf("after the soft delete: %d clients and %d credentials, want none", len(data.Clients), len(data.Credentials))
	}
}

func TestPEPClientMustFitItsKind(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	issuer := "https://idp.test"
	clientID := "orders-pdp"
	if err := db.UpsertIssuer(ctx, pool, db.PEPIssuer{Issuer: issuer, JWKSURL: issuer + "/jwks"}); err != nil {
		t.Fatal(err)
	}

	// A jwt client needs an issuer and a client id; a client of an issuer that does not exist is not valid either.
	if _, err := db.CreatePEPClient(ctx, pool, db.PEPClient{ServiceID: service, Name: "a", Kind: db.PEPKindJWT, Enabled: true}); !errors.Is(err, db.ErrPEPClientInvalid) {
		t.Errorf("a jwt client without an issuer: got %v, want ErrPEPClientInvalid", err)
	}
	unknown := "https://elsewhere.test"
	if _, err := db.CreatePEPClient(ctx, pool, db.PEPClient{ServiceID: service, Name: "b", Kind: db.PEPKindJWT,
		Issuer: &unknown, ClientID: &clientID, Enabled: true}); !errors.Is(err, db.ErrPEPClientInvalid) {
		t.Errorf("a jwt client of an unknown issuer: got %v, want ErrPEPClientInvalid", err)
	}

	jwt, err := db.CreatePEPClient(ctx, pool, db.PEPClient{ServiceID: service, Name: "pdp", Kind: db.PEPKindJWT,
		Issuer: &issuer, ClientID: &clientID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Only an api_key client has keys.
	if _, _, err := db.IssueAPIKey(ctx, pool, jwt.ID); !errors.Is(err, db.ErrPEPClientInvalid) {
		t.Errorf("a key for a jwt client: got %v, want ErrPEPClientInvalid", err)
	}
	// One live client per identity and per name; a deleted one frees both.
	if _, err := db.CreatePEPClient(ctx, pool, db.PEPClient{ServiceID: service, Name: "other", Kind: db.PEPKindJWT,
		Issuer: &issuer, ClientID: &clientID, Enabled: true}); !errors.Is(err, db.ErrPEPClientInvalid) {
		t.Errorf("a second live client with the same identity: got %v, want ErrPEPClientInvalid", err)
	}
	if err := db.DeletePEPClient(ctx, pool, service, jwt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePEPClient(ctx, pool, db.PEPClient{ServiceID: service, Name: "pdp", Kind: db.PEPKindJWT,
		Issuer: &issuer, ClientID: &clientID, Enabled: true}); err != nil {
		t.Errorf("a client that replaces a deleted one: %v", err)
	}
}

func TestPEPIssuerInUseCannotBeDeleted(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	issuer := "https://idp.test"
	clientID := "orders-pdp"
	if err := db.UpsertIssuer(ctx, pool, db.PEPIssuer{Issuer: issuer, JWKSURL: issuer + "/jwks"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePEPClient(ctx, pool, db.PEPClient{ServiceID: service, Name: "pdp", Kind: db.PEPKindJWT,
		Issuer: &issuer, ClientID: &clientID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteIssuer(ctx, pool, issuer); !errors.Is(err, db.ErrPEPIssuerInUse) {
		t.Fatalf("deleting an issuer a client uses: got %v, want ErrPEPIssuerInUse", err)
	}
}
