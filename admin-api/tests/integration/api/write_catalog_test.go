//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/api"
	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/outbox"
)

type cannedFixture struct {
	*world
	params map[string]string
	owner  string
	moved  []string
}

func arrangeCannedWrite(t *testing.T, operation string) *cannedFixture {
	t.Helper()
	w := newWorld(t)
	f := &cannedFixture{world: w, owner: w.org, params: map[string]string{
		"service_id": w.org, "type_resource_id": w.branch, "branch": w.branch, "mock": w.mock.URL, "dsn": w.cfg.DB.URI,
		"synchronizer_id": w.syncs["branch"], "subject_type": "user", "subject_id": "existing",
	}}
	put := func(key, path string, body any) string {
		id := w.must("POST", path, body)["id"].(string)
		f.params[key] = id
		return id
	}
	group := put("group_id", "/group", map[string]any{"service_id": w.org, "name": "existing"})
	put("member_id", "/group/"+group+"/member", map[string]any{"predicate": []any{}})
	role := put("role_id", "/role", map[string]any{"service_id": w.org, "name": "existing"})
	put("permission_id", "/role/"+role+"/permission", map[string]any{"type_resource_id": w.branch, "access": []string{"read"}})
	put("access_group_id", w.typePath("branch")+"/access_group", map[string]any{"name": "existing", "levels": []string{"read"}})
	put("rule_id", "/assignment_rule", map[string]any{"subject": "existing", "type_resource_id": w.branch, "access": []string{"read"}})
	client := put("client_id", "/service/"+w.org+"/pep-clients", map[string]any{"name": "existing", "kind": "api_key"})
	key := w.must("POST", "/service/"+w.org+"/pep-clients/"+client+"/keys", map[string]any{})
	f.params["key_id"] = fmt.Sprint(key["key_id"])
	w.must("PUT", "/platform/pep-issuers", w.pepIssuerBody("existing-issuer"))
	if operation == "delete_pep_issuer" {
		w.exec(`INSERT INTO pep_issuer_keys(issuer,key_set,digest,fetched_at) VALUES($1,'{"keys":[]}',decode(repeat('00',32),'hex'),now())`, w.mock.URL+"/existing-issuer")
	}
	w.must("PUT", "/service/"+w.org+"/subjects", map[string]any{"subjects": []any{map[string]any{"id": "existing", "name": "Existing"}}})
	put("scenario_id", "/service/"+w.org+"/policy/scenarios", map[string]any{"name": "existing", "subject": "alice", "checks": []any{map[string]any{"type": "branch", "action": "read", "expect": "deny"}}})
	switch operation {
	case "delete_service":
		f.owner = w.newService("empty")
		f.params["service_id"] = f.owner
	case "delete_type_resource", "create_synchronizer":
		f.params["type_resource_id"] = w.typeResource(w.org, "empty", w.branch)
	}
	ctx := context.Background()
	var base int64
	if err := w.pool.QueryRow(ctx, `SELECT revision FROM revision_state WHERE scope='global'`).Scan(&base); err != nil {
		t.Fatal(err)
	}
	f.params["base_revision"] = strconv.FormatInt(base, 10)
	rollback := put("rollback_group", "/group", map[string]any{"service_id": w.org, "name": "rollback"})
	var revision int64
	if err := w.pool.QueryRow(ctx, `SELECT max(revision) FROM change_log WHERE entity_id=$1`, rollback).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	f.params["revision"] = strconv.FormatInt(revision, 10)
	graph, err := w.app.LoadTypeGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := w.pool.Query(ctx, `SELECT id FROM type_resource WHERE deleted_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.app.EnsureSynced(ctx, graph, ids, true); err != nil {
		t.Fatal(err)
	}
	w.stopWorker()
	if operation == "delete_synchronizer" {
		f.moved = []string{w.branch, w.item}
	}
	if operation == "refresh_resources_of_type" {
		w.exec(`UPDATE src_branch SET name='Changed' WHERE id='B1'`)
		f.moved = []string{w.branch}
		startWorker(t, w.app.App)
	}
	return f
}

func (f *cannedFixture) request(c harness.CannedWrite) (string, any) {
	replace := func(s string) string {
		for key, value := range f.params {
			s = strings.ReplaceAll(s, "{"+key+"}", value)
		}
		return s
	}
	path := replace(c.Path)
	if c.OperationID == "delete_pep_issuer" {
		path += "?issuer=" + url.QueryEscape(f.mock.URL+"/existing-issuer")
	}
	body := replace(c.Body)
	if c.OperationID == "rollback_changes" {
		body = strings.ReplaceAll(body, `"`+f.params["base_revision"]+`"`, f.params["base_revision"])
	}
	if body == "" {
		return path, nil
	}
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		f.t.Fatal(err)
	}
	return path, value
}

func TestContract_WR05_WriteRoutePublications(t *testing.T) {
	e := setup(t)
	_, a := api.NewAdminAPI(e.app)
	declarations := map[string]api.OutboxExpectation{}
	routes := map[string]string{}
	for path, item := range a.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{"POST": item.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete, "HEAD": item.Head, "OPTIONS": item.Options, "TRACE": item.Trace} {
			if op == nil {
				continue
			}
			d, ok := api.OperationOutbox(op)
			if !ok {
				t.Fatalf("undeclared route %s", op.OperationID)
			}
			declarations[op.OperationID] = d
			routes[op.OperationID] = method + " " + path
		}
	}
	seen := map[string]bool{}
	for _, c := range harness.CannedWrites {
		if seen[c.OperationID] {
			t.Fatalf("duplicate canned route: %s", c.OperationID)
		}
		seen[c.OperationID] = true
		if routes[c.OperationID] != c.Method+" "+c.Path {
			t.Fatalf("orphan or mismatched canned route %s: %s", c.OperationID, routes[c.OperationID])
		}
	}
	for id := range declarations {
		if !seen[id] {
			t.Errorf("no canned request for %s", id)
		}
	}
	if t.Failed() {
		return
	}
	for _, c := range harness.CannedWrites {
		t.Run(c.OperationID, func(t *testing.T) {
			f := arrangeCannedWrite(t, c.OperationID)
			var before int64
			if err := f.pool.QueryRow(context.Background(), `SELECT revision FROM revision_state WHERE scope='global'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			path, body := f.request(c)
			result := f.must(c.Method, path, body)
			declaration := declarations[c.OperationID]
			rows, err := f.pool.Query(context.Background(), `SELECT kind,streams,services::text[] FROM outbox WHERE revision>$1 ORDER BY revision`, before)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var entries []outbox.Entry
			for rows.Next() {
				var entry outbox.Entry
				var services []string
				if err := rows.Scan(&entry.Kind, &entry.Streams, &services); err != nil {
					t.Fatal(err)
				}
				for _, s := range services {
					entry.Services = append(entry.Services, uuid.MustParse(s))
				}
				entries = append(entries, entry)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if declaration.Streams == nil {
				if len(entries) != 0 {
					t.Fatalf("read/tool operation published: %+v", entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("publications=%d, want 1: %+v", len(entries), entries)
			}
			got := entries[0]
			owner := f.owner
			if c.OperationID == "create_service" {
				owner = result["id"].(string)
			}
			var streams []string
			for _, s := range declaration.Streams {
				switch s {
				case "data:{moved-types}":
					for _, id := range f.moved {
						streams = append(streams, "data:"+id)
					}
				case "{operations}":
					streams = append(streams, "svc:"+owner)
				default:
					streams = append(streams, strings.ReplaceAll(s, "{owner}", owner))
				}
			}
			slices.Sort(streams)
			slices.Sort(got.Streams)
			var services []string
			if declaration.Services != "none" {
				services = append(services, owner)
				if owner == f.org {
					services = append(services, f.appSvc)
				}
			}
			actual := []string{}
			for _, id := range got.Services {
				actual = append(actual, id.String())
			}
			slices.Sort(services)
			slices.Sort(actual)
			if got.Kind != declaration.Kind || !slices.Equal(streams, got.Streams) || !slices.Equal(services, actual) {
				t.Fatalf("publication kind=%s streams=%v services=%v; want change %v %v", got.Kind, got.Streams, actual, streams, services)
			}
		})
	}
}
