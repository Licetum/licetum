package server

// The run layer of AuthZEN: the evaluations and the searches, decided on the check engine. The operations
// (authzen_routes.go) and the caller that verified the token (authzen_caller.go) feed it; nothing in this file
// registers an operation or reads a request.

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
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

const (
	// authzenPageMax is the most results of one page of a search.
	authzenPageMax = 1000
	// authzenSubjectScan bounds how many people of the directory one request of the subject search goes through; the page token
	// continues from where it stopped.
	authzenSubjectScan = 1000
	// authzenScanBatch is how many people of the directory are read and decided at a time.
	authzenScanBatch = 100
	// authzenScanWorkers is how many people of the directory are decided at the same time.
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
	req   model.CheckRequest
	sp    speaker
	attrs model.RequestContext
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
	// The handlers have checked the request (model.AuthzenEvaluationsRequest.Validate), which requires these fields; the
	// nil checks only keep the shape safe.
	if item.Subject == nil || item.Resource == nil || item.Action == nil {
		return apperr.BadRequest(i18n.AuthzenIncomplete)
	}
	sp, aerr := c.speaker(ctx, item.Subject)
	if aerr != nil {
		return aerr
	}
	e.sp = sp
	e.req = model.CheckRequest{Type: item.Resource.Type, Action: item.Action.Name, ResourceID: resourceIDOf(item.Resource)}
	// The settings of Licetum (context.licetum) are read from the top-level context of the batch only (authzenSettings): the
	// context of an item holds its attributes, and its own licetum key is not a setting.
	attrs, _, err := model.ParseAuthzenContext(item.Context)
	if err != nil {
		return badRequest(err)
	}
	e.attrs = attrs
	return nil
}

// resourceIDOf is the resource id an AuthZEN resource names, nil for the type as a whole: an empty id, or the wildcard
// model.AuthzenWholeType.
func resourceIDOf(r *model.AuthzenResource) *string {
	if r == nil || r.ID == "" || r.ID == model.AuthzenWholeType {
		return nil
	}
	id := r.ID
	return &id
}

// evaluate answers the items in order. Under execute_all, the items that carry the same person and the same request
// attributes are decided in one round (CheckAccess takes one environment per call). The other semantics stop at the
// evaluation that decides the batch: each evaluation is decided in turn, and nothing after the stopping one is evaluated,
// so the result ends with it. A question that cannot be decided has err set and no decision; anything else that goes
// wrong fails the whole request.
func (c *authzenCaller) evaluate(ctx context.Context, consistency engine.Consistency, items []model.AuthzenEvaluationRequest, defaults model.AuthzenEvaluationsRequest, explain bool, semantic string) ([]evaluation, error) {
	evals := make([]evaluation, len(items))
	if semantic != model.AuthzenExecuteAll {
		for i, item := range items {
			e := &evals[i]
			if e.err = c.prepare(ctx, e, item, defaults); e.err == nil {
				if err := c.decide(ctx, consistency, explain, evals, []int{i}); err != nil {
					return nil, err
				}
			}
			if stopsBatch(semantic, e.err == nil && e.resp.Decision) {
				return evals[:i+1], nil
			}
		}
		return evals, nil
	}
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
		if err := c.decide(ctx, consistency, explain, evals, groups[key]); err != nil {
			return nil, err
		}
	}
	return evals, nil
}

// decide answers the evaluations at idx with one CheckAccess call: they carry the same person and the same attributes.
func (c *authzenCaller) decide(ctx context.Context, consistency engine.Consistency, explain bool, evals []evaluation, idx []int) error {
	first := evals[idx[0]]
	group := make([]model.CheckRequest, len(idx))
	for j, i := range idx {
		group[j] = evals[i].req
	}
	env := c.app.NewEnv(first.sp.claims, first.attrs)
	results, err := c.app.CheckAccess(ctx, c.serviceID, first.sp.subject, env, group, explain, consistency)
	if err != nil {
		return err
	}
	c.app.LogDecisions(ctx, c.serviceID, first.sp.subject, group, results, consistency)
	allowed := 0
	for j, i := range idx {
		evals[i].resp = model.AuthzenEvaluationResponse{Decision: results[j].Allowed}
		if results[j].Allowed {
			allowed++
		}
		if explain && results[j].Explanation != nil {
			evals[i].resp.Context = map[string]any{"reason_admin": results[j].Explanation}
		}
	}
	c.note(first.sp, len(idx), allowed)
	return nil
}

// stopsBatch says whether the decision of an evaluation ends a batch under the semantic: a denial under deny_on_first_deny,
// a permit under permit_on_first_permit.
func stopsBatch(semantic string, decision bool) bool {
	switch semantic {
	case model.AuthzenDenyOnFirstDeny:
		return !decision
	case model.AuthzenPermitOnFirstPermit:
		return decision
	}
	return false
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

// ---------- searches ----------

// searchInput is what the three searches read from their requests. Each endpoint has its own request type, checked by its
// Validate; the handler passes the fields of the checked request on in this shape.
type searchInput struct {
	Subject  *model.AuthzenSubject
	Resource *model.AuthzenResource
	Action   *model.AuthzenAction
	Context  map[string]any
	Page     *model.AuthzenPageRequest
}

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
	// The key goes to the database as text, which cannot hold a NUL byte (JSON decoding already turns invalid UTF-8 into valid text):
	// it would be an error there, not a refusal here.
	if err != nil || json.Unmarshal(raw, &cur) != nil || cur.Sum != sum || strings.ContainsRune(cur.After, 0) {
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
func searchSum(kind string, in searchInput, limit int) string {
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
		// The type as a whole is the same request, however it is named.
		if id := resourceIDOf(in.Resource); id != nil && kind != "resource" {
			d.ResourceID = *id
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

// search is a search request, checked: the handler has validated its fields (model.Validate), and the context and the page
// token are checked here.
type search struct {
	attrs   model.RequestContext
	limit   int
	after   string
	started bool // the request carries a page token: the page starts after the key, which may be the empty string
	sum     string
	subject *model.AuthzenSubject
}

// parseSearch reads a search request of the kind: "subject", "resource" or "action".
func parseSearch(kind string, in searchInput) (*search, *apperr.Error) {
	s := &search{limit: pageLimit(in.Page), subject: in.Subject}
	attrs, _, err := model.ParseAuthzenContext(in.Context)
	if err != nil {
		return nil, badRequest(err)
	}
	s.attrs = attrs
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
	s.started = token != ""
	return s, nil
}

// permissions reads what the speaker may do in the service.
func (c *authzenCaller) permissions(ctx context.Context, sp speaker, attrs model.RequestContext, consistency engine.Consistency) (engine.PermissionResult, error) {
	return c.app.GetPermission(ctx, c.serviceID, sp.subject, c.app.NewEnv(sp.claims, attrs), consistency)
}

// searchResources answers the resource search: one page of the resources of the asked type that the speaker may do the action
// with, in bytewise order of their ids. The engine reads the candidates of the type by a keyset scan and decides them (see
// engine.SearchResources); the cursor is the key the page covers up to, so that no result is repeated across pages. A page
// the scan budget stopped is shorter than its limit and still carries a next_token. There is no total: it would need the
// whole result.
func (c *authzenCaller) searchResources(ctx context.Context, consistency engine.Consistency, in searchInput) (*model.AuthzenResourceSearchResponse, error) {
	s, aerr := parseSearch("resource", in)
	if aerr != nil {
		return nil, aerr
	}
	sp, aerr := c.speaker(ctx, s.subject)
	if aerr != nil {
		return nil, aerr
	}
	budget := c.cfg.PDP.SearchScanBudget
	if budget < 1 {
		budget = config.DefaultSearchScanBudget
	}
	query := engine.ResourceSearch{Type: in.Resource.Type, Action: in.Action.Name, Limit: s.limit, Budget: budget}
	if s.started {
		query.After = &s.after
	}
	page, err := c.app.SearchResources(ctx, c.serviceID, sp.subject, c.app.NewEnv(sp.claims, s.attrs), query, consistency)
	if err != nil {
		return nil, err
	}
	next := ""
	if !page.Done {
		next = encodeCursor(page.Last, s.sum)
	}
	out := &model.AuthzenResourceSearchResponse{Page: pageInfo(len(page.Hits), nil, next), Results: make([]model.AuthzenResource, len(page.Hits))}
	for i, h := range page.Hits {
		out.Results[i] = model.AuthzenResource{Type: in.Resource.Type, ID: h.ID, Properties: map[string]any{"name": h.Name}}
	}
	c.note(sp, 0, 0)
	return out, nil
}

func (c *authzenCaller) searchActions(ctx context.Context, consistency engine.Consistency, in searchInput) (*model.AuthzenActionSearchResponse, error) {
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
	id := resourceIDOf(res)
	if tp.Type == model.TypeModeNotSync {
		if id != nil {
			return nil, nil
		}
		return slices.Clone(tp.Access), nil
	}
	if id != nil {
		for _, r := range tp.Resources {
			if r.ID == *id {
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

// searchSubjects lists the people of the directory of the service (the type of subject.type) that may do the action on the
// resource (ADR 0001, 7.6). The directory is what the application sent; a person who is not in it is never found, and an empty
// directory gives an empty result. Only a PEP client with the search_subject operation, or a client application the service
// trusts, may ask: the answer names other people.
func (c *authzenCaller) searchSubjects(ctx context.Context, consistency engine.Consistency, in searchInput) (*model.AuthzenSubjectSearchResponse, error) {
	s, aerr := parseSearch("subject", in)
	if aerr != nil {
		return nil, aerr
	}
	if c.pep == nil {
		client, err := c.trusted()
		if err != nil {
			return nil, err
		}
		SetRequestUser(ctx, "client:"+client)
	}

	out := &model.AuthzenSubjectSearchResponse{Results: []model.AuthzenSubject{}}
	question := []model.CheckRequest{{Type: in.Resource.Type, Action: in.Action.Name, ResourceID: resourceIDOf(in.Resource)}}

	// The directory is read in the order of its keys (type, then id, bytewise), a batch at a time from the cursor, and every
	// person of a batch is decided (the decisions are cached like any other, so a repeated page costs little) until the page is
	// full or the request has gone through authzenSubjectScan people. The cursor is the last person the page covers: the order
	// does not depend on who was sent last, so no page skips or repeats a person.
	var found []db.DirectoryEntry
	var after *db.DirectoryKey
	if s.started {
		after = directoryCursor(s.after)
	}
	scanned, exhausted := 0, false
scan:
	for scanned < authzenSubjectScan && len(found) < s.limit {
		want := min(authzenScanBatch, authzenSubjectScan-scanned)
		batch, err := c.app.DirectoryAfter(ctx, c.serviceID, in.Subject.Type, after, want)
		if err != nil {
			return nil, err
		}
		allowed, err := c.decideKnown(ctx, consistency, batch, question, s.attrs)
		if err != nil {
			return nil, err
		}
		for i, e := range batch {
			scanned++
			after = &db.DirectoryKey{Type: e.Type, ID: e.ID}
			if allowed[i] {
				found = append(found, e)
				if len(found) == s.limit {
					break scan
				}
			}
		}
		if len(batch) < want {
			exhausted = true
			break
		}
	}
	next := ""
	if !exhausted && after != nil {
		next = encodeCursor(after.Type+":"+after.ID, s.sum)
	}
	// The number of all the results is not known without going through the whole directory.
	out.Page = pageInfo(len(found), nil, next)
	for _, e := range found {
		subject := model.AuthzenSubject{Type: e.Type, ID: e.ID}
		if e.DisplayName != "" {
			subject.Properties = map[string]any{"name": e.DisplayName}
		}
		out.Results = append(out.Results, subject)
	}
	// The search is about nobody in particular: the audit names it with a wildcard.
	c.delegated = append(c.delegated, "*")
	c.tally["*"] = &delegatedTally{questions: scanned, allowed: len(found)}
	return out, nil
}

// directoryCursor is the key a page of the subject search starts after: the type and the id, as encodeCursor was given them.
// A type cannot hold a colon, so the first one separates the two.
func directoryCursor(key string) *db.DirectoryKey {
	typ, id, ok := strings.Cut(key, ":")
	if !ok {
		return nil
	}
	return &db.DirectoryKey{Type: typ, ID: id}
}

// decideKnown says, for each person of a batch of the directory, whether the question is allowed to them, decided with their
// stored properties as claims. A subject that cannot be asked about (it looks like a group) is not an answer.
func (c *authzenCaller) decideKnown(ctx context.Context, consistency engine.Consistency, batch []db.DirectoryEntry, question []model.CheckRequest, attrs model.RequestContext) ([]bool, error) {
	allowed := make([]bool, len(batch))
	eg, ectx := errgroup.WithContext(ctx)
	eg.SetLimit(authzenScanWorkers)
	for i, e := range batch {
		eg.Go(func() error {
			claims, err := engine.PrepareDelegated(e.ID, engine.Claims(e.Properties))
			if err != nil {
				return nil
			}
			answers, err := c.app.CheckAccess(ectx, c.serviceID, e.ID, c.app.NewEnv(claims, attrs), question, false, consistency)
			if err != nil {
				return err
			}
			allowed[i] = answers[0].Allowed
			return nil
		})
	}
	return allowed, eg.Wait()
}
