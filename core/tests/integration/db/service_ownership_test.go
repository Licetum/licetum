//go:build integration

package db_test

import (
	"context"
	"testing"

	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	support "github.com/licetum/licetum/core/testsupport"
)

func TestCompositeReferencesKeepGroupsAndRoleItemsInsideTheirService(t *testing.T) {
	pool, first := newPEPPool(t)
	ctx := context.Background()
	second, group, child, role, typ := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO service(id,name,url,icon,slug) VALUES($1,'foreign','','','foreign')`, second); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_group(id,name,service_id) VALUES($1,'same',$3),($2,'same',$4)`, group, child, first, second); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role(id,name,service_id) VALUES($1,'role',$2)`, role, first); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO type_resource(id,name,service_id) VALUES($1,'foreign',$2)`, typ, second)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for name, statement := range map[string]string{
		"nested group": `INSERT INTO group_member(service_id,group_id,member_group_id,predicate,enabled) VALUES($1,$2,$3,'[]',true)`,
		"role item":    `INSERT INTO role_permission(service_id,role_id,type_resource_id,access) VALUES($1,$2,$3,'{read}')`,
	} {
		t.Run(name, func(t *testing.T) {
			parent, target := group, child
			if name == "role item" {
				parent, target = role, typ
			}
			err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, statement, first, parent, target); return err })
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
				t.Fatalf("foreign-service reference: %v", err)
			}
		})
	}
}
