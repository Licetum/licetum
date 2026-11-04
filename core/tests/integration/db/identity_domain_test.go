//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/licetum/licetum/core/db"
	support "github.com/licetum/licetum/core/testsupport"
)

func TestIdentityDomainWritesPublishEmptyHeadersAndTypesMatchTheirService(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	domain := uuid.New()
	position, err := db.PolicyTx(ctx, pool, nil, func(ctx context.Context, tx pgx.Tx) ([]db.Change, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO identity_domain(id,name) VALUES($1,'another-domain')`, domain); err != nil {
			return nil, err
		}
		return []db.Change{{Entity: "identity_domain", ID: &domain, Actor: "test", Action: "create", Touched: []db.Touched{{Table: "identity_domain", Key: map[string]any{"id": domain}}}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var kind string
	var services []uuid.UUID
	var streams []string
	var policy json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT kind,services,streams,policy FROM outbox WHERE revision=$1`, position.Revision).Scan(&kind, &services, &streams, &policy); err != nil {
		t.Fatal(err)
	}
	if kind != "change" || len(services) != 0 || len(streams) != 0 || string(policy) != "{}" {
		t.Fatalf("domain header: %s %v %v %s", kind, services, streams, policy)
	}
	service := uuid.New()
	if _, err := support.RawExec(ctx, pool, `INSERT INTO service(id,name,slug,url,icon,identity_domain_id) VALUES($1,'Domain service','domain-service','','',$2)`, service, domain); err != nil {
		t.Fatal(err)
	}
	_, err = support.RawExec(ctx, pool, `INSERT INTO type_resource(service_id,name,identity_domain_id) SELECT $1,'wrong-domain',id FROM identity_domain WHERE name='default'`, service)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
		t.Fatalf("cross-domain type accepted: %v", err)
	}
	if _, err := support.RawExec(ctx, pool, `INSERT INTO type_resource(service_id,name,identity_domain_id) VALUES($1,'right-domain',$2)`, service, domain); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityDomainRawWritesRequireAnOutboxPublication(t *testing.T) {
	pool, _ := newPEPPool(t)
	_, err := pool.Exec(context.Background(), `INSERT INTO identity_domain(name) VALUES('unpublished')`)
	if err == nil {
		t.Fatal("domain bypassed mandatory outbox")
	}
}
