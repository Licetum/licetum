package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/model"
)

// searchBatch is how many candidates the resource search reads and decides at a time: a batch costs a few set-based queries,
// whatever its size, so a large one keeps the round trips of a page few. The scan budget is counted in candidates.
const searchBatch = 500

// searchResultFormat is the version of the cached pages of the resource search: it changes with their format and with the
// decisions.
const searchResultFormat = "t1"

// ResourceSearch is a request of the resource search: the type and the action asked for, where the page starts, and its bounds.
type ResourceSearch struct {
	Type   string
	Action string
	// After is the key the page starts after; nil for the first page. A key may be the empty string (a source may give a resource
	// an empty id), so the empty string does not mean the first page.
	After *string
	// Limit is the size of the page. Budget is how many candidates one request reads at most (PDP__SEARCH_SCAN_BUDGET).
	Limit, Budget int
}

// ResourceHit is a resource the search found: the id the source gives it, and its name.
type ResourceHit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ResourcePage is one page of the resource search (SearchResources).
type ResourcePage struct {
	// Hits are the resources the caller may do the action with, in bytewise order of their ids.
	Hits []ResourceHit `json:"hits,omitempty"`
	// Last is the key the page covers up to; the next page starts after it. It is the key of the last hit of a full page, and the
	// last candidate read when the scan budget stopped the page, which may then be shorter than its limit, even empty. It is
	// meaningful when Done is false.
	Last string `json:"last,omitempty"`
	// Done says that the keys of the type are exhausted: there is no next page.
	Done bool `json:"done,omitempty"`
}

// SearchResources lists the resources of the type that the caller may do the action with: one page of the AuthZEN resource
// search (ADR 0001, 7.6). The candidates are the active resources of the type read from the local copy by a keyset scan in
// bytewise order (COLLATE "C"), starting after the key q.After, and each is decided with the rules of the caller, in batches, as
// CheckAccess decides a question about one resource. Only the asked type is read: the permission map of the whole service is not
// computed.
//
// The page is computed at the current position and nothing is pinned between pages. The rules are prepared once for the page,
// so all its batches are decided under one view of the policy. At most q.Budget candidates are read. When the page holds q.Limit
// hits and one more is found, the page ends after its last hit; when the budget stops the scan first, the page ends at the last
// candidate read, and a next page continues from there. A type without resources (a permission such as a feature flag), a type
// the service does not have, and a caller to whom no rule gives anything, give a finished empty page.
//
// A page is cached as the answers of CheckAccess are, per subject, per the attributes the rules look at, per revision of the
// rules, and as long as the data of the services it read is unchanged (see cachedAnswer), so a repeated page costs no query.
func (a *App) SearchResources(ctx context.Context, serviceID uuid.UUID, subject string, env Env, q ResourceSearch,
	consistency Consistency) (ResourcePage, error) {
	q.Limit, q.Budget = max(q.Limit, 1), max(q.Budget, 1)
	ask, err := json.Marshal(q)
	if err != nil {
		return ResourcePage{}, err
	}
	sum := sha256.Sum256(ask)
	if err := a.replicaGate(); err != nil {
		return ResourcePage{}, err
	}
	state := a.cacheState(ctx).forConsistency(ctx, consistency)
	key := fmt.Sprintf("authz:search:%s:r%d:%s:%s:%s:%s", searchResultFormat, state.version, serviceID, subject,
		a.requestFingerprint(ctx, state, env), hex.EncodeToString(sum[:]))
	return cachedAnswer(ctx, a, key, state, consistency, a.metrics.searchCache,
		func(ctx context.Context) (computed[ResourcePage], error) {
			if err := a.serviceExists(ctx, state, serviceID); err != nil {
				return computed[ResourcePage]{}, err
			}
			// The types the rules reach are synchronized (see prepare); a type no rule reaches has nothing to find.
			p, err := a.prepare(ctx, serviceID, subject, env, state, consistency, nil)
			if err != nil {
				return computed[ResourcePage]{}, err
			}
			page, err := a.scanResources(ctx, p, serviceID, q)
			if err != nil {
				return computed[ResourcePage]{}, err
			}
			return computed[ResourcePage]{value: page, deps: p.deps, ttl: p.ttl, hold: p.hold}, nil
		})
}

// scanResources is the page itself: the candidates after the key, decided with the prepared rules (see SearchResources).
func (a *App) scanResources(ctx context.Context, p *Prepared, serviceID uuid.UUID, q ResourceSearch) (ResourcePage, error) {
	typeID, ok := p.Graph.TypeID(serviceID, q.Type)
	if !ok || !p.hasResources(typeID) {
		return ResourcePage{Done: true}, nil
	}
	// With no rule that allows anything no resource is a hit, whatever the type holds: the scan would read it for nothing.
	if !slices.ContainsFunc(p.Insts, func(in Instance) bool { return !in.Deny }) {
		return ResourcePage{Done: true}, nil
	}
	// The unresolved parent links of the type do not depend on the batch: they are read once for the page.
	unresolved, err := a.UnresolvedParents(ctx, p.Graph, []uuid.UUID{typeID})
	if err != nil {
		return ResourcePage{}, err
	}

	var page ResourcePage
	cursor := q.After
	scanned := 0
	for {
		n := min(searchBatch, q.Budget-scanned)
		if n <= 0 {
			// The budget stops the scan: the next page continues after the last candidate read, even when this page is short.
			return page, nil
		}
		candidates, err := a.resourceKeysAfter(ctx, typeID, cursor, n)
		if err != nil {
			return ResourcePage{}, err
		}
		if len(candidates) == 0 {
			page.Done = true
			return page, nil
		}
		questions := make([]model.CheckRequest, len(candidates))
		for i := range candidates {
			questions[i] = model.CheckRequest{Type: q.Type, ResourceID: &candidates[i].ID, Action: q.Action}
		}
		results, err := a.decideChecksWith(ctx, p, serviceID, questions, false, unresolved)
		if err != nil {
			return ResourcePage{}, err
		}
		for i, c := range candidates {
			scanned++
			if results[i].Allowed {
				if len(page.Hits) == q.Limit {
					// One hit more than the page holds: the page ends after its last hit, which the next page starts after.
					page.Last = page.Hits[q.Limit-1].ID
					return page, nil
				}
				page.Hits = append(page.Hits, ResourceHit{ID: c.ID, Name: c.Name})
			}
			page.Last = c.ID
		}
		last := page.Last
		cursor = &last
		if len(candidates) < n {
			page.Done = true
			return page, nil
		}
	}
}

// resourceKeysAfter reads the next candidates of a type: its active resources in bytewise order of their ids, after the key
// (nil: from the first), at most limit. The order and the comparison are COLLATE "C", so that the pages do not depend on the
// collation of the database; ix_resource_cache_type_external_c serves the scan.
func (a *App) resourceKeysAfter(ctx context.Context, typeID uuid.UUID, after *string, limit int) ([]ResourceHit, error) {
	// Two statements, not one with "$2 IS NULL OR ...": the range condition then stays an index condition in a generic plan too,
	// and a deep page does not scan the type from its first key.
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = a.reader(ctx).Query(ctx, `
			SELECT external_id, name FROM resource_cache
			WHERE type_resource_id = $1 AND active
			ORDER BY external_id COLLATE "C"
			LIMIT $2`, typeID, limit)
	} else {
		rows, err = a.reader(ctx).Query(ctx, `
			SELECT external_id, name FROM resource_cache
			WHERE type_resource_id = $1 AND active AND external_id COLLATE "C" > $2
			ORDER BY external_id COLLATE "C"
			LIMIT $3`, typeID, *after, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ResourceHit, 0, limit)
	for rows.Next() {
		var h ResourceHit
		if err := rows.Scan(&h.ID, &h.Name); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
