package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/model"
)

// Reader is deliberately independent of db, whose transaction writer uses this package.
type Reader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

var keys = map[string][]string{
	"service": {"id"}, "type_resource": {"id"}, "type_resource_parent": {"type_resource_id", "parent_id"},
	"synchronizer": {"id"}, "access_group": {"id"}, "user_group": {"id"}, "group_member": {"id"},
	"role": {"id"}, "role_permission": {"id"}, "assignment_rule": {"id"}, "admin_binding": {"id"},
	"identity_domain": {"id"}, "pep_client": {"id"}, "pep_credential": {"key_id"}, "pep_issuer": {"issuer"},
	"pep_issuer_keys": {"issuer"}, "log_settings": {"id"},
	"subject_directory": {"service_id", "subject_type", "subject_id"}, "subject_directory_settings": {"service_id"},
}

// Project reads the full canonical post-image in the caller's transaction. Identifier
// interpolation is restricted to the fixed table/key registry; values are parameters.
// A missing or soft-deleted row becomes a tombstone. No secret settings are projected.
func Project(ctx context.Context, q Reader, table string, key map[string]any) (Image, error) {
	columns, ok := keys[table]
	if !ok || len(key) != len(columns) {
		return Image{}, fmt.Errorf("invalid outbox table/key %q", table)
	}
	where := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for i, c := range columns {
		v, ok := key[c]
		if !ok {
			return Image{}, fmt.Errorf("missing %s key %s", table, c)
		}
		where = append(where, fmt.Sprintf("t.%s = $%d", pgx.Identifier{c}.Sanitize(), i+1))
		args = append(args, v)
	}
	expr := "to_jsonb(t)"
	switch table {
	case "synchronizer":
		expr = "jsonb_build_object('id',t.id,'type_resource_id',t.type_resource_id,'regions',to_jsonb(t)->'regions','refresh_interval',to_jsonb(t)->'refresh_interval','refresh_interval_max',to_jsonb(t)->'refresh_interval_max','deleted_at',to_jsonb(t)->'deleted_at')"
	case "log_settings":
		expr += " - 'salt'"
	case "type_resource":
		expr += " || jsonb_build_object('mode',CASE WHEN " + model.TypeModeSQL("t") + " THEN 'sync' ELSE 'not_sync' END)"
	case "type_resource_parent":
		expr += " - ARRAY['inherit','live']"
	}
	var raw []byte
	sql := "SELECT " + expr + " FROM " + pgx.Identifier{table}.Sanitize() + " t WHERE " + strings.Join(where, " AND ")
	err := q.QueryRow(ctx, sql, args...).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Image{}, err
	}
	var row map[string]json.RawMessage
	if err == nil {
		if err := json.Unmarshal(raw, &row); err != nil {
			return Image{}, err
		}
	}
	if errors.Is(err, pgx.ErrNoRows) || (row["deleted_at"] != nil && string(row["deleted_at"]) != "null") {
		raw, err = Tombstone(key)
		if err != nil {
			return Image{}, err
		}
	}
	return Image{Table: table, Key: key, Row: raw}, nil
}

// ServicesBelow conservatively includes every service below the owners' type graph.
// Exported-link gating is intentionally deferred to P4.
func ServicesBelow(ctx context.Context, q Reader, owners []uuid.UUID) ([]uuid.UUID, error) {
	if len(owners) == 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := q.Query(ctx, `WITH RECURSIVE below(id) AS (
 SELECT id FROM type_resource WHERE service_id=ANY($1::uuid[])
 UNION SELECT p.type_resource_id FROM type_resource_parent p JOIN below b ON p.parent_id=b.id WHERE p.eff_live
) SELECT service_id FROM type_resource WHERE id IN(SELECT id FROM below)
UNION SELECT unnest($1::uuid[]) ORDER BY service_id`, owners)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return slices.Compact(out), rows.Err()
}
