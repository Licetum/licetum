package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// snapshot is the policy of the whole installation as one revision of it: the services, the type graph, the group
// memberships, the rules, the role items, the access groups and the names of the request attributes the rules read. A
// request that decides from the snapshot sends no query for any of them. It is immutable once built, replaced as a
// whole when the revision moves (see localView), and shared by every request of the process.
//
// The validity windows of the rules and of the memberships are not applied when the snapshot is built: they are
// compared with the clock at every request (ruleActive, memberActive), so the snapshot does not go stale as time
// passes, and the TTL of an answer (cacheTTL) is worked out from the window boundaries kept here.
type snapshot struct {
	revision int64
	loadedAt time.Time
	// bytes is the estimated memory of the snapshot.
	bytes int64

	services map[uuid.UUID]*model.Service // without client_secret
	bySlug   map[string]uuid.UUID
	graph    *TypeGraph

	// members are the memberships that are enabled, whatever their window.
	members []model.GroupMember
	// direct are the enabled rules with a literal subject, by subject; each list is in id order.
	direct map[string][]model.AssignmentRule
	// scanned are the enabled rules without a subject (a predicate, a group, everyone), in id order: these are scanned
	// for every request, since which of them match a subject cannot be looked up.
	scanned []model.AssignmentRule
	// roleItems are the items of each role, in id order.
	roleItems map[uuid.UUID][]model.RolePermission
	// attrs are the request attributes the rules look at (RequestAttributes).
	attrs []string
	// synced are the types that have a live synchronizer.
	synced map[uuid.UUID]bool
	// accessGroups are the levels of each access group, by type and name.
	accessGroups map[uuid.UUID]map[string][]string
	// bounds are the moments a rule or a membership starts or stops being in force (see cacheTTL).
	bounds []validityBound
}

// validityBound is a moment a rule or a membership starts or stops being in force. A rule belongs to a type, or is a
// role assignment (typeID is nil), which counts for every type.
type validityBound struct {
	at     time.Time
	typeID *uuid.UUID
	role   bool
	member bool
}

// errSnapshotTooLarge says the policy would not fit SNAPSHOT__MAX_BYTES.
var errSnapshotTooLarge = errors.New("the policy does not fit SNAPSHOT__MAX_BYTES")

// Rough sizes of the rows, in bytes, for the memory bound: a row costs its struct and the strings and conditions it
// holds. The bound is a guard against a policy that would take the process down, not an accounting.
const (
	ruleBaseBytes      = 640
	memberBaseBytes    = 280
	itemBaseBytes      = 360
	conditionBytes     = 160
	serviceBaseBytes   = 420
	typeBaseBytes      = 400
	flowEntryBytes     = 96
	accessGroupBytes   = 120
	snapshotFloorBytes = 1 // a floor so that an empty policy has a size
)

func ruleBytes(r *model.AssignmentRule) int64 {
	n := int64(ruleBaseBytes)
	n += int64(len(r.Predicate)+len(r.RequiresPredicate)+len(r.ResourceWhere)) * conditionBytes
	for _, a := range r.Access {
		n += int64(len(a)) + 16
	}
	if r.ResourceID != nil {
		n += int64(len(*r.ResourceID))
	}
	if r.Subject != nil {
		n += int64(len(*r.Subject))
	}
	if r.Name != nil {
		n += int64(len(*r.Name))
	}
	if r.Description != nil {
		n += int64(len(*r.Description))
	}
	return n
}

func itemBytes(p *model.RolePermission) int64 {
	n := int64(itemBaseBytes) + int64(len(p.ResourceWhere))*conditionBytes
	for _, a := range p.Access {
		n += int64(len(a)) + 16
	}
	if p.ResourceID != nil {
		n += int64(len(*p.ResourceID))
	}
	return n
}

// ServiceCheckColumns are the columns of a service the check API needs: the address of its JWKS, its issuer and its
// trusted clients, but not its client secret, which stays out of the memory of the check path.
const ServiceCheckColumns = `id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, trusted_clients`

// ScanServiceCheck reads the ServiceCheckColumns.
func ScanServiceCheck(row pgx.Row) (*model.Service, error) {
	var s model.Service
	err := row.Scan(&s.ID, &s.Name, &s.Slug, &s.URL, &s.Icon, &s.AuthorizationURL, &s.TokenURL, &s.JwksURL,
		&s.Issuer, &s.ClientID, &s.TrustedClients)
	return &s, err
}

// loadSnapshot reads the whole policy in one read-only transaction (so that every table is seen at the same moment) and
// labels it with revision. It returns errSnapshotTooLarge when the policy would not fit max bytes.
func (a *App) loadSnapshot(ctx context.Context, revision, max int64) (*snapshot, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
	defer cancel()

	tx, err := a.reader(ctx).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The sizes first: a policy that cannot fit is not read at all.
	var rules, members, items, services, types, groups int64
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM assignment_rule WHERE enabled), (SELECT count(*) FROM group_member WHERE enabled),
		       (SELECT count(*) FROM role_permission), (SELECT count(*) FROM service WHERE deleted_at IS NULL),
		       (SELECT count(*) FROM type_resource), (SELECT count(*) FROM access_group)`).
		Scan(&rules, &members, &items, &services, &types, &groups); err != nil {
		return nil, err
	}
	estimate := rules*ruleBaseBytes + members*memberBaseBytes + items*itemBaseBytes + services*serviceBaseBytes +
		types*typeBaseBytes + groups*accessGroupBytes
	if estimate > max {
		return nil, errSnapshotTooLarge
	}

	snap := &snapshot{
		revision:     revision,
		loadedAt:     time.Now(),
		bytes:        snapshotFloorBytes,
		services:     map[uuid.UUID]*model.Service{},
		bySlug:       map[string]uuid.UUID{},
		direct:       map[string][]model.AssignmentRule{},
		roleItems:    map[uuid.UUID][]model.RolePermission{},
		synced:       map[uuid.UUID]bool{},
		accessGroups: map[uuid.UUID]map[string][]string{},
	}
	grow := func(n int64) error {
		snap.bytes += n
		if snap.bytes > max {
			return errSnapshotTooLarge
		}
		return nil
	}

	// Services.
	rowsOf, err := tx.Query(ctx, `SELECT `+ServiceCheckColumns+` FROM service WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		s, err := ScanServiceCheck(rowsOf)
		if err != nil {
			rowsOf.Close()
			return nil, err
		}
		snap.services[s.ID] = s
		snap.bySlug[s.Slug] = s.ID
		if err := grow(serviceBaseBytes); err != nil {
			rowsOf.Close()
			return nil, err
		}
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}

	// The type graph.
	if snap.graph, err = loadTypeGraph(ctx, tx); err != nil {
		return nil, err
	}
	flow := 0
	for _, m := range snap.graph.flow {
		flow += len(m)
	}
	if err := grow(int64(len(snap.graph.nodes))*typeBaseBytes + int64(flow)*flowEntryBytes); err != nil {
		return nil, err
	}

	// Group memberships.
	rowsOf, err = tx.Query(ctx, `SELECT `+GroupMemberColumns+` FROM group_member WHERE enabled`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		m, err := ScanGroupMember(rowsOf)
		if err != nil {
			rowsOf.Close()
			return nil, err
		}
		snap.members = append(snap.members, *m)
		if err := grow(memberBaseBytes + int64(len(m.Predicate))*conditionBytes); err != nil {
			rowsOf.Close()
			return nil, err
		}
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}

	// Rules.
	rowsOf, err = tx.Query(ctx, `SELECT `+AssignmentRuleColumns+` FROM assignment_rule ar WHERE ar.enabled ORDER BY ar.id`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		r, err := ScanAssignmentRule(rowsOf)
		if err != nil {
			rowsOf.Close()
			return nil, err
		}
		if err := grow(ruleBytes(r)); err != nil {
			rowsOf.Close()
			return nil, err
		}
		if r.Subject != nil {
			snap.direct[*r.Subject] = append(snap.direct[*r.Subject], *r)
		} else {
			snap.scanned = append(snap.scanned, *r)
		}
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}

	// Role items.
	rowsOf, err = tx.Query(ctx, `SELECT `+RolePermissionColumns+` FROM role_permission rp ORDER BY rp.id`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		p, err := ScanRolePermission(rowsOf)
		if err != nil {
			rowsOf.Close()
			return nil, err
		}
		snap.roleItems[p.RoleID] = append(snap.roleItems[p.RoleID], *p)
		if err := grow(itemBytes(p)); err != nil {
			rowsOf.Close()
			return nil, err
		}
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}

	// The types that have a live synchronizer, the access groups, the request attributes and the validity boundaries.
	rowsOf, err = tx.Query(ctx, `SELECT DISTINCT type_resource_id FROM synchronizer WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		var id uuid.UUID
		if err := rowsOf.Scan(&id); err != nil {
			rowsOf.Close()
			return nil, err
		}
		snap.synced[id] = true
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}

	rowsOf, err = tx.Query(ctx, `SELECT type_resource_id, name, levels FROM access_group`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		var typeID uuid.UUID
		var name string
		var levels []string
		if err := rowsOf.Scan(&typeID, &name, &levels); err != nil {
			rowsOf.Close()
			return nil, err
		}
		if snap.accessGroups[typeID] == nil {
			snap.accessGroups[typeID] = map[string][]string{}
		}
		snap.accessGroups[typeID][name] = levels
		if err := grow(accessGroupBytes); err != nil {
			rowsOf.Close()
			return nil, err
		}
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}

	if snap.attrs, err = requestAttributes(ctx, tx); err != nil {
		return nil, err
	}

	// A rule contributes its boundaries whether or not it is enabled, as the query for the TTL of an answer did.
	rowsOf, err = tx.Query(ctx, `
		SELECT valid_from, expires_at, type_resource_id, role_id IS NOT NULL, false FROM assignment_rule
		WHERE valid_from IS NOT NULL OR expires_at IS NOT NULL
		UNION ALL
		SELECT valid_from, expires_at, NULL, false, true FROM group_member
		WHERE valid_from IS NOT NULL OR expires_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for rowsOf.Next() {
		var from, until *time.Time
		var typeID *uuid.UUID
		var role, member bool
		if err := rowsOf.Scan(&from, &until, &typeID, &role, &member); err != nil {
			rowsOf.Close()
			return nil, err
		}
		for _, at := range []*time.Time{from, until} {
			if at != nil {
				snap.bounds = append(snap.bounds, validityBound{at: *at, typeID: typeID, role: role, member: member})
			}
		}
	}
	rowsOf.Close()
	if err := rowsOf.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return snap, nil
}

// ruleActive reports whether the rule is inside its validity window at now (enabled was applied when the snapshot was
// built).
func ruleActive(r *model.AssignmentRule, now time.Time) bool {
	return (r.ValidFrom == nil || !r.ValidFrom.After(now)) && (r.ExpiresAt == nil || r.ExpiresAt.After(now))
}

func memberActive(m *model.GroupMember, now time.Time) bool {
	return (m.ValidFrom == nil || !m.ValidFrom.After(now)) && (m.ExpiresAt == nil || m.ExpiresAt.After(now))
}

// liveMembers are the memberships in force at now.
func (s *snapshot) liveMembers(now time.Time) []model.GroupMember {
	out := make([]model.GroupMember, 0, len(s.members))
	for i := range s.members {
		if memberActive(&s.members[i], now) {
			out = append(out, s.members[i])
		}
	}
	return out
}

// applicable returns the rules in force at now that may target the caller and that live on one of the relevant types
// (or are role assignments), in the order the queries gave them: the rules with a subject of the identity set first, by
// id, then the scanned ones, by id.
func (s *snapshot) applicable(relevant map[uuid.UUID]bool, subjects []string, now time.Time) (direct, scanned []model.AssignmentRule) {
	keep := func(r *model.AssignmentRule) bool {
		if !ruleActive(r, now) {
			return false
		}
		return r.RoleID != nil || (r.TypeResourceID != nil && relevant[*r.TypeResourceID])
	}
	for _, subject := range subjects {
		for i := range s.direct[subject] {
			if r := &s.direct[subject][i]; keep(r) {
				direct = append(direct, *r)
			}
		}
	}
	slices.SortFunc(direct, func(x, y model.AssignmentRule) int { return compareUUID(x.ID, y.ID) })
	for i := range s.scanned {
		if r := &s.scanned[i]; keep(r) {
			scanned = append(scanned, *r)
		}
	}
	return direct, scanned
}

func compareUUID(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }

// expandRoles is App.ExpandRoles over the snapshot.
func (s *snapshot) expandRoles(rules []model.AssignmentRule, relevant map[uuid.UUID]bool) []model.AssignmentRule {
	var out []model.AssignmentRule
	for _, r := range rules {
		if r.RoleID == nil {
			out = append(out, r)
			continue
		}
		for _, item := range s.roleItems[*r.RoleID] {
			if !relevant[item.TypeResourceID] {
				continue
			}
			if n, ok := s.graph.Node(item.TypeResourceID); !ok || n.Deleted {
				continue
			}
			typeID := item.TypeResourceID
			copied := r
			copied.TypeResourceID = &typeID
			copied.ResourceID = item.ResourceID
			copied.ResourceFromClaim = item.ResourceFromClaim
			copied.ResourceWhere = item.ResourceWhere
			copied.Access = item.Access
			out = append(out, copied)
		}
	}
	return out
}

// ttl is how long an answer that depends on the rules of the given types stays correct with respect to validity
// windows: until the nearest moment after now a rule on one of the types (or a role assignment) or a membership starts
// or stops being in force, at most ResultCacheTTL.
func (s *snapshot) ttl(relevant map[uuid.UUID]bool, now time.Time) time.Duration {
	ttl, _ := s.validity(relevant, now)
	return ttl
}

// validity is ttl together with hold: how long the answer stays correct with respect to validity windows alone, without the
// cap of ResultCacheTTL (holdUnbounded when no window ends or starts).
func (s *snapshot) validity(relevant map[uuid.UUID]bool, now time.Time) (ttl, hold time.Duration) {
	var until *time.Time
	for i := range s.bounds {
		b := &s.bounds[i]
		if !b.at.After(now) {
			continue
		}
		if !b.member && !b.role && (b.typeID == nil || !relevant[*b.typeID]) {
			continue
		}
		if until == nil || b.at.Before(*until) {
			until = &b.at
		}
	}
	return validityUntil(until, now)
}

// service returns a copy of the live service with the id.
func (s *snapshot) service(id uuid.UUID) (*model.Service, error) {
	svc, ok := s.services[id]
	if !ok {
		return nil, apperr.NotFound(i18n.ServiceNotFound, id)
	}
	c := *svc
	return &c, nil
}

// serviceID returns the id of the live service with the slug.
func (s *snapshot) serviceID(slug string) (uuid.UUID, error) {
	id, ok := s.bySlug[slug]
	if !ok {
		return uuid.Nil, apperr.NotFound(i18n.ServiceNotFound, slug)
	}
	return id, nil
}

func (s *snapshot) String() string {
	return fmt.Sprintf("snapshot(revision=%d, services=%d, rules=%d, bytes=%d)", s.revision, len(s.services),
		len(s.scanned)+len(s.direct), s.bytes)
}

// logTooLarge reports once per process that the policy does not fit.
func (a *App) logSnapshotFallback(revision int64, err error) {
	a.snapshotWarn.Do(func() {
		slog.Warn("the policy is not kept in memory: the queries read it for every answer that is not cached; "+
			"raise SNAPSHOT__MAX_BYTES to keep it", "revision", revision, "max_bytes", a.tuningOrDefault().snapshotMaxBytes, "reason", err)
	})
}

// accessGroupLevels is App.accessGroupLevels over the snapshot.
func (s *snapshot) accessGroupLevels(typeIDs []uuid.UUID) map[uuid.UUID]map[string][]string {
	out := map[uuid.UUID]map[string][]string{}
	for _, id := range typeIDs {
		if groups, ok := s.accessGroups[id]; ok {
			out[id] = groups
		}
	}
	return out
}
