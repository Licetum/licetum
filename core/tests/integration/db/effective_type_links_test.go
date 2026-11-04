//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/outbox"
	support "github.com/licetum/licetum/core/testsupport"
)

func TestTypeGraphComponentsAndCatalogUseOnlyEffectiveLinks(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	parent, child := uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{parent, child} {
		if _, err := support.RawExec(ctx, pool, `INSERT INTO type_resource(id,service_id,name) VALUES($1,$2,$3)`, id, service, []string{"parent", "child"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := support.RawExec(ctx, pool, `INSERT INTO type_resource_parent(type_resource_id,parent_id,inherit,live,eff_inherit,eff_live) VALUES($1,$2,ARRAY['write'],false,ARRAY['read'],true)`, child, parent); err != nil {
		t.Fatal(err)
	}
	app := engine.New(pool, &config.Config{})
	t.Cleanup(app.Close)
	graph, err := app.LoadTypeGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.DescendantsOf(parent)[child].Sorted(); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("desired mask leaked: %v", got)
	}
	if !slices.Equal(engine.ComponentKeys(graph, []uuid.UUID{child}), engine.ComponentKeys(graph, []uuid.UUID{parent})) {
		t.Fatal("effective link did not connect the components")
	}
	image, err := outbox.Project(ctx, pool, "type_resource_parent", map[string]any{"type_resource_id": child, "parent_id": parent})
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(image.Row, &row); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"inherit", "live"} {
		if _, present := row[column]; present {
			t.Fatalf("desired column %s leaked: %s", column, image.Row)
		}
	}
	if row["eff_live"] != true || row["eff_inherit"].([]any)[0] != "read" {
		t.Fatalf("effective image: %s", image.Row)
	}
	if _, err := support.RawExec(ctx, pool, `UPDATE type_resource_parent SET live=true,eff_live=false WHERE type_resource_id=$1`, child); err != nil {
		t.Fatal(err)
	}
	graph, err = app.LoadTypeGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if graph.AncestorTypes(child)[parent] {
		t.Fatal("desired-only link entered the effective graph")
	}
	if slices.Equal(engine.ComponentKeys(graph, []uuid.UUID{child}), engine.ComponentKeys(graph, []uuid.UUID{parent})) {
		t.Fatal("desired-only link joined effective components")
	}
}

func TestTypeLinksCannotCrossIdentityDomains(t *testing.T) {
	pool, service := newPEPPool(t)
	ctx := context.Background()
	domain, foreignService, child, parent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity_domain(id,name) VALUES($1,'foreign')`, []any{domain}},
		{`INSERT INTO service(id,name,slug,url,icon,identity_domain_id) VALUES($1,'Foreign','foreign','','',$2)`, []any{foreignService, domain}},
		{`INSERT INTO type_resource(id,service_id,name) VALUES($1,$2,'child')`, []any{child, service}},
		{`INSERT INTO type_resource(id,service_id,name,identity_domain_id) VALUES($1,$2,'parent',$3)`, []any{parent, foreignService, domain}},
	} {
		if _, err := support.RawExec(ctx, pool, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	_, err := support.RawExec(ctx, pool, `INSERT INTO type_resource_parent(type_resource_id,parent_id) VALUES($1,$2)`, child, parent)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
		t.Fatalf("cross-domain link accepted: %v", err)
	}
}
