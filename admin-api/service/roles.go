package service

import (
	"context"
	"errors"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// ---------- Role ----------

func (a *App) CreateRole(ctx context.Context, in *model.RoleInput) (*model.Role, error) {
	return a.createRole(ctx, in, nil)
}

// createRole creates a role with the given id, or with a new one when id is nil (see createAssignmentRule).
func (a *App) createRole(ctx context.Context, in *model.RoleInput, id *uuid.UUID) (*model.Role, error) {
	var r model.Role
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		err := q.QueryRow(ctx, `INSERT INTO role (id, name) VALUES (COALESCE($2::uuid, gen_random_uuid()), $1) RETURNING id, name`,
			in.Name, id).Scan(&r.ID, &r.Name)
		if db.IsPrimaryKeyViolation(err) {
			return change{}, idTaken(id)
		}
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.RoleNameTaken, in.Name)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "role", entityID: r.ID, action: actionCreate, after: r}, nil
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (a *App) ListRoles(ctx context.Context) ([]model.Role, error) {
	rows, err := a.DB.Query(ctx, `SELECT id, name FROM role ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[model.Role])
	if out == nil {
		out = []model.Role{}
	}
	return out, err
}

func (a *App) UpdateRole(ctx context.Context, roleID uuid.UUID, in *model.RoleInput) (*model.Role, error) {
	var r model.Role
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingRole(ctx, q, roleID)
		if err != nil {
			return change{}, err
		}
		err = q.QueryRow(ctx, `UPDATE role SET name = $2 WHERE id = $1 RETURNING id, name`, roleID, in.Name).
			Scan(&r.ID, &r.Name)
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.RoleNameTaken, in.Name)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "role", entityID: roleID, action: actionUpdate, before: old, after: r}, nil
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteRole is not allowed while rules grant the role (409, extra.blocked_by). Its items go with it.
func (a *App) DeleteRole(ctx context.Context, roleID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingRole(ctx, q, roleID)
		if err != nil {
			return change{}, err
		}
		granted, err := engine.Exists(ctx, q, `SELECT 1 FROM assignment_rule WHERE role_id = $1`, roleID)
		if err != nil {
			return change{}, err
		}
		if granted {
			return change{}, apperr.BlockedBy([]string{"assignment_rule"}, i18n.RoleDeleteBlocked, roleID)
		}
		if err := recordItemDeletes(ctx, q, roleID); err != nil {
			return change{}, err
		}

		if _, err := q.Exec(ctx, `DELETE FROM role WHERE id = $1`, roleID); err != nil {
			return change{}, err
		}
		return change{entity: "role", entityID: roleID, action: actionDelete, before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return roleID, nil
}

// ---------- RolePermission ----------

func (a *App) getExistingRolePermission(ctx context.Context, q db.Querier, roleID, itemID uuid.UUID) (*model.RolePermission, error) {
	p, err := engine.ScanRolePermission(q.QueryRow(ctx,
		`SELECT `+engine.RolePermissionColumns+` FROM role_permission rp WHERE rp.id = $1 AND rp.role_id = $2`, itemID, roleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.RolePermissionNotFound, itemID)
	}
	return p, err
}

// checkRolePermissionRefs checks the entities an item points at and its target (see checkResourceTarget).
func (a *App) checkRolePermissionRefs(ctx context.Context, q db.Querier, in *model.RolePermissionInput) error {
	if _, err := a.GetExistingTypeResourceAnyService(ctx, q, in.TypeResourceID); err != nil {
		return err
	}
	if err := a.checkResourceTarget(ctx, q, in.TypeResourceID, in.ResourceID, in.ResourceFromClaim, in.ResourceWhere); err != nil {
		return err
	}
	if err := a.checkAccessGroupRefs(ctx, q, in.TypeResourceID, in.Access); err != nil {
		return err
	}
	if in.ResourceFromClaim != nil {
		if _, err := a.GetExistingTypeResourceAnyService(ctx, q, in.ResourceFromClaim.LookupTypeResourceID); err != nil {
			return err
		}
	}
	return nil
}

// recordItemDeletes records the deletion of every item of a role. The items go with the role, so the history has to
// carry them too: a restore of the role brings its items back.
func recordItemDeletes(ctx context.Context, q db.Querier, roleID uuid.UUID) error {
	rows, err := q.Query(ctx, `SELECT `+engine.RolePermissionColumns+` FROM role_permission rp WHERE rp.role_id = $1 ORDER BY rp.id`, roleID)
	if err != nil {
		return err
	}
	var items []model.RolePermission
	for rows.Next() {
		p, err := engine.ScanRolePermission(rows)
		if err != nil {
			rows.Close()
			return err
		}
		items = append(items, *p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		serviceID, err := itemServiceID(ctx, q, item.TypeResourceID)
		if err != nil {
			return err
		}
		if err := recordChange(ctx, q, change{entity: "role_permission", entityID: item.ID, serviceID: serviceID,
			action: actionDelete, before: item}); err != nil {
			return err
		}
	}
	return nil
}

// itemServiceID is the service of the type an item points at (the change log names it; a role itself has none).
func itemServiceID(ctx context.Context, q db.Querier, typeResourceID uuid.UUID) (*uuid.UUID, error) {
	return serviceOfType(ctx, q, typeResourceID)
}

func (a *App) CreateRolePermission(ctx context.Context, roleID uuid.UUID, in *model.RolePermissionInput) (*model.RolePermission, error) {
	return a.createRolePermission(ctx, roleID, in, nil)
}

// createRolePermission creates an item with the given id, or with a new one when id is nil (see createAssignmentRule).
func (a *App) createRolePermission(ctx context.Context, roleID uuid.UUID, in *model.RolePermissionInput, id *uuid.UUID) (*model.RolePermission, error) {
	var p *model.RolePermission
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		if _, err := a.GetExistingRole(ctx, q, roleID); err != nil {
			return change{}, err
		}
		if err := a.checkRolePermissionRefs(ctx, q, in); err != nil {
			return change{}, err
		}
		serviceID, err := itemServiceID(ctx, q, in.TypeResourceID)
		if err != nil {
			return change{}, err
		}

		p, err = engine.ScanRolePermission(q.QueryRow(ctx, `
			INSERT INTO role_permission AS rp (id, role_id, type_resource_id, resource_id, resource_from_claim, access, resource_where)
			VALUES (COALESCE($7::uuid, gen_random_uuid()), $1, $2, $3, $4::jsonb, $5, $6::jsonb)
			RETURNING `+engine.RolePermissionColumns,
			roleID, in.TypeResourceID, in.ResourceID, fromClaimParam(in.ResourceFromClaim), in.Access, whereParam(in.ResourceWhere), id,
		))
		if db.IsPrimaryKeyViolation(err) {
			return change{}, idTaken(id)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "role_permission", entityID: p.ID, serviceID: serviceID, action: actionCreate, after: p}, nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListRolePermissions returns the items of a role with the names of their types.
func (a *App) ListRolePermissions(ctx context.Context, roleID uuid.UUID) ([]model.RolePermissionWithContext, error) {
	if _, err := a.GetExistingRole(ctx, a.DB, roleID); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `
		SELECT `+engine.RolePermissionColumns+`, t.service_id, s.name, t.name,
		       (rp.resource_id IS NULL AND rp.resource_from_claim IS NULL AND rp.resource_where IS NULL)
		       OR `+liveSynchronizerExists("t.id")+`
		FROM role_permission rp
		JOIN type_resource t ON t.id = rp.type_resource_id
		JOIN service s ON s.id = t.service_id
		WHERE rp.role_id = $1 AND t.deleted_at IS NULL AND s.deleted_at IS NULL
		ORDER BY s.name, t.name, rp.id`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.RolePermissionWithContext{}
	for rows.Next() {
		var c model.RolePermissionWithContext
		p, err := engine.ScanRolePermission(rows, &c.ServiceID, &c.ServiceName, &c.TypeResourceName, &c.TargetValid)
		if err != nil {
			return nil, err
		}
		c.RolePermission = *p
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *App) UpdateRolePermission(ctx context.Context, roleID, itemID uuid.UUID, in *model.RolePermissionInput) (*model.RolePermission, error) {
	var p *model.RolePermission
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingRolePermission(ctx, q, roleID, itemID)
		if err != nil {
			return change{}, err
		}
		if err := a.checkRolePermissionRefs(ctx, q, in); err != nil {
			return change{}, err
		}
		serviceID, err := itemServiceID(ctx, q, in.TypeResourceID)
		if err != nil {
			return change{}, err
		}

		p, err = engine.ScanRolePermission(q.QueryRow(ctx, `
			UPDATE role_permission AS rp SET
				type_resource_id = $3, resource_id = $4, resource_from_claim = $5::jsonb, access = $6, resource_where = $7::jsonb
			WHERE rp.id = $1 AND rp.role_id = $2
			RETURNING `+engine.RolePermissionColumns,
			itemID, roleID, in.TypeResourceID, in.ResourceID, fromClaimParam(in.ResourceFromClaim), in.Access, whereParam(in.ResourceWhere),
		))
		if err != nil {
			return change{}, err
		}
		return change{entity: "role_permission", entityID: itemID, serviceID: serviceID, action: actionUpdate,
			before: old, after: p}, nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (a *App) DeleteRolePermission(ctx context.Context, roleID, itemID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingRolePermission(ctx, q, roleID, itemID)
		if err != nil {
			return change{}, err
		}
		serviceID, err := itemServiceID(ctx, q, old.TypeResourceID)
		if err != nil {
			return change{}, err
		}
		if _, err := q.Exec(ctx, `DELETE FROM role_permission WHERE id = $1`, itemID); err != nil {
			return change{}, err
		}
		return change{entity: "role_permission", entityID: itemID, serviceID: serviceID, action: actionDelete,
			before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return itemID, nil
}

// PlatformPolicyOfType reports the platform policy a resource type is, when the type belongs to the
// self-service. Unknown types are not reported here: the request that names them answers 404.
func (a *App) PlatformPolicyOfType(ctx context.Context, typeResourceID uuid.UUID) (model.PlatformType, bool, error) {
	var name string
	var serviceID uuid.UUID
	err := a.DB.QueryRow(ctx, `SELECT name, service_id FROM type_resource WHERE id = $1`, typeResourceID).
		Scan(&name, &serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if serviceID != model.PlatformServiceID {
		return "", false, nil
	}
	return model.PlatformType(name), true, nil
}

// RolePermissionTarget returns the resource type an item points at, to check who may change the item.
func (a *App) RolePermissionTarget(ctx context.Context, roleID, itemID uuid.UUID) (uuid.UUID, error) {
	p, err := a.getExistingRolePermission(ctx, a.DB, roleID, itemID)
	if err != nil {
		return uuid.Nil, err
	}
	return p.TypeResourceID, nil
}
