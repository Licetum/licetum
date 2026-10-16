//go:build integration

package db_test

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3/lock"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

func TestMigrateCreatesCurrentSchemaAndSeedsPlatform(t *testing.T) {
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infrastructure.Terminate(ctx) })

	cfg := &config.Config{
		DB: config.Database{URI: infrastructure.DBURI, PoolSize: 3},
		Platform: config.Platform{
			Name: "Licetum", URL: "https://licetum.test", Icon: "https://licetum.test/icon.png",
			AuthorizationURL: "https://idp.test/auth", TokenURL: "https://idp.test/token", JwksURL: "https://idp.test/jwks",
			Issuer: "https://idp.test", ClientID: "licetum", ClientSecret: "secret",
			AdminGroupName: "Platform administrator", AdminClaim: "preferred_username", AdminClaimValues: []string{"admin"},
		},
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"service", "type_resource", "resource_cache", "assignment_rule", "user_group", "group_member", "role", "role_permission"} {
		t.Run("table "+table, func(t *testing.T) {
			var exists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("table %q was not created", table)
			}
		})
	}

	var services, groups, policies int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM service`).Scan(&services); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_group`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM type_resource WHERE deleted_at IS NULL AND service_id = $1`,
		model.PlatformServiceID).Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if services == 0 || groups == 0 || policies != len(model.PlatformTypes) {
		t.Fatalf("platform seed is incomplete: services=%d groups=%d policies=%d", services, groups, policies)
	}

	// The lists of the platform's own entities are never served from the cache of fetched lists: an entity created a
	// moment ago would be missing from it, and changing it would be answered with 403 until the list ran out.
	var uncached int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM synchronizer s JOIN type_resource t ON t.id = s.type_resource_id
		WHERE t.service_id = $1 AND s.config->>'cache_ttl' = '0'`, model.PlatformServiceID).Scan(&uncached); err != nil {
		t.Fatal(err)
	}
	if uncached != len(model.PlatformTypes) {
		t.Fatalf("%d of %d platform sources have no list cache", uncached, len(model.PlatformTypes))
	}
}

func platformConfig(uri string, poolSize int) *config.Config {
	return &config.Config{
		DB: config.Database{URI: uri, PoolSize: poolSize},
		Platform: config.Platform{
			Name: "Licetum", URL: "https://licetum.test", Icon: "https://licetum.test/icon.png",
			AuthorizationURL: "https://idp.test/auth", TokenURL: "https://idp.test/token", JwksURL: "https://idp.test/jwks",
			Issuer: "https://idp.test", ClientID: "licetum", ClientSecret: "secret",
			AdminGroupName: "Platform administrator", AdminClaim: "preferred_username", AdminClaimValues: []string{"admin"},
		},
	}
}

// seedState is what the seed of the self-service leaves behind, as a comparable summary.
type seedState struct {
	services, groups, members, policies, rules int
	rulesRevision                              int64
	appliedMigrations                          int
	versionRows                                int
}

func readSeedState(ctx context.Context, t *testing.T, uri string) seedState {
	t.Helper()
	pool, err := db.Connect(ctx, config.Database{URI: uri, PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var s seedState
	for query, dest := range map[string]*int{
		`SELECT count(*) FROM service`:                                              &s.services,
		`SELECT count(*) FROM user_group`:                                           &s.groups,
		`SELECT count(*) FROM group_member`:                                         &s.members,
		`SELECT count(*) FROM type_resource`:                                        &s.policies,
		`SELECT count(*) FROM assignment_rule`:                                      &s.rules,
		`SELECT count(*) FROM goose_db_version WHERE is_applied AND version_id > 0`: &s.appliedMigrations,
		`SELECT count(*) FROM goose_db_version WHERE version_id > 0`:                &s.versionRows,
	} {
		if err := pool.QueryRow(ctx, query).Scan(dest); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	rev, err := db.LoadRevisions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	s.rulesRevision = rev.Rules
	return s
}

// freshSchemaURI creates a schema of its own in the database of uri and returns the uri that works in it: every round of a
// test gets an empty database without a server of its own.
func freshSchemaURI(ctx context.Context, t *testing.T, uri, schema string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, db.NormalizeDSN(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(db.NormalizeDSN(uri))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// TestF5_10_ParallelMigrateRunsApplyEachMigrationOnce starts five `migrate` runs at the same moment on an empty database,
// as the pods of a release do: every run finishes without an error, each migration is applied by exactly one of them, and
// the seed of the self-service is made once (one admin group, one set of policies, the revision bumped once). Without the
// lock the runs collide only now and then, so the scenario is repeated.
func TestF5_10_ParallelMigrateRunsApplyEachMigrationOnce(t *testing.T) {
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infrastructure.Terminate(ctx) })

	// What one run alone leaves, in a schema of its own, is what five runs must leave.
	aloneURI := freshSchemaURI(ctx, t, infrastructure.DBURI, "alone")
	cfg := platformConfig(aloneURI, 2)
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	want := readSeedState(ctx, t, aloneURI)
	if want.appliedMigrations != int(db.LatestMigration()) {
		t.Fatalf("the reference run applied %d migrations, the latest is %d", want.appliedMigrations, db.LatestMigration())
	}

	const runs, rounds = 5, 4
	for round := range rounds {
		uri := freshSchemaURI(ctx, t, infrastructure.DBURI, fmt.Sprintf("parallel_%d", round))
		var wg sync.WaitGroup
		errs := make([]error, runs)
		start := make(chan struct{})
		for i := range runs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cfg := platformConfig(uri, 2)
				pool, err := db.Connect(ctx, cfg.DB)
				if err != nil {
					errs[i] = err
					return
				}
				defer pool.Close()
				<-start
				errs[i] = db.Migrate(ctx, pool, cfg)
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("round %d, run %d: %v", round, i, err)
			}
		}

		got := readSeedState(ctx, t, uri)
		if got != want {
			t.Fatalf("round %d: five parallel runs left %+v, one run alone leaves %+v", round, got, want)
		}
		if got.versionRows != got.appliedMigrations {
			t.Fatalf("round %d: %d rows in the version table for %d applied migrations: a migration was applied twice",
				round, got.versionRows, got.appliedMigrations)
		}
	}
}

// TestF5_10_MigrateWaitsForTheLockOfAnotherRun holds the lock of the migrations from outside, as a run in another pod
// does: migrate applies nothing while it is held and finishes once it is released. The rounds above show the effect when
// the runs race; this one shows the cause, and does not depend on the timing.
func TestF5_10_MigrateWaitsForTheLockOfAnotherRun(t *testing.T) {
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infrastructure.Terminate(ctx) })

	holder, err := pgx.Connect(ctx, db.NormalizeDSN(infrastructure.DBURI))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close(ctx) })
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_lock($1)`, lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}

	cfg := platformConfig(infrastructure.DBURI, 2)
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	done := make(chan error, 1)
	go func() { done <- db.Migrate(ctx, pool, cfg) }()

	select {
	case err := <-done:
		t.Fatalf("migrate finished (%v) while another run held the lock", err)
	case <-time.After(2500 * time.Millisecond):
	}
	if version, err := db.SchemaVersion(ctx, pool); err == nil && version != 0 {
		t.Fatalf("the schema is at version %d: migrate applied migrations while the lock was held", version)
	}

	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("migrate after the lock was released: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("migrate did not finish after the lock was released")
	}
	if version, err := db.SchemaVersion(ctx, pool); err != nil || version != db.LatestMigration() {
		t.Fatalf("schema version %d (%v), want %d", version, err, db.LatestMigration())
	}
}

// TestF5_10_SchemaVersionIsTheLatestMigration: after a run of migrate the database is at the version the binary expects,
// which is what the readiness probe compares.
func TestF5_10_SchemaVersionIsTheLatestMigration(t *testing.T) {
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infrastructure.Terminate(ctx) })
	cfg := platformConfig(infrastructure.DBURI, 2)
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if _, err := db.SchemaVersion(ctx, pool); err == nil {
		t.Fatal("a database that was never migrated has no schema version")
	}
	if err := db.Migrate(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	version, err := db.SchemaVersion(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if version != db.LatestMigration() || version == 0 {
		t.Fatalf("schema version %d, latest embedded migration %d", version, db.LatestMigration())
	}
}
