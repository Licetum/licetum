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

// signature fingerprints everything upstream of a type: the links (with their level filters) and the
// state of the types on them. The resource paths of a type depend on the type graph, not only on the
// data of its source, so a change anywhere above must change the sync hash and trigger a rebuild.
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
	return loadTypeGraph(ctx, a.DB)
}

// loadTypeGraph reads the type graph through q (the pool, or a transaction).
func loadTypeGraph(ctx context.Context, q db.Querier) (*TypeGraph, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.service_id, s.name, t.name, t.deleted_at IS NOT NULL, t.access_implies
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
		if err := rows.Scan(&n.ID, &n.ServiceID, &n.ServiceName, &n.Name, &n.Deleted, &raw); err != nil {
			return nil, err
		}
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
