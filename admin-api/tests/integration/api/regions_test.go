//go:build integration

package api_test

// F6 item 5: the regions of a synchronizer and the region of a worker. A source that can be reached from one region only is asked
// by the workers of that region, never by the others and never by a check process. The postgres engine claims jobs from a table
// and can filter them; the tests set it on the processes they start.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

func postgresEngine(c *config.Config) { c.Sync.Engine = "postgres" }

func inRegion(region string) func(*config.Config) {
	return func(c *config.Config) { postgresEngine(c); c.Sync.WorkerRegion = region }
}

func TestF6_6_ASourceOfOneRegionIsAskedOnlyByTheWorkersOfThatRegion(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("regional", src)
	e.stopWorker()
	// The synchronizer is reachable from "eu" only.
	if _, err := e.pool.Exec(context.Background(), `UPDATE synchronizer SET regions = '{eu}' WHERE type_resource_id = $1`, f.typ); err != nil {
		t.Fatal(err)
	}

	r := e.checkReplica(func(c *config.Config) { postgresEngine(c); c.Sync.RequestWait = 1500 * time.Millisecond })
	e.worker(inRegion("us"), engine.SyncHooks{})
	e.worker(inRegion("jp"), engine.SyncHooks{})

	// Only workers of other regions run: the request waits, the type has no data, and the source is not asked by anybody.
	status, body, header := r.permission(t, f, "")
	if status != http.StatusOK || strings.Contains(body, `"b1"`) || header.Get("X-Authz-Sync") != "pending" {
		t.Fatalf("with no worker of the region: %d sync=%q %s", status, header.Get("X-Authz-Sync"), body)
	}
	time.Sleep(time.Second)
	if got := src.calls.Load(); got != 0 {
		t.Fatalf("the source was asked %d times by workers outside its regions", got)
	}

	// A worker of the region takes the job that waited.
	e.worker(inRegion("eu"), engine.SyncHooks{})
	e.waitFor("the type to be synchronized by the worker of its region", 20*time.Second, func() bool { return e.resourceCount(f.typ) == 2 })
	if got := src.calls.Load(); got == 0 {
		t.Fatal("the source was never asked")
	}
	if status, body, _ := r.permission(t, f, ""); status != http.StatusOK || !strings.Contains(body, `"b1"`) {
		t.Fatalf("after the worker of the region: %d %s", status, body)
	}
}

func TestF6_6_AWorkerWithoutARegionTakesEveryJob(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1")...)
	f := e.syncFixture("anywhere", src)
	e.stopWorker()
	if _, err := e.pool.Exec(context.Background(), `UPDATE synchronizer SET regions = '{eu}' WHERE type_resource_id = $1`, f.typ); err != nil {
		t.Fatal(err)
	}
	e.worker(inRegion(""), engine.SyncHooks{})
	r := e.checkReplica(func(c *config.Config) { postgresEngine(c); c.Sync.RequestWait = 10 * time.Second })
	if status, body, _ := r.permission(t, f, ""); status != http.StatusOK || !strings.Contains(body, `"b1"`) {
		t.Fatalf("%d %s", status, body)
	}
}

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
