package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/model"
)

// typeEdge is a link from a resource type to one of its parent types: only the levels in inherit
// flow down along it (both grants and denials).
type typeEdge struct {
	Parent  uuid.UUID
	Inherit LevelSet
}

// TypeNode is a resource type with the data the permission computation needs.
type TypeNode struct {
	ID          uuid.UUID
	ServiceID   uuid.UUID
	ServiceName string
	Name        string
	Deleted     bool
	// Sync says the type has a live synchronizer: it is a sync type, and its resources are the ones that synchronizer
	// reported. A type without one is not_sync and has no resources, whatever rows a deleted synchronizer left in the local
	// copy (they stay there, as the rows a source stops reporting do, see IsSync).
	Sync bool
	// SourceDeleted says the type has no live synchronizer but had one: the rows its synchronizer reported may still be in
	// the local copy, and the closure of the types below it must not lead through them (see signature).
	SourceDeleted bool
	// Parents are the declared parent links (of any state of the parent type).
	Parents []typeEdge
	// Implies is the transitive closure of the type's access level hierarchy: level -> levels it includes.
	Implies map[string][]string
}

// TypeGraph is the DAG of resource types. Only links between live (not deleted) types count.
type TypeGraph struct {
	nodes    map[uuid.UUID]*TypeNode
	children map[uuid.UUID][]typeEdge // parent -> children (Parent field holds the child id)
	// names maps a service and the name of one of its live types to that type.
	names map[uuid.UUID]map[string]uuid.UUID
	// flow[a][b], for b a strict descendant of a, is the union over all routes a -> b of the levels
	// that flow along the whole route.
	flow map[uuid.UUID]map[uuid.UUID]LevelSet
	// noResources are the live types that have no live synchronizer (see withoutResources), sourceLost the ones among them
	// that had one: the rows it reported may still be in the local copy.
	noResources, sourceLost []uuid.UUID
}

func newTypeGraph(nodes []*TypeNode) *TypeGraph {
	g := &TypeGraph{
		nodes:    make(map[uuid.UUID]*TypeNode, len(nodes)),
		children: map[uuid.UUID][]typeEdge{},
		names:    map[uuid.UUID]map[string]uuid.UUID{},
		flow:     map[uuid.UUID]map[uuid.UUID]LevelSet{},
	}
	for _, n := range nodes {
		g.nodes[n.ID] = n
	}
	for _, n := range nodes {
		if n.Deleted {
			continue
		}
		if g.names[n.ServiceID] == nil {
			g.names[n.ServiceID] = map[string]uuid.UUID{}
		}
		g.names[n.ServiceID][n.Name] = n.ID
		for _, e := range n.Parents {
			if p, ok := g.nodes[e.Parent]; ok && !p.Deleted {
				g.children[e.Parent] = append(g.children[e.Parent], typeEdge{Parent: n.ID, Inherit: e.Inherit})
			}
		}
	}
	for _, n := range nodes {
		if !n.Deleted {
			g.flow[n.ID] = g.computeFlow(n.ID)
		}
	}
	g.noResources, g.sourceLost = []uuid.UUID{}, []uuid.UUID{}
	for _, n := range nodes {
		if n.Deleted || n.Sync {
			continue
		}
		g.noResources = append(g.noResources, n.ID)
		if n.SourceDeleted {
			g.sourceLost = append(g.sourceLost, n.ID)
		}
	}
	slices.SortFunc(g.noResources, compareUUID)
	slices.SortFunc(g.sourceLost, compareUUID)
	return g
}

// TypeID returns the live type of a service with the given name.
func (g *TypeGraph) TypeID(serviceID uuid.UUID, name string) (uuid.UUID, bool) {
	id, ok := g.names[serviceID][name]
	return id, ok
}

// computeFlow finds every strict descendant of root with the levels that reach it. Masks distribute
// over union, so each route is followed separately and a route is dropped once it adds nothing new
// (which also makes a cycle in hand-edited data terminate).
func (g *TypeGraph) computeFlow(root uuid.UUID) map[uuid.UUID]LevelSet {
	type step struct {
		node uuid.UUID
		mask LevelSet
	}
	reach := map[uuid.UUID]LevelSet{}
	queue := []step{{root, AllLevels}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.children[cur.node] {
			next := cur.mask.Intersect(e.Inherit)
			if next.empty() || e.Parent == root {
				continue
			}
			have, seen := reach[e.Parent]
			if seen && next.SubsetOf(have) {
				continue
			}
			reach[e.Parent] = have.union(next)
			queue = append(queue, step{e.Parent, next})
		}
	}
	return reach
}

func (g *TypeGraph) Node(id uuid.UUID) (*TypeNode, bool) {
	n, ok := g.nodes[id]
	return n, ok
}

func (g *TypeGraph) live(id uuid.UUID) bool {
	n, ok := g.nodes[id]
	return ok && !n.Deleted
}

// IsSync reports whether a type has resources: it is live and has a live synchronizer. This is what every decision reads
// about the resources of a type: a resource of a type that is not sync is not there (a question about it is
// resource_not_found, a rule on it anchors nothing, a cascade does not reach it and a link to it is unresolved), even when
// the local copy still holds it because the synchronizer of its type was deleted.
func (g *TypeGraph) IsSync(id uuid.UUID) bool {
	n, ok := g.nodes[id]
	return ok && !n.Deleted && n.Sync
}

// withoutResources returns the live types that have no live synchronizer, sorted: no resource of them leads anywhere in
// the closure, and a link to one is unresolved. It is a parameter of queries (x <> ALL), so it is never nil: a nil slice
// would be sent as NULL, and nothing is unequal to all of NULL. The graph is shared and never changed: so is the slice.
func (g *TypeGraph) withoutResources() []uuid.UUID { return g.noResources }

// liveParents returns the parent links of a type whose parent type is live.
func (g *TypeGraph) liveParents(id uuid.UUID) []typeEdge {
	n, ok := g.nodes[id]
	if !ok || n.Deleted {
		return nil
	}
	var out []typeEdge
	for _, e := range n.Parents {
		if g.live(e.Parent) {
			out = append(out, e)
		}
	}
	return out
}

// reaches reports whether levels of the given kind flow from type a down to a strictly lower type b.
func (g *TypeGraph) reaches(a, b uuid.UUID, level string) bool {
	m, ok := g.flow[a][b]
	return ok && m.has(level)
}

// DescendantsOf lists the strict descendant types of a (with the flowing levels).
func (g *TypeGraph) DescendantsOf(a uuid.UUID) map[uuid.UUID]LevelSet { return g.flow[a] }

// assumedMask is the set of levels a denial anchored at a resource of type anchorType may reach a resource with, through
// the unresolved links at or above it (see UnresolvedParents): a missing parent could be the anchor or lie below it, and
// the levels then flow from it along the type link that names it, and down the route to the resource. Empty when no link
// can be reached from the anchor's type.
func (g *TypeGraph) assumedMask(anchorType uuid.UUID, unresolved []UnresolvedLink) LevelSet {
	var out LevelSet
	for _, u := range unresolved {
		flow := AllLevels
		if anchorType != u.ParentType {
			m, ok := g.DescendantsOf(anchorType)[u.ParentType]
			if !ok {
				continue
			}
			flow = m
		}
		var link LevelSet
		for _, e := range g.liveParents(u.FromType) {
			if e.Parent == u.ParentType {
				link = e.Inherit
			}
		}
		out = out.union(link.Intersect(u.Route).Intersect(flow))
	}
	return out
}

// AncestorTypes returns every strict ancestor type of the given types (any flowing levels).
func (g *TypeGraph) AncestorTypes(ids ...uuid.UUID) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	var stack []uuid.UUID
	stack = append(stack, ids...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range g.liveParents(cur) {
			if !out[e.Parent] {
				out[e.Parent] = true
				stack = append(stack, e.Parent)
			}
		}
	}
	return out
}

// TypesBetween returns the live types on some route of type links from lower up to upper, both included, whatever
// levels flow along it (the walk of a resource_from_claim lookup climbs them: findAncestorsBatch). Nothing when upper is
// neither lower nor above it.
func (g *TypeGraph) TypesBetween(lower, upper uuid.UUID) []uuid.UUID {
	if lower == upper {
		return []uuid.UUID{lower}
	}
	above := g.AncestorTypes(lower)
	if !above[upper] {
		return nil
	}
	out := []uuid.UUID{lower, upper}
	for t := range above {
		if t != upper && g.AncestorTypes(t)[upper] {
			out = append(out, t)
		}
	}
	return out
}

// signature fingerprints everything upstream of a type: the links (with their level filters) and the
// state of the types on them. The resource paths of a type depend on the type graph, not only on the
// data of its source, so a change anywhere above must change the sync hash and trigger a rebuild.
//
// A type above that lost its synchronizer is part of that state: the closure leads through the resources of sync types
// only (rebuildClosureSQL), so the routes through the rows such a type left in the local copy go when its synchronizer is
// deleted and come back when it has one again, and the types below are rebuilt both times. A type that never had one is
// not marked: it has no rows to lead through, and the signatures stay those of the previous release.
func (g *TypeGraph) signature(id uuid.UUID) string {
	seen := map[uuid.UUID]bool{}
	var parts []string
	stack := []uuid.UUID{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		n, ok := g.nodes[cur]
		if !ok || n.Deleted {
			parts = append(parts, cur.String()+"!")
			continue
		}
		parts = append(parts, cur.String()+"|"+n.ServiceName+"/"+n.Name)
		if n.SourceDeleted {
			parts = append(parts, cur.String()+"~")
		}
		for _, e := range n.Parents {
			parts = append(parts, cur.String()+">"+e.Parent.String()+e.Inherit.key())
			stack = append(stack, e.Parent)
		}
	}
	slices.Sort(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, ";")))
	return hex.EncodeToString(sum[:8])
}

// LoadTypeGraph reads the whole type graph (all services, live and deleted types).
func (a *App) LoadTypeGraph(ctx context.Context) (*TypeGraph, error) {
	return loadTypeGraph(ctx, a.reader(ctx))
}

// LiveSynchronizerExists is the single SQL definition of a type's mode: a type with a live (not deleted) synchronizer is
// sync, otherwise it is not_sync. typeIDExpr is the SQL expression of the type id being checked. The engine reads it once
// per type graph (TypeNode.Sync); the admin API reads it where it lists or checks the targets of rules.
func LiveSynchronizerExists(typeIDExpr string) string {
	return `EXISTS (SELECT 1 FROM synchronizer s WHERE s.type_resource_id = ` + typeIDExpr + ` AND s.deleted_at IS NULL)`
}

// loadTypeGraph reads the type graph through q (the pool, or a transaction).
func loadTypeGraph(ctx context.Context, q db.Querier) (*TypeGraph, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.service_id, s.name, t.name, t.deleted_at IS NOT NULL, t.access_implies,
		       `+LiveSynchronizerExists("t.id")+`,
		       EXISTS (SELECT 1 FROM synchronizer s WHERE s.type_resource_id = t.id AND s.deleted_at IS NOT NULL)
		FROM type_resource t JOIN service s ON s.id = t.service_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*TypeNode
	byID := map[uuid.UUID]*TypeNode{}
	for rows.Next() {
		var n TypeNode
		var raw []byte
		var hadSource bool
		if err := rows.Scan(&n.ID, &n.ServiceID, &n.ServiceName, &n.Name, &n.Deleted, &raw, &n.Sync, &hadSource); err != nil {
			return nil, err
		}
		n.SourceDeleted = hadSource && !n.Sync
		var implies map[string][]string
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &implies); err != nil {
				return nil, err
			}
		}
		n.Implies = model.ImpliedClosure(implies)
		nodes = append(nodes, &n)
		byID[n.ID] = &n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	edges, err := q.Query(ctx, `SELECT type_resource_id, parent_id, inherit FROM type_resource_parent`)
	if err != nil {
		return nil, err
	}
	defer edges.Close()
	for edges.Next() {
		var child, parent uuid.UUID
		var inherit []string
		if err := edges.Scan(&child, &parent, &inherit); err != nil {
			return nil, err
		}
		if n, ok := byID[child]; ok {
			n.Parents = append(n.Parents, typeEdge{Parent: parent, Inherit: levelsFromDB(inherit)})
		}
	}
	if err := edges.Err(); err != nil {
		return nil, err
	}
	return newTypeGraph(nodes), nil
}
