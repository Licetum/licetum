package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// predicateUsesNow reports whether a predicate reads the time of the decision: then an answer is kept only until the
// next minute (see prepare).
func predicateUsesNow(conds []model.PredicateCondition) bool {
	for _, c := range conds {
		if c.Source == model.SourceNow {
			return true
		}
	}
	return false
}

// ruleUsesNow reports whether a rule reads the time of the decision.
func ruleUsesNow(r model.AssignmentRule) bool {
	return predicateUsesNow(r.Predicate) || predicateUsesNow(r.RequiresPredicate)
}

// untilNextMinute is the time to the next minute: an answer that reads the hour or the weekday is kept at most that long.
func untilNextMinute(now time.Time) time.Duration {
	return time.Until(now.Truncate(time.Minute).Add(time.Minute))
}

// ResourcesMatching returns, for every rule with a resource_where, the resources of its type that its conditions cover.
// The attributes are the ones the synchronizers reported for the resources (resource_cache.meta), read from the local copy.
// sourced says which types have resources (TypeGraph.IsSync): a rule on a type without a live synchronizer matches nothing,
// whatever rows its deleted synchronizer left in the local copy.
func (a *App) ResourcesMatching(ctx context.Context, rules []model.AssignmentRule, sourced func(uuid.UUID) bool) (map[int][]graphNode, error) {
	out := map[int][]graphNode{}
	for i, rule := range rules {
		if len(rule.ResourceWhere) == 0 || !sourced(*rule.TypeResourceID) {
			continue
		}
		nodes, err := a.resourcesWhere(ctx, *rule.TypeResourceID, rule.ResourceWhere)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			out[i] = nodes
		}
	}
	return out, nil
}

// maxContainedValues bounds the values of one equality or membership condition the database checks (see
// containmentClauses): a condition with more values is left to resourceMatches.
const maxContainedValues = 16

// resourcesWhere returns the active resources of a type whose attributes match the conditions. The equality and
// membership conditions narrow the rows in the database, through the GIN index on meta. A string attribute holds the
// value and a list attribute holds it as an element, and containment does not match a list against a scalar, so both
// shapes are looked up. resourceMatches then decides on every condition: the database only drops rows that cannot match.
func (a *App) resourcesWhere(ctx context.Context, typeID uuid.UUID, conds []model.PredicateCondition) ([]graphNode, error) {
	args := []any{typeID}
	where := "type_resource_id = $1 AND active"
	for _, clause := range containmentClauses(conds, &args) {
		where += " AND " + clause
	}
	rows, err := a.reader(ctx).Query(ctx, `SELECT id, type_resource_id, external_id, name, meta FROM resource_cache WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []graphNode
	for rows.Next() {
		var node graphNode
		var metaRaw []byte
		if err := rows.Scan(&node.ID, &node.Type, &node.Ext, &node.Name, &metaRaw); err != nil {
			return nil, err
		}
		var meta map[string]any
		if metaRaw != nil {
			if err := json.Unmarshal(metaRaw, &meta); err != nil {
				return nil, err
			}
		}
		if resourceMatches(conds, meta) {
			out = append(out, node)
		}
	}
	return out, rows.Err()
}

// containmentClauses returns the SQL conditions that every resource matching conds satisfies, and appends their arguments
// to args. An equality or membership condition with a few values gives one alternative per value and per shape of the
// attribute: {attr: value} for a string, {attr: [value]} for a list. Other conditions give none.
func containmentClauses(conds []model.PredicateCondition, args *[]any) []string {
	var clauses []string
	for _, c := range conds {
		var values []string
		switch {
		case c.Op == model.OpEq && !c.Value.IsList:
			values = []string{c.Value.Str}
		case c.Op == model.OpIn && c.Value.IsList:
			values = c.Value.List
		case c.Op == model.OpIn:
			values = []string{c.Value.Str}
		}
		if len(values) == 0 || len(values) > maxContainedValues {
			continue
		}
		var alternatives []string
		for _, v := range values {
			for _, shape := range []any{v, []string{v}} {
				raw, _ := json.Marshal(map[string]any{c.Attr: shape})
				*args = append(*args, string(raw))
				alternatives = append(alternatives, "meta @> $"+strconv.Itoa(len(*args))+"::jsonb")
			}
		}
		clauses = append(clauses, "("+strings.Join(alternatives, " OR ")+")")
	}
	return clauses
}

// accessGroupLevels reads the access groups of the given types: for each type, the levels of every group by its name.
func (a *App) accessGroupLevels(ctx context.Context, typeIDs []uuid.UUID) (map[uuid.UUID]map[string][]string, error) {
	out := map[uuid.UUID]map[string][]string{}
	if len(typeIDs) == 0 {
		return out, nil
	}
	rows, err := a.reader(ctx).Query(ctx,
		`SELECT type_resource_id, name, levels FROM access_group WHERE type_resource_id = ANY($1)`, typeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var typeID uuid.UUID
		var name string
		var levels []string
		if err := rows.Scan(&typeID, &name, &levels); err != nil {
			return nil, err
		}
		if out[typeID] == nil {
			out[typeID] = map[string][]string{}
		}
		out[typeID][name] = levels
	}
	return out, rows.Err()
}

// hasGroupReference reports whether an access list names access groups ("@name").
func hasGroupReference(access []string) bool {
	for _, level := range access {
		if strings.HasPrefix(level, model.AccessGroupPrefix) {
			return true
		}
	}
	return false
}

// expandAccess replaces the access group references of an access list by the levels of the groups: each level is kept
// once, in order. A reference to a group that does not exist stands for nothing (the write refused it).
func expandAccess(access []string, groups map[string][]string) []string {
	if !hasGroupReference(access) {
		return access
	}
	var out []string
	seen := map[string]bool{}
	add := func(level string) {
		if !seen[level] {
			seen[level] = true
			out = append(out, level)
		}
	}
	for _, level := range access {
		if name, ok := strings.CutPrefix(level, model.AccessGroupPrefix); ok {
			for _, l := range groups[name] {
				add(l)
			}
			continue
		}
		add(level)
	}
	return out
}
