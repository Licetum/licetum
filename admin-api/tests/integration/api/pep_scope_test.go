//go:build integration

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/licetum/licetum/core/db"
)

// TestIntegrationDeletingAServiceMovesThePEPScope: the PEP directory holds the clients of the live services only, and a deleted
// service is not a change of the rules alone, so the delete moves the pep revision too (see engine.PEPDirectory).
func TestIntegrationDeletingAServiceMovesThePEPScope(t *testing.T) {
	e := setup(t)
	id := e.must(http.MethodPost, "/service", servicePayload(e, "pep-scope"))["id"].(string)
	pepBefore := scopeRevision(t, e, db.ScopePEP)
	rulesBefore := scopeRevision(t, e, db.ScopeRules)

	status, raw := e.do(http.MethodDelete, e.admin.URL+"/service/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("delete = %d (%s)", status, raw)
	}
	if after := scopeRevision(t, e, db.ScopePEP); after <= pepBefore {
		t.Errorf("the pep revision stayed at %d after the service was deleted", after)
	}
	if after := scopeRevision(t, e, db.ScopeRules); after <= rulesBefore {
		t.Errorf("the rules revision stayed at %d after the service was deleted", after)
	}
}

// scopeRevision reads the revision of a scope; a scope nothing has bumped yet is 0.
func scopeRevision(t *testing.T, e *env, scope string) int64 {
	t.Helper()
	var revision int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT revision FROM revision_state WHERE scope = $1), 0)`, scope).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}
