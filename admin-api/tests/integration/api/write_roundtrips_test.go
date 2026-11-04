//go:build integration

package api_test

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/admin-api/api"
	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
)

//go:embed write_roundtrips.json
var measuredWriteBudgets []byte

type writeRoundTripBudget struct {
	Method  string             `json:"method"`
	Path    string             `json:"path"`
	Class   string             `json:"class"`
	Samples int                `json:"samples"`
	Maximum db.RoundTripCounts `json:"maximum"`
}

func writeClass(operation string) string {
	switch operation {
	case "run_batch", "revert_change", "rollback_changes", "import_policy":
		return "compound"
	case "create_synchronizer", "delete_synchronizer":
		return "change_mode"
	case "refresh_resources_of_type":
		return "synchronization"
	default:
		return "standalone"
	}
}

func TestContract_WR06_MeasuredWriteRoundTrips(t *testing.T) {
	update := os.Getenv("LICETUM_UPDATE_WRITE_BUDGETS") == "1"
	raw := measuredWriteBudgets
	if update {
		var err error
		raw, err = os.ReadFile("write_roundtrips.json")
		if err != nil {
			t.Fatal(err)
		}
	}
	budgets := map[string]writeRoundTripBudget{}
	if err := json.Unmarshal(raw, &budgets); err != nil {
		t.Fatal(err)
	}
	// Compare budgets with the real route table, not just the catalogue's length.
	_, a := api.NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
	routes := map[string]string{}
	for path, item := range a.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{"POST": item.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete, "HEAD": item.Head, "OPTIONS": item.Options, "TRACE": item.Trace} {
			if op != nil {
				routes[op.OperationID] = method + " " + path
			}
		}
	}
	for _, c := range harness.CannedWrites {
		if routes[c.OperationID] != c.Method+" "+c.Path {
			t.Fatalf("missing or mismatched route %s", c.OperationID)
		}
		delete(routes, c.OperationID)
		if !update {
			b, ok := budgets[c.OperationID]
			if !ok || b.Method != c.Method || b.Path != c.Path || b.Class != writeClass(c.OperationID) || b.Samples < 1 {
				t.Fatalf("missing or stale measured budget for %s", c.OperationID)
			}
		}
	}
	if len(routes) != 0 {
		t.Fatalf("routes without canned requests/budgets: %v", routes)
	}
	if !update && len(budgets) != len(harness.CannedWrites) {
		t.Fatalf("orphan budgets: %d for %d routes", len(budgets), len(harness.CannedWrites))
	}
	measured := map[string]writeRoundTripBudget{}
	for _, c := range harness.CannedWrites {
		t.Run(c.OperationID, func(t *testing.T) {
			f := arrangeCannedWrite(t, c.OperationID)
			// Prime authorization at the final fixture revision, before the counter starts.
			f.must("GET", "/platform/me", nil)
			handler := api.NewAdminHandler(f.app)
			done := make(chan db.RoundTripCounts, 1)
			f.admin = newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx, counter := db.CountRoundTrips(r.Context())
				defer func() { done <- counter.Counts() }()
				handler.ServeHTTP(w, r.WithContext(ctx))
			}))
			t.Cleanup(f.admin.Close)
			path, body := f.request(c)
			f.must(c.Method, path, body)
			counts := <-done
			t.Logf("%s: total=%d write=%d transactions=%d", c.OperationID, counts.Total, counts.Write, counts.Transactions)
			b := budgets[c.OperationID]
			if update {
				measured[c.OperationID] = writeRoundTripBudget{Method: c.Method, Path: c.Path, Class: writeClass(c.OperationID), Samples: b.Samples + 1, Maximum: db.RoundTripCounts{Total: max(b.Maximum.Total, counts.Total), Write: max(b.Maximum.Write, counts.Write), Transactions: max(b.Maximum.Transactions, counts.Transactions)}}
				return
			}
			if counts.Total > b.Maximum.Total || counts.Write > b.Maximum.Write {
				t.Errorf("%s round trips %+v exceed measured budget %+v", c.OperationID, counts, b.Maximum)
			}
			if counts.Transactions != b.Maximum.Transactions {
				t.Errorf("%s transactions=%d, want %d", c.OperationID, counts.Transactions, b.Maximum.Transactions)
			}
		})
	}
	if update && !t.Failed() {
		raw, err := json.MarshalIndent(measured, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("write_roundtrips.json", append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
