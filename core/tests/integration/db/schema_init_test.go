//go:build integration

package db_test

import (
	"testing"
	"time"

	"github.com/licetum/licetum/core/db"
)

func TestSchemaV2InitializerRoundTripPreservesDefaultsAndNamespace(t *testing.T) {
	for _, schema := range []string{"public", "v2_custom"} {
		t.Run(schema, func(t *testing.T) {
			ctx, pool, cfg := seededPool(t)
			if schema != "public" {
				conn := cfg.DB
				conn.URI = freshSchemaURI(ctx, t, conn.URI, schema)
				scoped, err := db.Connect(ctx, conn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(scoped.Close)
				pool = scoped
			}
			install := func() time.Time {
				t.Helper()
				if err := db.MigrateUpTo(ctx, pool, 1); err != nil {
					t.Fatal(err)
				}
				var ready bool
				if err := pool.QueryRow(ctx, `SELECT
    (SELECT count(*)=1 AND min(epoch)=1 AND min(revision)=0 AND bool_and(scope='global') FROM revision_state)
    AND (SELECT count(*)=1 AND bool_and(id=1 AND epoch=1 AND last_published=0) FROM publish_state)
    AND (SELECT count(*)=1 AND bool_and(id=1 AND bootstrapped_at IS NULL AND bootstrapped_by IS NULL) FROM platform_state)
    AND (SELECT count(*)=1 AND bool_and(id=1 AND version=1 AND updated_by='' AND settings='{"format":"json","level":"info","fields":[],"events":{"access":true,"decision":true,"sync":true},"subject":"hash","sample_allowed":0.1}'::jsonb) FROM log_settings)
    AND (SELECT count(*)=1 FROM identity_domain)
    AND (SELECT count(*)=0 FROM outbox)
    AND (SELECT count(*)=0 FROM tx_dirty)`).Scan(&ready); err != nil {
					t.Fatal(err)
				}
				if !ready {
					t.Fatal("initializer defaults differ from the verified chain")
				}
				var created time.Time
				if err := pool.QueryRow(ctx, `SELECT created_at FROM identity_domain WHERE id='09a5f963-232f-503e-9001-2ddf0d22e93d' AND name='default' AND description=''`).Scan(&created); err != nil {
					t.Fatal(err)
				}
				var triggers int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=to_regnamespace(current_schema()) AND NOT t.tgisinternal AND t.tgenabled='A'`).Scan(&triggers); err != nil {
					t.Fatal(err)
				}
				if triggers != 74 {
					t.Fatalf("ALWAYS triggers=%d, want 74", triggers)
				}
				var publicExecute int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc p CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a WHERE p.pronamespace=to_regnamespace(current_schema()) AND p.proname IN ('licetum_cover_table','licetum_mark_rows','licetum_mark_truncate','licetum_require_outbox') AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(&publicExecute); err != nil {
					t.Fatal(err)
				}
				if publicExecute != 0 {
					t.Fatal("mandatory-outbox functions are executable by PUBLIC")
				}
				return created
			}
			first := install()
			if err := db.MigrateDownTo(ctx, pool, 0); err != nil {
				t.Fatal(err)
			}
			version, err := db.SchemaVersion(ctx, pool)
			if err != nil || version != 0 {
				t.Fatalf("down version=%d: %v", version, err)
			}
			var objects int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_class WHERE relnamespace=to_regnamespace(current_schema()) AND relkind IN ('r','p') AND relname<>'goose_db_version_v2')+(SELECT count(*) FROM pg_proc WHERE pronamespace=to_regnamespace(current_schema()) AND proname LIKE 'licetum_%')`).Scan(&objects); err != nil {
				t.Fatal(err)
			}
			if objects != 0 {
				t.Fatalf("down left %d application objects", objects)
			}
			var kindExists bool
			if err := pool.QueryRow(ctx, `SELECT to_regtype('synchronizer_kind') IS NOT NULL`).Scan(&kindExists); err != nil {
				t.Fatal(err)
			}
			if kindExists {
				t.Fatal("down retained the application enum")
			}
			second := install()
			if !second.After(first) {
				t.Fatal("initializer baked a creation timestamp instead of using the installation time")
			}
		})
	}
}
