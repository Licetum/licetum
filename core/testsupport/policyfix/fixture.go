// Package policyfix describes a policy and a resource graph in external terms — service slugs, type names, resource
// external ids, group, role and rule names — together with questions and the answers expected of them. A fixture is the
// input of the decision contracts (DEC* in contracts.yaml): the engine runner of the admin API tests loads it through the
// API and the sources and asks the questions; the reference evaluator (core/testsupport/refeval) decides it in memory; the
// generator (core/testsupport/policygen) writes random ones. Nothing here depends on how a decision is computed.
//
// Fixtures are YAML files (testdata/decisions/*.yaml of this package). They are read through JSON, so the conditions are
// the model.PredicateCondition of the API with their own decoding.
package policyfix

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/licetum/licetum/core/model"
)

// Fixture is one policy, one resource graph, and the questions asked of them.
type Fixture struct {
	// Name identifies the fixture in test names; the file name when read from a file.
	Name string `json:"name"`
	// Contracts are the IDs of contracts.yaml the fixture checks (DEC01...).
	Contracts   []string `json:"contracts"`
	Description string   `json:"description,omitempty"`
	// Now is the time the questions are asked at unless a case says otherwise (RFC 3339; default 2030-01-01T12:00:00Z).
	Now          *time.Time    `json:"now,omitempty"`
	Services     []string      `json:"services"`
	Types        []Type        `json:"types"`
	Resources    []Resource    `json:"resources,omitempty"`
	AccessGroups []AccessGroup `json:"access_groups,omitempty"`
	Groups       []Group       `json:"groups,omitempty"`
	Roles        []Role        `json:"roles,omitempty"`
	Rules        []Rule        `json:"rules,omitempty"`
	Cases        []Case        `json:"cases"`
	// Before, when set, is how the state differed when the resources were first synchronized and the policy was written:
	// the fixture's state is reached by changes made afterwards (see Before). The questions are about the fixture's state.
	Before *Before `json:"before,omitempty"`
}

// Before is the state at the first synchronization, where it differs from the fixture's, for a state that only a change
// made after the resources were synchronized and the policy written can reach: a resource its source no longer reports
// (it is switched off, not deleted), a type whose synchronizer was deleted (its resources stay in the local copy), a
// parent type deleted under a live type, an access group whose levels changed under the rules that name it.
//
// The engine runner loads this state, synchronizes the types and writes the policy, then makes the changes — through the
// admin API and the sources, and a deleted type through the database, as the integration test it comes from does (the
// admin API refuses to delete a type that a live type names as its parent) — and synchronizes again. The reference
// evaluator never reads it: it decides the fixture's state, in which what was removed does not exist.
type Before struct {
	// Types are the types as they were, by name. A type the fixture does not have was deleted afterwards. A type of the
	// fixture may differ in two ways only: sync true where the fixture's is false (its synchronizer was deleted), and parent
	// links to deleted types besides the fixture's links (the link stays, the type it names is gone).
	Types []Type `json:"types,omitempty"`
	// Resources are reported by their sources at the first synchronization besides the fixture's resources, or in place of
	// the fixture's resource of the same type and id. One the fixture does not have left its source afterwards when its
	// type still has a synchronizer, else it stays in the local copy as it was. A parent of a deleted type is still
	// reported afterwards: the type was deleted, not the source's data.
	Resources []Resource `json:"resources,omitempty"`
	// AccessGroups are access groups of the fixture (by type and name) with the levels they had when the policy was
	// written; they are changed to the fixture's levels afterwards.
	AccessGroups []AccessGroup `json:"access_groups,omitempty"`
}

// TypesAtFirstSync returns the types as they were at the first synchronization: the fixture's, each replaced by its
// version in Before, then the types deleted since.
func (f *Fixture) TypesAtFirstSync() []Type {
	if f.Before == nil {
		return slices.Clone(f.Types)
	}
	out := make([]Type, 0, len(f.Types)+len(f.Before.Types))
	for _, t := range f.Types {
		if i := slices.IndexFunc(f.Before.Types, func(b Type) bool { return b.Name == t.Name }); i >= 0 {
			t = f.Before.Types[i]
		}
		out = append(out, t)
	}
	for _, b := range f.Before.Types {
		if _, ok := f.TypeByName(b.Name); !ok {
			out = append(out, b)
		}
	}
	return out
}

// ResourcesAtFirstSync returns what the sources reported at the first synchronization: the fixture's resources, each
// replaced by its version in Before, then the resources of Before the fixture does not have.
func (f *Fixture) ResourcesAtFirstSync() []Resource {
	if f.Before == nil {
		return slices.Clone(f.Resources)
	}
	out := make([]Resource, 0, len(f.Resources)+len(f.Before.Resources))
	for _, r := range f.Resources {
		if b, ok := f.Before.resourceOf(r.Type, r.ID); ok {
			r = *b
		}
		out = append(out, r)
	}
	for _, b := range f.Before.Resources {
		if _, ok := f.ResourceOf(b.Type, b.ID); !ok {
			out = append(out, b)
		}
	}
	return out
}

// DeletedTypes returns the names of the types deleted after the first synchronization.
func (f *Fixture) DeletedTypes() []string {
	var out []string
	if f.Before != nil {
		for _, b := range f.Before.Types {
			if _, ok := f.TypeByName(b.Name); !ok {
				out = append(out, b.Name)
			}
		}
	}
	return out
}

func (b *Before) resourceOf(typeName, id string) (*Resource, bool) {
	for i := range b.Resources {
		if b.Resources[i].Type == typeName && b.Resources[i].ID == id {
			return &b.Resources[i], true
		}
	}
	return nil, false
}

// Type is a resource type of a service. Its name is unique in the fixture.
type Type struct {
	Name    string       `json:"name"`
	Service string       `json:"service"`
	Parents []TypeParent `json:"parents,omitempty"`
	// Implies is access_implies: {"write": ["read"]} — granting write grants read, denying read denies write.
	Implies map[string][]string `json:"implies,omitempty"`
	// Sync says the type has a synchronizer, hence resources; a type without one is a capability (not_sync).
	Sync bool `json:"sync"`
}

// TypeParent is a parent link of a type; Inherit nil means every level flows down along it.
type TypeParent struct {
	Type    string   `json:"type"`
	Inherit []string `json:"inherit,omitempty"`
}

// Resource is a resource its source reports. A parent whose id no resource of that type has is an unresolved link.
type Resource struct {
	Type    string           `json:"type"`
	ID      string           `json:"id"`
	Name    string           `json:"name,omitempty"` // default: the id
	Parents []ResourceParent `json:"parents,omitempty"`
	Meta    map[string]any   `json:"meta,omitempty"`
}

// ResourceParent names a parent of a resource.
type ResourceParent struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// AccessGroup names a set of levels of one type (@name in an access list).
type AccessGroup struct {
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Levels []string `json:"levels"`
}

// Group is a union of people; its members are subjects, predicates over claims, or other groups.
type Group struct {
	Name    string   `json:"name"`
	Members []Member `json:"members,omitempty"`
}

// Member is a membership of a group: exactly one of Subject, Predicate (an empty list matches everybody when
// Everybody is set) and Group.
type Member struct {
	Subject   string                     `json:"subject,omitempty"`
	Predicate []model.PredicateCondition `json:"predicate,omitempty"`
	Everybody bool                       `json:"everybody,omitempty"` // an empty predicate
	Group     string                     `json:"group,omitempty"`
	Disabled  bool                       `json:"disabled,omitempty"`
	ValidFrom *time.Time                 `json:"valid_from,omitempty"`
	ExpiresAt *time.Time                 `json:"expires_at,omitempty"`
}

// Target is what a permission covers: the whole type, one resource, the resource found from a claim, or the resources
// whose attributes match.
type Target struct {
	Type      string                     `json:"type"`
	Resource  string                     `json:"resource,omitempty"`
	FromClaim *FromClaim                 `json:"from_claim,omitempty"`
	Where     []model.PredicateCondition `json:"where,omitempty"`
}

// FromClaim is resource_from_claim: the value of Claim is looked up among the resources of LookupType by MatchBy (name,
// id or meta.<attribute>).
type FromClaim struct {
	Claim      string `json:"claim"`
	LookupType string `json:"lookup_type"`
	MatchBy    string `json:"match_by,omitempty"` // default name
}

// Role is a named set of permissions.
type Role struct {
	Name  string     `json:"name"`
	Items []RoleItem `json:"items"`
}

// RoleItem is a permission of a role.
type RoleItem struct {
	Target `json:",inline"`
	Access []string `json:"access"`
}

// Rule is an assignment rule: to whom (exactly one of Subject, SubjectGroup, Predicate/Everybody, MemberOf) it grants
// or denies what (a Role, or an inline Target with Access).
type Rule struct {
	// Name names the rule in explanations and in the expectations of cases; unique in the fixture.
	Name         string                     `json:"name"`
	Subject      string                     `json:"subject,omitempty"`
	SubjectGroup string                     `json:"subject_group,omitempty"` // the group itself as a subject (group:<id>)
	Predicate    []model.PredicateCondition `json:"predicate,omitempty"`
	Everybody    bool                       `json:"everybody,omitempty"` // an empty predicate
	MemberOf     string                     `json:"member_of,omitempty"`
	Role         string                     `json:"role,omitempty"`
	*Target      `json:",inline,omitempty"`
	Access       []string `json:"access,omitempty"`
	Deny         bool     `json:"deny,omitempty"`
	Exception    bool     `json:"exception,omitempty"`
	// OnMissing is on_missing_attribute of a denial (skip or apply; default apply, as for a new deny rule).
	OnMissing         string                     `json:"on_missing,omitempty"`
	RequiresPredicate []model.PredicateCondition `json:"requires_predicate,omitempty"`
	RequiresGroup     string                     `json:"requires_group,omitempty"`
	Disabled          bool                       `json:"disabled,omitempty"`
	ValidFrom         *time.Time                 `json:"valid_from,omitempty"`
	ExpiresAt         *time.Time                 `json:"expires_at,omitempty"`
}

// Case is a set of questions asked by one subject with one set of claims and request attributes, at one time.
type Case struct {
	Name    string         `json:"name"`
	Subject string         `json:"subject"`
	Claims  map[string]any `json:"claims,omitempty"`
	Context map[string]any `json:"context,omitempty"`
	At      *time.Time     `json:"at,omitempty"`
	// Service is the service the questions are asked of (default: the first service).
	Service   string     `json:"service,omitempty"`
	Questions []Question `json:"questions"`
}

// Question asks whether the subject may perform Action on a resource (or on the type as a whole when Resource is empty),
// and says what the answer must be.
type Question struct {
	Type     string `json:"type"`
	Resource string `json:"resource,omitempty"`
	Action   string `json:"action"`
	Allowed  bool   `json:"allowed"`
	// Decision, when set, is the expected decision of the explanation: allowed, denied, no_access, resource_not_found,
	// type_not_found.
	Decision string `json:"decision,omitempty"`
	// GrantedBy and Denials, when set, are the rule names the explanation must name (order does not matter).
	GrantedBy []string         `json:"granted_by,omitempty"`
	Denials   []ExpectedDenial `json:"denials,omitempty"`
}

// ExpectedDenial is a denial an explanation must list.
type ExpectedDenial struct {
	Rule             string   `json:"rule"`
	LiftedBy         []string `json:"lifted_by,omitempty"`
	UnresolvedParent bool     `json:"unresolved_parent,omitempty"`
	// MissingAttribute, when set, is whether the denial applies only because a condition reads an attribute the request
	// does not carry (on_missing_attribute apply; the missing_attribute of the explanation). The engine runner compares it;
	// the reference evaluator does not state it.
	MissingAttribute *bool `json:"missing_attribute,omitempty"`
}

// Decisions of an explanation (model.Explanation.Decision).
const (
	DecisionAllowed          = "allowed"
	DecisionDenied           = "denied"
	DecisionNoAccess         = "no_access"
	DecisionResourceNotFound = "resource_not_found"
	DecisionTypeNotFound     = "type_not_found"
)

// DefaultNow is the time of a fixture that does not set one.
var DefaultNow = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)

// NowOf returns the time a case is asked at.
func (f *Fixture) NowOf(c *Case) time.Time {
	switch {
	case c.At != nil:
		return *c.At
	case f.Now != nil:
		return *f.Now
	}
	return DefaultNow
}

// ServiceOf returns the service a case is asked of.
func (f *Fixture) ServiceOf(c *Case) string {
	if c.Service != "" {
		return c.Service
	}
	return f.Services[0]
}

// TypeByName returns a type of the fixture.
func (f *Fixture) TypeByName(name string) (*Type, bool) {
	for i := range f.Types {
		if f.Types[i].Name == name {
			return &f.Types[i], true
		}
	}
	return nil, false
}

// ResourceOf returns a resource of the fixture.
func (f *Fixture) ResourceOf(typeName, id string) (*Resource, bool) {
	for i := range f.Resources {
		if f.Resources[i].Type == typeName && f.Resources[i].ID == id {
			return &f.Resources[i], true
		}
	}
	return nil, false
}

// NameOf returns the name of a resource: its own, or its id.
func (r *Resource) NameOf() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

// Parse reads one fixture from YAML (or JSON) and checks that its names refer to what it defines.
func Parse(name string, raw []byte) (*Fixture, error) {
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	js, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	var f Fixture
	dec := json.NewDecoder(strings.NewReader(string(js)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if f.Name == "" {
		f.Name = name
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &f, nil
}

//go:embed testdata/decisions/*.yaml
var decisions embed.FS

// Decisions returns the fixtures of the decision contracts (testdata/decisions), sorted by name.
func Decisions() ([]*Fixture, error) {
	entries, err := fs.ReadDir(decisions, "testdata/decisions")
	if err != nil {
		return nil, err
	}
	var out []*Fixture
	for _, e := range entries {
		raw, err := decisions.ReadFile(path.Join("testdata/decisions", e.Name()))
		if err != nil {
			return nil, err
		}
		f, err := Parse(strings.TrimSuffix(e.Name(), path.Ext(e.Name())), raw)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// For returns the fixtures that check a contract.
func For(fixtures []*Fixture, contract string) []*Fixture {
	var out []*Fixture
	for _, f := range fixtures {
		if slices.Contains(f.Contracts, contract) {
			out = append(out, f)
		}
	}
	return out
}

// Validate checks that every name a fixture uses is defined in it, and the shape of its rules.
func (f *Fixture) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if len(f.Services) == 0 {
		bad("no services")
	}
	types := map[string]*Type{}
	for i := range f.Types {
		t := &f.Types[i]
		if types[t.Name] != nil {
			bad("type %q twice", t.Name)
		}
		types[t.Name] = t
		if !slices.Contains(f.Services, t.Service) {
			bad("type %q: unknown service %q", t.Name, t.Service)
		}
	}
	for _, t := range f.Types {
		for _, p := range t.Parents {
			if types[p.Type] == nil {
				bad("type %q: unknown parent type %q", t.Name, p.Type)
			}
		}
	}
	syncedBefore := map[string]bool{} // the types without a synchronizer that had one at the first synchronization
	if f.Before != nil {
		errs = append(errs, f.validateBefore(types)...)
		for _, b := range f.Before.Types {
			if t := types[b.Name]; t != nil && !t.Sync && b.Sync {
				syncedBefore[b.Name] = true
			}
		}
	}
	resources := map[string]bool{}
	for _, r := range f.Resources {
		t := types[r.Type]
		switch {
		case t == nil:
			bad("resource %s/%s: unknown type", r.Type, r.ID)
			continue
		case !t.Sync:
			bad("resource %s/%s: type %q has no synchronizer", r.Type, r.ID, r.Type)
		}
		key := r.Type + "/" + r.ID
		if resources[key] {
			bad("resource %s twice", key)
		}
		resources[key] = true
		for _, p := range r.Parents {
			if !slices.ContainsFunc(t.Parents, func(tp TypeParent) bool { return tp.Type == p.Type }) {
				bad("resource %s: %q is not a parent type of %q", key, p.Type, r.Type)
			}
		}
	}
	groups := map[string]bool{}
	for _, g := range f.Groups {
		if groups[g.Name] {
			bad("group %q twice", g.Name)
		}
		groups[g.Name] = true
	}
	for _, g := range f.Groups {
		for _, m := range g.Members {
			n := 0
			for _, set := range []bool{m.Subject != "", len(m.Predicate) > 0 || m.Everybody, m.Group != ""} {
				if set {
					n++
				}
			}
			if n != 1 {
				bad("group %q: a member needs exactly one of subject, predicate/everybody, group", g.Name)
			}
			if m.Group != "" && !groups[m.Group] {
				bad("group %q: unknown member group %q", g.Name, m.Group)
			}
		}
	}
	checkTarget := func(where string, t *Target) {
		tt := types[t.Type]
		if tt == nil {
			bad("%s: unknown type %q", where, t.Type)
			return
		}
		n := 0
		for _, set := range []bool{t.Resource != "", t.FromClaim != nil, len(t.Where) > 0} {
			if set {
				n++
			}
		}
		if n > 1 {
			bad("%s: at most one of resource, from_claim, where", where)
		}
		// A type whose synchronizer was deleted after the policy was written may keep a rule on its resources: a dead
		// rule, which the admin API keeps and which covers nothing.
		if n == 1 && !tt.Sync && !syncedBefore[t.Type] {
			bad("%s: type %q has no synchronizer, only the whole type can be covered", where, t.Type)
		}
		if t.FromClaim != nil && types[t.FromClaim.LookupType] == nil {
			bad("%s: unknown lookup type %q", where, t.FromClaim.LookupType)
		}
	}
	roles := map[string]bool{}
	for _, r := range f.Roles {
		roles[r.Name] = true
		for i := range r.Items {
			checkTarget(fmt.Sprintf("role %q item %d", r.Name, i), &r.Items[i].Target)
		}
	}
	accessGroups := map[string]bool{}
	for _, g := range f.AccessGroups {
		if types[g.Type] == nil {
			bad("access group %q: unknown type %q", g.Name, g.Type)
		}
		accessGroups[g.Type+"/"+g.Name] = true
	}
	rules := map[string]bool{}
	for i := range f.Rules {
		r := &f.Rules[i]
		if r.Name == "" {
			bad("rule %d has no name", i)
		}
		if rules[r.Name] {
			bad("rule %q twice", r.Name)
		}
		rules[r.Name] = true
		n := 0
		for _, set := range []bool{r.Subject != "", r.SubjectGroup != "", len(r.Predicate) > 0 || r.Everybody, r.MemberOf != ""} {
			if set {
				n++
			}
		}
		if n != 1 {
			bad("rule %q: exactly one of subject, subject_group, predicate/everybody, member_of", r.Name)
		}
		for _, g := range []string{r.SubjectGroup, r.MemberOf, r.RequiresGroup} {
			if g != "" && !groups[g] {
				bad("rule %q: unknown group %q", r.Name, g)
			}
		}
		switch {
		case r.Role != "" && (r.Target != nil || len(r.Access) > 0):
			bad("rule %q: a role or an inline permission, not both", r.Name)
		case r.Role != "" && !roles[r.Role]:
			bad("rule %q: unknown role %q", r.Name, r.Role)
		case r.Role == "" && (r.Target == nil || len(r.Access) == 0):
			bad("rule %q: an inline permission needs a type and access", r.Name)
		case r.Role == "":
			checkTarget(fmt.Sprintf("rule %q", r.Name), r.Target)
			for _, a := range r.Access {
				if name, ok := strings.CutPrefix(a, model.AccessGroupPrefix); ok && !accessGroups[r.Type+"/"+name] {
					bad("rule %q: unknown access group %q of type %q", r.Name, name, r.Type)
				}
				if a == model.AllLevels && !r.Deny {
					bad("rule %q: * is only for denials", r.Name)
				}
			}
		}
		if r.Exception && r.Deny {
			bad("rule %q: an exception is an allow rule", r.Name)
		}
	}
	for _, c := range f.Cases {
		if c.Service != "" && !slices.Contains(f.Services, c.Service) {
			bad("case %q: unknown service %q", c.Name, c.Service)
		}
		for _, q := range c.Questions {
			for _, name := range q.GrantedBy {
				if !rules[name] {
					bad("case %q: unknown rule %q in granted_by", c.Name, name)
				}
			}
			for _, d := range q.Denials {
				if !rules[d.Rule] {
					bad("case %q: unknown rule %q in denials", c.Name, d.Rule)
				}
			}
		}
	}
	if len(f.Cases) == 0 {
		bad("no cases")
	}
	return errors.Join(errs...)
}

// validateBefore checks the state of Before against the fixture's types: what may differ, and that its resources and
// access groups name what that state has.
func (f *Fixture) validateBefore(types map[string]*Type) []error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf("before: "+format, args...)) }
	b := f.Before
	then := map[string]*Type{} // the types at the first synchronization
	atFirstSync := f.TypesAtFirstSync()
	for i := range atFirstSync {
		then[atFirstSync[i].Name] = &atFirstSync[i]
	}
	seen := map[string]bool{}
	for _, bt := range b.Types {
		if seen[bt.Name] {
			bad("type %q twice", bt.Name)
		}
		seen[bt.Name] = true
		if !slices.Contains(f.Services, bt.Service) {
			bad("type %q: unknown service %q", bt.Name, bt.Service)
		}
		for _, p := range bt.Parents {
			if then[p.Type] == nil {
				bad("type %q: unknown parent type %q", bt.Name, p.Type)
			}
		}
		now := types[bt.Name]
		if now == nil {
			continue // deleted afterwards
		}
		var kept []TypeParent // the links the type has now: the others must name deleted types
		for _, p := range bt.Parents {
			if types[p.Type] == nil {
				continue
			}
			kept = append(kept, p)
		}
		switch {
		case bt.Service != now.Service || !equalImplies(bt.Implies, now.Implies):
			bad("type %q: only the synchronizer and the links to deleted types can differ", bt.Name)
		case !slices.EqualFunc(kept, now.Parents, func(a, b TypeParent) bool { return a.Type == b.Type && slices.Equal(a.Inherit, b.Inherit) }):
			bad("type %q: its links to the types that are not deleted must be those of the fixture", bt.Name)
		case bt.Sync != now.Sync && !bt.Sync:
			bad("type %q: a synchronizer added afterwards is not a change Before describes", bt.Name)
		case bt.Sync == now.Sync && len(kept) == len(bt.Parents):
			bad("type %q: it does not differ from the fixture's", bt.Name)
		}
	}
	key := map[string]bool{}
	for _, r := range b.Resources {
		t := then[r.Type]
		switch {
		case t == nil:
			bad("resource %s/%s: unknown type", r.Type, r.ID)
			continue
		case !t.Sync:
			bad("resource %s/%s: type %q had no synchronizer", r.Type, r.ID, r.Type)
		}
		k := r.Type + "/" + r.ID
		if key[k] {
			bad("resource %s twice", k)
		}
		key[k] = true
		for _, p := range r.Parents {
			if !slices.ContainsFunc(t.Parents, func(tp TypeParent) bool { return tp.Type == p.Type }) {
				bad("resource %s: %q was not a parent type of %q", k, p.Type, r.Type)
			}
		}
	}
	for _, g := range b.AccessGroups {
		if !slices.ContainsFunc(f.AccessGroups, func(a AccessGroup) bool { return a.Type == g.Type && a.Name == g.Name }) {
			bad("access group %s/%s: the fixture has no such access group", g.Type, g.Name)
		}
	}
	return errs
}

func equalImplies(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !slices.Equal(v, w) {
			return false
		}
	}
	return true
}
