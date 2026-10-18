package refeval

import (
	"strings"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// resKey names a resource: its type and its external id.
type resKey struct{ typ, id string }

// openLink is an unresolved parent link at or above a resource: the parent the link names is not a resource of the
// fixture. mask is the set of levels that would flow from that missing parent down to the resource: the inherit of the
// link itself, intersected with the route from the resource that declares the link down to the resource asked about.
type openLink struct {
	missing string // the type of the missing parent
	mask    levelSet
}

// graph is the type graph and the resource graph of a fixture, with the lineage computed from the declared links.
// Everything is derived by walking the links; nothing is precomputed from outside.
type graph struct {
	types     map[string]*policyfix.Type
	resources map[resKey]*policyfix.Resource
	byType    map[string][]*policyfix.Resource
	groups    map[string]map[string]levelSet // type -> access group -> levels

	typeUp   map[string]map[string]levelSet  // memo of typeAncestors
	resUp    map[resKey]map[resKey]levelSet  // memo of ancestors
	open     map[resKey][]openLink           // memo of openAbove
	implies  map[string]map[string]levelSet  // memo of includes: type -> level -> what it includes
	meta     map[resKey]map[string]metaValue // memo of metaOf
	visiting map[any]bool                    // guards the walks against cycles a fixture could declare
}

func newGraph(f *policyfix.Fixture) *graph {
	g := &graph{
		types:     map[string]*policyfix.Type{},
		resources: map[resKey]*policyfix.Resource{},
		byType:    map[string][]*policyfix.Resource{},
		groups:    map[string]map[string]levelSet{},
		typeUp:    map[string]map[string]levelSet{},
		resUp:     map[resKey]map[resKey]levelSet{},
		open:      map[resKey][]openLink{},
		implies:   map[string]map[string]levelSet{},
		meta:      map[resKey]map[string]metaValue{},
		visiting:  map[any]bool{},
	}
	for i := range f.Types {
		g.types[f.Types[i].Name] = &f.Types[i]
	}
	for i := range f.Resources {
		r := &f.Resources[i]
		g.resources[resKey{r.Type, r.ID}] = r
		g.byType[r.Type] = append(g.byType[r.Type], r)
	}
	for _, ag := range f.AccessGroups {
		if g.groups[ag.Type] == nil {
			g.groups[ag.Type] = map[string]levelSet{}
		}
		g.groups[ag.Type][ag.Name] = levelsOf(ag.Levels...)
	}
	return g
}

// link returns the inherit mask of the link from a type to one of its parent types. A pair of types that is not linked
// lets nothing through (a fixture that passes Validate never declares such a resource link).
func (g *graph) link(child, parent string) levelSet {
	t := g.types[child]
	if t == nil {
		return noLevel
	}
	for _, p := range t.Parents {
		if p.Type == parent {
			return inheritMask(p.Inherit)
		}
	}
	return noLevel
}

// typeAncestors returns the proper ancestor types of a type, each with the levels that flow from it down to the type:
// the union over the routes of the intersection of the inherit masks along each route. An ancestor that nothing flows
// from is still listed, with an empty set.
func (g *graph) typeAncestors(typ string) map[string]levelSet {
	if up, ok := g.typeUp[typ]; ok {
		return up
	}
	key := "type:" + typ
	if g.visiting[key] {
		return nil // a cycle of types: the route that closes it is cut
	}
	g.visiting[key] = true
	defer delete(g.visiting, key)

	up := map[string]levelSet{}
	if t := g.types[typ]; t != nil {
		for _, p := range t.Parents {
			mask := inheritMask(p.Inherit)
			merge(up, p.Type, mask)
			for anc, flow := range g.typeAncestors(p.Type) {
				merge(up, anc, flow.and(mask))
			}
		}
	}
	delete(up, typ)
	g.typeUp[typ] = up
	return up
}

// typeFlow returns the levels that flow from a type down to another: every level onto itself, the union over the routes
// for a proper ancestor, and ok false when from is not above to.
func (g *graph) typeFlow(from, to string) (levelSet, bool) {
	if from == to {
		return everyLevel, true
	}
	flow, ok := g.typeAncestors(to)[from]
	return flow, ok
}

// resolved returns the parent a resource link names when it is a resource of the fixture.
func (g *graph) resolved(p policyfix.ResourceParent) (resKey, bool) {
	k := resKey{p.Type, p.ID}
	_, ok := g.resources[k]
	return k, ok
}

// ancestors returns the proper ancestors of a resource through its resolved links, each with the levels that flow from
// it down to the resource: the union over the routes of the intersection of the inherit masks of the type links along
// each route. Only links whose parent is a resource of the fixture give routes.
func (g *graph) ancestors(r resKey) map[resKey]levelSet {
	if up, ok := g.resUp[r]; ok {
		return up
	}
	if g.visiting[r] {
		return nil
	}
	g.visiting[r] = true
	defer delete(g.visiting, r)

	up := map[resKey]levelSet{}
	if res := g.resources[r]; res != nil {
		for _, p := range res.Parents {
			pk, ok := g.resolved(p)
			if !ok {
				continue
			}
			mask := g.link(r.typ, p.Type)
			merge(up, pk, mask)
			for anc, flow := range g.ancestors(pk) {
				merge(up, anc, flow.and(mask))
			}
		}
	}
	delete(up, r)
	g.resUp[r] = up
	return up
}

// resourceFlow returns the levels that flow from a resource down to another: every level onto itself, the union over
// the routes for a proper ancestor, and ok false when from is not above to.
func (g *graph) resourceFlow(from, to resKey) (levelSet, bool) {
	if from == to {
		return everyLevel, true
	}
	flow, ok := g.ancestors(to)[from]
	return flow, ok
}

// openAbove returns the unresolved links of a resource and of every one of its ancestors, each with the levels that
// would flow from the missing parent down to the resource.
func (g *graph) openAbove(r resKey) []openLink {
	if links, ok := g.open[r]; ok {
		return links
	}
	var links []openLink
	declare := func(n resKey, route levelSet) {
		res := g.resources[n]
		if res == nil {
			return
		}
		for _, p := range res.Parents {
			if _, ok := g.resolved(p); ok {
				continue
			}
			links = append(links, openLink{missing: p.Type, mask: g.link(n.typ, p.Type).and(route)})
		}
	}
	declare(r, everyLevel)
	for anc, route := range g.ancestors(r) {
		declare(anc, route)
	}
	g.open[r] = links
	return links
}

// includes returns the levels a level includes on a type (itself among them), transitively through access_implies:
// granting it grants all of them, and denying any of them denies it.
func (g *graph) includes(typ, level string) levelSet {
	if g.implies[typ] == nil {
		g.implies[typ] = map[string]levelSet{}
	}
	if set, ok := g.implies[typ][level]; ok {
		return set
	}
	seen := map[string]bool{level: true}
	queue := []string{level}
	t := g.types[typ]
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if t == nil {
			continue
		}
		for _, implied := range t.Implies[next] {
			if !seen[implied] {
				seen[implied] = true
				queue = append(queue, implied)
			}
		}
	}
	set := levelSet{set: seen}
	g.implies[typ][level] = set
	return set
}

// accessLevels expands the access list of a rule or of a role item on a type: '*' is every level, '@name' the levels
// of the access group of the type (nothing when the type has no such group), anything else the level itself.
func (g *graph) accessLevels(typ string, access []string) levelSet {
	out := noLevel
	for _, a := range access {
		if a == model.AllLevels {
			return everyLevel
		}
		if name, ok := strings.CutPrefix(a, model.AccessGroupPrefix); ok && name != "" {
			if levels, ok := g.groups[typ][name]; ok {
				out = out.or(levels)
			}
			continue
		}
		out = out.or(levelsOf(a))
	}
	return out
}

// merge adds levels to the entry of a key.
func merge[K comparable](m map[K]levelSet, k K, levels levelSet) {
	if old, ok := m[k]; ok {
		m[k] = old.or(levels)
		return
	}
	m[k] = levels
}
