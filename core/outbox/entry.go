package outbox

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

const (
	Catalog    = "catalog"
	PEP        = "pep"
	Settings   = "settings"
	Change     = "change"
	EpochStart = "epoch_start"
)

func Service(id uuid.UUID) string   { return "svc:" + id.String() }
func Directory(id uuid.UUID) string { return "dir:" + id.String() }
func Data(id uuid.UUID) string      { return "data:" + id.String() }

// Entry is an immutable publication header and the canonical images of its streams.
// Empty streams still publish: a binding or settings-only change must not create a hole.
type Entry struct {
	Kind     string             `json:"kind"`
	Services []uuid.UUID        `json:"services"`
	Streams  []string           `json:"streams"`
	Policy   map[string][]Image `json:"policy"`
}

// Image carries a row's post-image and its stable table/key identity.
type Image struct {
	Table string          `json:"table"`
	Key   map[string]any  `json:"key"`
	Row   json.RawMessage `json:"row"`
}

// Stream maps only known tables. A nil owner is valid only for global streams.
func Stream(table string, owner *uuid.UUID) (string, error) {
	switch table {
	case "service", "type_resource", "type_resource_parent", "synchronizer":
		return Catalog, nil
	case "pep_client", "pep_credential", "pep_issuer", "pep_issuer_keys":
		return PEP, nil
	case "log_settings":
		return Settings, nil
	case "admin_binding", "identity_domain", "subject_directory_settings":
		return "", nil
	case "access_group", "user_group", "group_member", "role", "role_permission", "assignment_rule":
		if owner != nil {
			return Service(*owner), nil
		}
	case "subject_directory":
		if owner != nil {
			return Directory(*owner), nil
		}
	case "resource_cache", "resource_edge", "resource_ancestor", "resource_unresolved":
		if owner != nil {
			return Data(*owner), nil
		}
	default:
		return "", fmt.Errorf("unknown outbox table %q", table)
	}
	return "", fmt.Errorf("outbox table %q requires an owner", table)
}

// Tombstone preserves the stable key even for composite-key tables.
func Tombstone(key map[string]any) (json.RawMessage, error) {
	out := make(map[string]any, len(key))
	for k, v := range key {
		out[k] = v
	}
	out["deleted"] = true
	return json.Marshal(out)
}
