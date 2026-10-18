package policygen_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
	"github.com/licetum/licetum/core/testsupport/policygen"
	"github.com/licetum/licetum/core/testsupport/refeval"
)

// seeds is the number of seeds the tests go through: 0 ... seeds-1.
const seeds = 500

// generated returns the fixtures of the seeds, generated once for the tests of the package.
var generated = func() []*policyfix.Fixture {
	out := make([]*policyfix.Fixture, seeds)
	for seed := range out {
		out[seed] = policygen.Generate(int64(seed))
	}
	return out
}()

func TestEverySeedGivesAValidFixture(t *testing.T) {
	for seed, f := range generated {
		if err := f.Validate(); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, problem := range shapeProblems(f) {
			t.Errorf("seed %d: %s", seed, problem)
		}
		for _, problem := range apiProblems(f) {
			t.Errorf("seed %d: the admin API would refuse it: %s", seed, problem)
		}
	}
}

func TestTheSameSeedGivesTheSameFixture(t *testing.T) {
	distinct := map[string]int{}
	for seed, f := range generated {
		again := policygen.Generate(int64(seed))
		if !reflect.DeepEqual(f, again) {
			t.Fatalf("seed %d gives two fixtures: %s", seed, firstDifference(jsonOf(t, f), jsonOf(t, again)))
		}
		// The name and the description name the seed; what the seed changes is the rest.
		body := *f
		body.Name, body.Description = "", ""
		if other, ok := distinct[jsonOf(t, &body)]; ok {
			t.Errorf("seeds %d and %d give the same fixture", other, seed)
		}
		distinct[jsonOf(t, &body)] = seed
	}
	if f := policygen.Generate(-7); f.Validate() != nil || !reflect.DeepEqual(f, policygen.Generate(-7)) {
		t.Error("a negative seed must give one valid fixture too")
	}
}

// A failure of the property test is reproduced from a file: the fixture written as JSON reads back unchanged.
func TestAGeneratedFixtureReadsBackUnchanged(t *testing.T) {
	for seed, f := range generated {
		raw := jsonOf(t, f)
		back, err := policyfix.Parse(f.Name, []byte(raw))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !reflect.DeepEqual(f, back) {
			t.Fatalf("seed %d reads back otherwise: %s", seed, firstDifference(raw, jsonOf(t, back)))
		}
	}
}

func TestEveryFeatureAppears(t *testing.T) {
	counts := map[string]int{}
	for _, f := range generated {
		for feature, used := range features(f) {
			if used {
				counts[feature]++
			}
		}
	}
	var missing []string
	for _, want := range wantedFeatures() {
		// Each one in 2% of the fixtures at least: a feature that shows up once is luck, not coverage.
		if counts[want] < seeds/50 {
			missing = append(missing, fmt.Sprintf("%s (%d)", want, counts[want]))
		}
	}
	if len(missing) > 0 {
		t.Errorf("features too rare in %d fixtures: %s", seeds, strings.Join(missing, ", "))
	}
	var lines []string
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		lines = append(lines, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	t.Logf("fixtures with each feature, of %d: %s", seeds, strings.Join(lines, " "))
}

// The property compares two deciders; it proves something only when their answers vary. The reference evaluator measures
// what the generated questions get.
func TestAllowedAndDeniedAnswersAreCommon(t *testing.T) {
	decisions := map[string]int{}
	var total, lifted, assumed, bothInOneFixture int
	for _, f := range generated {
		ev := refeval.New(f)
		own := map[string]int{}
		for ci := range f.Cases {
			c := &f.Cases[ci]
			for qi := range c.Questions {
				a := ev.Decide(c, &c.Questions[qi])
				decisions[a.Decision]++
				own[a.Decision]++
				total++
				for _, d := range a.Denials {
					if len(d.LiftedBy) > 0 {
						lifted++
					}
					if d.UnresolvedParent {
						assumed++
					}
				}
			}
		}
		if own[policyfix.DecisionAllowed] > 0 && own[policyfix.DecisionDenied] > 0 {
			bothInOneFixture++
		}
	}
	share := func(decision string) float64 { return float64(decisions[decision]) / float64(total) }
	t.Logf("%d questions: allowed %.1f%%, denied %.1f%%, no access %.1f%%, resource not found %.1f%%, type not found %.1f%%; "+
		"%d denials lifted by an exception, %d assumed above an unresolved parent; %d of %d fixtures have allowed and denied answers",
		total, 100*share(policyfix.DecisionAllowed), 100*share(policyfix.DecisionDenied), 100*share(policyfix.DecisionNoAccess),
		100*share(policyfix.DecisionResourceNotFound), 100*share(policyfix.DecisionTypeNotFound), lifted, assumed, bothInOneFixture, seeds)
	// The bounds leave room below what the generator gives today (about 27% allowed, 20% denied, 35% no access, and both
	// allowed and denied answers in 94% of the fixtures), so that a change of the reference evaluator does not trip them.
	switch {
	case share(policyfix.DecisionAllowed) < 0.2:
		t.Errorf("too few allowed answers: %.1f%%", 100*share(policyfix.DecisionAllowed))
	case share(policyfix.DecisionDenied) < 0.12:
		t.Errorf("too few denied answers: %.1f%%", 100*share(policyfix.DecisionDenied))
	case share(policyfix.DecisionNoAccess) < 0.2:
		t.Errorf("too few answers with no access: %.1f%%", 100*share(policyfix.DecisionNoAccess))
	case bothInOneFixture < seeds*85/100:
		t.Errorf("only %d of %d fixtures have both allowed and denied answers", bothInOneFixture, seeds)
	case lifted < seeds || assumed < seeds:
		t.Errorf("too few exceptions that lift a denial (%d) or denials assumed above an unresolved parent (%d)", lifted, assumed)
	}
}

// ---------- the shape of a fixture ----------

// shapeProblems checks the sizes the generator promises, and that the questions expect nothing.
func shapeProblems(f *policyfix.Fixture) []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	inRange := func(what string, n, lo, hi int) {
		if n < lo || n > hi {
			bad("%d %s, want %d-%d", n, what, lo, hi)
		}
	}
	if !slices.Equal(f.Contracts, []string{policygen.Contract}) {
		bad("contracts %v", f.Contracts)
	}
	if len(f.Services) != 2 || f.Services[0] == f.Services[1] {
		bad("services %v", f.Services)
	}
	inRange("types", len(f.Types), 4, 7)
	inRange("resources", len(f.Resources), 15, 40)
	inRange("rules", len(f.Rules), 20, 60)
	inRange("cases", len(f.Cases), 6, 12)
	notSync := 0
	syncOf := map[string]int{}
	for _, typ := range f.Types {
		if typ.Sync {
			syncOf[typ.Service]++
		} else {
			notSync++
		}
	}
	inRange("types without a synchronizer", notSync, 1, 2)
	for _, s := range f.Services {
		if syncOf[s] == 0 {
			bad("service %s has no type with a synchronizer", s)
		}
	}
	asked := map[string]bool{}
	for _, c := range f.Cases {
		inRange("questions in "+c.Name, len(c.Questions), 10, 30)
		asked[f.ServiceOf(&c)] = true
		if c.Claims["preferred_username"] != c.Subject {
			bad("case %s: preferred_username %v is not the subject %s", c.Name, c.Claims["preferred_username"], c.Subject)
		}
		for _, q := range c.Questions {
			if q.Allowed || q.Decision != "" || q.GrantedBy != nil || q.Denials != nil {
				bad("case %s: a question expects something: %+v", c.Name, q)
			}
		}
	}
	if len(asked) != 2 {
		bad("the cases are asked of %d services", len(asked))
	}
	return out
}

// apiProblems checks what the runner of the engine sends through the admin API with the validation of its inputs, and
// what the generator promises beyond them: a group nests only groups before it (never itself, which the API refuses, and
// never in a cycle), a resource names parents of types with a synchronizer only, the request attributes are what the
// check API takes, and no claim is one the token sets itself.
func apiProblems(f *policyfix.Fixture) []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	ids := map[string]uuid.UUID{}
	id := func(name string) *uuid.UUID {
		if _, ok := ids[name]; !ok {
			ids[name] = uuid.New()
		}
		v := ids[name]
		return &v
	}
	for _, s := range f.Services {
		if !model.ValidSlug(s) || s == model.PlatformSlug {
			bad("service slug %q", s)
		}
	}
	types := map[string]*policyfix.Type{}
	for i := range f.Types {
		typ := &f.Types[i]
		types[typ.Name] = typ
		in := model.TypeResourceInput{Name: typ.Name, AccessImplies: typ.Implies}
		for _, p := range typ.Parents {
			if p.Inherit != nil && len(p.Inherit) == 0 {
				bad("type %s: an empty inherit reads back as none", typ.Name)
			}
			in.Parents = append(in.Parents, model.TypeParent{TypeResourceID: *id("type " + p.Type), Inherit: slices.Clone(p.Inherit)})
		}
		if err := in.Validate(); err != nil {
			bad("type %s: %v", typ.Name, err)
		}
	}
	for _, r := range f.Resources {
		for _, p := range r.Parents {
			if !types[p.Type].Sync {
				bad("resource %s/%s names a parent of %s, which has no synchronizer", r.Type, r.ID, p.Type)
			}
		}
		for k := range r.Meta {
			if !model.ValidMetaKey(k) {
				bad("resource %s/%s: attribute %q", r.Type, r.ID, k)
			}
		}
	}
	for _, g := range f.AccessGroups {
		in := model.AccessGroupInput{Name: g.Name, Levels: g.Levels}
		if err := in.Validate(); err != nil {
			bad("access group %s/%s: %v", g.Type, g.Name, err)
		}
	}
	seen := map[string]bool{}
	for _, g := range f.Groups {
		for _, m := range g.Members {
			if m.Group != "" && !seen[m.Group] {
				bad("group %s nests %s, which is not before it", g.Name, m.Group)
			}
			in := model.GroupMemberInput{Predicate: m.Predicate, ValidFrom: m.ValidFrom, ExpiresAt: m.ExpiresAt}
			if m.Group != "" {
				in.MemberGroupID = id("group " + m.Group)
			}
			if err := in.Validate(); err != nil {
				bad("group %s: a member: %v", g.Name, err)
			}
		}
		seen[g.Name] = true
	}
	fromClaim := func(fc *policyfix.FromClaim) *model.ResourceFromClaim {
		if fc == nil {
			return nil
		}
		matchBy := fc.MatchBy
		if matchBy == "" {
			matchBy = model.MatchByName
		}
		if _, isMeta := model.MetaKeyOf(matchBy); matchBy != model.MatchByName && matchBy != model.MatchByID && !isMeta {
			bad("match_by %q", fc.MatchBy)
		}
		return &model.ResourceFromClaim{Claim: fc.Claim, LookupTypeResourceID: *id("type " + fc.LookupType), MatchBy: matchBy}
	}
	resourceID := func(t *policyfix.Target) *string {
		if t.Resource == "" {
			return nil
		}
		return &t.Resource
	}
	for _, r := range f.Roles {
		for i, item := range r.Items {
			in := model.RolePermissionInput{
				TypeResourceID: *id("type " + item.Type), ResourceID: resourceID(&item.Target), ResourceFromClaim: fromClaim(item.FromClaim),
				ResourceWhere: item.Where, Access: item.Access,
			}
			if err := in.Validate(); err != nil {
				bad("role %s item %d: %v", r.Name, i, err)
			}
		}
	}
	for _, r := range f.Rules {
		in := model.AssignmentRuleInput{
			Name: &r.Name, Exception: r.Exception, ValidFrom: r.ValidFrom, ExpiresAt: r.ExpiresAt, RequiresPredicate: r.RequiresPredicate,
			Effect: model.EffectAllow, Predicate: r.Predicate,
		}
		if r.Deny {
			in.Effect = model.EffectDeny
		}
		if r.OnMissing != "" {
			in.OnMissingAttribute = &r.OnMissing
		}
		switch {
		case r.Subject != "":
			in.Subject = &r.Subject
			if strings.HasPrefix(r.Subject, model.GroupSubjectPrefix) {
				bad("rule %s: the subject %s would be taken for a group", r.Name, r.Subject)
			}
		case r.SubjectGroup != "":
			in.Subject = ptr(model.GroupSubject(*id("group " + r.SubjectGroup)))
		case r.MemberOf != "":
			in.MemberOfGroupID = id("group " + r.MemberOf)
		}
		if r.Role != "" {
			in.RoleID = id("role " + r.Role)
		} else {
			in.TypeResourceID, in.ResourceID, in.ResourceFromClaim, in.ResourceWhere = id("type "+r.Type), resourceID(r.Target), fromClaim(r.FromClaim), r.Where
			in.Access = r.Access
		}
		if err := in.Validate(); err != nil {
			bad("rule %s: %v", r.Name, err)
		}
	}
	for _, c := range f.Cases {
		if err := model.RequestContext(c.Context).Validate(); err != nil {
			bad("case %s: context: %v", c.Name, err)
		}
		for name := range c.Claims {
			switch name {
			case "sub", "iss", "aud", "exp", "iat", "nbf", "azp", "client_id":
				bad("case %s: the claim %s is the token's own", c.Name, name)
			}
		}
	}
	return out
}

// ---------- features ----------

// wantedFeatures are the features every run of the tests must see (see features).
func wantedFeatures() []string {
	out := []string{
		"type/several-parents", "type/inherit-none", "type/inherit-mask", "type/implies", "type/not-sync", "type/two-not-sync",
		"type/not-sync-above-sync", "type/link-across-services",
		"resource/several-parents", "resource/two-parents-of-a-type", "resource/no-parent", "resource/unresolved",
		"resource/unresolved-above", "resource/shared-name", "resource/no-name", "resource/meta-string", "resource/meta-list",
		"access-group",
		"member/subject", "member/predicate", "member/everybody", "member/group", "member/nested-two-levels", "member/disabled",
		"member/valid-from", "member/expires-at",
		"item/whole", "item/resource", "item/from-claim", "item/where", "item/access-group",
		"rule/subject", "rule/outsider", "rule/subject-group", "rule/predicate", "rule/everybody", "rule/member-of", "rule/role",
		"rule/role-deny", "rule/role-exception", "rule/whole", "rule/resource", "rule/missing-resource", "rule/missing-parent",
		"rule/from-claim-default", "rule/from-claim-name", "rule/from-claim-id", "rule/from-claim-meta", "rule/from-claim-below",
		"rule/where", "rule/requires-predicate", "rule/requires-group", "rule/deny", "rule/deny-wildcard", "rule/exception",
		"rule/on-missing-skip", "rule/on-missing-apply", "rule/allow-on-missing-skip", "rule/valid-from", "rule/expires-at",
		"rule/window", "rule/disabled", "rule/access-group", "rule/deny-access-group",
		"source/claim-default", "source/claim", "source/context", "source/now-hour", "source/now-weekday",
		"value/list-eq", "value/scalar-in",
		"case/first-service-default", "case/first-service-named", "case/second-service", "case/missing-claim", "case/null-claim",
		"case/no-context", "case/missing-context", "case/null-context", "case/number-claim", "case/bool-claim", "case/list-claim",
		"case/at-now", "case/at-boundary", "case/beside-boundary", "case/hours-away",
		"question/resource", "question/missing-resource", "question/unresolved-parent-id", "question/whole-type",
		"question/not-sync", "question/other-service", "question/no-such-type", "question/rare-level",
	}
	for _, op := range []string{
		model.OpEq, model.OpNe, model.OpIn, model.OpNotIn, model.OpRegex, model.OpGt, model.OpGe, model.OpLt, model.OpLe, model.OpInCIDR,
	} {
		out = append(out, "op/"+op, "where-op/"+op)
	}
	return out
}

// features returns the features a fixture uses.
func features(f *policyfix.Fixture) map[string]bool {
	has := map[string]bool{}
	types := map[string]*policyfix.Type{}
	for i := range f.Types {
		types[f.Types[i].Name] = &f.Types[i]
	}
	notSync := 0
	for _, typ := range f.Types {
		if !typ.Sync {
			notSync++
		}
		has["type/several-parents"] = has["type/several-parents"] || len(typ.Parents) > 1
		has["type/implies"] = has["type/implies"] || len(typ.Implies) > 0
		for _, p := range typ.Parents {
			has["type/inherit-none"] = has["type/inherit-none"] || p.Inherit == nil
			has["type/inherit-mask"] = has["type/inherit-mask"] || p.Inherit != nil
			has["type/link-across-services"] = has["type/link-across-services"] || types[p.Type].Service != typ.Service
			has["type/not-sync-above-sync"] = has["type/not-sync-above-sync"] || !types[p.Type].Sync && typ.Sync
		}
	}
	has["type/not-sync"] = notSync > 0
	has["type/two-not-sync"] = notSync > 1

	exists := map[string]bool{}
	missing := map[string]bool{}
	names := map[string]int{}
	for _, r := range f.Resources {
		exists[r.Type+"/"+r.ID] = true
		if r.Name != "" {
			names[r.Type+"/"+r.Name]++
		} else {
			has["resource/no-name"] = true
		}
	}
	isParent := map[string]bool{}
	for _, r := range f.Resources {
		perType := map[string]int{}
		for _, p := range r.Parents {
			perType[p.Type]++
			isParent[p.Type+"/"+p.ID] = true
			if !exists[p.Type+"/"+p.ID] {
				missing[p.Type+"/"+p.ID] = true
				has["resource/unresolved"] = true
			}
		}
		has["resource/several-parents"] = has["resource/several-parents"] || len(r.Parents) > 1
		has["resource/no-parent"] = has["resource/no-parent"] || len(r.Parents) == 0 && len(types[r.Type].Parents) > 0
		for _, n := range perType {
			has["resource/two-parents-of-a-type"] = has["resource/two-parents-of-a-type"] || n > 1
		}
		for _, v := range r.Meta {
			if _, ok := v.([]any); ok {
				has["resource/meta-list"] = true
			} else {
				has["resource/meta-string"] = true
			}
		}
	}
	for _, r := range f.Resources {
		for _, p := range r.Parents {
			if missing[p.Type+"/"+p.ID] && isParent[r.Type+"/"+r.ID] {
				has["resource/unresolved-above"] = true
			}
		}
	}
	for _, n := range names {
		has["resource/shared-name"] = has["resource/shared-name"] || n > 1
	}
	has["access-group"] = len(f.AccessGroups) > 0

	conditions := func(prefix string, conds []model.PredicateCondition) {
		for _, c := range conds {
			has[prefix+c.Op] = true
			if prefix == "where-op/" {
				continue
			}
			switch c.Source {
			case "":
				has["source/claim-default"] = true
			case model.SourceNow:
				has["source/now-"+c.Attr] = true
			default:
				has["source/"+c.Source] = true
			}
			has["value/list-eq"] = has["value/list-eq"] || c.Op == model.OpEq && c.Value.IsList
			has["value/scalar-in"] = has["value/scalar-in"] || (c.Op == model.OpIn || c.Op == model.OpNotIn) && !c.Value.IsList
		}
	}
	depth := map[string]int{}
	for _, g := range f.Groups {
		for _, m := range g.Members {
			switch {
			case m.Subject != "":
				has["member/subject"] = true
			case m.Everybody:
				has["member/everybody"] = true
			case m.Group != "":
				has["member/group"] = true
				depth[g.Name] = max(depth[g.Name], depth[m.Group]+1)
			default:
				has["member/predicate"] = true
			}
			conditions("op/", m.Predicate)
			has["member/disabled"] = has["member/disabled"] || m.Disabled
			has["member/valid-from"] = has["member/valid-from"] || m.ValidFrom != nil
			has["member/expires-at"] = has["member/expires-at"] || m.ExpiresAt != nil
		}
	}
	for _, d := range depth {
		has["member/nested-two-levels"] = has["member/nested-two-levels"] || d >= 2
	}

	target := func(prefix string, t *policyfix.Target, access []string) {
		switch {
		case t.Resource != "":
			has[prefix+"resource"] = true
			if !exists[t.Type+"/"+t.Resource] {
				has[prefix+"missing-resource"] = true
			}
			if missing[t.Type+"/"+t.Resource] {
				has[prefix+"missing-parent"] = true
			}
		case t.FromClaim != nil:
			has[prefix+"from-claim"] = true
			switch fc := t.FromClaim; {
			case fc.MatchBy == "":
				has[prefix+"from-claim-default"] = true
			case strings.HasPrefix(fc.MatchBy, model.MatchByMetaPrefix):
				has[prefix+"from-claim-meta"] = true
			default:
				has[prefix+"from-claim-"+fc.MatchBy] = true
			}
			if t.FromClaim.LookupType != t.Type {
				has[prefix+"from-claim-below"] = true
			}
		case len(t.Where) > 0:
			has[prefix+"where"] = true
			conditions("where-op/", t.Where)
		default:
			has[prefix+"whole"] = true
		}
		for _, a := range access {
			if strings.HasPrefix(a, model.AccessGroupPrefix) {
				has[prefix+"access-group"] = true
			}
		}
	}
	for _, r := range f.Roles {
		for i := range r.Items {
			target("item/", &r.Items[i].Target, r.Items[i].Access)
		}
	}
	for _, r := range f.Rules {
		switch {
		case r.Subject != "":
			has["rule/subject"] = true
			has["rule/outsider"] = has["rule/outsider"] || !slices.ContainsFunc(f.Cases, func(c policyfix.Case) bool { return c.Subject == r.Subject })
		case r.SubjectGroup != "":
			has["rule/subject-group"] = true
		case r.MemberOf != "":
			has["rule/member-of"] = true
		case r.Everybody:
			has["rule/everybody"] = true
		default:
			has["rule/predicate"] = true
		}
		conditions("op/", r.Predicate)
		conditions("op/", r.RequiresPredicate)
		if r.Role != "" {
			has["rule/role"] = true
			has["rule/role-deny"] = has["rule/role-deny"] || r.Deny
			has["rule/role-exception"] = has["rule/role-exception"] || r.Exception
		} else {
			target("rule/", r.Target, r.Access)
			if r.Deny && slices.ContainsFunc(r.Access, func(a string) bool { return strings.HasPrefix(a, model.AccessGroupPrefix) }) {
				has["rule/deny-access-group"] = true
			}
			has["rule/deny-wildcard"] = has["rule/deny-wildcard"] || slices.Contains(r.Access, model.AllLevels)
		}
		has["rule/deny"] = has["rule/deny"] || r.Deny
		has["rule/exception"] = has["rule/exception"] || r.Exception
		switch {
		case r.OnMissing == model.OnMissingApply:
			has["rule/on-missing-apply"] = true
		case r.OnMissing == model.OnMissingSkip && r.Deny:
			has["rule/on-missing-skip"] = true
		case r.OnMissing == model.OnMissingSkip:
			has["rule/allow-on-missing-skip"] = true
		}
		has["rule/requires-predicate"] = has["rule/requires-predicate"] || len(r.RequiresPredicate) > 0
		has["rule/requires-group"] = has["rule/requires-group"] || r.RequiresGroup != ""
		has["rule/disabled"] = has["rule/disabled"] || r.Disabled
		has["rule/valid-from"] = has["rule/valid-from"] || r.ValidFrom != nil
		has["rule/expires-at"] = has["rule/expires-at"] || r.ExpiresAt != nil
		has["rule/window"] = has["rule/window"] || r.ValidFrom != nil && r.ExpiresAt != nil
	}

	bounds := map[time.Time]bool{}
	for _, r := range f.Rules {
		for _, b := range []*time.Time{r.ValidFrom, r.ExpiresAt} {
			if b != nil {
				bounds[*b] = true
			}
		}
	}
	for _, g := range f.Groups {
		for _, m := range g.Members {
			for _, b := range []*time.Time{m.ValidFrom, m.ExpiresAt} {
				if b != nil {
					bounds[*b] = true
				}
			}
		}
	}
	for _, c := range f.Cases {
		switch c.Service {
		case "":
			has["case/first-service-default"] = true
		case f.Services[0]:
			has["case/first-service-named"] = true
		default:
			has["case/second-service"] = true
		}
		for _, claim := range []string{"department", "region", "level", "vip", "teams", "team", "home", "home_id"} {
			v, ok := c.Claims[claim]
			has["case/missing-claim"] = has["case/missing-claim"] || !ok
			has["case/null-claim"] = has["case/null-claim"] || ok && v == nil
		}
		for _, v := range c.Claims {
			switch v.(type) {
			case float64:
				has["case/number-claim"] = true
			case bool:
				has["case/bool-claim"] = true
			case []any:
				has["case/list-claim"] = true
			}
		}
		if c.Context == nil {
			has["case/no-context"] = true
		}
		for _, attr := range []string{"ip", "channel", "risk"} {
			v, ok := c.Context[attr]
			has["case/missing-context"] = has["case/missing-context"] || c.Context != nil && !ok
			has["case/null-context"] = has["case/null-context"] || ok && v == nil
		}
		switch at := f.NowOf(&c); {
		case c.At == nil:
			has["case/at-now"] = true
		case bounds[at]:
			has["case/at-boundary"] = true
		case bounds[at.Add(time.Second)] || bounds[at.Add(-time.Second)] || bounds[at.Add(time.Minute)] || bounds[at.Add(-time.Minute)]:
			has["case/beside-boundary"] = true
		default:
			has["case/hours-away"] = true
		}
		service := f.ServiceOf(&c)
		for _, q := range c.Questions {
			typ, ok := types[q.Type]
			switch {
			case !ok:
				has["question/no-such-type"] = true
			case typ.Service != service:
				has["question/other-service"] = true
			case q.Resource == "":
				has["question/whole-type"] = true
				has["question/not-sync"] = has["question/not-sync"] || !typ.Sync
			case missing[q.Type+"/"+q.Resource]:
				has["question/unresolved-parent-id"] = true
			case !exists[q.Type+"/"+q.Resource]:
				has["question/missing-resource"] = true
			default:
				has["question/resource"] = true
			}
			has["question/rare-level"] = has["question/rare-level"] || q.Action == "approve"
		}
	}
	return has
}

// ---------- helpers ----------

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// firstDifference shows where two texts part.
func firstDifference(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	from := max(0, i-120)
	return fmt.Sprintf("at %d:\n  …%s\n  …%s", i, a[from:min(len(a), i+120)], b[from:min(len(b), i+120)])
}

func ptr[T any](v T) *T { return &v }

// The checks below keep the conditions the generator writes within what the admin API takes, beyond the validation of the
// inputs above: they are the values a condition compares with, parsed as the engine parses them.
func TestTheConditionValuesParse(t *testing.T) {
	for seed, f := range generated {
		for _, c := range allConditions(f) {
			values := c.Value.List
			if !c.Value.IsList {
				values = []string{c.Value.Str}
			}
			for _, v := range values {
				var err error
				switch c.Op {
				case model.OpRegex:
					_, err = regexp.Compile(v)
				case model.OpGt, model.OpGe, model.OpLt, model.OpLe:
					_, err = strconv.ParseFloat(v, 64)
				case model.OpInCIDR:
					_, _, err = net.ParseCIDR(v)
				}
				if err != nil {
					t.Errorf("seed %d: %s %s %q: %v", seed, c.Attr, c.Op, v, err)
				}
			}
		}
	}
}

// allConditions returns every condition of a fixture.
func allConditions(f *policyfix.Fixture) []model.PredicateCondition {
	var out []model.PredicateCondition
	for _, g := range f.Groups {
		for _, m := range g.Members {
			out = append(out, m.Predicate...)
		}
	}
	for _, r := range f.Roles {
		for _, item := range r.Items {
			out = append(out, item.Where...)
		}
	}
	for _, r := range f.Rules {
		out = append(out, r.Predicate...)
		out = append(out, r.RequiresPredicate...)
		if r.Target != nil {
			out = append(out, r.Where...)
		}
	}
	return out
}
