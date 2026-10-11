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

// ---------- Group ----------

func (a *App) CreateGroup(ctx context.Context, in *model.GroupInput) (*model.Group, error) {
	return a.createGroup(ctx, in, nil)
}

// createGroup creates a group with the given id, or with a new one when id is nil (see createAssignmentRule).
func (a *App) createGroup(ctx context.Context, in *model.GroupInput, id *uuid.UUID) (*model.Group, error) {
	var g model.Group
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		err := q.QueryRow(ctx, `INSERT INTO user_group (id, name) VALUES (COALESCE($2::uuid, gen_random_uuid()), $1) RETURNING id, name`,
			in.Name, id).Scan(&g.ID, &g.Name)
		if db.IsPrimaryKeyViolation(err) {
			return change{}, idTaken(id)
		}
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.GroupNameTaken, in.Name)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "group", entityID: g.ID, action: actionCreate, after: g}, nil
	})
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (a *App) ListGroups(ctx context.Context) ([]model.Group, error) {
	rows, err := a.DB.Query(ctx, `SELECT id, name FROM user_group ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[model.Group])
	if out == nil {
		out = []model.Group{}
	}
	return out, err
}

func (a *App) UpdateGroup(ctx context.Context, groupID uuid.UUID, in *model.GroupInput) (*model.Group, error) {
	var g model.Group
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingGroup(ctx, q, groupID)
		if err != nil {
			return change{}, err
		}
		err = q.QueryRow(ctx, `UPDATE user_group SET name = $2 WHERE id = $1 RETURNING id, name`, groupID, in.Name).
			Scan(&g.ID, &g.Name)
		if db.IsUniqueViolation(err) {
			return change{}, apperr.Conflict(i18n.GroupNameTaken, in.Name)
		}
		if err != nil {
			return change{}, err
		}
		return change{entity: "group", entityID: groupID, action: actionUpdate, before: old, after: g}, nil
	})
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (a *App) DeleteGroup(ctx context.Context, groupID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.GetExistingGroup(ctx, q, groupID)
		if err != nil {
			return change{}, err
		}

		var blockedBy []string
		inRules, err := engine.Exists(ctx, q,
			`SELECT 1 FROM assignment_rule WHERE subject = $1 OR member_of_group_id = $2 OR requires_group_id = $2`,
			model.GroupSubject(groupID), groupID)
		if err != nil {
			return change{}, err
		}
		if inRules {
			blockedBy = append(blockedBy, "assignment_rule")
		}
		inMembers, err := engine.Exists(ctx, q,
			`SELECT 1 FROM group_member WHERE group_id = $1 OR member_group_id = $1`, groupID)
		if err != nil {
			return change{}, err
		}
		if inMembers {
			blockedBy = append(blockedBy, "group_member")
		}
		if len(blockedBy) > 0 {
			return change{}, apperr.BlockedBy(blockedBy, i18n.GroupDeleteBlocked, groupID)
		}

		if _, err := q.Exec(ctx, `DELETE FROM user_group WHERE id = $1`, groupID); err != nil {
			return change{}, err
		}
		return change{entity: "group", entityID: groupID, action: actionDelete, before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return groupID, nil
}

// ---------- GroupMember ----------

func (a *App) getExistingGroupMember(ctx context.Context, q db.Querier, groupID, memberID uuid.UUID) (*model.GroupMember, error) {
	m, err := engine.ScanGroupMember(q.QueryRow(ctx,
		`SELECT `+engine.GroupMemberColumns+` FROM group_member WHERE id = $1 AND group_id = $2`, memberID, groupID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound(i18n.GroupMemberNotFound, memberID)
	}
	return m, err
}

func (a *App) checkMemberGroup(ctx context.Context, q db.Querier, groupID uuid.UUID, in *model.GroupMemberInput) error {
	if in.MemberGroupID == nil {
		return nil
	}
	if *in.MemberGroupID == groupID {
		return apperr.BadRequest(i18n.GroupSelfNest)
	}
	_, err := a.GetExistingGroup(ctx, q, *in.MemberGroupID)
	return err
}

func (a *App) CreateGroupMember(ctx context.Context, groupID uuid.UUID, in *model.GroupMemberInput) (*model.GroupMember, error) {
	return a.createGroupMember(ctx, groupID, in, nil)
}

// createGroupMember creates a membership with the given id, or with a new one when id is nil (see createAssignmentRule).
func (a *App) createGroupMember(ctx context.Context, groupID uuid.UUID, in *model.GroupMemberInput, id *uuid.UUID) (*model.GroupMember, error) {
	var m *model.GroupMember
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		if _, err := a.GetExistingGroup(ctx, q, groupID); err != nil {
			return change{}, err
		}
		if err := a.checkMemberGroup(ctx, q, groupID, in); err != nil {
			return change{}, err
		}

		created, err := engine.ScanGroupMember(q.QueryRow(ctx, `
			INSERT INTO group_member (id, group_id, member_group_id, predicate, enabled, valid_from, expires_at)
			VALUES (COALESCE($7::uuid, gen_random_uuid()), $1, $2, $3::jsonb, $4, $5, $6)
			RETURNING `+engine.GroupMemberColumns,
			groupID, engine.ToNullUUID(in.MemberGroupID), engine.MustJSON(in.Predicate), *in.Enabled, in.ValidFrom, in.ExpiresAt, id,
		))
		if db.IsPrimaryKeyViolation(err) {
			return change{}, idTaken(id)
		}
		if err != nil {
			return change{}, err
		}
		m = created
		return change{entity: "group_member", entityID: m.ID, action: actionCreate, after: m}, nil
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (a *App) ListGroupMembers(ctx context.Context, groupID uuid.UUID) ([]model.GroupMember, error) {
	if _, err := a.GetExistingGroup(ctx, a.DB, groupID); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `SELECT `+engine.GroupMemberColumns+` FROM group_member WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.GroupMember{}
	for rows.Next() {
		m, err := engine.ScanGroupMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (a *App) UpdateGroupMember(ctx context.Context, groupID, memberID uuid.UUID, in *model.GroupMemberInput) (*model.GroupMember, error) {
	var m *model.GroupMember
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingGroupMember(ctx, q, groupID, memberID)
		if err != nil {
			return change{}, err
		}
		if err := a.checkMemberGroup(ctx, q, groupID, in); err != nil {
			return change{}, err
		}

		m, err = engine.ScanGroupMember(q.QueryRow(ctx, `
			UPDATE group_member SET member_group_id = $2, predicate = $3::jsonb, enabled = $4, valid_from = $5, expires_at = $6
			WHERE id = $1
			RETURNING `+engine.GroupMemberColumns,
			memberID, engine.ToNullUUID(in.MemberGroupID), engine.MustJSON(in.Predicate), *in.Enabled, in.ValidFrom, in.ExpiresAt,
		))
		if err != nil {
			return change{}, err
		}
		return change{entity: "group_member", entityID: memberID, action: actionUpdate, before: old, after: m}, nil
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (a *App) DeleteGroupMember(ctx context.Context, groupID, memberID uuid.UUID) (uuid.UUID, error) {
	err := a.mutate(ctx, func(q db.Querier) (change, error) {
		old, err := a.getExistingGroupMember(ctx, q, groupID, memberID)
		if err != nil {
			return change{}, err
		}
		if _, err := q.Exec(ctx, `DELETE FROM group_member WHERE id = $1`, memberID); err != nil {
			return change{}, err
		}
		return change{entity: "group_member", entityID: memberID, action: actionDelete, before: old}, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return memberID, nil
}
