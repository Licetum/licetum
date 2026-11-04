package outbox_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/outbox"
)

func TestStreamsRejectUnknownTablesAndMissingOwners(t *testing.T) {
	id := uuid.New()
	for table, want := range map[string]string{"service": outbox.Catalog, "type_resource_parent": outbox.Catalog, "assignment_rule": outbox.Service(id), "subject_directory": outbox.Directory(id), "pep_credential": outbox.PEP, "log_settings": outbox.Settings, "admin_binding": ""} {
		got, err := outbox.Stream(table, &id)
		if err != nil || got != want {
			t.Fatalf("%s: %q %v", table, got, err)
		}
	}
	if _, err := outbox.Stream("assignment_rule", nil); err == nil {
		t.Fatal("accepted an ownerless policy row")
	}
	if _, err := outbox.Stream("unknown", &id); err == nil {
		t.Fatal("accepted an unknown table")
	}
	key := map[string]any{"service_id": id.String(), "subject_type": "user", "subject_id": "a"}
	raw, err := outbox.Tombstone(key)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row["deleted"] != true || row["subject_id"] != "a" || len(key) != 3 {
		t.Fatal("tombstone lost or mutated its key")
	}
}
