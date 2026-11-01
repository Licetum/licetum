//go:build integration

package api_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/db"
)

// TestIntegrationDeletingAServiceMovesThePEPScope: the PEP directory holds the clients of the live services only, and a deleted
// service is not a change of the rules alone, so the delete moves the pep revision too (see engine.PEPDirectory).
func TestIntegrationDeletingAServiceMovesThePEPScope(t *testing.T) {
	e := setup(t)
	id := e.must(http.MethodPost, "/service", servicePayload(e, "pep-scope"))["id"].(string)
	pepBefore := scopeRevision(t, e, db.ScopeGlobal)
	rulesBefore := scopeRevision(t, e, db.ScopeGlobal)

	status, raw := e.do(http.MethodDelete, e.admin.URL+"/service/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("delete = %d (%s)", status, raw)
	}
	if after := scopeRevision(t, e, db.ScopeGlobal); after <= pepBefore {
		t.Errorf("the pep revision stayed at %d after the service was deleted", after)
	}
	if after := scopeRevision(t, e, db.ScopeGlobal); after <= rulesBefore {
		t.Errorf("the rules revision stayed at %d after the service was deleted", after)
	}
}

func TestPEPKeyRotationPublishesTheGlobalPositionAndPEPStreamOnly(t *testing.T) {
	e := setup(t)
	service := e.newService("rotation-position")
	client := e.must("POST", "/service/"+service+"/pep-clients", map[string]any{"name": "rotating", "kind": "api_key"})["id"].(string)
	before, err := db.LoadRevisions(context.Background(), e.pool)
	if err != nil {
		t.Fatal(err)
	}
	status, header, _ := e.call("POST", e.admin.URL+"/service/"+service+"/pep-clients/"+client+"/keys", nil)
	if status != http.StatusOK {
		t.Fatalf("key issue status = %d", status)
	}
	parts := strings.Split(header.Get("Licetum-Revision"), ".")
	if len(parts) != 2 {
		t.Fatal("key issue omitted its exact global position")
	}
	revision, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || revision <= before.Revision {
		t.Fatal("key rotation did not advance the global revision")
	}
	var streams []string
	var services int
	if err := e.pool.QueryRow(context.Background(), `SELECT streams,cardinality(services) FROM outbox WHERE revision=$1`, revision).Scan(&streams, &services); err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0] != "pep" || services != 0 {
		t.Fatalf("unexpected key-rotation header: streams=%v services=%d", streams, services)
	}
	var leaked int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM change_log WHERE revision=$1 AND (before ? 'sha256' OR after ? 'sha256')`, revision).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("credential hashes leaked into history")
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
