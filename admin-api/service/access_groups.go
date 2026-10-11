package service

import (
	"context"
	"errors"
	"strings"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// Access groups name a set of access levels of one resource type. A rule or a role item lists "@name" in its access
// and gets the levels of the group (see expandAccess). A group belongs to its type, so a change of a group is a change
// of the permissions of the type. A group that a rule or an item still names cannot be renamed or deleted: the
// reference would dangle.

const accessGroupColumns = `id, type_resource_id, name, levels`

func scanAccessGroup(row pgx.Row) (*model.AccessGroup, error) {
	var g model.AccessGroup
	if err := row.Scan(&g.ID, &g.TypeResourceID, &g.Name, &g.Levels); err != nil {
		return nil, err
	}
	return &g, nil
}

// getExistingAccessGroup reads a group of the given resource type.
func (a *App) getExistingAccessGroup(ctx context.Context, q db.Querier, typeResourceID, groupID uuid.UUID) (*model.AccessGroup, error) {
	g, err := scanAccessGroup(q.QueryRow(ctx,
		`SELECT `+accessGroupColumns+` FROM access_group WHERE id = $1 AND type_resource_id = $2`, groupID, typeResourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.AccessGroupMissing, groupID)
	}
	return g, err
}

// checkAccessGroupRefs checks that every "@name" of an access list names an access group of the resource type.
func (a *App) checkAccessGroupRefs(ctx context.Context, q db.Querier, typeResourceID uuid.UUID, access []string) error {
	for _, level := range access {
		name, ok := strings.CutPrefix(level, model.AccessGroupPrefix)
		if !ok {
			continue
		}
		found, err := engine.Exists(ctx, q, `SELECT 1 FROM access_group WHERE type_resource_id = $1 AND name = $2`, typeResourceID, name)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound(i18n.AccessGroupNotFound, name)
		}
	}
	return nil
}

// checkAccessGroupUnused refuses a change that would break the "@name" references of the rules and the role items of
// the type (409, extra.blocked_by).
func (a *App) checkAccessGroupUnused(ctx context.Context, q db.Querier, typeResourceID uuid.UUID, name string) error {
	ref := model.AccessGroupPrefix + name
	var blockers []string
	rules, err := engine.Exists(ctx, q, `SELECT 1 FROM assignment_rule WHERE type_resource_id = $1 AND $2 = ANY(access)`, typeResourceID, ref)
	if err != nil {
		return err
	}
	if rules {
		blockers = append(blockers, "assignment_rule")
	}
	items, err := engine.Exists(ctx, q, `SELECT 1 FROM role_permission WHERE type_resource_id = $1 AND $2 = ANY(access)`, typeResourceID, ref)
	if err != nil {
		return err
	}
	if items {
		blockers = append(blockers, "role_permission")
	}
	if len(blockers) > 0 {
		return apperr.BlockedBy(blockers, i18n.AccessGroupInUse, name)
	}
	return nil
}

// ListAccessGroups returns the access groups of a resource type, by name.
func (a *App) ListAccessGroups(ctx context.Context, typeResourceID uuid.UUID) ([]model.AccessGroup, error) {
	if _, err := a.GetExistingTypeResourceAnyService(ctx, a.DB, typeResourceID); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx,
		`SELECT `+accessGroupColumns+` FROM access_group WHERE type_resource_id = $1 ORDER BY name`, typeResourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.AccessGroup{}
	for rows.Next() {
		g, err := scanAccessGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (a *App) CreateAccessGroup(ctx context.Context, typeResourceID uuid.UUID, in *model.AccessGroupInput) (*model.AccessGroup, error) {
	return a.createAccessGroup(ctx, typeResourceID, in, nil)
}

// createAccessGroup creates a group with the given id, or with a new one when id is nil (see createAssignmentRule).
func (a *App) createAccessGroup(ctx context.Context, typeResourceID uuid.UUID, in *model.AccessGroupInput, id *uuid.UUID) (*model.AccessGroup, error) {
	var g *model.AccessGroup
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		if _, err := a.GetExistingTypeResourceAnyService(ctx, q, typeResourceID); err != nil {
			return change{}, err
		}
		serviceID, err := serviceOfType(ctx, q, typeResourceID)
		if err != nil {
			return change{}, err
		}
		g, err = scanAccessGroup(q.QueryRow(ctx, `
			INSERT INTO access_group (id, type_resource_id, name, levels) VALUES (COALESCE($4::uuid, gen_random_uuid()), $1, $2, $3)
			RETURNING `+accessGroupColumns, typeResourceID, in.Name, in.Levels, id))
		if db.IsPrimaryKeyViolation(err) {
			return change{}, idTaken(id)
		}
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.AccessGroupNameTaken, in.Name)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "access_group", entityID: g.ID, serviceID: serviceID, action: actionCreate, after: g}, nil
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (a *App) UpdateAccessGroup(ctx context.Context, typeResourceID, groupID uuid.UUID, in *model.AccessGroupInput) (*model.AccessGroup, error) {
	var g *model.AccessGroup
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingAccessGroup(ctx, q, typeResourceID, groupID)
		if err != nil {
			return change{}, err
		}
		serviceID, err := serviceOfType(ctx, q, typeResourceID)
		if err != nil {
			return change{}, err
		}
		if in.Name != old.Name {
			if err := a.checkAccessGroupUnused(ctx, q, typeResourceID, old.Name); err != nil {
				return change{}, err
			}
		}
		g, err = scanAccessGroup(q.QueryRow(ctx, `
			UPDATE access_group SET name = $3, levels = $4 WHERE id = $1 AND type_resource_id = $2
			RETURNING `+accessGroupColumns, groupID, typeResourceID, in.Name, in.Levels))
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.AccessGroupNameTaken, in.Name)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "access_group", entityID: groupID, serviceID: serviceID, action: actionUpdate,
			before: old, after: g}, nil
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (a *App) DeleteAccessGroup(ctx context.Context, typeResourceID, groupID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingAccessGroup(ctx, q, typeResourceID, groupID)
		if err != nil {
			return change{}, err
		}
		serviceID, err := serviceOfType(ctx, q, typeResourceID)
		if err != nil {
			return change{}, err
		}
		if err := a.checkAccessGroupUnused(ctx, q, typeResourceID, old.Name); err != nil {
			return change{}, err
		}
		if _, err := q.Exec(ctx, `DELETE FROM access_group WHERE id = $1`, groupID); err != nil {
			return change{}, err
		}
		return change{entity: "access_group", entityID: groupID, serviceID: serviceID, action: actionDelete,
			before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return groupID, nil
}
