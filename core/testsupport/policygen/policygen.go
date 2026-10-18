// Package policygen writes random policy fixtures (policyfix.Fixture) for the property test of the decision contracts
// (DECP in contracts.yaml): the engine and the reference evaluator (core/testsupport/refeval) decide the questions of the
// same generated fixture and must give the same answers. Generate builds one fixture from a seed; the same seed always
// gives the same fixture, so a failure is reproduced from its seed alone, and a fixture written out with encoding/json
// reads back through policyfix.Parse unchanged.
//
// A fixture uses every feature of the format, so that the property covers the whole of the decision semantics rather
// than the cases someone thought of:
//
//   - two services, and 4-7 resource types forming a DAG across them: several parents, inherit masks on some links (an
//     absent mask on others), access_implies on some types, one or two types without a synchronizer (not_sync), now and
//     then above types that have one;
//   - 15-40 resources with one or more parents (two of the same type now and then), resources whose source reports no
//     parent, parents that do not exist (unresolved links, at least one of them above other resources), duplicate and
//     missing names, and attributes (strings and lists) that resource_where and resource_from_claim read;
//   - access groups, groups whose members are subjects, predicates over the claims, the request context and the clock
//     (every operator), everybody, other groups (nested a few levels deep, never in a cycle), with disabled memberships
//     and validity windows around the fixture's now;
//   - roles whose items take every kind of target, and 20-60 rules of every kind: to a subject, to a group as a subject,
//     to the members of a group, by a predicate, to everybody; inline or by a role; allow, deny (with '*' too) and
//     exceptions; on_missing skip and apply; guards; disabled rules and validity windows; targets on the whole type, on
//     a resource (one that does not exist too), from a claim with every match_by, and by resource attributes;
//   - 6-12 cases asked of both services by the subjects the rules name, with claims and request attributes that are
//     sometimes missing (or null), at the fixture's now, at the boundaries of the validity windows, just beside them, or
//     hours away; each with 10-30 questions over existing and missing resources, whole types, types without a
//     synchronizer, the types of the other service and a type that does not exist.
//
// The expected answers are not known: every question has Allowed false and no other expectation, because the property
// compares two deciders and reads neither. The distributions are chosen so that allowed, denied and no_access answers are
// all common (policygen_test.go measures them with the reference evaluator).
//
// Everything the generator writes is also accepted by the admin API, so the engine runner of the admin API tests
// (admin-api/tests/integration/contracts) can load it: an allow rule never applies on a missing attribute (on_missing
// apply is for denials), '*' is only in inline denials, a condition on the clock is never a regex or in_cidr, the values
// of ne, regex and the numeric operators are strings, a resource names parents only of the parent types that have a
// synchronizer, and the windows end after they start.
package policygen

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// Contract is the contract the generated fixtures check.
const Contract = "DECP"

// The vocabulary of the generated fixtures. The first letters of the type names differ, so the first letter of a type,
// upper-cased, prefixes the ids of its resources.
var (
	serviceSlugs = []string{"org", "app", "docs", "shop", "crm", "hr"}
	typeNames    = []string{"division", "area", "branch", "project", "folder", "item", "team", "space", "catalog", "ledger", "workflow", "zone"}
	groupNames   = []string{"admins", "sales", "engineers", "auditors", "managers", "night-shift", "contractors"}
	roleNames    = []string{"viewer", "editor", "owner", "approver"}

	// levels are the access levels the rules grant and the questions ask; rareLevel is granted and asked now and then,
	// and is the level an access group or a '*' denial covers where nothing else names it.
	levels    = []string{"read", "write", "admin"}
	rareLevel = "approve"

	// subjects are the people the cases ask as and the rules name; outsider is named by some rules and never asks.
	subjects = []string{"alice", "bob", "carol", "dave", "erin"}
	outsider = "mallory"

	departments = []string{"sales", "eng", "ops"}
	regions     = []string{"eu", "us", "asia"}
	tags        = []string{"red", "green", "blue", "gold"}
	channels    = []string{"web", "mobile", "api"}
	addresses   = []string{"10.1.2.3", "10.200.0.7", "192.168.1.20", "8.8.8.8", "2001:db8::1"}
	networks    = []string{"10.0.0.0/8", "192.168.0.0/16", "2001:db8::/32", "8.8.8.0/24"}

	// spans are the distances of the validity boundaries from the fixture's now.
	spans = []time.Duration{time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour, 24 * time.Hour}
	// nudges move a case off a boundary, just before or just after it.
	nudges = []time.Duration{-time.Minute, -time.Second, time.Second, time.Minute}

	// operators are the condition operators; a condition picks one first, so that each is as common as the others.
	operators = []string{
		model.OpEq, model.OpNe, model.OpIn, model.OpNotIn, model.OpRegex,
		model.OpGt, model.OpGe, model.OpLt, model.OpLe, model.OpInCIDR,
	}
)

// accessGroupDefs are the access groups a type may have.
var accessGroupDefs = []struct {
	name   string
	levels []string
}{
	{"viewer", []string{"read"}},
	{"editor", []string{"read", "write"}},
	{"manager", []string{"read", "write", "admin"}},
	{"approver", []string{"approve", "read"}},
}

// attrKind is what the values of an attribute look like, which decides the operators that are natural for it.
type attrKind int

const (
	categorical attrKind = iota
	numeric
	boolean
	address
	listValued
)

// attribute is something a condition reads: a claim, a request attribute or the clock (requestAttributes), or a resource
// attribute of a resource_where (resourceAttributes, with no source).
type attribute struct {
	source   string // model.SourceClaim, model.SourceContext, model.SourceNow; empty for a resource attribute
	name     string
	kind     attrKind
	values   []string // the values a condition compares the attribute with
	patterns []string // the regular expressions it is matched against
}

// requestAttributes are what the predicates and the guards read. The values include some that no case carries ("legal",
// "mars"), so that a condition can be known and false.
var requestAttributes = []attribute{
	{model.SourceClaim, "department", categorical, []string{"sales", "eng", "ops", "legal"}, []string{"^s", "^(eng|ops)$", "e", "(?i)^SALES$"}},
	{model.SourceClaim, "region", categorical, []string{"eu", "us", "asia", "mars"}, []string{"^(eu|us)$", "a"}},
	{model.SourceClaim, "preferred_username", categorical, subjects, []string{"^[a-c]", "e$", "^(alice|dave)$"}},
	{model.SourceClaim, "level", numeric, []string{"1", "2", "2.5", "3", "4", "5"}, []string{"^[1-3]"}},
	{model.SourceClaim, "vip", boolean, []string{"True", "False"}, []string{"^T"}},
	{model.SourceClaim, "teams", listValued, tags, []string{"red", "gold|blue"}},
	{model.SourceContext, "ip", address, addresses, []string{`^10\.`, ":"}},
	{model.SourceContext, "channel", categorical, channels, []string{"^(web|api)$", "mob"}},
	{model.SourceContext, "risk", numeric, []string{"0", "3", "5", "7.5", "9"}, []string{"^[0-4]"}},
	{model.SourceNow, "hour", numeric, []string{"0", "6", "9", "12", "17", "20", "23"}, nil},
	{model.SourceNow, "weekday", numeric, []string{"1", "2", "3", "4", "5", "6", "7"}, nil},
}

// resourceAttributes are the attributes the resources carry (see gen.meta) and a resource_where reads.
var resourceAttributes = []attribute{
	{"", "owner", categorical, []string{"alice", "bob", "carol", "dave", "erin", "mallory"}, []string{"^[a-c]", "^(bob|erin)$"}},
	{"", "tags", listValued, tags, []string{"^(red|gold)$", "e"}},
	{"", "region", categorical, regions, []string{"^(eu|us)$", "a"}},
	{"", "level", numeric, []string{"1", "2", "3", "4", "5"}, []string{"^[1-2]$"}},
	{"", "addr", address, addresses, []string{`^10\.`}},
}

// gen is the state of one generation: the fixture so far, and what the later steps pick from.
type gen struct {
	rng *rand.Rand
	f   *policyfix.Fixture
	now time.Time
	// bounds are the validity boundaries the fixture has; some cases are asked at them, or just beside them.
	bounds []time.Time

	typeNames []string            // the types in the order of the fixture: parents first
	sync      map[string]bool     // the types with a synchronizer
	service   map[string]string   // type -> its service
	children  map[string][]string // type -> its child types, in the order of the fixture
	ids       map[string][]string // sync type -> the ids of its resources
	names     map[string][]string // sync type -> the names of its resources
	missing   map[string][]string // type -> the ids its resources name as parents that no resource has
	agroups   map[string][]string // type -> the names of its access groups
	groups    []string
	roles     []string
}

// Generate returns the fixture of a seed: valid (Fixture.Validate passes), accepted by the admin API, and the same for
// the same seed. Its expectations are not known: every question has Allowed false and nothing else.
func Generate(seed int64) *policyfix.Fixture {
	g := &gen{
		// The bits of the seed, a negative one included: any int64 names one fixture.
		rng:      rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15)), //nolint:gosec // a fixture of a test: no secret depends on it
		sync:     map[string]bool{},
		service:  map[string]string{},
		children: map[string][]string{},
		ids:      map[string][]string{},
		names:    map[string][]string{},
		missing:  map[string][]string{},
		agroups:  map[string][]string{},
	}
	// The fixture's now moves over a week, so that the conditions on the hour and the weekday are true for some seeds and
	// false for others.
	g.now = policyfix.DefaultNow.Add(time.Duration(g.rng.IntN(7*24)) * time.Hour)
	now := g.now
	g.f = &policyfix.Fixture{
		Name:        fmt.Sprintf("generated-%d", seed),
		Contracts:   []string{Contract},
		Description: fmt.Sprintf("Generated by policygen from seed %d.", seed),
		Now:         &now,
	}
	g.typesAndServices()
	g.resources()
	g.accessGroups()
	g.groupsAndMembers()
	g.rolesAndItems()
	g.rules()
	g.cases()
	return g.f
}

// ---------- random helpers ----------

// between returns a number from lo to hi, both included.
func (g *gen) between(lo, hi int) int { return lo + g.rng.IntN(hi-lo+1) }

// chance is true with the given probability in percent.
func (g *gen) chance(percent int) bool { return g.rng.IntN(100) < percent }

// pick returns one of the options.
func pick[T any](g *gen, options []T) T { return options[g.rng.IntN(len(options))] }

// subset returns from lo to hi distinct options (fewer when there are not so many), in their order.
func (g *gen) subset(options []string, lo, hi int) []string {
	hi = min(hi, len(options))
	n := g.between(min(lo, hi), hi)
	idx := g.rng.Perm(len(options))[:n]
	slices.Sort(idx)
	out := make([]string, n)
	for i, j := range idx {
		out[i] = options[j]
	}
	return out
}

// anyList turns strings into the list a claim, a request attribute or a resource attribute holds once read from YAML.
func anyList(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

// prefix is the prefix of the ids of the resources of a type.
func prefix(typ string) string { return strings.ToUpper(typ[:1]) }

// title is the name of a type as the names of its resources start.
func title(typ string) string { return strings.ToUpper(typ[:1]) + typ[1:] }

// ---------- types and services ----------

// typesAndServices writes the services and the types: a DAG in which every type takes its parents among the types before
// it, so the order of the fixture is a topological one.
func (g *gen) typesAndServices() {
	slugs := g.rng.Perm(len(serviceSlugs))
	g.f.Services = []string{serviceSlugs[slugs[0]], serviceSlugs[slugs[1]]}

	k := g.between(4, 7)
	order := g.rng.Perm(len(typeNames))[:k]
	types := make([]policyfix.Type, k)
	secondRoot := false
	for i := range types {
		t := policyfix.Type{Name: typeNames[order[i]]}
		if i > 0 {
			n := 1
			switch x := g.rng.IntN(100); {
			case x < 12 && i >= 2 && !secondRoot:
				// Another root: two hierarchies meet further down.
				n, secondRoot = 0, true
			case x < 42:
				n = 2
			case x < 50:
				n = 3
			}
			for _, p := range g.parentIndexes(i, min(n, i)) {
				t.Parents = append(t.Parents, policyfix.TypeParent{Type: types[p].Name, Inherit: g.inherit()})
			}
		}
		if g.chance(40) {
			t.Implies = g.implies()
		}
		types[i] = t
	}
	g.completeLinks(types)

	for i := range types {
		g.typeNames = append(g.typeNames, types[i].Name)
		for _, p := range types[i].Parents {
			g.children[p.Type] = append(g.children[p.Type], types[i].Name)
		}
	}
	// One or two types have no synchronizer, the first now and then above types that have one: a whole-type rule on it
	// cascades to their resources.
	notSync := map[int]bool{}
	var inner []int
	for i := range types {
		if len(g.children[types[i].Name]) > 0 {
			inner = append(inner, i)
		}
	}
	if len(inner) > 0 && g.chance(60) {
		notSync[pick(g, inner)] = true
	} else {
		notSync[g.rng.IntN(k)] = true
	}
	if g.chance(35) {
		notSync[g.rng.IntN(k)] = true // perhaps the same one: one type then
	}
	var syncIdx []int
	for i := range types {
		types[i].Sync = !notSync[i]
		g.sync[types[i].Name] = types[i].Sync
		if types[i].Sync {
			syncIdx = append(syncIdx, i)
		}
	}
	// Each service has a type with a synchronizer, so a case asked of either has resources to ask about; the others go to
	// either service, so the links cross services.
	first := g.rng.Perm(len(syncIdx))
	for i := range types {
		switch i {
		case syncIdx[first[0]]:
			types[i].Service = g.f.Services[0]
		case syncIdx[first[1]]:
			types[i].Service = g.f.Services[1]
		default:
			types[i].Service = pick(g, g.f.Services)
		}
		g.service[types[i].Name] = types[i].Service
	}
	g.f.Types = types
}

// parentIndexes picks n distinct parents of type i among the types before it, the one just before it most often, so that
// the hierarchies run several levels deep.
func (g *gen) parentIndexes(i, n int) []int {
	var out []int
	for len(out) < n {
		p := i - 1
		if len(out) > 0 || !g.chance(60) {
			p = g.rng.IntN(i)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// completeLinks makes sure the type graph has a type with several parents, a link with an inherit mask and a link
// without one.
func (g *gen) completeLinks(types []policyfix.Type) {
	last := &types[len(types)-1]
	if !slices.ContainsFunc(types, func(t policyfix.Type) bool { return len(t.Parents) > 1 }) {
		for _, p := range g.rng.Perm(len(types) - 1) {
			if !slices.ContainsFunc(last.Parents, func(tp policyfix.TypeParent) bool { return tp.Type == types[p].Name }) {
				last.Parents = append(last.Parents, policyfix.TypeParent{Type: types[p].Name, Inherit: g.inherit()})
				break
			}
		}
	}
	var links []*policyfix.TypeParent
	for i := range types {
		for j := range types[i].Parents {
			links = append(links, &types[i].Parents[j])
		}
	}
	masked := slices.IndexFunc(links, func(l *policyfix.TypeParent) bool { return l.Inherit != nil })
	if masked < 0 {
		masked = g.rng.IntN(len(links))
		links[masked].Inherit = []string{"read"}
	}
	if !slices.ContainsFunc(links, func(l *policyfix.TypeParent) bool { return l.Inherit == nil }) {
		open := (masked + 1 + g.rng.IntN(len(links)-1)) % len(links)
		links[open].Inherit = nil
	}
}

// inherit returns the inherit mask of a type link: none (every level flows) half of the time, else a sorted, non-empty
// set of levels (an empty one would read back as none).
func (g *gen) inherit() []string {
	switch x := g.rng.IntN(100); {
	case x < 50:
		return nil
	case x < 72:
		return []string{"read"}
	case x < 84:
		return []string{"read", "write"}
	case x < 91:
		return []string{"write"}
	case x < 96:
		return []string{"admin", "read"}
	}
	return []string{"approve", "read"}
}

// implies returns an access_implies of a type, with no cycle.
func (g *gen) implies() map[string][]string {
	switch g.rng.IntN(5) {
	case 0:
		return map[string][]string{"write": {"read"}}
	case 1:
		return map[string][]string{"admin": {"write"}, "write": {"read"}}
	case 2:
		return map[string][]string{"admin": {"read", "write"}}
	case 3:
		return map[string][]string{"approve": {"read"}}
	}
	return map[string][]string{"write": {"read"}, "approve": {"write"}}
}

// syncParents returns the parent types of a type that have a synchronizer: the only ones a resource can name as parents.
func (g *gen) syncParents(typ string) []string {
	t, _ := g.f.TypeByName(typ)
	var out []string
	for _, p := range t.Parents {
		if g.sync[p.Type] {
			out = append(out, p.Type)
		}
	}
	return out
}

// syncBelow returns the types with a synchronizer strictly below a type, in the order of the fixture.
func (g *gen) syncBelow(typ string) []string {
	below := map[string]bool{}
	queue := []string{typ}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range g.children[next] {
			if !below[c] {
				below[c] = true
				queue = append(queue, c)
			}
		}
	}
	var out []string
	for _, name := range g.typeNames {
		if below[name] && g.sync[name] {
			out = append(out, name)
		}
	}
	return out
}

// typesOf returns the types of a service, in the order of the fixture; with syncOnly, only those with a synchronizer.
func (g *gen) typesOf(service string, syncOnly bool) []string {
	var out []string
	for _, name := range g.typeNames {
		if g.service[name] == service && (!syncOnly || g.sync[name]) {
			out = append(out, name)
		}
	}
	return out
}

// ---------- resources ----------

// resources writes 15-40 resources over the types with a synchronizer, each type one at least and the lower types more,
// with their parents, names and attributes.
func (g *gen) resources() {
	depth := map[string]int{}
	var syncTypes []string
	for i := range g.f.Types {
		t := &g.f.Types[i]
		for _, p := range t.Parents {
			depth[t.Name] = max(depth[t.Name], depth[p.Type]+1)
		}
		if t.Sync {
			syncTypes = append(syncTypes, t.Name)
		}
	}
	count := map[string]int{}
	var weighted []string // a type appears once per level of depth, plus once
	for _, typ := range syncTypes {
		count[typ] = 1
		for range depth[typ] + 1 {
			weighted = append(weighted, typ)
		}
	}
	for range g.between(15, 40) - len(syncTypes) {
		count[pick(g, weighted)]++
	}

	for _, typ := range syncTypes {
		parents := g.syncParents(typ)
		for j := 1; j <= count[typ]; j++ {
			r := policyfix.Resource{Type: typ, ID: prefix(typ) + strconv.Itoa(j)}
			switch x := g.rng.IntN(100); {
			case x < 12:
				// No name: the id is the name.
			case x < 24 && len(g.names[typ]) > 0:
				r.Name = pick(g, g.names[typ]) // a name two resources share
			default:
				r.Name = title(typ) + " " + strconv.Itoa(j)
			}
			r.Parents = g.resourceParents(parents)
			r.Meta = g.meta()
			g.ids[typ] = append(g.ids[typ], r.ID)
			g.names[typ] = append(g.names[typ], r.NameOf())
			g.f.Resources = append(g.f.Resources, r)
		}
	}
	g.unresolvedAbove()
}

// resourceParents picks the parents of a resource among the resources of its parent types (written before it): one per
// parent type most of the time, sometimes none, two of the same type now and then, and now and then one that no
// resource is (an unresolved link).
func (g *gen) resourceParents(parentTypes []string) []policyfix.ResourceParent {
	link := 88
	if len(parentTypes) > 1 {
		link = 65
	}
	var out []policyfix.ResourceParent
	for _, pt := range parentTypes {
		if !g.chance(link) {
			continue
		}
		if g.chance(8) {
			out = append(out, policyfix.ResourceParent{Type: pt, ID: g.newMissing(pt)})
			continue
		}
		ids := g.ids[pt]
		first := pick(g, ids)
		out = append(out, policyfix.ResourceParent{Type: pt, ID: first})
		if len(ids) > 1 && g.chance(7) {
			second := pick(g, ids)
			if second != first {
				out = append(out, policyfix.ResourceParent{Type: pt, ID: second})
			}
		}
	}
	return out
}

// newMissing returns an id of a type that no resource has, to name as a parent; the rules aim at such ids too.
func (g *gen) newMissing(typ string) string {
	id := prefix(typ) + strconv.Itoa(90+len(g.missing[typ]))
	g.missing[typ] = append(g.missing[typ], id)
	return id
}

// unresolvedAbove makes sure an unresolved link lies above other resources: a resource that is the parent of another,
// and whose type has a parent type with a synchronizer, names a parent that no resource is. Without such a resource, any
// resource whose type has such a parent type does.
func (g *gen) unresolvedAbove() {
	isParent := map[string]bool{}
	for _, r := range g.f.Resources {
		for _, p := range r.Parents {
			isParent[p.Type+"/"+p.ID] = true
		}
	}
	unresolved := func(r *policyfix.Resource) bool {
		return slices.ContainsFunc(r.Parents, func(p policyfix.ResourceParent) bool { return slices.Contains(g.missing[p.Type], p.ID) })
	}
	var above, anywhere []int
	for i := range g.f.Resources {
		r := &g.f.Resources[i]
		if len(g.syncParents(r.Type)) == 0 {
			continue
		}
		if isParent[r.Type+"/"+r.ID] {
			if unresolved(r) {
				return
			}
			above = append(above, i)
		}
		anywhere = append(anywhere, i)
	}
	candidates := above
	if len(candidates) == 0 {
		candidates = anywhere
	}
	if len(candidates) == 0 {
		return // no type with a synchronizer has a parent type with one
	}
	r := &g.f.Resources[pick(g, candidates)]
	pt := pick(g, g.syncParents(r.Type))
	r.Parents = append(r.Parents, policyfix.ResourceParent{Type: pt, ID: g.newMissing(pt)})
}

// meta returns the attributes of a resource (see resourceAttributes): scalars and lists of strings, each of them
// missing on some resources.
func (g *gen) meta() map[string]any {
	m := map[string]any{}
	switch x := g.rng.IntN(100); {
	case x < 55:
		m["owner"] = pick(g, subjects)
	case x < 70:
		m["owner"] = anyList(g.subset(subjects, 2, 2))
	}
	switch x := g.rng.IntN(100); {
	case x < 50:
		m["tags"] = anyList(g.subset(tags, 1, 3))
	case x < 65:
		m["tags"] = pick(g, tags)
	}
	if g.chance(60) {
		m["region"] = pick(g, regions)
	}
	if g.chance(50) {
		m["level"] = strconv.Itoa(g.between(1, 5))
	}
	if g.chance(30) {
		m["addr"] = pick(g, addresses)
	}
	if len(m) == 0 {
		return nil // no attributes, as a fixture read back has them
	}
	return m
}

// ---------- access groups, groups, roles ----------

// accessGroups gives one to three types (with a synchronizer or not) an access group each, one of them perhaps two.
func (g *gen) accessGroups() {
	for range g.between(1, 3) {
		typ := pick(g, g.typeNames)
		def := pick(g, accessGroupDefs)
		if slices.Contains(g.agroups[typ], def.name) {
			continue
		}
		g.agroups[typ] = append(g.agroups[typ], def.name)
		g.f.AccessGroups = append(g.f.AccessGroups, policyfix.AccessGroup{Type: typ, Name: def.name, Levels: slices.Clone(def.levels)})
	}
}

// groupsAndMembers writes 4-7 groups with one to four memberships each. A group nests only groups written before it, so
// nesting has no cycle; the last group nests the one before it, which often nests another, so it runs a few levels deep.
func (g *gen) groupsAndMembers() {
	order := g.rng.Perm(len(groupNames))[:g.between(4, 7)]
	for i, n := range order {
		name := groupNames[n]
		group := policyfix.Group{Name: name}
		nested := map[string]bool{}
		for range g.between(1, 4) {
			m := g.member(i, nested)
			group.Members = append(group.Members, m)
		}
		if i == len(order)-1 && !nested[g.groups[i-1]] {
			group.Members = append(group.Members, policyfix.Member{Group: g.groups[i-1]})
		}
		g.groups = append(g.groups, name)
		g.f.Groups = append(g.f.Groups, group)
	}
}

// member returns a membership of the i-th group: a subject, a predicate, everybody or a group before it (each nested
// once), now and then disabled or within a validity window.
func (g *gen) member(i int, nested map[string]bool) policyfix.Member {
	var m policyfix.Member
	switch x := g.rng.IntN(100); {
	case x < 30:
		m.Subject = pick(g, subjects)
	case x < 64:
		m.Predicate = g.predicate()
	case x < 70:
		m.Everybody = true
	case i > 0:
		// The group just before it half of the time: chains of nesting.
		child := g.groups[i-1]
		if g.chance(50) {
			child = g.groups[g.rng.IntN(i)]
		}
		if nested[child] {
			m.Subject = pick(g, subjects)
			break
		}
		nested[child] = true
		m.Group = child
	default:
		m.Subject = pick(g, subjects)
	}
	if g.chance(8) {
		m.Disabled = true
	}
	if g.chance(15) {
		m.ValidFrom, m.ExpiresAt = g.window()
	}
	return m
}

// rolesAndItems writes two to four roles with one to four items each, of every kind of target.
func (g *gen) rolesAndItems() {
	for _, name := range roleNames[:g.between(2, 4)] {
		role := policyfix.Role{Name: name}
		for range g.between(1, 4) {
			t := g.target()
			role.Items = append(role.Items, policyfix.RoleItem{Target: t, Access: g.access(t.Type, false)})
		}
		g.roles = append(g.roles, name)
		g.f.Roles = append(g.f.Roles, role)
	}
}

// ---------- rules ----------

// rules writes 20-60 rules.
func (g *gen) rules() {
	for i := range g.between(20, 60) {
		g.f.Rules = append(g.f.Rules, g.rule(i))
	}
}

// rule returns the i-th rule: to whom, which effect, what, and the options, each of every kind. Its name says what it
// is, for the reports of the property test.
func (g *gen) rule(i int) policyfix.Rule {
	var r policyfix.Rule
	switch x := g.rng.IntN(100); {
	case x < 38:
		r.Subject = pick(g, subjects)
		if g.chance(8) {
			r.Subject = outsider
		}
	case x < 50:
		r.SubjectGroup = pick(g, g.groups)
	case x < 66:
		r.MemberOf = pick(g, g.groups)
	case x < 76:
		r.Everybody = true
	default:
		r.Predicate = g.predicate()
	}
	effect := "allow"
	switch x := g.rng.IntN(100); {
	case x < 21:
		r.Deny, effect = true, "deny"
	case x < 35:
		r.Exception, effect = true, "except"
	}
	var what string
	if g.chance(15) {
		r.Role = pick(g, g.roles)
		what = "role-" + r.Role
	} else {
		t := g.target()
		r.Target = &t
		r.Access = g.access(t.Type, r.Deny)
		what = t.Type
	}
	switch {
	case r.Deny:
		switch x := g.rng.IntN(100); {
		case x < 30:
			r.OnMissing = model.OnMissingSkip
		case x < 55:
			r.OnMissing = model.OnMissingApply
		}
	case g.chance(10):
		r.OnMissing = model.OnMissingSkip // what an allow rule does anyway
	}
	if g.chance(12) {
		r.RequiresPredicate = []model.PredicateCondition{g.condition(requestAttributes)}
	}
	if g.chance(9) {
		r.RequiresGroup = pick(g, g.groups)
	}
	if g.chance(6) {
		r.Disabled = true
	}
	if g.chance(22) {
		r.ValidFrom, r.ExpiresAt = g.window()
	}
	r.Name = fmt.Sprintf("r%02d-%s-%s", i, effect, what)
	return r
}

// target returns what a rule or a role item covers: the whole of a type (always for a type without a synchronizer), or,
// on a type with one, a resource (now and then one that does not exist), the resource found from a claim, or the
// resources whose attributes match.
func (g *gen) target() policyfix.Target {
	t := policyfix.Target{Type: pick(g, g.typeNames)}
	if !g.sync[t.Type] {
		return t
	}
	switch x := g.rng.IntN(100); {
	case x < 35:
	case x < 68:
		t.Resource = g.resourceID(t.Type)
	case x < 84:
		t.FromClaim = g.fromClaim(t.Type)
	default:
		t.Where = g.where()
	}
	return t
}

// resourceID returns the id of a resource of a type, or now and then one that no resource has: most often a parent some
// resource names, so the rule is anchored where an unresolved link points.
func (g *gen) resourceID(typ string) string {
	if g.chance(8) {
		if len(g.missing[typ]) > 0 && g.chance(70) {
			return pick(g, g.missing[typ])
		}
		return prefix(typ) + "99"
	}
	return pick(g, g.ids[typ])
}

// fromClaim returns a resource_from_claim on a type: the claim is looked up among the resources of the type itself (the
// owner pattern), of a type below it (the rule climbs up), or of any type, by name (match_by given or left out), id or
// an attribute. The cases carry claims that match some resources and claims that match none (see gen.claims).
func (g *gen) fromClaim(typ string) *policyfix.FromClaim {
	fc := &policyfix.FromClaim{LookupType: typ}
	switch x := g.rng.IntN(100); {
	case x < 50:
		if below := g.syncBelow(typ); len(below) > 0 {
			fc.LookupType = pick(g, below)
		}
	case x < 58:
		fc.LookupType = pick(g, g.typeNames)
	}
	switch x := g.rng.IntN(100); {
	case x < 15:
		fc.Claim = "home" // by name, the default
	case x < 40:
		fc.Claim, fc.MatchBy = "home", model.MatchByName
	case x < 62:
		fc.Claim, fc.MatchBy = "home_id", model.MatchByID
	case x < 80:
		fc.Claim, fc.MatchBy = "preferred_username", model.MatchByMetaPrefix+"owner"
	case x < 90:
		fc.Claim, fc.MatchBy = "team", model.MatchByMetaPrefix+"tags"
	default:
		fc.Claim, fc.MatchBy = "region", model.MatchByMetaPrefix+"region"
	}
	return fc
}

// where returns the conditions of a resource_where: one, or now and then two, over the resource attributes.
func (g *gen) where() []model.PredicateCondition {
	out := []model.PredicateCondition{g.condition(resourceAttributes)}
	if g.chance(25) {
		out = append(out, g.condition(resourceAttributes))
	}
	return out
}

// access returns the access list of a permission on a type: levels, an access group of the type (with a level now and
// then), or, for a denial, '*' (never in a role item, which is never a denial here).
func (g *gen) access(typ string, deny bool) []string {
	if deny && g.chance(30) {
		return []string{model.AllLevels}
	}
	if groups := g.agroups[typ]; len(groups) > 0 && g.chance(35) {
		out := []string{model.AccessGroupPrefix + pick(g, groups)}
		if g.chance(25) {
			out = append(out, pick(g, levels))
		}
		return out
	}
	switch x := g.rng.IntN(100); {
	case x < 35:
		return []string{"read"}
	case x < 55:
		return []string{"write"}
	case x < 65:
		return []string{"admin"}
	case x < 85:
		return []string{"read", "write"}
	case x < 95:
		return []string{"read", "write", "admin"}
	}
	return []string{rareLevel}
}

// ---------- conditions and windows ----------

// predicate returns the conditions of a predicate: one, two now and then, three rarely.
func (g *gen) predicate() []model.PredicateCondition {
	n := 1
	switch x := g.rng.IntN(100); {
	case x < 5:
		n = 3
	case x < 30:
		n = 2
	}
	out := make([]model.PredicateCondition, n)
	for i := range out {
		out[i] = g.condition(requestAttributes)
	}
	return out
}

// condition returns a condition over one of the attributes. The operator is picked first, so every operator is as common
// as the others; the attribute is one the operator is natural for (a number for gt, an address for in_cidr, ...), or
// rarely any attribute validation lets the operator read, so that a condition can be known and false for its type.
func (g *gen) condition(attrs []attribute) model.PredicateCondition {
	op := pick(g, operators)
	natural := g.chance(94)
	var candidates []attribute
	for _, a := range attrs {
		if allowed(op, a) && (!natural || isNatural(op, a)) {
			candidates = append(candidates, a)
		}
	}
	a := pick(g, candidates)
	c := model.PredicateCondition{Attr: a.name, Op: op, Value: g.value(op, a), Source: a.source}
	if a.source == model.SourceClaim && g.chance(75) {
		c.Source = "" // the default source
	}
	return c
}

// allowed reports whether validation lets an operator read an attribute: the clock is neither a regex nor an address.
func allowed(op string, a attribute) bool {
	return a.source != model.SourceNow || op != model.OpRegex && op != model.OpInCIDR
}

// isNatural reports whether an operator suits the values of an attribute.
func isNatural(op string, a attribute) bool {
	switch op {
	case model.OpRegex:
		return a.source != model.SourceNow && a.kind != numeric
	case model.OpGt, model.OpGe, model.OpLt, model.OpLe:
		return a.kind == numeric
	case model.OpInCIDR:
		return a.kind == address
	}
	return true
}

// value returns the value a condition compares with: a string where validation wants one (ne, regex, the numeric
// operators), a list for in, not_in and in_cidr (a string now and then, which reads as a list of one), and a list for eq
// rarely (it holds when the attribute equals one of its items).
func (g *gen) value(op string, a attribute) model.PredicateValue {
	scalar := func(s string) model.PredicateValue { return model.PredicateValue{Str: s} }
	list := func(l []string) model.PredicateValue { return model.PredicateValue{List: l, IsList: true} }
	switch op {
	case model.OpEq:
		if g.chance(5) {
			return list(g.subset(a.values, 2, 2))
		}
		return scalar(pick(g, a.values))
	case model.OpNe:
		return scalar(pick(g, a.values))
	case model.OpIn, model.OpNotIn:
		if g.chance(10) {
			return scalar(pick(g, a.values))
		}
		return list(g.subset(a.values, 1, 3))
	case model.OpRegex:
		return scalar(pick(g, a.patterns))
	case model.OpInCIDR:
		if g.chance(20) {
			return scalar(pick(g, networks))
		}
		return list(g.subset(networks, 1, 2))
	}
	if a.kind != numeric {
		return scalar(pick(g, []string{"2", "3"}))
	}
	return scalar(pick(g, a.values))
}

// window returns a validity window around the fixture's now: ended, not begun, in force, open on one side, or wholly in
// the past. Its boundaries are kept, so that some cases are asked at them.
func (g *gen) window() (from, to *time.Time) {
	at := func(d time.Duration) *time.Time {
		t := g.now.Add(d)
		g.bounds = append(g.bounds, t)
		return &t
	}
	d := pick(g, spans)
	switch g.rng.IntN(6) {
	case 0:
		return nil, at(-d)
	case 1:
		return at(d), nil
	case 2:
		return at(-d), at(pick(g, spans))
	case 3:
		return at(-d), nil
	case 4:
		return nil, at(d)
	}
	return at(-d - pick(g, spans)), at(-d)
}

// ---------- cases ----------

// cases writes 6-12 cases: the first asked of the first service, the second of the other one, the rest of either.
func (g *gen) cases() {
	for i := range g.between(6, 12) {
		svc := g.rng.IntN(2)
		if i < 2 {
			svc = i
		}
		c := policyfix.Case{Name: fmt.Sprintf("case-%02d", i), Subject: pick(g, subjects)}
		// The first service is the default; it is named now and then.
		if svc == 1 || g.chance(30) {
			c.Service = g.f.Services[svc]
		}
		c.Claims = g.claims(c.Subject)
		c.Context = g.context()
		c.At = g.at()
		c.Questions = g.questions(g.f.Services[svc])
		g.f.Cases = append(g.f.Cases, c)
	}
}

// claims returns the claims of a case. preferred_username is always the subject: it is what the token names the subject
// by, and what the runner of the engine sets. The other claims are each missing from some cases, and rarely null.
func (g *gen) claims(subject string) map[string]any {
	c := map[string]any{"preferred_username": subject}
	if g.chance(75) {
		c["department"] = pick(g, departments)
	} else if g.chance(10) {
		c["department"] = nil
	}
	if g.chance(70) {
		c["region"] = pick(g, regions)
	}
	switch x := g.rng.IntN(100); {
	case x < 55:
		c["level"] = float64(g.between(1, 5))
	case x < 62:
		c["level"] = 2.5
	case x < 72:
		c["level"] = "3" // a number as a string
	}
	if g.chance(45) {
		c["vip"] = g.chance(50)
	}
	if g.chance(40) {
		c["teams"] = anyList(g.subset(tags, 1, 2))
	}
	if g.chance(60) {
		c["team"] = pick(g, tags)
	}
	// The claims resource_from_claim looks up: a resource's name or id most of the time, one that no resource has now and
	// then.
	typ := g.syncTypeWithResources()
	if g.chance(65) {
		c["home"] = pick(g, g.names[typ])
		if g.chance(12) {
			c["home"] = "Nowhere"
		}
	}
	if g.chance(55) {
		c["home_id"] = pick(g, g.ids[typ])
		if g.chance(12) {
			c["home_id"] = "nowhere"
		}
	}
	return c
}

// syncTypeWithResources returns a type that has resources.
func (g *gen) syncTypeWithResources() string {
	var out []string
	for _, name := range g.typeNames {
		if len(g.ids[name]) > 0 {
			out = append(out, name)
		}
	}
	return pick(g, out)
}

// context returns the request attributes of a case: none for some cases, and each of them missing from some others.
func (g *gen) context() map[string]any {
	if g.chance(20) {
		return nil
	}
	c := map[string]any{}
	if g.chance(65) {
		c["ip"] = pick(g, addresses)
	}
	if g.chance(55) {
		c["channel"] = pick(g, channels)
	} else if g.chance(8) {
		c["channel"] = nil
	}
	if g.chance(50) {
		c["risk"] = float64(g.between(0, 9))
	}
	if len(c) == 0 {
		return nil // an empty context reads back as none
	}
	return c
}

// at returns the time a case is asked at: the fixture's now (left out), a validity boundary, just beside one, or hours
// away, on another hour and perhaps another weekday.
func (g *gen) at() *time.Time {
	var t time.Time
	switch x := g.rng.IntN(100); {
	case x < 40:
		return nil
	case x < 60 && len(g.bounds) > 0:
		t = pick(g, g.bounds)
	case x < 75 && len(g.bounds) > 0:
		t = pick(g, g.bounds).Add(pick(g, nudges))
	default:
		t = g.now.Add(time.Duration(g.between(-48, 48)) * time.Hour)
	}
	return &t
}

// questions returns 10-30 questions of a case asked of a service: mostly about the resources of its types, then about
// its types as a whole, resources that do not exist, the types of the other service, and a type that does not exist.
func (g *gen) questions(service string) []policyfix.Question {
	own, ownSync := g.typesOf(service, false), g.typesOf(service, true)
	other := g.f.Services[0]
	if other == service {
		other = g.f.Services[1]
	}
	others := g.typesOf(other, false)
	out := make([]policyfix.Question, g.between(10, 30))
	for i := range out {
		q := policyfix.Question{Action: g.action()}
		switch x := g.rng.IntN(100); {
		case x < 68:
			q.Type = pick(g, ownSync)
			q.Resource = pick(g, g.ids[q.Type])
		case x < 82:
			q.Type = pick(g, own)
		case x < 90:
			q.Type = pick(g, own)
			q.Resource = prefix(q.Type) + "98"
			if len(g.missing[q.Type]) > 0 && g.chance(50) {
				q.Resource = pick(g, g.missing[q.Type])
			}
		case x < 98 && len(others) > 0:
			q.Type = pick(g, others)
			if len(g.ids[q.Type]) > 0 && g.chance(60) {
				q.Resource = pick(g, g.ids[q.Type])
			}
		default:
			q.Type = "nowhere"
			if g.chance(50) {
				q.Resource = "N1"
			}
		}
		out[i] = q
	}
	return out
}

// action returns the level a question asks for.
func (g *gen) action() string {
	switch x := g.rng.IntN(100); {
	case x < 48:
		return "read"
	case x < 82:
		return "write"
	case x < 96:
		return "admin"
	}
	return rareLevel
}
