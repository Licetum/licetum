//go:build integration

package db_test

import (
	"testing"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
)

func TestRestoreAdminAccessClearsLiteralSubjectType(t *testing.T) {
	t.Setenv("ADMIN_AUTH__ISSUER", "https://idp.test")
	t.Setenv("ADMIN_AUTH__AUDIENCE", "licetum-admin")
	t.Setenv("ADMIN_AUTH__JWKS_URL", "https://idp.test/jwks")
	t.Setenv("PLATFORM__ADMIN_CLAIM_VALUES", "admin")
	ctx, pool, cfg := seededPool(t)
	if err := db.Migrate(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	id := model.PlatformAdminRuleID(model.PlatformAssignmentRule)
	for _, subjectType := range []string{"user", "service"} {
		t.Run(subjectType, func(t *testing.T) {
			if _, err := support.RawExec(ctx, pool, `
				UPDATE assignment_rule SET subject='rescuer',subject_type=$2,subject_group_id=NULL
				WHERE id=$1`, id, subjectType); err != nil {
				t.Fatal(err)
			}
			if _, err := db.RestoreAdminAccess(ctx, pool, cfg, nil); err != nil {
				t.Fatalf("restore a %s literal: %v", subjectType, err)
			}
			var restored bool
			if err := pool.QueryRow(ctx, `SELECT subject IS NULL AND subject_type IS NULL AND subject_group_id=$2
				FROM assignment_rule WHERE id=$1`, id, model.PlatformAdminGroupID).Scan(&restored); err != nil {
				t.Fatal(err)
			}
			if !restored {
				t.Fatal("the group rule retained its literal subject or type")
			}
			var before, after int64
			if err := pool.QueryRow(ctx, `SELECT revision FROM revision_state WHERE scope='global'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := db.RestoreAdminAccess(ctx, pool, cfg, nil); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT revision FROM revision_state WHERE scope='global'`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("repeated recovery published a change: revision %d -> %d", before, after)
			}
		})
	}
}
