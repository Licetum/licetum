//go:build integration

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	support "github.com/licetum/licetum/core/testsupport"
)

func TestNewTypesInheritTheIdentityDomainOfTheirService(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	domain, service := uuid.New(), uuid.New()
	if _, err := support.RawExec(ctx, e.pool, `INSERT INTO identity_domain(id,name) VALUES($1,'private-domain')`, domain); err != nil {
		t.Fatal(err)
	}
	if _, err := support.RawExec(ctx, e.pool, `INSERT INTO service(id,name,slug,url,icon,identity_domain_id) VALUES($1,'Private','private-domain','','',$2)`, service, domain); err != nil {
		t.Fatal(err)
	}
	typ := e.must(http.MethodPost, "/service/"+service.String()+"/type_resource", map[string]any{"name": "private-item"})
	var got uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT identity_domain_id FROM type_resource WHERE id=$1`, typ["id"]).Scan(&got); err != nil || got != domain {
		t.Fatalf("type domain: %v %v", got, err)
	}
	created := e.must(http.MethodPost, "/service", map[string]any{"name": "Default", "slug": "default-domain-service"})
	var name string
	if err := e.pool.QueryRow(ctx, `SELECT d.name FROM service s JOIN identity_domain d ON d.id=s.identity_domain_id WHERE s.id=$1`, created["id"]).Scan(&name); err != nil || name != "default" {
		t.Fatalf("new service domain: %s %v", name, err)
	}
}
