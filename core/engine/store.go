package engine

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/model"
)

const AssignmentRuleColumns = `ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access,
	ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, ar.effect, ar.exception, ar.valid_from, ar.expires_at,
	ar.name, ar.description, ar.on_missing_attribute, ar.requires_predicate, ar.requires_group_id, ar.resource_where,
	ar.subject_kind`

// RuleActiveSQL selects the rules in force now: enabled and inside their validity period.
const RuleActiveSQL = `ar.enabled
	AND (ar.valid_from IS NULL OR ar.valid_from <= now())
	AND (ar.expires_at IS NULL OR ar.expires_at > now())`

// ScanAssignmentRule reads the assignmentRuleColumns columns plus any extra dest (if given).
func ScanAssignmentRule(row pgx.Row, extra ...any) (*model.AssignmentRule, error) {
	var r model.AssignmentRule
	var fromClaim, predicate, requires, where []byte
	var memberOf, requiresGroup uuid.NullUUID
	dest := append([]any{&r.ID, &r.RoleID, &r.TypeResourceID, &r.ResourceID, &fromClaim, &r.Access,
		&r.Subject, &predicate, &memberOf, &r.Enabled, &r.Effect, &r.Exception, &r.ValidFrom, &r.ExpiresAt,
		&r.Name, &r.Description, &r.OnMissingAttribute, &requires, &requiresGroup, &where, &r.SubjectKind}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	r.MemberOfGroupID = NullUUID(memberOf)
	r.RequiresGroupID = NullUUID(requiresGroup)
	if fromClaim != nil && string(fromClaim) != "null" {
		r.ResourceFromClaim = &model.ResourceFromClaim{}
		if err := json.Unmarshal(fromClaim, r.ResourceFromClaim); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(predicate, &r.Predicate); err != nil {
		return nil, err
	}
	if r.Predicate == nil {
		r.Predicate = []model.PredicateCondition{}
	}
	if err := unmarshalConditions(requires, &r.RequiresPredicate); err != nil {
		return nil, err
	}
	if r.RequiresPredicate == nil {
		r.RequiresPredicate = []model.PredicateCondition{}
	}
	if err := unmarshalConditions(where, &r.ResourceWhere); err != nil {
		return nil, err
	}
	// An inline rule always has access levels; a role rule has none of its own (null).
	if r.Access == nil && r.RoleID == nil {
		r.Access = []string{}
	}
	return &r, nil
}

// GroupMemberColumns are the columns scanGroupMember reads (enabled comes with the validity period).
const GroupMemberColumns = `id, group_id, member_group_id, predicate, enabled, valid_from, expires_at`

func ScanGroupMember(row pgx.Row) (*model.GroupMember, error) {
	var m model.GroupMember
	var memberGroup uuid.NullUUID
	var predicate []byte
	if err := row.Scan(&m.ID, &m.GroupID, &memberGroup, &predicate, &m.Enabled, &m.ValidFrom, &m.ExpiresAt); err != nil {
		return nil, err
	}
	m.MemberGroupID = NullUUID(memberGroup)
	if err := json.Unmarshal(predicate, &m.Predicate); err != nil {
		return nil, err
	}
	if m.Predicate == nil {
		m.Predicate = []model.PredicateCondition{}
	}
	return &m, nil
}

// RolePermissionColumns are the columns scanRolePermission reads.
const RolePermissionColumns = `rp.id, rp.role_id, rp.type_resource_id, rp.resource_id, rp.resource_from_claim, rp.access,
	rp.resource_where`

// ScanRolePermission reads the rolePermissionColumns columns plus any extra dest (if given).
func ScanRolePermission(row pgx.Row, extra ...any) (*model.RolePermission, error) {
	var p model.RolePermission
	var fromClaim, where []byte
	dest := append([]any{&p.ID, &p.RoleID, &p.TypeResourceID, &p.ResourceID, &fromClaim, &p.Access, &where}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if fromClaim != nil && string(fromClaim) != "null" {
		p.ResourceFromClaim = &model.ResourceFromClaim{}
		if err := json.Unmarshal(fromClaim, p.ResourceFromClaim); err != nil {
			return nil, err
		}
	}
	if err := unmarshalConditions(where, &p.ResourceWhere); err != nil {
		return nil, err
	}
	if p.Access == nil {
		p.Access = []string{}
	}
	return &p, nil
}

func ScanSynchronizer(row pgx.Row) (*model.Synchronizer, error) {
	var s model.Synchronizer
	var kind string
	var raw []byte
	if err := row.Scan(&s.ID, &s.TypeResourceID, &kind, &raw); err != nil {
		return nil, err
	}
	cfg, err := model.ConfigFromDB(kind, raw)
	if err != nil {
		return nil, err
	}
	s.Config = cfg
	return &s, nil
}

const SynchronizerColumns = `id, type_resource_id, kind::text, config`

// ActiveSynchronizer returns the first non-deleted synchronizer of a type (or nil).
func (a *App) ActiveSynchronizer(ctx context.Context, typeResourceID uuid.UUID) (*model.Synchronizer, error) {
	s, err := ScanSynchronizer(a.DB.QueryRow(ctx,
		`SELECT `+SynchronizerColumns+` FROM synchronizer
		 WHERE type_resource_id = $1 AND deleted_at IS NULL
		 ORDER BY id LIMIT 1`, typeResourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// unmarshalConditions reads a jsonb list of conditions: SQL NULL and JSON null are no list.
func unmarshalConditions(raw []byte, out *[]model.PredicateCondition) error {
	if raw == nil || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}
