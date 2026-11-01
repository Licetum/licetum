//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/outbox"
	support "github.com/licetum/licetum/core/testsupport"
)

// This registry is deliberately independent of the production projector: the
// oracle reads the tables, never the writers' touched-row lists or Project.
var canonicalTables = map[string][]string{
	"service": {"id"}, "type_resource": {"id"}, "type_resource_parent": {"type_resource_id", "parent_id"},
	"synchronizer": {"id"}, "access_group": {"id"}, "user_group": {"id"}, "group_member": {"id"},
	"role": {"id"}, "role_permission": {"id"}, "assignment_rule": {"id"}, "admin_binding": {"id"},
	"identity_domain": {"id"}, "pep_client": {"id"}, "pep_credential": {"key_id"}, "pep_issuer": {"issuer"},
	"pep_issuer_keys": {"issuer"}, "log_settings": {"id"}, "subject_directory": {"service_id", "subject_type", "subject_id"},
}

type canonicalRow struct {
	Table string
	Key   map[string]any
	Row   map[string]any
}

func canonicalIdentity(table string, key map[string]any) string {
	raw, _ := json.Marshal(key)
	return table + string(raw)
}

func canonicalSnapshot(t *testing.T, e *env) map[string]canonicalRow {
	t.Helper()
	result := map[string]canonicalRow{}
	names := make([]string, 0, len(canonicalTables))
	for name := range canonicalTables {
		names = append(names, name)
	}
	sort.Strings(names)
	coveredRows, err := e.pool.Query(context.Background(), `SELECT c.relname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE t.tgname='licetum_outbox_insert' AND c.relnamespace=to_regnamespace(current_schema()) ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	var covered []string
	for coveredRows.Next() {
		var name string
		if err := coveredRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		covered = append(covered, name)
	}
	coveredRows.Close()
	if err := coveredRows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, covered) {
		t.Fatalf("canonical oracle tables %v do not match covered tables %v", names, covered)
	}
	var queries []string
	for _, table := range names {
		columns := canonicalTables[table]
		var key []string
		for _, column := range columns {
			key = append(key, "'"+column+"'", "t."+column)
		}
		image := "to_jsonb(t)"
		switch table {
		case "service":
			image += " - ARRAY['authorization_url','token_url','jwks_url','issuer','client_id','client_secret','trusted_clients']"
		case "synchronizer":
			image += " - ARRAY['kind','config']"
		case "log_settings":
			image += " - 'salt'"
		case "type_resource_parent":
			image += " - ARRAY['inherit','live']"
		case "type_resource":
			image += " || jsonb_build_object('mode',CASE WHEN t.synced_at IS NOT NULL AND EXISTS(SELECT 1 FROM synchronizer s WHERE s.type_resource_id=t.id AND s.deleted_at IS NULL) THEN 'sync' ELSE 'not_sync' END)"
		}
		queries = append(queries, fmt.Sprintf("SELECT '%s',jsonb_build_object(%s),%s FROM %s t WHERE (to_jsonb(t)->>'deleted_at') IS NULL", table, strings.Join(key, ","), image, table))
	}
	rows, err := e.pool.Query(context.Background(), strings.Join(queries, " UNION ALL "))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		var keyJSON, rowJSON []byte
		if err := rows.Scan(&table, &keyJSON, &rowJSON); err != nil {
			t.Fatal(err)
		}
		var key, row map[string]any
		if err := decodeCanonicalJSON(keyJSON, &key); err != nil {
			t.Fatal(err)
		}
		if err := decodeCanonicalJSON(rowJSON, &row); err != nil {
			t.Fatal(err)
		}
		result[canonicalIdentity(table, key)] = canonicalRow{Table: table, Key: key, Row: row}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertCanonicalDiff(t *testing.T, e *env, before, after map[string]canonicalRow, revision int64) {
	t.Helper()
	want := map[string]map[string]any{}
	for id, row := range after {
		if old, ok := before[id]; !ok || !reflect.DeepEqual(old.Row, row.Row) {
			want[id] = row.Row
		}
	}
	for id, row := range before {
		if _, ok := after[id]; !ok {
			deleted := map[string]any{"deleted": true}
			for key, value := range row.Key {
				deleted[key] = value
			}
			want[id] = deleted
		}
	}
	rows, err := e.pool.Query(context.Background(), `SELECT policy,payload FROM outbox WHERE revision>$1 ORDER BY revision`, revision)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]map[string]any{}
	for rows.Next() {
		var raw []byte
		var payload *string
		if err := rows.Scan(&raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload != nil {
			t.Fatal("canned fixture unexpectedly exceeds inline image limit; add chunk decoding to oracle")
		}
		var streams map[string][]outbox.Image
		if err := json.Unmarshal(raw, &streams); err != nil {
			t.Fatal(err)
		}
		for _, images := range streams {
			for _, image := range images {
				id := canonicalIdentity(image.Table, image.Key)
				if _, ok := got[id]; ok {
					t.Errorf("duplicate image %s", id)
				}
				var row map[string]any
				if err := decodeCanonicalJSON(image.Row, &row); err != nil {
					t.Fatal(err)
				}
				got[id] = row
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for id, row := range want {
		if !reflect.DeepEqual(got[id], row) {
			a, _ := json.Marshal(row)
			b, _ := json.Marshal(got[id])
			t.Errorf("image %s: want %s, got %s", id, a, b)
		}
	}
	for id, row := range got {
		if _, ok := want[id]; !ok {
			raw, _ := json.Marshal(row)
			t.Errorf("extra image %s: %s", id, raw)
		}
	}
}

func TestContract_WR03_WriteRouteImages(t *testing.T) {
	for _, c := range harness.CannedWrites {
		t.Run(c.OperationID, func(t *testing.T) {
			f := arrangeCannedWrite(t, c.OperationID)
			before := canonicalSnapshot(t, f.env)
			var revision int64
			if err := f.pool.QueryRow(context.Background(), `SELECT revision FROM revision_state WHERE scope='global'`).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			path, body := f.request(c)
			f.must(c.Method, path, body)
			after := canonicalSnapshot(t, f.env)
			assertCanonicalDiff(t, f.env, before, after, revision)
		})
	}
}

func TestContract_WR03_NetChangesAndUnchangedRows(t *testing.T) {
	for _, name := range []string{"service_same_slug", "service_stored_url", "literal_timestamp_names", "group_same_name", "role_same_name", "source_config_only", "type_same_parents", "type_dormant_parent", "member_same", "role_permission_same", "assignment_same", "access_group_same", "pep_client_same", "pep_issuer_same", "batch_update_back", "batch_create_delete"} {
		t.Run(name, func(t *testing.T) {
			f := arrangeCannedWrite(t, name)
			method, path, body := "PATCH", "", any(nil)
			switch name {
			case "service_same_slug":
				path = "/service/" + f.org
				body = map[string]any{"slug": "org"}
			case "service_stored_url":
				f.exec(`UPDATE service SET url='https://EXAMPLE.com:443/app' WHERE id=$1`, f.org)
				path = "/service/" + f.org
				body = map[string]any{"name": "org"}
			case "literal_timestamp_names":
				path = "/group/" + f.params["group_id"]
				f.exec(`UPDATE user_group SET name=$2 WHERE id=$1`, f.params["group_id"], "2030-01-01T00:00:00Z")
				body = map[string]any{"name": "2030-01-01T00:00:00+00:00"}
			case "group_same_name":
				path = "/group/" + f.params["group_id"]
				body = map[string]any{"name": "existing"}
			case "role_same_name":
				path = "/role/" + f.params["role_id"]
				body = map[string]any{"name": "existing"}
			case "source_config_only":
				path = f.typePath("branch") + "/resource/synchronizer/" + f.syncs["branch"]
				body = map[string]any{"config": map[string]any{"kind": "sql", "dsn": f.cfg.DB.URI, "query": "SELECT id,name,parent_id FROM src_branch WHERE id=:id /* edited */", "list_query": "SELECT id,name,parent_id FROM src_branch"}, "probe_resource_id": "B1"}
			case "type_same_parents":
				path = f.typePath("branch")
				body = map[string]any{"name": "branch", "parents": []any{map[string]any{"type_resource_id": f.rrs}}}
			case "type_dormant_parent":
				f.exec(`UPDATE type_resource_parent SET eff_live=false WHERE type_resource_id=$1`, f.branch)
				path = f.typePath("branch")
				body = map[string]any{"name": "branch", "parents": []any{}}
			case "member_same":
				path = "/group/" + f.params["group_id"] + "/member/" + f.params["member_id"]
				body = map[string]any{"predicate": []any{}, "enabled": true}
			case "role_permission_same":
				path = "/role/" + f.params["role_id"] + "/permission/" + f.params["permission_id"]
				body = map[string]any{"type_resource_id": f.branch, "access": []string{"read"}}
			case "assignment_same":
				path = "/assignment_rule/" + f.params["rule_id"]
				body = map[string]any{"subject": "existing", "type_resource_id": f.branch, "access": []string{"read"}}
			case "access_group_same":
				path = f.typePath("branch") + "/access_group/" + f.params["access_group_id"]
				body = map[string]any{"name": "existing", "levels": []string{"read"}}
			case "pep_client_same":
				path = "/service/" + f.org + "/pep-clients/" + f.params["client_id"]
				body = map[string]any{"name": "existing"}
			case "pep_issuer_same":
				method, path = "PUT", "/platform/pep-issuers"
				body = f.pepIssuerBody("existing-issuer")
			case "batch_update_back":
				method, path = "POST", "/batch"
				body = map[string]any{"operations": []any{
					map[string]any{"op": "update", "entity": "group", "id": f.params["group_id"], "body": map[string]any{"name": "temporary"}},
					map[string]any{"op": "update", "entity": "group", "id": f.params["group_id"], "body": map[string]any{"name": "existing"}},
				}}
			case "batch_create_delete":
				method, path = "POST", "/batch"
				id := uuid.NewString()
				body = map[string]any{"operations": []any{
					map[string]any{"op": "create", "entity": "group", "id": id, "body": map[string]any{"service_id": f.org, "name": "temporary"}},
					map[string]any{"op": "delete", "entity": "group", "id": id},
				}}
			}
			verifyCanonicalWrite(t, f.env, func() { f.must(method, path, body) })
		})
	}
}

func verifyCanonicalWrite(t *testing.T, e *env, write func()) {
	t.Helper()
	before := canonicalSnapshot(t, e)
	var revision int64
	if err := e.pool.QueryRow(context.Background(), `SELECT revision FROM revision_state WHERE scope='global'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	write()
	assertCanonicalDiff(t, e, before, canonicalSnapshot(t, e), revision)
}

func TestContract_WR03_SeedAndRestoreImages(t *testing.T) {
	e := setup(t)
	e.stopWorker()
	ctx := context.Background()
	for _, name := range []string{"seed", "restore"} {
		t.Run(name, func(t *testing.T) {
			for _, table := range []string{"assignment_rule", "group_member"} {
				if _, err := support.RawExec(ctx, e.pool, "DELETE FROM "+table); err != nil {
					t.Fatal(err)
				}
			}
			write := func() {
				if name == "seed" {
					if err := db.SeedPlatform(ctx, e.pool, e.cfg); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := db.RestoreAdminAccess(ctx, e.pool, e.cfg, []string{"rescuer"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			verifyCanonicalWrite(t, e, write)
			// A second application is independently checked against an empty table diff.
			verifyCanonicalWrite(t, e, write)
		})
	}
}

func decodeCanonicalJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}
