package service

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

// PolicyClaims lists the claims and request attributes the policy of a service reads: the conditions of the enabled rules
// that apply to its resource types (and to those above them) and of their guards, the claim-based targets of those rules
// and of their roles, and the conditions of the group memberships (groups are shared by every service). It tells what a
// trusted client has to put into the claims it sends, or into the tokens it gets, for the answers to be the real ones.
func (a *App) PolicyClaims(ctx context.Context, serviceID uuid.UUID) ([]model.PolicyClaim, error) {
	if _, err := a.GetExistingService(ctx, a.DB, serviceID); err != nil {
		return nil, err
	}
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	own := engine.ServiceTypes(g, serviceID)
	relevant := map[uuid.UUID]bool{}
	for _, id := range own {
		relevant[id] = true
	}
	for id := range g.AncestorTypes(own...) {
		relevant[id] = true
	}
	rows, err := a.DB.Query(ctx, `
		WITH rules AS (
			SELECT ar.* FROM assignment_rule ar
			WHERE ar.enabled AND (
				ar.type_resource_id = ANY($1)
				OR (ar.role_id IS NOT NULL AND EXISTS (
					SELECT 1 FROM role_permission rp WHERE rp.role_id = ar.role_id AND rp.type_resource_id = ANY($1))))
		), uses AS (
			SELECT 'rule' AS kind, COALESCE(c->>'source', 'claim') AS source, c->>'attr' AS attr
			FROM rules r, jsonb_array_elements(r.predicate) c
			UNION ALL SELECT 'rule', COALESCE(c->>'source', 'claim'), c->>'attr'
			FROM rules r, jsonb_array_elements(r.requires_predicate) c
			UNION ALL SELECT 'target', 'claim', r.resource_from_claim->>'claim'
			FROM rules r WHERE r.resource_from_claim IS NOT NULL
			UNION ALL SELECT 'target', 'claim', rp.resource_from_claim->>'claim'
			FROM rules r JOIN role_permission rp ON rp.role_id = r.role_id AND rp.type_resource_id = ANY($1)
			WHERE rp.resource_from_claim IS NOT NULL
			UNION ALL SELECT 'membership', COALESCE(c->>'source', 'claim'), c->>'attr'
			FROM group_member gm, jsonb_array_elements(gm.predicate) c WHERE gm.enabled
		)
		SELECT kind, source, attr, count(*) FROM uses
		WHERE attr IS NOT NULL AND source <> 'now'
		GROUP BY kind, source, attr`, engine.Keys(relevant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ source, name string }
	byKey := map[key]*model.PolicyClaim{}
	for rows.Next() {
		var kind, source, attr string
		var n int
		if err := rows.Scan(&kind, &source, &attr, &n); err != nil {
			return nil, err
		}
		c := byKey[key{source, attr}]
		if c == nil {
			c = &model.PolicyClaim{Name: attr, Source: source}
			byKey[key{source, attr}] = c
		}
		switch kind {
		case "rule":
			c.Rules += n
		case "target":
			c.Targets += n
		case "membership":
			c.Memberships += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]model.PolicyClaim, 0, len(byKey))
	for _, c := range byKey {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(x, y model.PolicyClaim) int {
		if x.Source != y.Source {
			return strings.Compare(x.Source, y.Source)
		}
		return strings.Compare(x.Name, y.Name)
	})
	return out, nil
}
