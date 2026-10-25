//go:build integration

package api_test

// F6 item 5: the API stores and validates the regions of a synchronizer. Since the table synchronization engine is gone nothing
// enforces them until P5 (the regional-fetch contract); a source with regions may be asked by any executor.

import (
	"net/http"
	"testing"
)

func TestF6_6_RegionsOfASynchronizerThroughTheAPI(t *testing.T) {
	e := setup(t)
	org := e.must("POST", "/service", servicePayload(e, "api-regions"))["id"].(string)
	typ := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	src := newSource(t, branches("b1")...)
	path := "/service/" + org + "/type_resource/" + typ + "/resource/synchronizer"
	body := map[string]any{
		"config":            map[string]any{"kind": "http", "url": src.URL + "/src", "cache_ttl": 0},
		"probe_resource_id": "b1",
		"regions":           []string{"us", "eu", "us"},
	}
	created := e.must("POST", path, body)
	if got := created["regions"].([]any); len(got) != 2 || got[0] != "eu" || got[1] != "us" {
		t.Fatalf("regions of a created synchronizer: %v, want [eu us]", created["regions"])
	}
	id := created["id"].(string)

	// Absent on an update keeps them; an empty list clears them.
	delete(body, "regions")
	if got := e.must("PATCH", path+"/"+id, body)["regions"].([]any); len(got) != 2 {
		t.Fatalf("an update without regions changed them: %v", got)
	}
	body["regions"] = []string{}
	if got := e.must("PATCH", path+"/"+id, body)["regions"].([]any); len(got) != 0 {
		t.Fatalf("an empty list did not clear them: %v", got)
	}

	body["regions"] = []string{"Not A Region"}
	if status, _ := e.do("PATCH", e.admin.URL+path+"/"+id, body); status != http.StatusUnprocessableEntity {
		t.Fatalf("an invalid region: status %d, want 422", status)
	}
}
