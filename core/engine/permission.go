package engine

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/licetum/licetum/core/model"
)

const (
	ResultCacheTTL = 60 * time.Second
	MaxGroupDepth  = 16
	// resultCacheFormat is the version of the cached result format: when the format changes, new keys
	// do not overlap the old ones, and stale entries simply expire by TTL.
	resultCacheFormat = "t6"
)

// PermissionResult — {type_resource_name: sync {resources} | not_sync {access}}.
type PermissionResult map[string]model.TypePermission

// Consistency is what a caller asks of the freshness of an answer.
type Consistency string

const (
	// MinimizeLatency (the default) may answer from the result cache and does not ask a source again
	// within the sync throttle.
	MinimizeLatency Consistency = "minimize_latency"
	// HigherConsistency skips the result cache and fetches every involved source again.
	HigherConsistency Consistency = "higher"
)

func (c Consistency) fresh() bool { return c == HigherConsistency }

// permissionData is the whole outcome of a computation: what the check API returns, plus what the
// self-service needs to tell "no access" from "denied".
type permissionData struct {
	Types PermissionResult `json:"types"`
	// Whole are the levels on each type of the service as a whole (only rules anchored on the type
	// itself count): what creating new entities needs.
	Whole map[string][]string `json:"whole"`
	// Denied lists, per type, the resources with levels that were granted but are denied.
	Denied map[string][]model.ResourceAccess `json:"denied"`
}

// GetPermission is the core of Licetum: what the subject (in the given environment: its claims, and the attributes of
// the request) may do with the resources of serviceID, per resource type. A type's mode is derived from its
// synchronizer: a type with one is sync — {"type": "sync", "resources": [...]} — without one it is not_sync —
// {"type": "not_sync", "access": [...]}. A type without applicable rules is not in the result.
//
// The answer is cached (see cachedAnswer), per subject and per the attributes the rules look at, until an
// admin write or a change of the resource data it was computed from. An unknown service is a NotFound: the service
// is looked up when the answer is computed, and deleting a service bumps the version, so no answer outlives it.
func (a *App) GetPermission(ctx context.Context, serviceID uuid.UUID, subject string, env Env, consistency Consistency) (PermissionResult, error) {
	data, err := a.GetPermissionData(ctx, serviceID, subject, env, consistency)
	if err != nil {
		return nil, err
	}
	return data.Types, nil
}

// GetPermissionData is GetPermission with what the self-service needs too: the denials, and the levels
// on each type as a whole.
func (a *App) GetPermissionData(ctx context.Context, serviceID uuid.UUID, subject string, env Env, consistency Consistency) (*permissionData, error) {
	state := a.readCacheState(ctx)
	key := fmt.Sprintf("authz:permission:%s:v%d:%s:%s:%s", resultCacheFormat, state.version, serviceID, subject,
		a.requestFingerprint(ctx, state, env))
	return cachedAnswer(ctx, a, key, state, consistency, a.metrics.permissionCache,
		func(ctx context.Context) (computed[*permissionData], error) {
			if _, err := a.GetExistingService(ctx, a.DB, serviceID); err != nil {
				return computed[*permissionData]{}, err
			}
			started := time.Now()
			data, deps, ttl, err := a.computePermissionData(ctx, serviceID, subject, env, state, consistency)
			if err != nil {
				return computed[*permissionData]{}, err
			}
			a.metrics.permissionCompute.Observe(time.Since(started).Seconds())
			return computed[*permissionData]{value: data, deps: deps, ttl: ttl}, nil
		})
}

// requestFingerprint hashes what the request carries that the rules look at (its claims and its context attributes):
// two requests with the same fingerprint get the same answer. The names of those attributes are kept in the cache state
// for the current rules version; when they are not there, they are read from the rules and stored for the requests that
// follow.
func (a *App) requestFingerprint(ctx context.Context, state cacheState, env Env) string {
	if state.attrsOK {
		return fingerprint(state.attrs, env)
	}
	attrs, err := a.RequestAttributes(ctx)
	if err != nil {
		// Without the list, fall back to every attribute the request carries: correct, only less shared.
		slog.WarnContext(ctx, "failed to read the attributes the rules look at", "error", err)
		return fingerprint(allRequestAttributes(env), env)
	}
	if state.ok {
		if record, err := json.Marshal(claimAttrsRecord{Version: state.version, Attrs: attrs}); err == nil {
			a.Background(func(ctx context.Context) {
				if err := a.Redis.Set(ctx, claimAttrsKey, record, claimAttrsTTL).Err(); err != nil {
					slog.WarnContext(ctx, "failed to store the attributes the rules look at", "error", err)
				}
			})
		}
	}
	return fingerprint(attrs, env)
}

// conditionAttrSQL names the attribute of a condition by its source: "context:<name>" for a context attribute, and
// "claim:<name>" otherwise. The time (source now) reads nothing from the request.
const conditionAttrSQL = `CASE WHEN c->>'source' = 'context' THEN 'context:' ELSE 'claim:' END || (c->>'attr')`

const notNowSQL = `COALESCE(c->>'source', 'claim') <> 'now'`

// RequestAttributes reads the attributes the rules look at: the conditions of the rules, of their guards and of the group
// memberships, and the claims of the resource_from_claim specs (of rules and of role items).
func (a *App) RequestAttributes(ctx context.Context) ([]string, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT DISTINCT attr FROM (
			SELECT `+conditionAttrSQL+` AS attr FROM assignment_rule, jsonb_array_elements(predicate) c WHERE `+notNowSQL+`
			UNION ALL SELECT `+conditionAttrSQL+` FROM assignment_rule, jsonb_array_elements(requires_predicate) c WHERE `+notNowSQL+`
			UNION ALL SELECT `+conditionAttrSQL+` FROM group_member, jsonb_array_elements(predicate) c WHERE `+notNowSQL+`
			UNION ALL SELECT 'claim:' || (resource_from_claim->>'claim') FROM assignment_rule WHERE resource_from_claim IS NOT NULL
			UNION ALL SELECT 'claim:' || (resource_from_claim->>'claim') FROM role_permission WHERE resource_from_claim IS NOT NULL
		) x WHERE attr IS NOT NULL ORDER BY attr`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attrs []string
	for rows.Next() {
		var attr string
		if err := rows.Scan(&attr); err != nil {
			return nil, err
		}
		attrs = append(attrs, attr)
	}
	return attrs, rows.Err()
}

// allRequestAttributes names every attribute the request carries (the fallback without the list of the rules).
func allRequestAttributes(env Env) []string {
	var names []string
	for k := range env.Claims {
		names = append(names, "claim:"+k)
	}
	for k := range env.Context {
		names = append(names, "context:"+k)
	}
	slices.Sort(names)
	return names
}

// attribute returns the value of an attribute named by its source ("claim:x" or "context:x"); a name without a source
// is a claim, as the names stored before the sources were.
func (e Env) attribute(name string) (any, bool) {
	if key, ok := strings.CutPrefix(name, "context:"); ok {
		v, found := e.Context[key]
		return v, found && v != nil
	}
	key := strings.TrimPrefix(name, "claim:")
	v, found := e.Claims[key]
	return v, found && v != nil
}

func fingerprint(attrs []string, env Env) string {
	var b strings.Builder
	for _, attr := range attrs {
		b.WriteString(attr)
		b.WriteByte(0)
		if v, ok := env.attribute(attr); ok {
			b.WriteString(PyStr(v))
		} else {
			b.WriteByte(1) // absent differs from an empty value
		}
		b.WriteByte(2)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// ResolveIdentitySet: a group is a subject too (see model.GroupSubject). A subject "is" a group either
// through a predicate over the request (a condition the request does not satisfy leaves it out of the group), or
// because that group is nested as a whole into another group the subject already belongs to. We build the transitive
// closure: the subject plus every group it ends up in. Only memberships in force now count. The bool reports whether a
// membership reads the time of the decision.
func (a *App) ResolveIdentitySet(ctx context.Context, subject string, env Env) (identitySet, bool, error) {
	members, err := a.LiveMemberships(ctx)
	if err != nil {
		return nil, false, err
	}
	identity, usesNow := IdentityOf(members, subject, env)
	return identity, usesNow, nil
}

// LiveMemberships reads the group memberships in force now.
func (a *App) LiveMemberships(ctx context.Context) ([]model.GroupMember, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT `+GroupMemberColumns+` FROM group_member
		WHERE enabled AND (valid_from IS NULL OR valid_from <= now()) AND (expires_at IS NULL OR expires_at > now())`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []model.GroupMember
	for rows.Next() {
		m, err := ScanGroupMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, *m)
	}
	return members, rows.Err()
}

// IdentityOf is resolveIdentitySet over memberships that are already loaded: many subjects can be resolved against one
// read. It is pure.
func IdentityOf(members []model.GroupMember, subject string, env Env) (identitySet, bool) {
	usesNow := false
	groups := map[uuid.UUID]truth{}
	var nested []model.GroupMember
	for _, m := range members {
		if predicateUsesNow(m.Predicate) {
			usesNow = true
		}
		if m.MemberGroupID == nil {
			groups[m.GroupID] = groups[m.GroupID].or(evaluatePredicate(m.Predicate, env))
		} else {
			nested = append(nested, m)
		}
	}

	// A member of a group that is a member of this one is a member of this one too, as far as the child is known.
	for range MaxGroupDepth {
		changed := false
		for _, m := range nested {
			if t := groups[m.GroupID].or(groups[*m.MemberGroupID]); t != groups[m.GroupID] {
				groups[m.GroupID] = t
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	identity := identitySet{subject: Yes}
	for id, t := range groups {
		if t != no {
			identity[model.GroupSubject(id)] = t
		}
	}
	return identity, usesNow
}

// LoadRules reads assignment rules (the assignmentRuleColumns columns of alias ar).
func (a *App) LoadRules(ctx context.Context, sql string, args ...any) ([]model.AssignmentRule, error) {
	rows, err := a.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.AssignmentRule
	for rows.Next() {
		r, err := ScanAssignmentRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// applicableRules loads the assignments in force that target the caller: a literal subject from the identity set,
// every member of a group, or everyone whose conditions hold for the request. The assignments of roles are then
// expanded into the permissions of their roles (see expandRoles). It returns the ids of the assignments that apply
// only because a denial applies on a missing attribute, and whether a loaded rule reads the time of the decision.
func (a *App) applicableRules(ctx context.Context, typeIDs []uuid.UUID, identity identitySet, env Env) ([]model.AssignmentRule, map[uuid.UUID]bool, bool, error) {
	identityList := make([]string, 0, len(identity))
	for s := range identity {
		identityList = append(identityList, s)
	}

	// An inline rule lives on a relevant type that is not deleted; a role assignment is checked by the
	// types of its items in expandRoles.
	const ruleQuery = `
		SELECT ` + AssignmentRuleColumns + `
		FROM assignment_rule ar
		LEFT JOIN type_resource t ON t.id = ar.type_resource_id
		WHERE ` + RuleActiveSQL + `
		  AND (ar.role_id IS NOT NULL OR (ar.type_resource_id = ANY($1) AND t.deleted_at IS NULL))`

	// Indexed path (formerly Grant): point rows with subject IN identity — fast even with a large
	// number of point grants.
	direct, err := a.LoadRules(ctx, ruleQuery+` AND ar.subject = ANY($2) ORDER BY ar.id`, typeIDs, identityList)
	if err != nil {
		return nil, nil, false, err
	}
	// Rule path: a predicate over the request or member_of_group_id — scanned in full, since we cannot know in
	// advance which rule matches which subject.
	byRule, err := a.LoadRules(ctx, ruleQuery+` AND ar.subject IS NULL ORDER BY ar.id`, typeIDs)
	if err != nil {
		return nil, nil, false, err
	}

	var out []model.AssignmentRule
	missing := map[uuid.UUID]bool{}
	usesNow := false
	for _, rule := range slices.Concat(direct, byRule) {
		if ruleUsesNow(rule) {
			usesNow = true
		}
		applies, miss := RuleApplies(rule, identity, env)
		if !applies {
			continue
		}
		out = append(out, rule)
		if miss {
			missing[rule.ID] = true
		}
	}
	rules, err := a.ExpandRoles(ctx, out, typeIDs)
	if err != nil {
		return nil, nil, false, err
	}
	return rules, missing, usesNow, nil
}

// ExpandRoles turns every assignment of a role into one rule per item of the role: a copy of the
// assignment (same id, subject, validity and effect) that carries the item's target and access levels.
// The engine then sees the permissions of a role exactly like inline rules. Items on types outside
// typeIDs or on deleted types do not apply.
func (a *App) ExpandRoles(ctx context.Context, rules []model.AssignmentRule, typeIDs []uuid.UUID) ([]model.AssignmentRule, error) {
	var roleIDs []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, r := range rules {
		if r.RoleID != nil && !seen[*r.RoleID] {
			seen[*r.RoleID] = true
			roleIDs = append(roleIDs, *r.RoleID)
		}
	}

	items := map[uuid.UUID][]model.RolePermission{}
	if len(roleIDs) > 0 {
		rows, err := a.DB.Query(ctx, `
			SELECT `+RolePermissionColumns+`
			FROM role_permission rp
			JOIN type_resource t ON t.id = rp.type_resource_id
			WHERE rp.role_id = ANY($1) AND rp.type_resource_id = ANY($2) AND t.deleted_at IS NULL
			ORDER BY rp.id`, roleIDs, typeIDs)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := ScanRolePermission(rows)
			if err != nil {
				return nil, err
			}
			items[p.RoleID] = append(items[p.RoleID], *p)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	var out []model.AssignmentRule
	for _, r := range rules {
		if r.RoleID == nil {
			out = append(out, r)
			continue
		}
		for _, item := range items[*r.RoleID] {
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
	return out, nil
}

// ruleTruth is the outcome of the conditions of a rule for the caller: the target (a literal subject from the identity
// set, every member of a group, or a predicate over the request), and the guards (the conditions of requires_predicate,
// and membership of requires_group_id). An attribute the request does not carry makes the outcome unknown.
func ruleTruth(rule model.AssignmentRule, identity identitySet, env Env) truth {
	var target truth
	switch {
	case rule.Subject != nil:
		target = identity[*rule.Subject]
	case rule.MemberOfGroupID != nil:
		target = identity[model.GroupSubject(*rule.MemberOfGroupID)]
	default:
		target = evaluatePredicate(rule.Predicate, env)
	}
	guard := evaluatePredicate(rule.RequiresPredicate, env)
	if rule.RequiresGroupID != nil {
		guard = guard.and(identity[model.GroupSubject(*rule.RequiresGroupID)])
	}
	return target.and(guard)
}

// RuleApplies reports whether a rule applies to the caller now, and whether it does only because a condition reads an
// attribute the request does not carry. An allow rule applies only when its conditions hold: it grants nothing on a
// missing attribute. A deny rule set to apply (on_missing_attribute) applies on a missing attribute too: fail closed.
func RuleApplies(rule model.AssignmentRule, identity identitySet, env Env) (applies, missing bool) {
	switch ruleTruth(rule, identity, env) {
	case Yes:
		return true, false
	case unknown:
		if rule.Effect == model.EffectDeny && rule.OnMissingAttribute == model.OnMissingApply {
			return true, true
		}
	}
	return false, false
}

// RuleOrigin says in words how a rule reaches the caller (for explanations).
func RuleOrigin(rule model.AssignmentRule) string {
	var who string
	switch {
	case rule.Subject != nil && strings.HasPrefix(*rule.Subject, model.GroupSubjectPrefix):
		who = "group " + strings.TrimPrefix(*rule.Subject, model.GroupSubjectPrefix)
	case rule.Subject != nil:
		who = "subject " + *rule.Subject
	case rule.MemberOfGroupID != nil:
		who = "members of group " + rule.MemberOfGroupID.String()
	case len(rule.Predicate) > 0:
		who = "claims predicate"
	default:
		who = "everyone"
	}
	if rule.RoleID != nil {
		return "role " + rule.RoleID.String() + " for " + who
	}
	return who
}

// cacheTTL returns how long the cached result stays correct with respect to validity periods: the
// time until the nearest moment a rule (on the given types) or a group membership starts or stops
// being in force. Capped at ResultCacheTTL.
func (a *App) cacheTTL(ctx context.Context, typeIDs []uuid.UUID) time.Duration {
	var until *time.Time
	err := a.DB.QueryRow(ctx, `
		SELECT min(b) FROM (
			-- a role assignment has no type of its own: its boundaries count for every type
			SELECT valid_from AS b FROM assignment_rule
			WHERE valid_from > now() AND (type_resource_id = ANY($1) OR role_id IS NOT NULL)
			UNION ALL SELECT expires_at FROM assignment_rule
			WHERE expires_at > now() AND (type_resource_id = ANY($1) OR role_id IS NOT NULL)
			UNION ALL SELECT valid_from FROM group_member WHERE valid_from > now()
			UNION ALL SELECT expires_at FROM group_member WHERE expires_at > now()
		) x`, typeIDs).Scan(&until)
	if err != nil {
		slog.WarnContext(ctx, "failed to read the next validity boundary", "error", err)
		return time.Second
	}
	if until == nil {
		return ResultCacheTTL
	}
	return min(ResultCacheTTL, max(time.Second, time.Until(*until)))
}

// ServiceTypes returns the live types owned by a service.
func ServiceTypes(g *TypeGraph, serviceID uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for id, n := range g.nodes {
		if n.ServiceID == serviceID && !n.Deleted {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	return out
}

// TypesWithSynchronizer returns which of the given types have a live synchronizer (are sync types).
func (a *App) TypesWithSynchronizer(ctx context.Context, typeIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := a.DB.Query(ctx,
		`SELECT DISTINCT type_resource_id FROM synchronizer WHERE type_resource_id = ANY($1) AND deleted_at IS NULL`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Prepared is a permission computation up to the point where the engine can decide: the applicable
// rules turned into instances, the engine, and what the response is made of.
type Prepared struct {
	Graph       *TypeGraph
	engine      *engine
	Insts       []Instance
	serviceType []uuid.UUID
	syncTypes   map[uuid.UUID]bool // the service types with a live synchronizer
	resultTypes []uuid.UUID        // the sync types of the service whose data is current
	ttl         time.Duration
	// deps are the data versions of the services whose resource data the decision reads (see prepare).
	deps map[string]int64
}

// syncedTypes keeps the service types whose data is current: the ones with a live synchronizer that were
// synchronized now or are within their throttle.
func (p *Prepared) syncedTypes(synced map[uuid.UUID]bool) {
	p.resultTypes = nil
	for _, id := range p.serviceType {
		if synced[id] {
			p.resultTypes = append(p.resultTypes, id)
		}
	}
}

// prepare loads and resolves everything a decision needs, except the resources themselves. The independent
// reads run in parallel: the identity set and the type graph first, then the applicable rules, the validity
// boundary and the sync types. asked names the types of the service the caller asks about: they are
// synchronized even if no rule reaches them. The data versions the answer reads are recorded in p.deps.
func (a *App) prepare(ctx context.Context, serviceID uuid.UUID, subject string, env Env, state cacheState, consistency Consistency, asked []string) (*Prepared, error) {
	var identity identitySet
	var identityUsesNow bool
	var g *TypeGraph
	eg, ectx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		identity, identityUsesNow, err = a.ResolveIdentitySet(ectx, subject, env)
		return err
	})
	eg.Go(func() (err error) {
		g, err = a.LoadTypeGraph(ectx)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	p := &Prepared{Graph: g, serviceType: ServiceTypes(g, serviceID)}

	// A rule is relevant if its type belongs to serviceID OR is an ancestor (of any service) of such a type.
	relevant := map[uuid.UUID]bool{}
	for _, id := range p.serviceType {
		relevant[id] = true
	}
	for id := range g.AncestorTypes(p.serviceType...) {
		relevant[id] = true
	}
	relevantIDs := Keys(relevant)

	var rules []model.AssignmentRule
	var missing map[uuid.UUID]bool
	var rulesUseNow bool
	eg, ectx = errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		rules, missing, rulesUseNow, err = a.applicableRules(ectx, relevantIDs, identity, env)
		return err
	})
	eg.Go(func() error {
		p.ttl = a.cacheTTL(ectx, relevantIDs)
		return nil
	})
	eg.Go(func() (err error) {
		p.syncTypes, err = a.TypesWithSynchronizer(ectx, p.serviceType)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	// An answer that reads the hour or the weekday is correct until the next minute only.
	if identityUsesNow || rulesUseNow {
		p.ttl = min(p.ttl, max(untilNextMinute(env.Now), time.Second))
	}

	// The types the applicable rules reach (the type itself, its descendants and the lookup type of
	// resource_from_claim) are brought up to the state of their sources. From here on everything is SQL
	// over the local copy: not a single live request per resource.
	involved := map[uuid.UUID]bool{}
	for _, name := range asked {
		if id, ok := g.TypeID(serviceID, name); ok {
			involved[id] = true
		}
	}
	for _, rule := range rules {
		involved[*rule.TypeResourceID] = true
		for id := range g.DescendantsOf(*rule.TypeResourceID) {
			involved[id] = true
		}
		if rule.ResourceFromClaim != nil {
			involved[rule.ResourceFromClaim.LookupTypeResourceID] = true
		}
	}
	involvedIDs := Keys(involved)
	var synced map[uuid.UUID]bool
	var err error
	if consistency.fresh() {
		synced, err = a.syncTypesOnce(ctx, involvedIDs, true, func(ctx context.Context) (map[uuid.UUID]bool, error) {
			return a.EnsureSynced(ctx, g, involvedIDs, true)
		})
	} else {
		synced, err = a.syncWhileServing(ctx, g, involvedIDs)
	}
	if err != nil {
		return nil, err
	}
	p.syncedTypes(synced)

	// What the answer reads is the resource data of the involved types. Their services are recorded with the
	// data versions this request saw before reading any of that data.
	p.deps = map[string]int64{}
	for id := range involved {
		if n, ok := g.Node(id); ok {
			svc := n.ServiceID.String()
			p.deps[svc] = state.data[svc]
		}
	}

	if p.Insts, err = a.BuildInstances(ctx, rules, env, missing); err != nil {
		return nil, err
	}
	pairs, err := a.instancePairs(ctx, p.Insts)
	if err != nil {
		return nil, err
	}
	p.engine = newEngine(g, p.Insts, pairs)
	return p, nil
}

// BuildInstances turns the applicable rules into instances anchored at whole types or at resources.
// A rule on a resource that is not in the cache has no anchor and no effect; a rule with resource_from_claim
// yields one instance per ancestor found, and a rule with resource_where one per resource whose attributes match.
// The lookups of all the rules run together, in parallel with the anchors of the rules that name a resource.
func (a *App) BuildInstances(ctx context.Context, rules []model.AssignmentRule, env Env, missing map[uuid.UUID]bool) ([]Instance, error) {
	var refs []anchorRef
	var lookups []ancestorLookup
	claimValues := map[int]string{}
	var groupTypes []uuid.UUID
	for i, r := range rules {
		if hasGroupReference(r.Access) {
			groupTypes = append(groupTypes, *r.TypeResourceID)
		}
		switch {
		case r.ResourceFromClaim != nil:
			spec := r.ResourceFromClaim
			value := env.Claims[spec.Claim]
			if !PyTruthy(value) {
				continue
			}
			claimValues[i] = PyStr(value)
			lookups = append(lookups, ancestorLookup{
				Index: i, Lookup: spec.LookupTypeResourceID, Target: *r.TypeResourceID,
				Value: PyStr(value), MatchBy: spec.MatchBy,
			})
		case r.ResourceID != nil:
			refs = append(refs, anchorRef{Type: *r.TypeResourceID, Ext: *r.ResourceID})
		}
	}

	var rows map[anchorRef]uuid.UUID
	var found, matched map[int][]graphNode
	var groups map[uuid.UUID]map[string][]string
	eg, ectx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		rows, err = a.anchorRowIDs(ectx, refs)
		return err
	})
	eg.Go(func() (err error) {
		found, err = a.findAncestorsBatch(ectx, lookups)
		return err
	})
	eg.Go(func() (err error) {
		matched, err = a.ResourcesMatching(ectx, rules)
		return err
	})
	eg.Go(func() (err error) {
		groups, err = a.accessGroupLevels(ectx, groupTypes)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	var out []Instance
	for i := range rules {
		rule := &rules[i]
		base := Instance{
			Rule: rule, Deny: rule.Effect == model.EffectDeny, Exception: rule.Exception,
			Levels: expandAccess(rule.Access, groups[*rule.TypeResourceID]), AnchorType: *rule.TypeResourceID,
			Note: RuleOrigin(*rule), Missing: missing[rule.ID],
		}
		switch {
		case rule.ResourceFromClaim != nil:
			for _, n := range found[i] {
				in := base
				id := n.ID
				in.AnchorRow, in.AnchorExt = &id, n.Ext
				in.Note += fmt.Sprintf("; resource %s found by claim %s=%s", n.Ext, rule.ResourceFromClaim.Claim, claimValues[i])
				out = append(out, in)
			}
		case rule.ResourceID != nil:
			id, ok := rows[anchorRef{Type: *rule.TypeResourceID, Ext: *rule.ResourceID}]
			if !ok {
				continue
			}
			in := base
			in.AnchorRow, in.AnchorExt = &id, *rule.ResourceID
			out = append(out, in)
		case len(rule.ResourceWhere) > 0:
			for _, n := range matched[i] {
				in := base
				id := n.ID
				in.AnchorRow, in.AnchorExt = &id, n.Ext
				in.Note += fmt.Sprintf("; resource %s matches its attributes", n.Ext)
				out = append(out, in)
			}
		default:
			out = append(out, base)
		}
	}
	return out, nil
}

// instancePairs relates the anchors of denials and exceptions: for every denial and exception both
// anchored at resources, the levels that flow from the denial's anchor down to the exception's anchor.
func (a *App) instancePairs(ctx context.Context, insts []Instance) (map[[2]int]LevelSet, error) {
	var idx [][2]int
	var pairs [][2]uuid.UUID
	for d := range insts {
		if !insts[d].Deny || insts[d].Whole() {
			continue
		}
		for e := range insts {
			if insts[e].Exception && !insts[e].Whole() {
				idx = append(idx, [2]int{d, e})
				pairs = append(pairs, [2]uuid.UUID{*insts[d].AnchorRow, *insts[e].AnchorRow})
			}
		}
	}
	found, err := a.pairMasks(ctx, pairs)
	if err != nil {
		return nil, err
	}
	out := map[[2]int]LevelSet{}
	for i, p := range pairs {
		if m, ok := found[p]; ok {
			out[idx[i]] = m
		}
	}
	return out, nil
}

// nodeState is a resource with the rules that cover it.
type nodeState struct {
	graphNode
	covers []Cover
}

func hasCover(n *nodeState, inst int) bool {
	return slices.ContainsFunc(n.covers, func(c Cover) bool { return c.Inst == inst && !c.Assumed })
}

// coverNodes finds the resources of the result types covered by the instances. Allow instances
// create the candidates; denials only attach to resources that have a grant to take away.
func (a *App) coverNodes(ctx context.Context, p *Prepared) (map[uuid.UUID]*nodeState, error) {
	g := p.Graph
	inResult := map[uuid.UUID]bool{}
	for _, id := range p.resultTypes {
		inResult[id] = true
	}
	nodes := map[uuid.UUID]*nodeState{}
	byType := map[uuid.UUID][]*nodeState{}

	// The resources of the types that whole-type instances reach.
	wholeTypes := map[uuid.UUID]bool{}
	for i := range p.Insts {
		in := &p.Insts[i]
		if !in.Whole() {
			continue
		}
		if inResult[in.AnchorType] {
			wholeTypes[in.AnchorType] = true
		}
		for id := range g.DescendantsOf(in.AnchorType) {
			if inResult[id] {
				wholeTypes[id] = true
			}
		}
	}
	wholeTypeIDs := Keys(wholeTypes)

	// Anchors at resources, expanded through the closure.
	var anchorInst []int
	var anchorRows []uuid.UUID
	for i := range p.Insts {
		if !p.Insts[i].Whole() {
			anchorInst = append(anchorInst, i)
			anchorRows = append(anchorRows, *p.Insts[i].AnchorRow)
		}
	}

	// The three reads do not depend on each other: the resources of the whole types, the descendants of the
	// anchors, and the unresolved parent links of the result types.
	var listed map[uuid.UUID][]graphNode
	var expanded [][]CoveredNode
	var unresolved map[uuid.UUID][]uuid.UUID
	eg, ectx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		listed, err = a.listCachedResources(ectx, wholeTypeIDs)
		return err
	})
	eg.Go(func() (err error) {
		expanded, err = a.ExpandAnchors(ectx, anchorRows, p.resultTypes)
		return err
	})
	eg.Go(func() (err error) {
		unresolved, err = a.UnresolvedParents(ectx, p.resultTypes)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	expandedOf := map[int][]CoveredNode{}
	for i, inst := range anchorInst {
		expandedOf[inst] = expanded[i]
	}

	attach := func(inst int, n graphNode, mask LevelSet, create bool) {
		st, ok := nodes[n.ID]
		if !ok {
			if !create {
				return
			}
			st = &nodeState{graphNode: n}
			nodes[n.ID] = st
			byType[n.Type] = append(byType[n.Type], st)
		}
		st.covers = append(st.covers, Cover{Inst: inst, Mask: mask})
	}
	wholeMask := func(anchorType, nodeType uuid.UUID) (LevelSet, bool) {
		if anchorType == nodeType {
			return AllLevels, true
		}
		m, ok := g.DescendantsOf(anchorType)[nodeType]
		return m, ok
	}

	// Pass A: grants create the candidates.
	for i := range p.Insts {
		in := &p.Insts[i]
		if in.Deny {
			continue
		}
		if in.Whole() {
			for _, typeID := range wholeTypeIDs {
				if m, ok := wholeMask(in.AnchorType, typeID); ok {
					for _, n := range listed[typeID] {
						attach(i, n, m, true)
					}
				}
			}
			continue
		}
		for _, n := range expandedOf[i] {
			attach(i, n.graphNode, n.Mask, true)
		}
	}
	// Pass B: denials cover the candidates.
	for i := range p.Insts {
		in := &p.Insts[i]
		if !in.Deny {
			continue
		}
		if in.Whole() {
			for typeID, list := range byType {
				if m, ok := wholeMask(in.AnchorType, typeID); ok {
					for _, n := range list {
						n.covers = append(n.covers, Cover{Inst: i, Mask: m})
					}
				}
			}
			continue
		}
		for _, n := range expandedOf[i] {
			attach(i, n.graphNode, n.Mask, false)
		}
	}
	// Pass C: a resource with an unresolved parent link may hang below any denial anchored above
	// that parent's type — the hierarchy above it is not fully known, so the denial applies.
	for id, parentTypes := range unresolved {
		n, ok := nodes[id]
		if !ok {
			continue
		}
		nodeType, _ := g.Node(n.Type)
		for i := range p.Insts {
			in := &p.Insts[i]
			if !in.Deny || in.Whole() || hasCover(n, i) {
				continue
			}
			for _, parentType := range parentTypes {
				edgeMask := LevelSet{}
				for _, e := range nodeType.Parents {
					if e.Parent == parentType {
						edgeMask = e.Inherit
					}
				}
				flowMask, reaches := wholeMask(in.AnchorType, parentType)
				if !reaches {
					continue
				}
				if m := edgeMask.Intersect(flowMask); !m.empty() {
					n.covers = append(n.covers, Cover{Inst: i, Mask: m, Assumed: true})
				}
			}
		}
	}
	return nodes, nil
}

// computePermissionData computes the permissions of a subject in a service. Along with the answer it returns
// the data versions it read (see prepare) and how long it stays correct.
func (a *App) computePermissionData(ctx context.Context, serviceID uuid.UUID, subject string, env Env, state cacheState, consistency Consistency) (*permissionData, map[string]int64, time.Duration, error) {
	p, err := a.prepare(ctx, serviceID, subject, env, state, consistency, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	nodes, err := a.coverNodes(ctx, p)
	if err != nil {
		return nil, nil, 0, err
	}

	data := &permissionData{
		Types:  PermissionResult{},
		Whole:  map[string][]string{},
		Denied: map[string][]model.ResourceAccess{},
	}
	resources := map[string][]model.ResourceAccess{}
	for _, n := range nodes {
		typ, ok := p.Graph.Node(n.Type)
		if !ok {
			continue
		}
		v := p.engine.decideMemo(n.Type, n.covers)
		if len(v.Allowed) > 0 {
			resources[typ.Name] = append(resources[typ.Name], model.ResourceAccess{ID: n.Ext, Name: n.Name, Access: v.Allowed})
		}
		if len(v.Blocked) > 0 {
			data.Denied[typ.Name] = append(data.Denied[typ.Name], model.ResourceAccess{ID: n.Ext, Name: n.Name, Access: v.Blocked})
		}
	}
	sortAccess := func(list []model.ResourceAccess) {
		// A stable order: by name, then by id.
		slices.SortFunc(list, func(x, y model.ResourceAccess) int {
			return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.ID, y.ID))
		})
	}
	for name, list := range resources {
		sortAccess(list)
		data.Types[name] = model.TypePermission{Type: model.TypeModeSync, Resources: list}
	}
	for _, list := range data.Denied {
		sortAccess(list)
	}

	for _, id := range p.serviceType {
		typ, _ := p.Graph.Node(id)
		v := p.engine.decideWhole(id)
		if len(v.Allowed) > 0 {
			data.Whole[typ.Name] = v.Allowed
			// Types without a synchronizer have no resources: the levels on the type itself are their
			// whole answer. On a name collision the sync type stays.
			if !p.syncTypes[id] {
				if _, taken := data.Types[typ.Name]; !taken {
					data.Types[typ.Name] = model.TypePermission{Type: model.TypeModeNotSync, Access: v.Allowed}
				}
			}
		}
	}
	return data, p.deps, p.ttl, nil
}

func Keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
