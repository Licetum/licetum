//go:build integration

package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

// seededPool starts PostgreSQL and returns a pool on it and the configuration the environment gives.
func seededPool(t *testing.T) (context.Context, *pgxpool.Pool, *config.Config) {
	t.Helper()
	ctx := context.Background()
	infrastructure, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infrastructure.Terminate(ctx) })
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DB = config.Database{URI: infrastructure.DBURI, PoolSize: 3}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool, cfg
}

// TestContract_ADM13_TheSeedAndRestoreAdminNeedOnlyTheAdminAuthSettings: with ADMIN_AUTH__* and no OAuth variable of PLATFORM__*
// the self-service is seeded (its service row carries no OAuth client), and restore-admin puts the access back and matches
// its users by the claim of ADMIN_AUTH__SUBJECT_CLAIM.
func TestContract_ADM13_TheSeedAndRestoreAdminNeedOnlyTheAdminAuthSettings(t *testing.T) {
	t.Setenv("ADMIN_AUTH__ISSUER", "https://idp.test")
	t.Setenv("ADMIN_AUTH__AUDIENCE", "licetum-admin")
	t.Setenv("ADMIN_AUTH__JWKS_URL", "https://idp.test/jwks")
	t.Setenv("ADMIN_AUTH__SUBJECT_CLAIM", "email")
	t.Setenv("PLATFORM__ADMIN_CLAIM_VALUES", "admin@example.com")
	ctx, pool, cfg := seededPool(t)

	if err := db.Migrate(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	var policies, rules int
	var oauth int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM type_resource WHERE deleted_at IS NULL AND service_id = $1`,
		model.PlatformServiceID).Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM service WHERE id = $1 AND authorization_url IS NULL AND token_url IS NULL
		AND jwks_url IS NULL AND issuer IS NULL AND client_id IS NULL AND client_secret IS NULL`,
		model.PlatformServiceID).Scan(&oauth); err != nil {
		t.Fatal(err)
	}
	if policies != len(model.PlatformTypes) || oauth != 1 {
		t.Fatalf("the seed made %d of %d policies, and the platform service without an OAuth client is %d", policies, len(model.PlatformTypes), oauth)
	}
	var attr string
	if err := pool.QueryRow(ctx, `SELECT predicate->0->>'attr' FROM group_member WHERE id = $1`, model.PlatformAdminGroupMemberID).Scan(&attr); err != nil {
		t.Fatal(err)
	}
	if attr != "email" {
		t.Errorf("the admin group is matched by %q, want the subject claim email", attr)
	}

	// Nobody can manage the platform: the group is emptied and its rules are gone.
	for _, stmt := range []string{`DELETE FROM group_member`, `DELETE FROM assignment_rule`} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	report, err := db.RestoreAdminAccess(ctx, pool, cfg, []string{"rescuer@example.com"})
	if err != nil {
		t.Fatalf("restore-admin with only ADMIN_AUTH__*: %v", err)
	}
	if report.Members != 2 {
		t.Errorf("restored %d memberships, want 2 (the claim values and the user)", report.Members)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assignment_rule WHERE enabled AND effect = 'allow' AND resource_id IS NULL
		AND subject = $1`, model.GroupSubject(model.PlatformAdminGroupID)).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != len(model.PlatformTypes) {
		t.Errorf("%d of %d admin rules are back", rules, len(model.PlatformTypes))
	}
	if err := pool.QueryRow(ctx, `SELECT predicate->0->>'attr' FROM group_member WHERE id = $1`,
		model.PlatformRestoredMemberID("rescuer@example.com")).Scan(&attr); err != nil {
		t.Fatal(err)
	}
	if attr != "email" {
		t.Errorf("the restored user is matched by %q, want the subject claim email", attr)
	}
}

// Without ADMIN_AUTH__* nobody could sign in to the admin API: the seed is skipped with a log line and restore-admin says why it
// cannot run.
func TestSeedIsSkippedAndRestoreRefusedWithoutAdminAuth(t *testing.T) {
	ctx, pool, cfg := seededPool(t)
	if cfg.AdminAuth.IsConfigured() {
		t.Skip("ADMIN_AUTH__* is set in the environment")
	}
	if err := db.Migrate(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	var services int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM service`).Scan(&services); err != nil {
		t.Fatal(err)
	}
	if services != 0 {
		t.Fatalf("%d services were seeded without ADMIN_AUTH__*", services)
	}
	if _, err := db.RestoreAdminAccess(ctx, pool, cfg, []string{"rescuer"}); err == nil || !strings.Contains(err.Error(), "ADMIN_AUTH__") {
		t.Fatalf("restore-admin without ADMIN_AUTH__* = %v, want an error that names it", err)
	}
}
