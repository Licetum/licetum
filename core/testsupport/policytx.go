package support

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/outbox"
)

// RawPolicyTx is for fixture setup, never production writers. Its explicit empty
// header lets raw fixture SQL obey the same mandatory-publication boundary.
func RawPolicyTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		_, err := db.TakeRevision(ctx, tx, outbox.Entry{})
		return err
	})
}

var coveredSQL = regexp.MustCompile(`(?i)\b(?:insert\s+into|update|delete\s+from|truncate(?:\s+table)?)\s+(?:[a-z_][a-z0-9_]*\.)?"?(?:service|type_resource|type_resource_parent|synchronizer|access_group|user_group|group_member|role|role_permission|assignment_rule|log_settings|subject_directory|pep_client|pep_credential|pep_issuer|pep_issuer_keys|identity_domain|admin_binding)"?\b`)

// RawExec routes covered fixture writes through RawPolicyTx while leaving source
// data, clocks, DDL and pre-v2 migration fixtures unchanged. Negative enforcement
// tests deliberately use the native pool/transaction methods instead.
func RawExec(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (pgconn.CommandTag, error) {
	if !coveredSQL.MatchString(sql) {
		return pool.Exec(ctx, sql, args...)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('outbox') IS NOT NULL`).Scan(&exists); err != nil {
		return pgconn.CommandTag{}, err
	}
	if !exists {
		return pool.Exec(ctx, sql, args...)
	}
	var tag pgconn.CommandTag
	err := RawPolicyTx(ctx, pool, func(tx pgx.Tx) error { var err error; tag, err = tx.Exec(ctx, sql, args...); return err })
	return tag, err
}

// AdvanceRevision is a fixture-only publication with no projected images. Tests
// use it when they need a new position independently of a policy mutation.
func AdvanceRevision(ctx context.Context, q db.Querier) (int64, error) {
	var position db.Position
	var err error
	switch writer := q.(type) {
	case pgx.Tx:
		position, err = db.TakeRevision(ctx, writer, outbox.Entry{})
	case *pgxpool.Pool:
		err = db.InTx(ctx, writer, func(tx pgx.Tx) error { position, err = db.TakeRevision(ctx, tx, outbox.Entry{}); return err })
	default:
		return 0, fmt.Errorf("fixture publication requires a transaction or pool")
	}
	return position.Revision, err
}
