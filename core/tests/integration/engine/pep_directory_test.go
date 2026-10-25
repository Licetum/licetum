//go:build integration

package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/pepauth"
	support "github.com/licetum/licetum/core/testsupport"
)

// pepFixture is an engine with the policy in memory (the snapshot on), on an empty database with one live service.
// The poll interval is an hour, so that nothing renews the view but the requests and the writes the test makes.
type pepFixture struct {
	pool    *pgxpool.Pool
	app     *engine.App
	service uuid.UUID
}

func newPEPFixture(t *testing.T) *pepFixture {
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

	app := engine.New(pool, &config.Config{Revision: config.Revision{PollInterval: time.Hour}})
	t.Cleanup(app.Close)

	service := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO service (id, name, url, icon, slug) VALUES ($1, 'pep', '', '', 'pep')`, service); err != nil {
		t.Fatal(err)
	}
	return &pepFixture{pool: pool, app: app, service: service}
}

// directory returns the directory of the fixture, failing the test when it cannot be read.
func (fx *pepFixture) directory(t *testing.T, ctx context.Context) *engine.PEPDirectory {
	t.Helper()
	d, err := fx.app.PEPDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPEPDirectoryLoadsTheIssuersClientsAndKeys(t *testing.T) {
	fx := newPEPFixture(t)
	ctx := context.Background()
	issuer := "https://idp.test"
	jwtClientID := "orders-pdp"
	support.RegisterPEPIssuer(t, fx.pool, db.PEPIssuer{Issuer: issuer, JWKSURL: issuer + "/jwks", Audiences: []string{"orders"}})
	support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "pdp", Kind: db.PEPKindJWT,
		Issuer: &issuer, ClientID: &jwtClientID, Operations: []string{"evaluate", "explain"}, Enabled: true})
	gateway := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "gateway",
		Kind: db.PEPKindAPIKey, Enabled: true})
	// A disabled client is not in the directory, and neither are its keys.
	disabled := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "old", Kind: db.PEPKindAPIKey})
	support.IssueTestAPIKey(t, fx.pool, disabled.ID)

	key, keyID := support.IssueTestAPIKey(t, fx.pool, gateway.ID)
	d := fx.directory(t, ctx)

	if d.Verifiers[issuer] == nil {
		t.Errorf("no verifier of the issuer %s", issuer)
	}
	if got := d.ClientsByService[fx.service]; len(got) != 2 {
		t.Errorf("the enabled clients of the service: got %d, want 2 (gateway and pdp)", len(got))
	}
	cred, client, ok := d.APIKey(keyID, time.Now())
	if !ok || client.ID != gateway.ID {
		t.Fatalf("the key %s: ok=%v client=%v, want the gateway client", keyID, ok, client.ID)
	}
	_, random, err := pepauth.ParseAPIKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if !pepauth.CompareSecret(cred.SHA256, random) {
		t.Error("the stored hash does not match the secret of the issued key")
	}
	if pepauth.CompareSecret(cred.SHA256, random+"x") {
		t.Error("a wrong secret matches the stored hash")
	}
}

func TestPEPDirectoryChecksTheExpiryOfAKeyAgainstTheClock(t *testing.T) {
	fx := newPEPFixture(t)
	ctx := context.Background()
	gateway := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "gateway",
		Kind: db.PEPKindAPIKey, Enabled: true})
	_, keyID := support.IssueTestAPIKey(t, fx.pool, gateway.ID)

	// The expiry is set in the table and announced by a pep write: the directory keeps the key, and the clock decides.
	if _, err := fx.pool.Exec(ctx, `UPDATE pep_credential SET expires_at = now() + interval '1 hour' WHERE key_id = $1`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BumpRevision(ctx, fx.pool, db.ScopePEP); err != nil {
		t.Fatal(err)
	}
	fx.app.AfterPEPWrite()
	d := fx.directory(t, ctx)

	if _, _, ok := d.APIKey(keyID, time.Now()); !ok {
		t.Error("a key that expires in an hour does not authenticate now")
	}
	if _, _, ok := d.APIKey(keyID, time.Now().Add(2*time.Hour)); ok {
		t.Error("an expired key authenticates")
	}
}

func TestPEPDirectoryReloadsAfterAPEPWrite(t *testing.T) {
	fx := newPEPFixture(t)
	ctx := context.Background()
	gateway := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "gateway",
		Kind: db.PEPKindAPIKey, Enabled: true})
	_, keyID := support.IssueTestAPIKey(t, fx.pool, gateway.ID)
	before := fx.directory(t, ctx)

	if err := db.RevokeAPIKey(ctx, fx.pool, keyID); err != nil {
		t.Fatal(err)
	}
	fx.app.AfterPEPWrite()
	after := fx.directory(t, ctx)

	if after == before {
		t.Fatal("the directory was not loaded again after a revocation")
	}
	if _, _, ok := after.APIKey(keyID, time.Now()); ok {
		t.Error("a revoked key still authenticates")
	}
}

func TestPEPDirectoryIsNotLoadedAgainForARulesWrite(t *testing.T) {
	fx := newPEPFixture(t)
	ctx := context.Background()
	gateway := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "gateway",
		Kind: db.PEPKindAPIKey, Enabled: true})
	support.IssueTestAPIKey(t, fx.pool, gateway.ID)
	before := fx.directory(t, ctx)

	rev, err := db.BumpRevision(ctx, fx.pool, db.ScopeRules)
	if err != nil {
		t.Fatal(err)
	}
	fx.app.AfterWrite(ctx, rev)
	if after := fx.directory(t, ctx); after != before {
		t.Error("a rules write loaded the PEP directory again")
	}
}

func TestPEPDirectoryWarmCallReadsNothing(t *testing.T) {
	fx := newPEPFixture(t)
	gateway := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "gateway",
		Kind: db.PEPKindAPIKey, Enabled: true})
	support.IssueTestAPIKey(t, fx.pool, gateway.ID)

	// A load reads the database: the counter of the request sees it, so the warm call below is not zero by accident.
	loadCtx, loadTrace := engine.WithTrace(context.Background())
	fx.app.AfterPEPWrite()
	loaded := fx.directory(t, loadCtx)
	if loadTrace.DBQueries() == 0 {
		t.Fatal("the load of the directory read nothing: the query counter does not see it")
	}

	warmCtx, warmTrace := engine.WithTrace(context.Background())
	warm, err := fx.app.PEPDirectory(warmCtx)
	if err != nil {
		t.Fatal(err)
	}
	if warm != loaded {
		t.Error("a warm call did not return the directory the process holds")
	}
	if n := warmTrace.DBQueries(); n != 0 {
		t.Errorf("a warm call sent %d queries to PostgreSQL, want none", n)
	}
}

// TestPEPDirectoryFailsWhenThePolicyIsUnconfirmed: the view cannot confirm the revisions (PostgreSQL is gone), so the directory is
// read from PostgreSQL and the read fails. The last copy is not served: a key revoked meanwhile must not keep working because the
// process cannot see the revocation. The staleness bound of a reload that fails under a confirmed view is not covered here.
func TestPEPDirectoryFailsWhenThePolicyIsUnconfirmed(t *testing.T) {
	fx := newPEPFixture(t)
	ctx := context.Background()
	gateway := support.RegisterPEPClient(t, fx.pool, db.PEPClient{ServiceID: fx.service, Name: "gateway",
		Kind: db.PEPKindAPIKey, Enabled: true})
	support.IssueTestAPIKey(t, fx.pool, gateway.ID)
	_ = fx.directory(t, ctx)

	fx.app.AfterPEPWrite()
	fx.pool.Close()
	got, err := fx.app.PEPDirectory(ctx)
	if err == nil {
		t.Fatalf("the directory with the revisions unreadable was served: %v", got)
	}
	if got != nil {
		t.Error("a directory was returned together with the read error")
	}
}
