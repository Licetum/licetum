package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

const secretDSN = "postgresql+asyncpg://user:s3cret@host:5432/db?sslmode=disable"

func TestSynchronizerSecretsAreMaskedWithoutMutatingInput(t *testing.T) {
	input := model.SynchronizerConfig{Kind: model.KindSQL, DSN: secretDSN}
	masked := input.Masked()
	if strings.Contains(masked.DSN, "s3cret") || masked.DSN != "postgresql+asyncpg://user:"+model.SecretMask+"@host:5432/db?sslmode=disable" {
		t.Fatalf("masked DSN = %q", masked.DSN)
	}

	withHeaders := model.SynchronizerConfig{Kind: model.KindHTTP, Headers: map[string]string{"Authorization": "Bearer xyz"}}
	masked = withHeaders.Masked()
	if masked.Headers["Authorization"] != model.SecretMask || withHeaders.Headers["Authorization"] != "Bearer xyz" {
		t.Fatalf("masking must copy headers: masked=%v original=%v", masked.Headers, withHeaders.Headers)
	}
}

func TestSynchronizerSecretsRestoreOnlyMaskedValues(t *testing.T) {
	old := model.SynchronizerConfig{Kind: model.KindHTTP, DSN: "postgresql://user:p%40ss@host/db", Headers: map[string]string{
		"Authorization": "Bearer xyz", "X": "1",
	}}
	input := old.Masked()
	input.Headers["X"] = "changed"
	input.Headers["New"] = model.SecretMask

	restored := input.RestoreSecrets(old)
	if restored.DSN != old.DSN {
		t.Fatalf("DSN = %q, want %q", restored.DSN, old.DSN)
	}
	want := map[string]string{"Authorization": "Bearer xyz", "X": "changed", "New": model.SecretMask}
	encoded, _ := json.Marshal(restored.Headers)
	wantEncoded, _ := json.Marshal(want)
	if string(encoded) != string(wantEncoded) {
		t.Fatalf("headers = %s, want %s", encoded, wantEncoded)
	}

	if got := (model.SynchronizerConfig{DSN: "postgresql://user:%2A%2A%2A%2A%2A%2A%2A%2A@host/db"}).RestoreSecrets(old).DSN; got != old.DSN {
		t.Fatalf("escaped mask = %q, want old secret", got)
	}
	if got := (model.SynchronizerConfig{DSN: "postgresql://user:other@host/db"}).RestoreSecrets(old).DSN; got != "postgresql://user:other@host/db" {
		t.Fatalf("new password = %q, want explicit password", got)
	}
}

func TestTypePermissionJSONRoundTripPreservesPublicShape(t *testing.T) {
	for _, raw := range []string{
		`{"type":"sync","resources":[{"id":"b1","name":"Branch1","access":["read"]}]}`,
		`{"type":"not_sync","access":["read","write"]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var permission model.TypePermission
			if err := json.Unmarshal([]byte(raw), &permission); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(permission)
			if err != nil || string(encoded) != raw {
				t.Fatalf("roundtrip = %s, error = %v; want %s", encoded, err, raw)
			}
		})
	}
	encoded, _ := json.Marshal(model.TypePermission{Type: model.TypeModeNotSync})
	if string(encoded) != `{"type":"not_sync","access":[]}` {
		t.Fatalf("empty not_sync = %s", encoded)
	}
}
