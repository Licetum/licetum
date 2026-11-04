//go:build integration

package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	support "github.com/licetum/licetum/core/testsupport"
)

func TestContract_ADM11_ServiceSlugsAreImmutableAndNeverReused(t *testing.T) {
	e := setup(t)
	service := e.must(http.MethodPost, "/service", servicePayload(e, "permanent-slug"))["id"].(string)
	status, raw := e.do(http.MethodPatch, e.admin.URL+"/service/"+service, map[string]any{"slug": "different-slug"})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "service_slug_immutable")
	e.must(http.MethodPatch, "/service/"+service, map[string]any{"slug": "permanent-slug", "name": "Renamed display name"})
	for _, mutation := range []struct {
		sql string
		arg any
	}{
		{`UPDATE service SET slug=$2 WHERE id=$1`, "forged-slug"},
		{`UPDATE service SET identity_domain_id=$2 WHERE id=$1`, uuid.New()},
	} {
		_, err := support.RawExec(context.Background(), e.pool, mutation.sql, service, mutation.arg)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatalf("raw identity mutation accepted: %v", err)
		}
	}
	e.must(http.MethodDelete, "/service/"+service, nil)
	status, raw = e.do(http.MethodPost, e.admin.URL+"/service", servicePayload(e, "permanent-slug"))
	requireErrorCode(t, status, raw, http.StatusConflict, "service_slug_taken")
}
