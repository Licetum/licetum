package service

import (
	"context"

	"github.com/licetum/licetum/core/engine"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// checkParents validates the parent links of a type. A parent may belong to any service (e.g.
// replan.planogram -> filials.branch). This is not only a structural link: a grant on a resource of
// a parent type cascades down to the linked resources of the child type (see resource_cache.go). Only
// set a parent where such permission inheritance is actually needed.
//
// The type graph must stay acyclic: a cycle would make every walk over the hierarchy endless.
func (a *App) checkParents(ctx context.Context, q db.Querier, parents []model.TypeParent, selfID *uuid.UUID) error {
	for _, p := range parents {
		if selfID != nil && p.TypeResourceID == *selfID {
			return apperr.BadRequest(i18n.TypeResourceSelfParent)
		}
		if _, err := a.GetExistingTypeResourceAnyService(ctx, q, p.TypeResourceID); err != nil {
			return err
		}
		if selfID == nil {
			continue
		}
		var cycle bool
		err := q.QueryRow(ctx, `
			WITH RECURSIVE ancestors(id) AS (
				SELECT $1::uuid
				UNION
				SELECT p.parent_id FROM type_resource_parent p JOIN ancestors a ON p.type_resource_id = a.id
			)
			SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $2)`,
			p.TypeResourceID, *selfID,
		).Scan(&cycle)
		if err != nil {
			return err
		}
		if cycle {
			return apperr.BadRequest(i18n.TypeResourceCycle, p.TypeResourceID, *selfID)
		}
	}
	return nil
}

// replaceTypeParents stores the parent links of a type.
func replaceTypeParents(ctx context.Context, q db.Querier, typeID uuid.UUID, parents []model.TypeParent) error {
	if _, err := q.Exec(ctx, `DELETE FROM type_resource_parent WHERE type_resource_id = $1`, typeID); err != nil {
		return err
	}
	for _, p := range parents {
		if _, err := q.Exec(ctx,
			`INSERT INTO type_resource_parent (type_resource_id, parent_id, inherit) VALUES ($1, $2, $3)`,
			typeID, p.TypeResourceID, p.Inherit); err != nil {
			return err
		}
	}
	return nil
}

// checkNameUnique: a resource type name is unique within its service — /permission returns types by
// name, and two types with the same name would merge in the response.
func (a *App) checkNameUnique(ctx context.Context, q db.Querier, serviceID uuid.UUID, name string, selfID *uuid.UUID) error {
	taken, err := engine.Exists(ctx, q, `
		SELECT 1 FROM type_resource
		WHERE service_id = $1 AND name = $2 AND deleted_at IS NULL AND ($3::uuid IS NULL OR id <> $3)`,
		serviceID, name, engine.ToNullUUID(selfID))
	if err != nil {
		return err
	}
	if taken {
		return apperr.Conflict(i18n.TypeResourceNameTaken, name)
	}
	return nil
}

// nameTakenOnRace maps a concurrent insert of the same name (the partial unique index) to the same 409.
func nameTakenOnRace(err error, name string) error {
	if db.IsUniqueViolation(err) {
		return apperr.Conflict(i18n.TypeResourceNameTaken, name)
	}
	return err
}

func (a *App) CreateTypeResource(ctx context.Context, serviceID uuid.UUID, in *model.TypeResourceInput) (*model.TypeResource, error) {
	var t *model.TypeResource
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		if _, err := a.GetExistingService(ctx, q, serviceID); err != nil {
			return change{}, err
		}
		if err := a.checkNameUnique(ctx, q, serviceID, in.Name, nil); err != nil {
			return change{}, err
		}
		if err := a.checkParents(ctx, q, in.Parents, nil); err != nil {
			return change{}, err
		}

		var err error
		t, err = engine.ScanTypeResource(q.QueryRow(ctx, `
			INSERT INTO type_resource (service_id, name, access_implies)
			VALUES ($1, $2, $3::jsonb)
			RETURNING `+engine.TypeResourceColumns,
			serviceID, in.Name, engine.MustJSON(in.AccessImplies),
		))
		if err != nil {
			return change{}, err
		}
		if err := replaceTypeParents(ctx, q, t.ID, in.Parents); err != nil {
			return change{}, err
		}
		if err := a.LoadTypeParents(ctx, q, []*model.TypeResource{t}); err != nil {
			return change{}, err
		}
		return change{entity: "type_resource", entityID: t.ID, serviceID: &serviceID, action: actionCreate, after: t}, nil
	})
	if err != nil {
		return nil, nameTakenOnRace(err, in.Name)
	}
	return t, nil
}

func (a *App) ListTypeResources(ctx context.Context, serviceID uuid.UUID) ([]model.TypeResource, error) {
	if _, err := a.GetExistingService(ctx, a.DB, serviceID); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx,
		`SELECT `+engine.TypeResourceColumns+` FROM type_resource WHERE service_id = $1 AND deleted_at IS NULL`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ptrs []*model.TypeResource
	for rows.Next() {
		t, err := engine.ScanTypeResource(rows)
		if err != nil {
			return nil, err
		}
		ptrs = append(ptrs, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := a.LoadTypeParents(ctx, a.DB, ptrs); err != nil {
		return nil, err
	}
	out := make([]model.TypeResource, len(ptrs))
	for i, t := range ptrs {
		out[i] = *t
	}
	return out, nil
}

// ListAllTypeResources returns the resource types of every service: for the cross-service parent
// picker (a type's parent may belong to another service).
func (a *App) ListAllTypeResources(ctx context.Context) ([]model.TypeResourceWithService, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT t.id, t.service_id, t.name, t.access_implies, s.name
		FROM type_resource t
		JOIN service s ON s.id = t.service_id
		WHERE t.deleted_at IS NULL AND s.deleted_at IS NULL
		ORDER BY s.name, t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ptrs []*model.TypeResource
	var serviceNames []string
	for rows.Next() {
		var serviceName string
		t, err := engine.ScanTypeResource(rows, &serviceName)
		if err != nil {
			return nil, err
		}
		ptrs = append(ptrs, t)
		serviceNames = append(serviceNames, serviceName)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := a.LoadTypeParents(ctx, a.DB, ptrs); err != nil {
		return nil, err
	}
	out := make([]model.TypeResourceWithService, len(ptrs))
	for i, t := range ptrs {
		out[i] = model.TypeResourceWithService{TypeResource: *t, ServiceName: serviceNames[i]}
	}
	return out, nil
}

func (a *App) UpdateTypeResource(ctx context.Context, serviceID, typeResourceID uuid.UUID, in *model.TypeResourceInput) (*model.TypeResource, error) {
	var t *model.TypeResource
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingTypeResource(ctx, q, serviceID, typeResourceID)
		if err != nil {
			return change{}, err
		}
		if err := a.LoadTypeParents(ctx, q, []*model.TypeResource{old}); err != nil {
			return change{}, err
		}
		if err := a.checkNameUnique(ctx, q, serviceID, in.Name, &typeResourceID); err != nil {
			return change{}, err
		}
		if err := a.checkParents(ctx, q, in.Parents, &typeResourceID); err != nil {
			return change{}, err
		}

		t, err = engine.ScanTypeResource(q.QueryRow(ctx, `
			UPDATE type_resource SET name = $2, access_implies = $3::jsonb
			WHERE id = $1
			RETURNING `+engine.TypeResourceColumns,
			typeResourceID, in.Name, engine.MustJSON(in.AccessImplies),
		))
		if err != nil {
			return change{}, err
		}
		if err := replaceTypeParents(ctx, q, typeResourceID, in.Parents); err != nil {
			return change{}, err
		}
		if err := a.LoadTypeParents(ctx, q, []*model.TypeResource{t}); err != nil {
			return change{}, err
		}
		return change{entity: "type_resource", entityID: typeResourceID, serviceID: &serviceID, action: actionUpdate,
			before: old, after: t}, nil
	})
	if err != nil {
		return nil, nameTakenOnRace(err, in.Name)
	}
	return t, nil
}

func (a *App) DeleteTypeResource(ctx context.Context, serviceID, typeResourceID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingTypeResource(ctx, q, serviceID, typeResourceID)
		if err != nil {
			return change{}, err
		}
		if err := a.LoadTypeParents(ctx, q, []*model.TypeResource{old}); err != nil {
			return change{}, err
		}

		checks := []struct {
			name string
			sql  string
		}{
			{"synchronizer", `SELECT 1 FROM synchronizer WHERE type_resource_id = $1 AND deleted_at IS NULL`},
			// A type is also referenced as the lookup type of a claim-based target (resource_from_claim).
			{"assignment_rule", `SELECT 1 FROM assignment_rule WHERE type_resource_id = $1
				OR resource_from_claim->>'lookup_type_resource_id' = $1::text`},
			{"role_permission", `SELECT 1 FROM role_permission WHERE type_resource_id = $1
				OR resource_from_claim->>'lookup_type_resource_id' = $1::text`},
			{"type_resource", `SELECT 1 FROM type_resource_parent p JOIN type_resource c ON c.id = p.type_resource_id
				WHERE p.parent_id = $1 AND c.deleted_at IS NULL`},
		}
		var blockedBy []string
		for _, c := range checks {
			found, err := engine.Exists(ctx, q, c.sql, typeResourceID)
			if err != nil {
				return change{}, err
			}
			if found {
				blockedBy = append(blockedBy, c.name)
			}
		}
		if len(blockedBy) > 0 {
			return change{}, apperr.BlockedBy(blockedBy, i18n.TypeResourceDeleteBlocked, typeResourceID)
		}

		if _, err := q.Exec(ctx, `UPDATE type_resource SET deleted_at = now() WHERE id = $1`, typeResourceID); err != nil {
			return change{}, err
		}
		return change{entity: "type_resource", entityID: typeResourceID, serviceID: &serviceID, action: actionDelete,
			before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return typeResourceID, nil
}
