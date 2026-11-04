//go:build integration

package db_test

import (
	"testing"

	"github.com/licetum/licetum/core/db"
)

func TestSchemaV2RetiresLegacyObjectsOnFreshInstall(t *testing.T) {
	ctx, pool, _ := seededPool(t)
	if err := db.MigrateUpTo(ctx, pool, db.LatestMigration()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"authz_change", "authz_change_id_seq", "delegated_call", "delegated_call_id_seq", "known_subject", "sync_job", "sync_job_id_seq", "sync_job_token_seq"} {
		var absent bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, name).Scan(&absent); err != nil {
			t.Fatal(err)
		}
		if !absent {
			t.Errorf("retired object %s still exists", name)
		}
	}
	var scopes int
	var global bool
	if err := pool.QueryRow(ctx, `SELECT count(*),bool_and(scope='global') FROM revision_state`).Scan(&scopes, &global); err != nil {
		t.Fatal(err)
	}
	if scopes != 1 || !global {
		t.Fatalf("revision scopes: count=%d global=%v", scopes, global)
	}
	for _, name := range []string{"sync_demand", "sync_executor", "sync_state"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("current synchronization object %s was removed", name)
		}
	}
	var invalidationColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='sync_state' AND column_name='invalidated_at'`).Scan(&invalidationColumns); err != nil {
		t.Fatal(err)
	}
	if invalidationColumns != 1 {
		t.Fatal("sync_state.invalidated_at is still required")
	}
	var uncommented int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE relnamespace=to_regnamespace(current_schema()) AND relkind IN ('r','p') AND relname NOT IN ('goose_db_version','goose_db_version_v2') AND obj_description(oid,'pg_class') IS NULL`).Scan(&uncommented); err != nil {
		t.Fatal(err)
	}
	if uncommented != 0 {
		t.Fatalf("%d application tables have no schema comment", uncommented)
	}
}
