package model_test

import (
	"encoding/json"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func decodeAndValidate[T any](t *testing.T, raw string) (*T, error) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	if validator, ok := any(&value).(model.Validator); ok {
		return &value, validator.Validate()
	}
	return &value, nil
}

func TestNormalizeHTTPURL(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"adds trailing slash", "https://example.com", "https://example.com/"},
		{"normalizes scheme and host", "HTTP://Example.COM/a/b?x=1", "http://example.com/a/b?x=1"},
		{"keeps path", "http://localhost:8000/icon", "http://localhost:8000/icon"},
		{"trims whitespace", " https://example.com/path/ ", "https://example.com/path/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.NormalizeHTTPURL("url", tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeHTTPURL(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
			}
		})
	}
	for _, input := range []string{"ftp://x", "example.com", "http://", "not a url"} {
		t.Run("rejects "+input, func(t *testing.T) {
			if _, err := model.NormalizeHTTPURL("url", input); err == nil {
				t.Fatalf("NormalizeHTTPURL(%q) must return an error", input)
			}
		})
	}
}

func TestValidatePostgresDSN(t *testing.T) {
	for _, input := range []string{"postgresql://u:p@h:5432/db", "postgres://h/db", "postgresql+asyncpg://u@h/db"} {
		t.Run("accepts "+input, func(t *testing.T) {
			if _, err := model.ValidatePostgresDSN("dsn", input); err != nil {
				t.Fatalf("%q: %v", input, err)
			}
		})
	}
	for _, input := range []string{"mysql://h/db", "postgresql:///db", "::"} {
		t.Run("rejects "+input, func(t *testing.T) {
			if _, err := model.ValidatePostgresDSN("dsn", input); err == nil {
				t.Fatalf("%q must be rejected", input)
			}
		})
	}
}

func TestAssignmentRuleValidationEnforcesTargetShape(t *testing.T) {
	const typeResourceID = "294e1950-4746-541b-9ea3-b073b60fd6e9"
	valid, err := decodeAndValidate[model.AssignmentRuleInput](t, `{"type_resource_id":"`+typeResourceID+`","access":["read"]}`)
	if err != nil || valid == nil || !*valid.Enabled || valid.Predicate == nil {
		t.Fatalf("defaults were not applied: %+v, %v", valid, err)
	}
	fromClaim, err := decodeAndValidate[model.AssignmentRuleInput](t, `{"type_resource_id":"`+typeResourceID+`","access":["read"],"resource_from_claim":{"claim":"owner","lookup_type_resource_id":"`+typeResourceID+`"}}`)
	if err != nil || fromClaim.ResourceFromClaim.MatchBy != model.MatchByName {
		t.Fatalf("match_by must default to name: %+v, %v", fromClaim, err)
	}

	invalid := map[string]string{
		"missing access":             `{"type_resource_id":"` + typeResourceID + `"}`,
		"empty access":               `{"type_resource_id":"` + typeResourceID + `","access":[]}`,
		"multiple subject targets":   `{"type_resource_id":"` + typeResourceID + `","access":["read"],"subject":"a","predicate":[{"attr":"a","op":"eq","value":"b"}]}`,
		"resource target conflict":   `{"type_resource_id":"` + typeResourceID + `","access":["read"],"resource_id":"x","resource_from_claim":{"claim":"c","lookup_type_resource_id":"` + typeResourceID + `"}}`,
		"unknown predicate operator": `{"type_resource_id":"` + typeResourceID + `","access":["read"],"predicate":[{"attr":"a","op":"gt","value":"b"}]}`,
		"invalid regular expression": `{"type_resource_id":"` + typeResourceID + `","access":["read"],"predicate":[{"attr":"a","op":"regex","value":"("}]}`,
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeAndValidate[model.AssignmentRuleInput](t, raw); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSynchronizerValidationNormalizesKindSpecificConfig(t *testing.T) {
	input, err := decodeAndValidate[model.SynchronizerInput](t, `{"config":{"kind":"http","url":"http://src.local","dsn":"ignored"},"probe_resource_id":"1"}`)
	if err != nil || input.Config.URL != "http://src.local/" || input.Config.DSN != "" {
		t.Fatalf("HTTP config = %+v, error = %v", input.Config, err)
	}
	stored, _ := json.Marshal(input.Config.StoredConfig())
	if string(stored) != `{"url":"http://src.local/"}` {
		t.Fatalf("StoredConfig = %s", stored)
	}

	invalid := map[string]string{
		"unknown kind":       `{"config":{"kind":"ftp"},"probe_resource_id":"1"}`,
		"SQL without query":  `{"config":{"kind":"sql","dsn":"postgresql://h/db","list_query":"q"},"probe_resource_id":"1"}`,
		"SQL with wrong DSN": `{"config":{"kind":"sql","dsn":"mysql://h/db","query":"q","list_query":"q"},"probe_resource_id":"1"}`,
		"HTTP without URL":   `{"config":{"kind":"http"},"probe_resource_id":"1"}`,
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeAndValidate[model.SynchronizerInput](t, raw); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestServiceUpdateIsAPartialPatch(t *testing.T) {
	if _, err := decodeAndValidate[model.ServiceUpdate](t, `{}`); err == nil {
		t.Fatal("an empty patch must be rejected")
	}
	update, err := decodeAndValidate[model.ServiceUpdate](t, `{"url":"https://x.io"}`)
	if err != nil || update.URL == nil || *update.URL != "https://x.io/" {
		t.Fatalf("normalized patch = %+v, error = %v", update, err)
	}
	// An empty address removes it; the other fields stay as they were sent.
	cleared, err := decodeAndValidate[model.ServiceUpdate](t, `{"icon":""}`)
	if err != nil || cleared.Icon == nil || *cleared.Icon != "" || cleared.URL != nil {
		t.Fatalf("an empty icon removes it: %+v, %v", cleared, err)
	}
	if _, err := decodeAndValidate[model.ServiceUpdate](t, `{"slug":"Bad Slug"}`); err == nil {
		t.Fatal("an invalid slug must be rejected")
	}
	// The OAuth configuration and the trusted clients of the previous release are not fields any more: the body allows extra
	// members (the web interface sends its form), and they are ignored.
	if _, err := decodeAndValidate[model.ServiceUpdate](t, `{"client_secret":"s"}`); err == nil {
		t.Fatal("a patch that sets nothing the service has must be rejected")
	}
}

func TestPredicateValueJSONRoundTrip(t *testing.T) {
	for _, raw := range []string{`{"attr":"a","op":"in","value":["x","y"]}`, `{"attr":"a","op":"eq","value":"x"}`} {
		t.Run(raw, func(t *testing.T) {
			var condition model.PredicateCondition
			if err := json.Unmarshal([]byte(raw), &condition); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(condition)
			if err != nil || string(encoded) != raw {
				t.Fatalf("roundtrip = %s, error = %v; want %s", encoded, err, raw)
			}
		})
	}
}

func TestMetaMatchNamesFollowPublicRules(t *testing.T) {
	for matchBy, want := range map[string]bool{
		"meta.owner": true, "meta.team-lead_2": true,
		"meta.": false, "meta.bad name": false, "meta.id": false, "meta.name": false, "meta": false,
	} {
		t.Run(matchBy, func(t *testing.T) {
			_, ok := model.MetaKeyOf(matchBy)
			if ok != want {
				t.Fatalf("MetaKeyOf(%q) accepted=%v, want %v", matchBy, ok, want)
			}
		})
	}
}
