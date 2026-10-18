package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

const (
	// authzenPageMax is the most results of one page of a search.
	authzenPageMax = 1000
	// authzenSubjectScan bounds how many known subjects one request of the subject search goes through; the page token
	// continues from where it stopped.
	authzenSubjectScan = 1000
	// authzenScanBatch is how many known subjects are read and decided at a time.
	authzenScanBatch = 100
	// authzenScanWorkers is how many known subjects are decided at the same time.
	authzenScanWorkers = 8
)

// badRequest turns a validation error (422) into the 400 the specification has for a request that is not valid.
func badRequest(err error) *apperr.Error {
	ae, ok := apperr.As(err)
	if !ok {
		return apperr.Internal()
	}
	c := *ae
	if c.Status == http.StatusUnprocessableEntity {
		c.Status = http.StatusBadRequest
	}
	return &c
}

// ---------- evaluations ----------

// evaluation is one question of an Access Evaluation(s) request while it is being answered.
type evaluation struct {
	req     model.CheckRequest
	sp      speaker
	attrs   model.RequestContext
	explain bool
	// err is why the question could not be decided; the decision is then false.
	err  *apperr.Error
	resp model.AuthzenEvaluationResponse
}

// prepare fills an evaluation from the item and the defaults of the batch, and checks it.
func (c *authzenCaller) prepare(ctx context.Context, e *evaluation, item model.AuthzenEvaluationRequest, d model.AuthzenEvaluationsRequest) *apperr.Error {
	if item.Subject == nil {
		item.Subject = d.Subject
	}
	if item.Resource == nil {
		item.Resource = d.Resource
	}
	if item.Action == nil {
		item.Action = d.Action
	}
	if item.Context == nil {
		item.Context = d.Context
	}
	if item.Subject == nil || item.Resource == nil || item.Action == nil {
		return apperr.BadRequest(i18n.AuthzenIncomplete)
	}
	// An ordered list, not a map: with several fields missing the same one is always reported.
	for _, f := range []struct {
		name  string
		empty bool
	}{{"subject.id", item.Subject.ID == ""}, {"resource.type", item.Resource.Type == ""}, {"action.name", item.Action.Name == ""}} {
		if f.empty {
			return apperr.BadRequest(i18n.AuthzenFieldRequired, f.name)
		}
	}
	sp, aerr := c.speaker(ctx, item.Subject)
	if aerr != nil {
		return aerr
	}
	e.sp = sp
	e.req = model.CheckRequest{Type: item.Resource.Type, Action: item.Action.Name}
	if item.Resource.ID != "" {
		id := item.Resource.ID
		e.req.ResourceID = &id
	}
	e.explain, _ = item.Context["explain"].(bool)
	e.attrs = authzenAttributes(item.Context)
	if err := e.attrs.Validate(); err != nil {
		return badRequest(err)
	}
	return nil
}

// evaluate answers the items. The items that carry the same person and the same request attributes are decided in one
// round (CheckAccess takes one environment per call). A question that cannot be decided has err set and no decision;
// anything else that goes wrong fails the whole request.
func (c *authzenCaller) evaluate(ctx context.Context, consistency engine.Consistency, items []model.AuthzenEvaluationRequest, defaults model.AuthzenEvaluationsRequest) ([]evaluation, error) {
	evals := make([]evaluation, len(items))
	groups := map[string][]int{}
	var order []string
	for i, item := range items {
		e := &evals[i]
		if e.err = c.prepare(ctx, e, item, defaults); e.err != nil {
			continue
		}
		key := struct {
			Subject string
			Claims  engine.Claims
			Attrs   model.RequestContext
		}{Subject: e.sp.subject, Attrs: e.attrs}
		if e.sp.delegated {
			key.Claims = e.sp.claims
		}
		raw, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		if _, seen := groups[string(raw)]; !seen {
			order = append(order, string(raw))
		}
		groups[string(raw)] = append(groups[string(raw)], i)
	}

	for _, key := range order {
		idx := groups[key]
		first := evals[idx[0]]
		group := make([]model.CheckRequest, len(idx))
		anyExplain := false
		for j, i := range idx {
			group[j] = evals[i].req
			anyExplain = anyExplain || evals[i].explain
		}
		env := c.app.NewEnv(first.sp.claims, first.attrs)
		results, err := c.app.CheckAccess(ctx, c.serviceID, first.sp.subject, env, group, anyExplain, consistency)
		if err != nil {
			return nil, err
		}
		c.app.LogDecisions(ctx, c.serviceID, first.sp.subject, group, results, consistency)
		allowed := 0
		for j, i := range idx {
			evals[i].resp = model.AuthzenEvaluationResponse{Decision: results[j].Allowed}
			if results[j].Allowed {
				allowed++
			}
			if evals[i].explain && results[j].Explanation != nil {
				evals[i].resp.Context = map[string]any{"reason_admin": results[j].Explanation}
			}
		}
		c.note(first.sp, len(idx), allowed)
	}
	return evals, nil
}

// responseOf is the decision of an evaluation: a question that could not be decided is a denial that says why.
func responseOf(e *evaluation, lang i18n.Lang) model.AuthzenEvaluationResponse {
	if e.err == nil {
		return e.resp
	}
	return model.AuthzenEvaluationResponse{Context: map[string]any{
		"error": map[string]any{"status": e.err.Status, "message": e.err.Localize(lang).Detail},
	}}
}

// applySemantic returns the decisions of a batch up to the evaluation that stops it: with deny_on_first_deny the first
// denial (or error), with permit_on_first_permit the first permit. The stopping denial says so in its context.
func applySemantic(semantic string, evals []evaluation, lang i18n.Lang) []model.AuthzenEvaluationResponse {
	out := make([]model.AuthzenEvaluationResponse, 0, len(evals))
	for i := range evals {
		r := responseOf(&evals[i], lang)
		switch {
		case semantic == model.AuthzenDenyOnFirstDeny && !r.Decision:
			ctx := map[string]any{}
			for k, v := range r.Context {
				ctx[k] = v
			}
			ctx["code"], ctx["reason"] = "200", semantic
			r.Context = ctx
			return append(out, r)
		case semantic == model.AuthzenPermitOnFirstPermit && r.Decision:
			return append(out, r)
		}
		out = append(out, r)
	}
	return out
}

// authzenAttributes returns the request attributes of an AuthZEN context: every key but explain, which is the
// caller's request for the explanation of the decision.
func authzenAttributes(ctx map[string]any) model.RequestContext {
	attrs := model.RequestContext{}
	for name, v := range ctx {
		if name != "explain" {
			attrs[name] = v
		}
	}
	return attrs
}

// ---------- searches ----------

// pageCursor is what a page token holds: where the next page starts, and a digest of the request that gave it (the
// specification asks for an error when the request changes in the middle of a pagination).
type pageCursor struct {
	After string `json:"a"`
	Sum   string `json:"s"`
}

func encodeCursor(after, sum string) string {
	raw, _ := json.Marshal(pageCursor{After: after, Sum: sum})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor returns the key the page starts after: empty for the first page.
func decodeCursor(token, sum string) (string, *apperr.Error) {
	if token == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	var cur pageCursor
	if err != nil || json.Unmarshal(raw, &cur) != nil || cur.Sum != sum {
		return "", apperr.BadRequest(i18n.AuthzenPageInvalid)
	}
	return cur.After, nil
}

// pageLimit is the page size of a request: the asked one, at most authzenPageMax.
func pageLimit(p *model.AuthzenPageRequest) int {
	if p == nil || p.Limit == nil || *p.Limit <= 0 || *p.Limit > authzenPageMax {
		return authzenPageMax
	}
	return *p.Limit
}

// searchSum digests the parts of a search request that must not change between its pages. The identifier of the entity
// searched for is ignored by the API, so it is left out.
func searchSum(kind string, in model.AuthzenSearchRequest, limit int) string {
	type digest struct {
		Kind                                 string
		SubjectType, SubjectID               string
		ResourceType, ResourceID, ActionName string
		Context                              map[string]any
		Limit                                int
	}
	d := digest{Kind: kind, Context: in.Context, Limit: limit}
	if in.Subject != nil {
		d.SubjectType = in.Subject.Type
		if kind != "subject" {
			d.SubjectID = in.Subject.ID
		}
	}
	if in.Resource != nil {
		d.ResourceType = in.Resource.Type
		if kind != "resource" {
			d.ResourceID = in.Resource.ID
		}
	}
	if in.Action != nil {
		d.ActionName = in.Action.Name
	}
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// pageOf cuts the page that starts after the key from items, which are sorted by their keys. next is the token of the
// page after it, empty on the last page.
func pageOf[T any](items []T, key func(T) string, after string, limit int, sum string) (page []T, next string) {
	start := 0
	if after != "" {
		start = sort.Search(len(items), func(i int) bool { return key(items[i]) > after })
	}
	page = items[start:]
	if len(page) > limit {
		page = page[:limit]
		next = encodeCursor(key(page[len(page)-1]), sum)
	}
	return page, next
}

func pageInfo(count int, total *int, next string) *model.AuthzenPageResponse {
	return &model.AuthzenPageResponse{NextToken: next, Count: count, Total: total}
}

func intPtr(n int) *int { return &n }

// search is a search request, checked.
type search struct {
	in      model.AuthzenSearchRequest
	attrs   model.RequestContext
	limit   int
	after   string
	sum     string
	subject *model.AuthzenSubject
}

// parseSearch checks a search request of the kind: "subject", "resource" or "action".
func parseSearch(kind string, in model.AuthzenSearchRequest) (*search, *apperr.Error) {
	s := &search{in: in, limit: pageLimit(in.Page)}
	required := func(name string, present bool) *apperr.Error {
		if !present {
			return apperr.BadRequest(i18n.AuthzenFieldRequired, name)
		}
		return nil
	}
	if in.Subject == nil {
		return nil, apperr.BadRequest(i18n.AuthzenFieldRequired, "subject")
	}
	if in.Resource == nil {
		return nil, apperr.BadRequest(i18n.AuthzenFieldRequired, "resource")
	}
	checks := []struct {
		name    string
		present bool
	}{
		{"subject.type", in.Subject.Type != ""},
		{"subject.id", kind == "subject" || in.Subject.ID != ""},
		{"resource.type", in.Resource.Type != ""},
		{"action.name", kind == "action" || (in.Action != nil && in.Action.Name != "")},
	}
	for _, ch := range checks {
		if err := required(ch.name, ch.present); err != nil {
			return nil, err
		}
	}
	s.attrs = authzenAttributes(in.Context)
	if err := s.attrs.Validate(); err != nil {
		return nil, badRequest(err)
	}
	s.sum = searchSum(kind, in, s.limit)
	var token string
	if in.Page != nil {
		token = in.Page.Token
	}
	after, aerr := decodeCursor(token, s.sum)
	if aerr != nil {
		return nil, aerr
	}
	s.after = after
	s.subject = in.Subject
	return s, nil
}

// permissions reads what the speaker may do in the service.
func (c *authzenCaller) permissions(ctx context.Context, sp speaker, attrs model.RequestContext, consistency engine.Consistency) (engine.PermissionResult, error) {
	return c.app.GetPermission(ctx, c.serviceID, sp.subject, c.app.NewEnv(sp.claims, attrs), consistency)
}

func (c *authzenCaller) searchResources(ctx context.Context, consistency engine.Consistency, in model.AuthzenSearchRequest) (*model.AuthzenResourceSearchResponse, error) {
	s, aerr := parseSearch("resource", in)
	if aerr != nil {
		return nil, aerr
	}
	sp, aerr := c.speaker(ctx, s.subject)
	if aerr != nil {
		return nil, aerr
	}
	result, err := c.permissions(ctx, sp, s.attrs, consistency)
	if err != nil {
		return nil, err
	}
	var all []model.ResourceAccess
	// A type without resources (a permission such as a feature flag) has nothing to list; an unknown type has no answer.
	if tp, ok := result[in.Resource.Type]; ok && tp.Type != model.TypeModeNotSync {
		for _, r := range tp.Resources {
			if slices.Contains(r.Access, in.Action.Name) {
				all = append(all, r)
			}
		}
	}
	slices.SortFunc(all, func(a, b model.ResourceAccess) int { return strings.Compare(a.ID, b.ID) })
	page, next := pageOf(all, func(r model.ResourceAccess) string { return r.ID }, s.after, s.limit, s.sum)
	out := &model.AuthzenResourceSearchResponse{Page: pageInfo(len(page), intPtr(len(all)), next), Results: make([]model.AuthzenResource, len(page))}
	for i, r := range page {
		out.Results[i] = model.AuthzenResource{Type: in.Resource.Type, ID: r.ID, Properties: map[string]any{"name": r.Name}}
	}
	c.note(sp, 0, 0)
	return out, nil
}

func (c *authzenCaller) searchActions(ctx context.Context, consistency engine.Consistency, in model.AuthzenSearchRequest) (*model.AuthzenActionSearchResponse, error) {
	s, aerr := parseSearch("action", in)
	if aerr != nil {
		return nil, aerr
	}
	sp, aerr := c.speaker(ctx, s.subject)
	if aerr != nil {
		return nil, aerr
	}
	result, err := c.permissions(ctx, sp, s.attrs, consistency)
	if err != nil {
		return nil, err
	}
	levels, err := c.levelsOf(ctx, sp, s.attrs, consistency, result, in.Resource)
	if err != nil {
		return nil, err
	}
	slices.Sort(levels)
	levels = slices.Compact(levels)
	page, next := pageOf(levels, func(l string) string { return l }, s.after, s.limit, s.sum)
	out := &model.AuthzenActionSearchResponse{Page: pageInfo(len(page), intPtr(len(levels)), next), Results: make([]model.AuthzenAction, len(page))}
	for i, l := range page {
		out.Results[i] = model.AuthzenAction{Name: l}
	}
	c.note(sp, 0, 0)
	return out, nil
}

// levelsOf returns the access levels the speaker has on a resource, or on a resource type as a whole. For a type with
// resources the candidates are the levels the speaker has on any of its resources: a level no resource carries cannot
// be granted by a rule on the type, and the levels of the type itself are decided by the engine.
func (c *authzenCaller) levelsOf(ctx context.Context, sp speaker, attrs model.RequestContext, consistency engine.Consistency, result engine.PermissionResult, res *model.AuthzenResource) ([]string, error) {
	tp, ok := result[res.Type]
	if !ok {
		return nil, nil
	}
	if tp.Type == model.TypeModeNotSync {
		if res.ID != "" {
			return nil, nil
		}
		return slices.Clone(tp.Access), nil
	}
	if res.ID != "" {
		for _, r := range tp.Resources {
			if r.ID == res.ID {
				return slices.Clone(r.Access), nil
			}
		}
		return nil, nil
	}
	var candidates []string
	for _, r := range tp.Resources {
		candidates = append(candidates, r.Access...)
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	if len(candidates) == 0 {
		return nil, nil
	}
	questions := make([]model.CheckRequest, len(candidates))
	for i, l := range candidates {
		questions[i] = model.CheckRequest{Type: res.Type, Action: l}
	}
	answers, err := c.app.CheckAccess(ctx, c.serviceID, sp.subject, c.app.NewEnv(sp.claims, attrs), questions, false, consistency)
	if err != nil {
		return nil, err
	}
	var levels []string
	for i, a := range answers {
		if a.Allowed {
			levels = append(levels, candidates[i])
		}
	}
	return levels, nil
}

// searchSubjects lists the known subjects (the registry of people seen by a verified token) that may do the action on the
// resource. Only a client application the service trusts may ask: the answer names other people.
func (c *authzenCaller) searchSubjects(ctx context.Context, consistency engine.Consistency, in model.AuthzenSearchRequest) (*model.AuthzenSubjectSearchResponse, error) {
	s, aerr := parseSearch("subject", in)
	if aerr != nil {
		return nil, aerr
	}
	client, err := c.trusted()
	if err != nil {
		return nil, err
	}
	SetRequestUser(ctx, "client:"+client)

	out := &model.AuthzenSubjectSearchResponse{Results: []model.AuthzenSubject{}}
	if !c.app.Cfg.Subjects.Enabled {
		out.Context = map[string]any{"subjects_registry": "disabled"}
		out.Page = pageInfo(0, nil, "")
		return out, nil
	}
	question := []model.CheckRequest{{Type: in.Resource.Type, Action: in.Action.Name}}
	if in.Resource.ID != "" {
		id := in.Resource.ID
		question[0].ResourceID = &id
	}

	// The registry is read in the order of the subjects, a batch at a time from the cursor, and every subject of a batch
	// is decided (the decisions are cached like any other, so a repeated page costs little) until the page is full or
	// the request has gone through authzenSubjectScan subjects. The cursor is the last subject the page covers: the order
	// does not depend on who was seen last, so no page skips or repeats a person.
	var found []engine.KnownSubject
	scanned, cursor, exhausted := 0, s.after, false
scan:
	for scanned < authzenSubjectScan && len(found) < s.limit {
		batch, err := c.app.LoadKnownSubjectsAfter(ctx, cursor, min(authzenScanBatch, authzenSubjectScan-scanned))
		if err != nil {
			return nil, err
		}
		allowed, err := c.decideKnown(ctx, consistency, batch, question, s.attrs)
		if err != nil {
			return nil, err
		}
		for i, k := range batch {
			scanned++
			cursor = k.Subject
			if allowed[i] {
				found = append(found, k)
				if len(found) == s.limit {
					break scan
				}
			}
		}
		if len(batch) < authzenScanBatch {
			exhausted = true
			break
		}
	}
	next := ""
	if !exhausted {
		next = encodeCursor(cursor, s.sum)
	}
	// The number of all the results is not known without going through the whole registry.
	out.Page = pageInfo(len(found), nil, next)
	for _, k := range found {
		subject := model.AuthzenSubject{Type: in.Subject.Type, ID: k.Subject}
		if k.Name != nil {
			subject.Properties = map[string]any{"name": *k.Name}
		}
		out.Results = append(out.Results, subject)
	}
	// The search is about nobody in particular: the audit names it with a wildcard.
	c.delegated = append(c.delegated, "*")
	c.tally["*"] = &delegatedTally{questions: scanned, allowed: len(found)}
	return out, nil
}

// decideKnown says, for each known subject of a batch, whether the question is allowed to them. A subject that cannot be
// asked about (it looks like a group) is not an answer.
func (c *authzenCaller) decideKnown(ctx context.Context, consistency engine.Consistency, batch []engine.KnownSubject, question []model.CheckRequest, attrs model.RequestContext) ([]bool, error) {
	allowed := make([]bool, len(batch))
	eg, ectx := errgroup.WithContext(ctx)
	eg.SetLimit(authzenScanWorkers)
	for i, k := range batch {
		eg.Go(func() error {
			claims, err := engine.PrepareDelegated(k.Subject, k.Claims)
			if err != nil {
				return nil
			}
			answers, err := c.app.CheckAccess(ectx, c.serviceID, k.Subject, c.app.NewEnv(claims, attrs), question, false, consistency)
			if err != nil {
				return err
			}
			allowed[i] = answers[0].Allowed
			return nil
		})
	}
	return allowed, eg.Wait()
}
