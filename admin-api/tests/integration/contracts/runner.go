//go:build integration

// Package contracts is the engine runner of the decision contracts (DEC* in contracts.yaml): it loads a policy fixture
// (core/testsupport/policyfix) into a running Licetum — the services, the resource types, a synchronizer per type with
// resources fed by the fake source of the harness, and the policy written through the admin API — and asks the questions of
// the fixture through the check API, as a client service does, at the moment the fixture names. The answers come back in the
// terms of the fixture (rule names), so a test compares them with what the fixture expects (see Compare).
//
// Every question is asked with consistency=higher, so that no answer comes from a cache and each one is computed by the
// engine from the current policy and data, and with explain, so that the rules that decided it are named.
package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
	support "github.com/licetum/licetum/core/testsupport"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// Answer is the answer to one question, in the terms of the fixture.
type Answer struct {
	Allowed bool
	// Decision is the decision of the explanation (policyfix.DecisionAllowed ...).
	Decision string
	// GrantedBy names the rules that grant the level, sorted, each once (a role assignment and a rule that finds several
	// anchors take part through several instances).
	GrantedBy []string
	// Denials are the denials that apply to the level, one per rule (see byRule).
	Denials []Denial
}

// Denial is a denial of an explanation.
type Denial struct {
	Rule string
	// Level is the denied level that matched: the asked one, a level it includes, or *.
	Level string
	// LiftedBy names the exceptions that lift the denial, sorted.
	LiftedBy         []string
	UnresolvedParent bool
	MissingAttribute bool
}

func (d Denial) String() string {
	var b strings.Builder
	b.WriteString(d.Rule)
	if len(d.LiftedBy) > 0 {
		fmt.Fprintf(&b, " lifted by %v", d.LiftedBy)
	}
	if d.UnresolvedParent {
		b.WriteString(" (unresolved parent)")
	}
	return b.String()
}

// Loaded is a fixture loaded into a cluster of its own: a database, an admin API, a synchronization worker and one check
// replica whose clock the runner sets to the time of each case.
type Loaded struct {
	t       testing.TB
	Cluster *harness.Cluster
	replica *harness.Replica
	clock   *manualClock
	f       *policyfix.Fixture

	services map[string]uuid.UUID // slug -> id
	types    map[string]uuid.UUID // type name -> id
	kinds    map[string]string    // type name -> the kind of the fake source that feeds it
	policy   writtenPolicy
}

// writtenPolicy is what the runner wrote of the policy of a fixture: ReplaceRules deletes it, and the explanations are
// read back through the names of the rules.
type writtenPolicy struct {
	ruleNames    map[uuid.UUID]string
	rules, roles []uuid.UUID
	groups       []uuid.UUID
	members      []childID // parent: the group
	accessGroups []childID // parent: the resource type
}

type childID struct{ id, parent uuid.UUID }

// manualClock is the clock of the check replica: the time of the case being asked.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// syncWait bounds how long the runner waits for the synchronization of the fixture's types (the first one starts the
// worker's queue).
const syncWait = time.Minute

// Load starts a cluster on a database of its own (harness.StartReplicas with one check replica) and loads the fixture into
// it: the services by slug, the resource types with their parents, inherit masks and access_implies, a synchronizer of kind
// http per type with resources (the fake source reports their names, parents and attributes), and the policy. The types are
// synchronized before it returns, so that every parent the fixture declares is resolved or not as the fixture says. Any
// failure fails t; the cluster stops with t.
func Load(t testing.TB, infra *support.Infrastructure, f *policyfix.Fixture) *Loaded {
	t.Helper()
	c := harness.StartReplicas(t, infra, 1, harness.WithConfig(func(cfg *config.Config) {
		// Every question asks the sources again (consistency=higher): the worker must not space the calls out by a second.
		cfg.Sync.FreshMinInterval = time.Millisecond
		// A question waits for the synchronization it asked for; the worker of a fresh cluster may need a moment to start.
		cfg.Sync.RequestWait = 30 * time.Second
	}))
	l := &Loaded{
		t: t, Cluster: c, replica: c.Replicas[0], clock: &manualClock{t: policyfix.DefaultNow}, f: f,
		services: map[string]uuid.UUID{}, types: map[string]uuid.UUID{}, kinds: map[string]string{},
	}
	// The replica decides at the time of the case (see Ask). It has not served a request yet.
	l.replica.App.SetClock(l.clock.Now)

	for _, slug := range f.Services {
		l.services[slug] = uuid.MustParse(c.Must(http.MethodPost, "/service", c.ServicePayload(slug))["id"].(string))
	}
	order, err := typeOrder(f)
	if err != nil {
		t.Fatalf("fixture %s: %v", f.Name, err)
	}
	for i, typ := range order {
		parents := []map[string]any{}
		for _, p := range typ.Parents {
			link := map[string]any{"type_resource_id": l.types[p.Type]}
			if p.Inherit != nil {
				link["inherit"] = p.Inherit // an empty list lets nothing down; an absent one, every level
			}
			parents = append(parents, link)
		}
		body := map[string]any{"name": typ.Name, "parents": parents}
		if typ.Implies != nil {
			body["access_implies"] = typ.Implies
		}
		path := "/service/" + l.services[typ.Service].String() + "/type_resource"
		l.types[typ.Name] = uuid.MustParse(c.Must(http.MethodPost, path, body)["id"].(string))
		if typ.Sync {
			l.kinds[typ.Name] = "t" + strconv.Itoa(i)
		}
	}
	for _, typ := range order {
		if typ.Sync {
			l.addSynchronizer(typ)
		}
	}
	l.writePolicy(f)
	l.synchronize()
	return l
}

// typeOrder returns the types of the fixture parents first.
func typeOrder(f *policyfix.Fixture) ([]*policyfix.Type, error) {
	var out []*policyfix.Type
	placed := map[string]bool{}
	for len(out) < len(f.Types) {
		progress := false
		for i := range f.Types {
			typ := &f.Types[i]
			if placed[typ.Name] || slices.ContainsFunc(typ.Parents, func(p policyfix.TypeParent) bool { return !placed[p.Type] }) {
				continue
			}
			placed[typ.Name] = true
			out = append(out, typ)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("the parents of the types make a cycle")
		}
	}
	return out, nil
}

// addSynchronizer gives a type its source: the fake source serves the fixture's resources of the type under a kind of
// its own. Creating a synchronizer probes the source for one resource; a type the fixture gives no resources is probed on
// a row that is gone before the first synchronization.
func (l *Loaded) addSynchronizer(typ *policyfix.Type) {
	l.t.Helper()
	kind := l.kinds[typ.Name]
	rows := l.sourceRows(l.f, typ.Name)
	probe := "probe"
	if len(rows) > 0 {
		probe = rows[0]["id"].(string)
		l.Cluster.Source.Set(kind, rows...)
	} else {
		l.Cluster.Source.Set(kind, map[string]any{"id": probe, "name": probe})
	}
	path := "/service/" + l.services[typ.Service].String() + "/type_resource/" + l.types[typ.Name].String() + "/resource/synchronizer"
	l.Cluster.Must(http.MethodPost, path, map[string]any{
		// No list is kept in Redis: a synchronization always reads what the source has now.
		"config":            map[string]any{"kind": "http", "url": l.Cluster.Source.ListURL(kind), "cache_ttl": 0},
		"probe_resource_id": probe,
	})
	l.Cluster.Source.Set(kind, rows...)
}

// sourceRows are the resources of a type as its source reports them: id, name, the declared parents (by the name of the
// parent type) and the attributes.
func (l *Loaded) sourceRows(f *policyfix.Fixture, typeName string) []map[string]any {
	var rows []map[string]any
	for i := range f.Resources {
		r := &f.Resources[i]
		if r.Type != typeName {
			continue
		}
		row := map[string]any{"id": r.ID, "name": r.NameOf()}
		if len(r.Parents) > 0 {
			parents := make([]map[string]any, len(r.Parents))
			for j, p := range r.Parents {
				parents[j] = map[string]any{"type": p.Type, "id": p.ID}
			}
			row["parents"] = parents
		}
		if len(r.Meta) > 0 {
			row["meta"] = r.Meta
		}
		rows = append(rows, row)
	}
	return rows
}

// synchronize brings every type with a source up to what the source reports, and fails the test when a source did not
// answer or the worker did not finish in time.
func (l *Loaded) synchronize() {
	l.t.Helper()
	var ids []uuid.UUID
	for name := range l.kinds {
		ids = append(ids, l.types[name])
	}
	if len(ids) == 0 {
		return
	}
	res, err := l.Cluster.Admin.Coordinator().Ensure(context.Background(), ids, engine.SyncOptions{Fresh: true, Wait: syncWait})
	switch {
	case err != nil:
		l.t.Fatalf("fixture %s: synchronize the types: %v", l.f.Name, err)
	case res.Pending:
		l.t.Fatalf("fixture %s: the types were not synchronized within %s", l.f.Name, syncWait)
	case len(res.Unanswered) > 0:
		l.t.Fatalf("fixture %s: a source did not answer: %v", l.f.Name, res.Unanswered)
	}
}

// ReplaceRules replaces the policy of the loaded fixture with the policy of f: its access groups, groups with their
// members, roles with their items and rules. The services and the types stay; they must be those of the loaded fixture.
// The resources of f replace the loaded ones in the sources, and the types are synchronized again when they differ. The
// writes go through POST /batch, a hundred operations a request. The cases of f are asked with Ask afterwards.
func (l *Loaded) ReplaceRules(f *policyfix.Fixture) {
	l.t.Helper()
	if !slices.Equal(f.Services, l.f.Services) || !reflect.DeepEqual(f.Types, l.f.Types) {
		l.t.Fatalf("ReplaceRules: fixture %s has other services or types than %s", f.Name, l.f.Name)
	}
	var ops []model.BatchOperation
	var what []string
	del := func(entity string, id uuid.UUID, parent *uuid.UUID) {
		ops = append(ops, model.BatchOperation{Op: model.BatchDelete, Entity: entity, ID: &id, Parent: parent})
		what = append(what, "delete "+entity+" "+id.String())
	}
	// Whatever refers to an entity goes before it: the rules name roles, groups and access groups, the items of a role go
	// with it, and a group is deleted once no membership names it.
	p := l.policy
	for _, id := range p.rules {
		del(model.BatchAssignmentRule, id, nil)
	}
	for _, id := range p.roles {
		del(model.BatchRole, id, nil)
	}
	for _, m := range p.members {
		del(model.BatchGroupMember, m.id, &m.parent)
	}
	for _, id := range p.groups {
		del(model.BatchGroup, id, nil)
	}
	for _, g := range p.accessGroups {
		del(model.BatchAccessGroup, g.id, &g.parent)
	}
	l.runBatch(ops, what)

	resourcesChanged := !reflect.DeepEqual(f.Resources, l.f.Resources)
	l.f = f
	if resourcesChanged {
		for name, kind := range l.kinds {
			l.Cluster.Source.Set(kind, l.sourceRows(f, name)...)
		}
	}
	l.writePolicy(f)
	if resourcesChanged {
		l.synchronize()
	}
}

// writePolicy writes the policy of f through POST /batch, with the ids of the new entities chosen here so that the
// operations can name them and the explanations can be read back.
func (l *Loaded) writePolicy(f *policyfix.Fixture) {
	l.t.Helper()
	p := writtenPolicy{ruleNames: map[uuid.UUID]string{}}
	var ops []model.BatchOperation
	var what []string
	create := func(entity, name string, parent *uuid.UUID, body any) uuid.UUID {
		id := uuid.New()
		ops = append(ops, model.BatchOperation{Op: model.BatchCreate, Entity: entity, ID: &id, Parent: parent, Body: body})
		what = append(what, entity+" "+name)
		return id
	}

	for _, g := range f.AccessGroups {
		typeID := l.types[g.Type]
		id := create(model.BatchAccessGroup, g.Type+"/@"+g.Name, &typeID, model.AccessGroupInput{Name: g.Name, Levels: g.Levels})
		p.accessGroups = append(p.accessGroups, childID{id: id, parent: typeID})
	}
	groups := map[string]uuid.UUID{}
	for _, g := range f.Groups {
		groups[g.Name] = create(model.BatchGroup, g.Name, nil, model.GroupInput{Name: g.Name})
		p.groups = append(p.groups, groups[g.Name])
	}
	for _, g := range f.Groups {
		group := groups[g.Name]
		for i, m := range g.Members {
			in := model.GroupMemberInput{Predicate: m.Predicate, Enabled: ptr(!m.Disabled), ValidFrom: m.ValidFrom, ExpiresAt: m.ExpiresAt}
			switch {
			case m.Subject != "":
				// A membership names people by their claims: the subject is the preferred_username of the token (see Ask).
				in.Predicate = []model.PredicateCondition{{Attr: "preferred_username", Op: model.OpEq, Value: model.PredicateValue{Str: m.Subject}}}
			case m.Group != "":
				in.MemberGroupID = ptr(groups[m.Group])
			}
			if in.Predicate == nil {
				in.Predicate = []model.PredicateCondition{} // everybody, or a nested group
			}
			id := create(model.BatchGroupMember, fmt.Sprintf("%s#%d", g.Name, i), &group, in)
			p.members = append(p.members, childID{id: id, parent: group})
		}
	}
	for _, r := range f.Roles {
		role := create(model.BatchRole, r.Name, nil, model.RoleInput{Name: r.Name})
		p.roles = append(p.roles, role)
		for i := range r.Items {
			item := &r.Items[i]
			in := model.RolePermissionInput{Access: item.Access}
			in.TypeResourceID, in.ResourceID, in.ResourceFromClaim, in.ResourceWhere = l.target(&item.Target)
			create(model.BatchRolePermission, fmt.Sprintf("%s#%d", r.Name, i), &role, in)
		}
	}
	roles := map[string]uuid.UUID{}
	for i, r := range f.Roles {
		roles[r.Name] = p.roles[i]
	}
	for i := range f.Rules {
		r := &f.Rules[i]
		in := model.AssignmentRuleInput{
			Name: ptr(r.Name), Enabled: ptr(!r.Disabled), Exception: r.Exception, ValidFrom: r.ValidFrom, ExpiresAt: r.ExpiresAt,
			RequiresPredicate: r.RequiresPredicate, Effect: model.EffectAllow,
		}
		if r.Deny {
			in.Effect = model.EffectDeny
		}
		if r.OnMissing != "" {
			in.OnMissingAttribute = ptr(r.OnMissing)
		}
		if r.RequiresGroup != "" {
			in.RequiresGroupID = ptr(groups[r.RequiresGroup])
		}
		switch {
		case r.Subject != "":
			in.Subject = ptr(r.Subject)
		case r.SubjectGroup != "":
			in.Subject = ptr(model.GroupSubject(groups[r.SubjectGroup]))
		case r.MemberOf != "":
			in.MemberOfGroupID = ptr(groups[r.MemberOf])
		default:
			// A predicate over the request; an empty one (everybody) applies to everyone.
			in.Predicate = r.Predicate
			if in.Predicate == nil {
				in.Predicate = []model.PredicateCondition{}
			}
		}
		if r.Role != "" {
			in.RoleID = ptr(roles[r.Role])
		} else {
			in.Access = r.Access
			var typeID uuid.UUID
			typeID, in.ResourceID, in.ResourceFromClaim, in.ResourceWhere = l.target(r.Target)
			in.TypeResourceID = &typeID
		}
		id := create(model.BatchAssignmentRule, r.Name, nil, in)
		p.rules = append(p.rules, id)
		p.ruleNames[id] = r.Name
	}
	l.runBatch(ops, what)
	l.policy = p
}

// target is the target of a rule or a role item in the terms of the API.
func (l *Loaded) target(t *policyfix.Target) (typeID uuid.UUID, resourceID *string, fromClaim *model.ResourceFromClaim, where []model.PredicateCondition) {
	typeID = l.types[t.Type]
	if t.Resource != "" {
		resourceID = ptr(t.Resource)
	}
	if fc := t.FromClaim; fc != nil {
		matchBy := fc.MatchBy
		if matchBy == "" {
			matchBy = model.MatchByName
		}
		fromClaim = &model.ResourceFromClaim{Claim: fc.Claim, LookupTypeResourceID: l.types[fc.LookupType], MatchBy: matchBy}
	}
	return typeID, resourceID, fromClaim, t.Where
}

// batchSize is the most operations POST /batch takes at once.
const batchSize = 100

// runBatch sends the operations in order, a hundred a request. what describes each operation for a failure.
func (l *Loaded) runBatch(ops []model.BatchOperation, what []string) {
	l.t.Helper()
	for start := 0; start < len(ops); start += batchSize {
		end := min(start+batchSize, len(ops))
		status, raw := l.Cluster.AdminDo(http.MethodPost, "/batch", model.BatchInput{Operations: ops[start:end]})
		if status == http.StatusOK {
			continue
		}
		var failure struct {
			Extra struct {
				Operation *int `json:"operation"`
			} `json:"extra"`
		}
		failed := "?"
		if json.Unmarshal(raw, &failure) == nil && failure.Extra.Operation != nil && start+*failure.Extra.Operation < end {
			failed = what[start+*failure.Extra.Operation]
		}
		l.t.Fatalf("fixture %s: the policy was refused at %s: %d %s", l.f.Name, failed, status, raw)
	}
}

// maxQuestions is the most questions POST /service/{id}/check takes at once.
const maxQuestions = 100

// Ask asks the questions of a case of the loaded fixture: as the case's subject, with the case's claims in the token
// (preferred_username is the subject), the case's request attributes in the body, at the case's time (the clock of the
// replica), through POST /service/{slug}/check?consistency=higher with explain. The answers are in the order of the
// questions. A request that fails, or an answer made without the data of some type, fails the test.
func (l *Loaded) Ask(c *policyfix.Case) []Answer {
	l.t.Helper()
	l.clock.Set(l.f.NowOf(c))
	claims := jwt.MapClaims{}
	maps.Copy(claims, c.Claims)
	claims["preferred_username"] = c.Subject
	token := l.Cluster.Token(claims)
	path := "/service/" + url.PathEscape(l.f.ServiceOf(c)) + "/check?consistency=higher"

	answers := make([]Answer, 0, len(c.Questions))
	for start := 0; start < len(c.Questions); start += maxQuestions {
		batch := c.Questions[start:min(start+maxQuestions, len(c.Questions))]
		checks := make([]model.CheckRequest, len(batch))
		for i, q := range batch {
			checks[i] = model.CheckRequest{Type: q.Type, Action: q.Action}
			if q.Resource != "" {
				checks[i].ResourceID = ptr(q.Resource)
			}
		}
		body := model.CheckInput{Checks: checks, Explain: true, Context: c.Context}
		res := l.askOnce(path, token, body)
		var out model.CheckResponse
		if err := json.Unmarshal(res.Body, &out); err != nil || len(out.Results) != len(checks) {
			l.t.Fatalf("fixture %s, case %q: the answer does not decode into %d results: %v %s", l.f.Name, c.Name, len(checks), err, res.Body)
		}
		for _, r := range out.Results {
			answers = append(answers, l.answerOf(r))
		}
	}
	return answers
}

// askOnce sends one check request. An answer made while the synchronization of some type had not finished (X-Authz-Sync:
// pending) lacks data the fixture declares: it is asked again a few times before the test fails.
func (l *Loaded) askOnce(path, token string, body model.CheckInput) *harness.Result {
	l.t.Helper()
	for attempt := 1; ; attempt++ {
		res := l.replica.Do(l.Cluster, http.MethodPost, path, token, body)
		if res.Status != http.StatusOK {
			l.t.Fatalf("fixture %s: POST %s: %d %s", l.f.Name, path, res.Status, res.Body)
		}
		if res.Header.Get("X-Authz-Sync") != "pending" {
			return res
		}
		if attempt == 3 {
			l.t.Fatalf("fixture %s: POST %s: the answer was made without the data of some type three times", l.f.Name, path)
		}
	}
}

// answerOf reads a result back in the terms of the fixture.
func (l *Loaded) answerOf(r model.CheckResult) Answer {
	a := Answer{Allowed: r.Allowed}
	e := r.Explanation
	if e == nil {
		return a
	}
	a.Decision = e.Decision
	for _, g := range e.GrantedBy {
		a.GrantedBy = append(a.GrantedBy, l.ruleName(g.RuleID))
	}
	a.GrantedBy = sortedUnique(a.GrantedBy)
	for _, d := range e.Denials {
		denial := Denial{Rule: l.ruleName(d.Rule.RuleID), Level: d.Level, UnresolvedParent: d.UnresolvedParent, MissingAttribute: d.MissingAttribute}
		for _, x := range d.LiftedBy {
			denial.LiftedBy = append(denial.LiftedBy, l.ruleName(x.RuleID))
		}
		denial.LiftedBy = sortedUnique(denial.LiftedBy)
		a.Denials = append(a.Denials, denial)
	}
	a.Denials = byRule(a.Denials)
	return a
}

// byRule merges the denials of an explanation by rule, as the fixtures and the reference evaluator state them. The engine
// lists a denial per instance and matched level (a role with two items, or a rule found at two anchors, appears twice,
// each with its anchor). A rule with a standing denial is listed once, standing; a rule whose every denial is lifted is
// listed once, with every exception that lifts one of them. It is assumed for an unresolved parent when every denial
// that decides it is (the standing ones, or all when none stands), and its Level is the first of those.
func byRule(in []Denial) []Denial {
	var order []string
	standing, lifted := map[string][]Denial{}, map[string][]Denial{}
	for _, d := range in {
		if _, seen := standing[d.Rule]; !seen {
			if _, seen := lifted[d.Rule]; !seen {
				order = append(order, d.Rule)
			}
		}
		if len(d.LiftedBy) == 0 {
			standing[d.Rule] = append(standing[d.Rule], d)
		} else {
			lifted[d.Rule] = append(lifted[d.Rule], d)
		}
	}
	out := make([]Denial, 0, len(order))
	for _, rule := range order {
		merged := Denial{Rule: rule, UnresolvedParent: true}
		deciding := standing[rule]
		if len(deciding) == 0 {
			deciding = lifted[rule]
			for _, d := range deciding {
				merged.LiftedBy = append(merged.LiftedBy, d.LiftedBy...)
			}
			merged.LiftedBy = sortedUnique(merged.LiftedBy)
		}
		merged.Level = deciding[0].Level
		for _, d := range deciding {
			merged.UnresolvedParent = merged.UnresolvedParent && d.UnresolvedParent
			merged.MissingAttribute = merged.MissingAttribute || d.MissingAttribute
		}
		out = append(out, merged)
	}
	return out
}

// ruleName is the name of a rule the runner wrote; a rule it did not write (it should not take part) is named by its id.
func (l *Loaded) ruleName(id uuid.UUID) string {
	if name, ok := l.policy.ruleNames[id]; ok {
		return name
	}
	return "unknown rule " + id.String()
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func ptr[T any](v T) *T { return &v }
