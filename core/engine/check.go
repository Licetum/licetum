package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/licetum/licetum/core/model"
)

// Decision codes of an explanation.
const (
	DecisionAllowed          = "allowed"
	DecisionDenied           = "denied"
	DecisionNoAccess         = "no_access"
	DecisionResourceNotFound = "resource_not_found"
	DecisionTypeNotFound     = "type_not_found"
)

// checkResultFormat is the version of the cached check answers (see cachedAnswer): it changes with their format.
const checkResultFormat = "t3"

// CheckAccess answers the questions "may the caller do X with Y" without building the whole permission map: the
// rules are loaded once, and the questions are answered together, each from the ancestors of its resource. The
// rules, the hierarchy and the decision are exactly those of GetPermission. The answers are cached per subject,
// the attributes of the request the rules look at, and the questions, as the permissions are (see cachedAnswer).
func (a *App) CheckAccess(ctx context.Context, serviceID uuid.UUID, subject string, env Env, requests []model.CheckRequest, explain bool, consistency Consistency) ([]model.CheckResult, error) {
	questions, err := json.Marshal(requests)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(questions)
	if err := a.replicaGate(); err != nil {
		return nil, err
	}
	state := a.cacheState(ctx).forConsistency(ctx, consistency)
	key := fmt.Sprintf("authz:check:%s:r%d:%s:%s:%s:%t:%s", checkResultFormat, state.version, serviceID, subject,
		a.requestFingerprint(ctx, state, env), explain, hex.EncodeToString(sum[:]))
	results, err := cachedAnswer(ctx, a, key, state, consistency, a.metrics.checkCache,
		func(ctx context.Context) (computed[[]model.CheckResult], error) {
			if err := a.serviceExists(ctx, state, serviceID); err != nil {
				return computed[[]model.CheckResult]{}, err
			}
			results, deps, ttl, hold, err := a.checkAccess(ctx, serviceID, subject, env, requests, explain, state, consistency)
			if err != nil {
				return computed[[]model.CheckResult]{}, err
			}
			return computed[[]model.CheckResult]{value: results, deps: deps, ttl: ttl, hold: hold}, nil
		})
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.Allowed {
			a.metrics.checks.WithLabelValues("allowed").Inc()
		} else {
			a.metrics.checks.WithLabelValues("denied").Inc()
		}
	}
	return results, nil
}

// checkAccess computes the answers of one request (the cache around it is in CheckAccess).
func (a *App) checkAccess(ctx context.Context, serviceID uuid.UUID, subject string, env Env, requests []model.CheckRequest, explain bool, state cacheState, consistency Consistency) ([]model.CheckResult, map[string]int64, time.Duration, time.Duration, error) {
	asked := make([]string, len(requests))
	for i, r := range requests {
		asked[i] = r.Type
	}
	p, err := a.prepare(ctx, serviceID, subject, env, state, consistency, asked)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	results, err := a.decideChecks(ctx, p, serviceID, requests, explain)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	return results, p.deps, p.ttl, p.hold, nil
}

// checkInputs is what the questions of one request are answered from, read once for all of them.
type checkInputs struct {
	rows       map[anchorRef]uuid.UUID              // the resources the questions name
	ancestors  map[uuid.UUID]map[uuid.UUID]LevelSet // per resource row: its ancestors, with the levels flowing from them
	unresolved map[uuid.UUID][]UnresolvedLink       // per resource row: the unresolved links at or above it
}

// decideChecks answers the questions of one request. The resources they name, their ancestors and their unresolved
// parent links are read in a fixed number of queries, not once per question.
func (a *App) decideChecks(ctx context.Context, p *Prepared, serviceID uuid.UUID, requests []model.CheckRequest, explain bool) ([]model.CheckResult, error) {
	typeIDs := make([]uuid.UUID, len(requests))
	found := make([]bool, len(requests))
	var refs []anchorRef
	askedTypes := map[uuid.UUID]bool{}
	for i, r := range requests {
		id, ok := p.Graph.TypeID(serviceID, r.Type)
		if !ok {
			continue
		}
		typeIDs[i], found[i] = id, true
		askedTypes[id] = true
		if r.ResourceID != nil {
			refs = append(refs, anchorRef{Type: id, Ext: *r.ResourceID})
		}
	}

	in := &checkInputs{}
	var unresolved map[uuid.UUID][]UnresolvedLink
	eg, ectx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		in.rows, err = a.anchorRowIDs(ectx, refs)
		return err
	})
	eg.Go(func() (err error) {
		unresolved, err = a.UnresolvedParents(ectx, Keys(askedTypes))
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	rowIDs := make([]uuid.UUID, 0, len(in.rows))
	for _, id := range in.rows {
		rowIDs = append(rowIDs, id)
	}
	var err error
	if in.ancestors, err = a.AncestorMasks(ctx, rowIDs); err != nil {
		return nil, err
	}
	in.unresolved = unresolved

	out := make([]model.CheckResult, len(requests))
	for i, r := range requests {
		out[i] = p.answerCheck(in, typeIDs[i], found[i], r, explain)
	}
	return out, nil
}

// answerCheck answers one question. A question about a type that does not exist is type_not_found.
func (p *Prepared) answerCheck(in *checkInputs, typeID uuid.UUID, found bool, r model.CheckRequest, explain bool) model.CheckResult {
	answer := func(decision string, allowed bool, lv *levelVerdict, covers []Cover) model.CheckResult {
		res := model.CheckResult{Allowed: allowed}
		if explain {
			res.Explanation = p.explanation(decision, lv, covers)
		}
		return res
	}
	if !found {
		return answer(DecisionTypeNotFound, false, nil, nil)
	}

	var verdict nodeVerdict
	var covers []Cover
	if r.ResourceID == nil {
		// The type as a whole: only the rules anchored on the type itself count.
		verdict = p.engine.decideWhole(typeID)
		for i := range p.Insts {
			if p.Insts[i].Whole() && p.Insts[i].AnchorType == typeID {
				covers = append(covers, Cover{Inst: i, Mask: AllLevels})
			}
		}
	} else {
		node, ok := in.rows[anchorRef{Type: typeID, Ext: *r.ResourceID}]
		if !ok {
			return answer(DecisionResourceNotFound, false, nil, nil)
		}
		covers = p.CoversOf(typeID, in.ancestors[node], in.unresolved[node])
		verdict = p.engine.decide(typeID, covers)
	}

	i := slices.IndexFunc(verdict.Levels, func(lv levelVerdict) bool { return lv.Level == r.Action })
	switch {
	case i < 0:
		return answer(DecisionNoAccess, false, nil, covers)
	case verdict.Levels[i].Allowed:
		return answer(DecisionAllowed, true, &verdict.Levels[i], covers)
	default:
		return answer(DecisionDenied, false, &verdict.Levels[i], covers)
	}
}

// CoversOf finds the instances that cover one resource of type typeID, given its ancestors with the levels that
// flow from them, and the unresolved links at or above it (see UnresolvedParents).
func (p *Prepared) CoversOf(typeID uuid.UUID, ancestors map[uuid.UUID]LevelSet, unresolved []UnresolvedLink) []Cover {
	covers := []Cover{}
	for i := range p.Insts {
		in := &p.Insts[i]
		if in.Whole() {
			if in.AnchorType == typeID {
				covers = append(covers, Cover{Inst: i, Mask: AllLevels})
			} else if m, ok := p.Graph.DescendantsOf(in.AnchorType)[typeID]; ok {
				covers = append(covers, Cover{Inst: i, Mask: m})
			}
			continue
		}
		if m, ok := ancestors[*in.AnchorRow]; ok {
			covers = append(covers, Cover{Inst: i, Mask: m})
			continue
		}
		if !in.Deny {
			continue
		}
		// A denial may reach a resource whose hierarchy is not fully known.
		if m := p.Graph.assumedMask(in.AnchorType, unresolved); !m.empty() {
			covers = append(covers, Cover{Inst: i, Mask: m, Assumed: true})
		}
	}
	return covers
}

// RuleRef describes an instance for an explanation.
func (p *Prepared) RuleRef(inst int) model.RuleRef {
	in := &p.Insts[inst]
	typeName := in.AnchorType.String()
	if n, ok := p.Graph.Node(in.AnchorType); ok {
		typeName = n.Name
	}
	ref := model.RuleRef{
		Effect: model.EffectAllow, Exception: in.Exception, Access: slices.Clone(in.Levels),
		Anchor: model.Anchor{Type: typeName}, AppliesTo: in.Note,
	}
	if in.Rule != nil {
		ref.RuleID = in.Rule.ID
	}
	if in.Deny {
		ref.Effect = model.EffectDeny
	}
	if !in.Whole() {
		ext := in.AnchorExt
		ref.Anchor.ResourceID = &ext
	}
	return ref
}

// explanation builds the explanation of a verdict on one level.
func (p *Prepared) explanation(decision string, lv *levelVerdict, covers []Cover) *model.Explanation {
	e := &model.Explanation{Decision: decision, GrantedBy: []model.RuleRef{}, Denials: []model.DenialRef{}}
	if lv == nil {
		return e
	}
	for _, i := range lv.GrantedBy {
		e.GrantedBy = append(e.GrantedBy, p.RuleRef(i))
	}
	for _, d := range lv.Denials {
		ref := model.DenialRef{Rule: p.RuleRef(d.Inst), Level: d.Level, LiftedBy: []model.RuleRef{},
			MissingAttribute: p.Insts[d.Inst].Missing}
		for _, x := range d.LiftedBy {
			ref.LiftedBy = append(ref.LiftedBy, p.RuleRef(x))
		}
		ref.UnresolvedParent = slices.ContainsFunc(covers, func(c Cover) bool { return c.Inst == d.Inst && c.Assumed })
		e.Denials = append(e.Denials, ref)
	}
	return e
}

// RefOf is ruleRef for an instance that is not in the list (a rule found by a claim).
func (p *Prepared) RefOf(in *Instance) model.RuleRef {
	typeName := in.AnchorType.String()
	if n, ok := p.Graph.Node(in.AnchorType); ok {
		typeName = n.Name
	}
	ref := model.RuleRef{
		RuleID: in.Rule.ID, Effect: model.EffectAllow, Exception: in.Exception, Access: slices.Clone(in.Levels),
		Anchor: model.Anchor{Type: typeName}, AppliesTo: in.Note,
	}
	if in.Deny {
		ref.Effect = model.EffectDeny
	}
	return ref
}
