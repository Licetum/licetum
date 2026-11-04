//go:build integration

package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/licetum/licetum/core/db"
	support "github.com/licetum/licetum/core/testsupport"
)

func TestAdminBindingsEnforceSelectorsScopesRolesAndGroupOwnership(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	group := uuid.New()
	foreign := uuid.New()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_group(id,service_id,name) VALUES($1,$2,'managed')`, []any{group, service}},
		{`INSERT INTO service(id,name,slug,url,icon) VALUES($1,'Foreign','binding-foreign','','')`, []any{foreign}},
	} {
		if _, err := support.RawExec(ctx, pool, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	for name, sql := range map[string]string{
		"platform subject":  `INSERT INTO admin_binding(scope_kind,role,subject) VALUES('platform','platform_viewer','viewer')`,
		"service predicate": `INSERT INTO admin_binding(scope_kind,service_id,role,predicate) VALUES('service',$1,'service_viewer','[]')`,
		"group manager":     `INSERT INTO admin_binding(scope_kind,service_id,group_id,role,subject) VALUES('group',$1,$2,'group_manager','manager')`,
	} {
		t.Run(name, func(t *testing.T) {
			args := []any{}
			if strings.Contains(sql, "$1") {
				args = append(args, service)
			}
			if strings.Contains(sql, "$2") {
				args = append(args, group)
			}
			if _, err := support.RawExec(ctx, pool, sql, args...); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, sql := range map[string]string{
		"neither selector": `INSERT INTO admin_binding(scope_kind,role) VALUES('platform','platform_admin')`,
		"both selectors":   `INSERT INTO admin_binding(scope_kind,role,subject,predicate) VALUES('platform','platform_admin','admin','[]')`,
		"predicate object": `INSERT INTO admin_binding(scope_kind,role,predicate) VALUES('platform','platform_admin','{}')`,
		"platform service": `INSERT INTO admin_binding(scope_kind,role,service_id,subject) VALUES('platform','platform_admin',$1,'admin')`,
		"service missing":  `INSERT INTO admin_binding(scope_kind,role,subject) VALUES('service','service_owner','owner')`,
		"group missing":    `INSERT INTO admin_binding(scope_kind,service_id,role,subject) VALUES('group',$1,'group_manager','manager')`,
		"platform role":    `INSERT INTO admin_binding(scope_kind,role,subject) VALUES('platform','service_owner','owner')`,
		"service role":     `INSERT INTO admin_binding(scope_kind,service_id,role,subject) VALUES('service',$1,'platform_admin','admin')`,
	} {
		t.Run(name, func(t *testing.T) {
			args := []any{}
			if strings.Contains(sql, "$1") {
				args = append(args, service)
			}
			_, err := support.RawExec(ctx, pool, sql, args...)
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "23514" {
				t.Fatalf("invalid binding accepted: %v", err)
			}
		})
	}
	_, err := support.RawExec(ctx, pool, `INSERT INTO admin_binding(scope_kind,service_id,group_id,role,subject) VALUES('group',$1,$2,'group_manager','foreign')`, foreign, group)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23503" {
		t.Fatalf("foreign group binding accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admin_binding(scope_kind,role,subject) VALUES('platform','platform_admin','unpublished')`); err == nil || !strings.Contains(err.Error(), "outbox") {
		t.Fatalf("binding bypassed mandatory publication: %v", err)
	}
}

func TestReservedV2TablesAndSynchronizerIntervals(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	var empty bool
	if err := pool.QueryRow(ctx, `SELECT bootstrapped_at IS NULL AND bootstrapped_by IS NULL FROM platform_state WHERE id=1`).Scan(&empty); err != nil || !empty {
		t.Fatalf("unexpected bootstrap state: %v %v", empty, err)
	}
	var unresolved int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_unresolved`).Scan(&unresolved); err != nil || unresolved != 0 {
		t.Fatalf("reserved unresolved table: %d %v", unresolved, err)
	}
	typ, source := uuid.New(), uuid.New()
	if _, err := support.RawExec(ctx, pool, `INSERT INTO type_resource(id,service_id,name) VALUES($1,$2,'interval-type')`, typ, service); err != nil {
		t.Fatal(err)
	}
	if _, err := support.RawExec(ctx, pool, `INSERT INTO synchronizer(id,type_resource_id,kind,config) VALUES($1,$2,'HTTP','{"url":"http://source.test"}')`, source, typ); err != nil {
		t.Fatal(err)
	}
	var seconds float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM refresh_interval)::float8 FROM synchronizer WHERE id=$1`, source).Scan(&seconds); err != nil || seconds != 300 {
		t.Fatalf("default interval: %v %v", seconds, err)
	}
	if _, err := support.RawExec(ctx, pool, `UPDATE synchronizer SET refresh_interval=interval '9 seconds' WHERE id=$1`, source); err == nil {
		t.Fatal("interval shorter than ten seconds accepted")
	}
	if _, err := support.RawExec(ctx, pool, `UPDATE synchronizer SET refresh_interval=interval '10 seconds',refresh_interval_max=interval '1 minute' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	issuer := "https://issuer.test"
	if _, err := support.RawExec(ctx, pool, `INSERT INTO pep_issuer(issuer,jwks_url) VALUES($1,$1||'/jwks')`, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pep_issuer_keys(issuer,key_set,digest) VALUES($1,'{"keys":[]}',decode(repeat('00',32),'hex'))`, issuer); err == nil || !strings.Contains(err.Error(), "outbox") {
		t.Fatalf("key set bypassed publication: %v", err)
	}
	if _, err := db.PolicyTx(ctx, pool, nil, func(ctx context.Context, tx pgx.Tx) ([]db.Change, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO pep_issuer_keys(issuer,key_set,digest) VALUES($1,'{"keys":[]}',decode(repeat('00',32),'hex'))`, issuer); err != nil {
			return nil, err
		}
		return []db.Change{{Entity: "pep_issuer_keys", Actor: "test", Action: "create", Touched: []db.Touched{{Table: "pep_issuer_keys", Key: map[string]any{"issuer": issuer}}}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := support.RawExec(ctx, pool, `DELETE FROM pep_issuer WHERE issuer=$1`, issuer); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pep_issuer_keys`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("issuer keys did not cascade: %d %v", remaining, err)
	}
}
