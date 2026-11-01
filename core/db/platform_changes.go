package db

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/outbox"
)

func platformImages(ctx context.Context, q outbox.Reader) (map[string]outbox.Image, error) {
	out := map[string]outbox.Image{}
	for _, spec := range []struct{ table, where string }{
		{"service", "id=$1"}, {"type_resource", "service_id=$1"},
		{"synchronizer", "type_resource_id IN(SELECT id FROM type_resource WHERE service_id=$1)"},
		{"user_group", "id=$2"}, {"group_member", "group_id=$2"},
		{"assignment_rule", "type_resource_id IN(SELECT id FROM type_resource WHERE service_id=$1)"},
	} {
		// Both identifiers and predicates are compile-time constants.
		args := []any{model.PlatformServiceID}
		where := spec.where
		if spec.table == "user_group" || spec.table == "group_member" {
			where = "id=$1"
			if spec.table == "group_member" {
				where = "group_id=$1"
			}
			args = []any{model.PlatformAdminGroupID}
		}
		rows, err := q.Query(ctx, "SELECT id FROM "+spec.table+" WHERE "+where, args...)
		if err != nil {
			return nil, err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, id := range ids {
			image, err := outbox.Project(ctx, q, spec.table, map[string]any{"id": id})
			if err != nil {
				return nil, err
			}
			out[spec.table+id.String()] = image
		}
	}
	return out, nil
}

func platformChanges(before, after map[string]outbox.Image, actor string) []Change {
	names := make([]string, 0, len(after))
	for key := range after {
		names = append(names, key)
	}
	slices.Sort(names)
	var out []Change
	for _, key := range names {
		image := after[key]
		old, existed := before[key]
		if existed && string(old.Row) == string(image.Row) {
			continue
		}
		id := image.Key["id"].(uuid.UUID)
		owner := model.PlatformServiceID
		action := "create"
		if existed {
			action = "update"
		}
		entity := image.Table
		if entity == "user_group" {
			entity = "group"
		}
		c := Change{Entity: entity, ID: &id, ServiceID: &owner, Actor: actor, Action: action, After: image.Row, Touched: []Touched{{Table: image.Table, Key: image.Key, ServiceID: &owner}}}
		if existed {
			c.Before = old.Row
		}
		out = append(out, c)
	}
	return out
}
