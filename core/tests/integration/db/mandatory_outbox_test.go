//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	support "github.com/licetum/licetum/core/testsupport"
)

func TestContract_WR02_RawWritesTruncationAndReplicaModeRequireOutbox(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	id := uuid.New()
	if err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO role(id,name) VALUES($1,'baseline')`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	typ := uuid.New()
	if err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO type_resource(id,service_id,name) VALUES($1,$2,'cascade-type')`, typ, service); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO role_permission(role_id,type_resource_id,access) VALUES($1,$2,'{read}')`, id, typ)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for name, sql := range map[string]string{
		"insert":      `INSERT INTO role(id,name) VALUES(gen_random_uuid(),'unpublished')`,
		"update":      `UPDATE role SET name='unpublished'`,
		"cascade":     `ALTER TABLE role DISABLE TRIGGER licetum_outbox_delete; DELETE FROM role; ALTER TABLE role ENABLE ALWAYS TRIGGER licetum_outbox_delete`,
		"delete":      `DELETE FROM role`,
		"truncate":    `TRUNCATE role CASCADE`,
		"replica":     `SET LOCAL session_replication_role=replica; INSERT INTO role(name) VALUES('unpublished')`,
		"immediate":   `INSERT INTO role(name) VALUES('unpublished'); SET CONSTRAINTS ALL IMMEDIATE`,
		"temp_shadow": `CREATE TEMP TABLE tx_dirty(xid xid8 PRIMARY KEY); INSERT INTO role(name) VALUES('unpublished')`,
	} {
		t.Run(name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, sql)
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "licetum_outbox_required" {
				t.Fatalf("write was not rejected by the deferred outbox check: %v", err)
			}
		})
	}
}

func TestContract_WR02_EmptyStatementsAndRolledBackSubtransactionsNeedNoEntry(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	id := uuid.New()
	if err := support.RawPolicyTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO role(id,name) VALUES($1,'baseline')`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE role SET name='nothing' WHERE false`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO role(id,name) VALUES($1,'ignored') ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatal(err)
	}
	sub, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Exec(ctx, `INSERT INTO role(name) VALUES('rolled-back')`); err != nil {
		t.Fatal(err)
	}
	if err := sub.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("no tuple survived, but commit needed an entry: %v", err)
	}
}

func TestContract_WR02_OwnerCanDisableTriggersAndReconciliationMustDetectIt(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE role DISABLE TRIGGER ALL; INSERT INTO role(name) VALUES('owner-bypass'); ALTER TABLE role ENABLE TRIGGER ALL`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"insert", "update", "delete", "truncate"} {
		if _, err := tx.Exec(ctx, `ALTER TABLE role ENABLE ALWAYS TRIGGER licetum_outbox_`+name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("an owner bypass is outside the check's guarantee: %v", err)
	}
}
