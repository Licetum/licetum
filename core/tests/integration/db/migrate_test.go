//go:build integration

package db_test

import (
	"context"
	"testing"

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
