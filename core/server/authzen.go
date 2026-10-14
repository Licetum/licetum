package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The OpenID AuthZEN Authorization API 1.0 on top of the check engine: the access evaluation and evaluations, the
// subject, resource and action searches, and the discovery document of the Policy Decision Point. Every endpoint is
// scoped to one service (it is in the path), so the "PDP" of AuthZEN is a service: its identifier is
// <public url>/service/<id or slug>, and the default paths of the specification are appended to it.

// authzenTag is the tag of the operations; a request that fails the schema is a 400 there (the specification has no 422).
const authzenTag = "AuthZEN"

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

// authzenCaller is the holder of a verified token, who asks AuthZEN questions: about themselves, or — when the service
// trusts their client application — about other people, whose claims they give as subject.properties.
type authzenCaller struct {
	app       *engine.App
	serviceID uuid.UUID
	svc       *model.Service
	subject   string
	claims    engine.Claims

	// The client application of the token, when the service trusts it (see engine.AuthorizeDelegate); asked once.
	trustChecked bool
	client       string
	trustErr     error
	// loggedOnBehalf is set once the access log line names the first person asked about.
	loggedOnBehalf bool

	// The people asked about on behalf, in the order first met, with what was asked (the audit of delegated calls).
	delegated []string
	tally     map[string]*delegatedTally
}

type delegatedTally struct{ questions, allowed int }

// speaker is the person a question is about, with the claims the rules read.
type speaker struct {
	subject   string
	claims    engine.Claims
	delegated bool
}

func newAuthzenCaller(ctx context.Context, app *engine.App, serviceID uuid.UUID, authorization string) (*authzenCaller, error) {
	svc, claims, err := verifiedCaller(ctx, app, serviceID, authorization)
	if err != nil {
		return nil, err
	}
	subject, err := engine.SubjectFromClaims(claims)
	if err != nil {
		return nil, err
	}
	SetRequestUser(ctx, subject)
	c := &authzenCaller{app: app, serviceID: serviceID, svc: svc, subject: subject, claims: claims, tally: map[string]*delegatedTally{}}
	// The caller is a subject like any other, a trusted client application included: rules may grant a service account
	// access by the attributes of its claims, so it belongs to the registry as well.
	app.NoteSubject(subject, claims)
	return c, nil
}

// trusted returns the client application of the token when the service trusts it.
func (c *authzenCaller) trusted() (string, error) {
	if !c.trustChecked {
		c.client, c.trustErr = engine.AuthorizeDelegate(c.svc, c.claims)
		c.trustChecked = true
	}
	return c.client, c.trustErr
}

// speaker says who a subject object stands for. The holder of the token speaks for themselves with the claims of the
// token; any other subject needs a trusted client, and brings the claims in its properties.
func (c *authzenCaller) speaker(ctx context.Context, s *model.AuthzenSubject) (speaker, *apperr.Error) {
	if s.ID == c.subject {
		return speaker{subject: c.subject, claims: c.claims}, nil
	}
	client, err := c.trusted()
	if err != nil {
		return speaker{}, apperr.Forbidden(i18n.AuthzenSubjectMismatch)
	}
	claims, err := engine.PrepareDelegated(s.ID, s.Properties)
	if err != nil {
		return speaker{}, badRequest(err)
	}
	if !c.loggedOnBehalf {
		c.loggedOnBehalf = true
		SetRequestUser(ctx, "client:"+client)
		SetOnBehalfOf(ctx, s.ID)
	}
	return speaker{subject: s.ID, claims: claims, delegated: true}, nil
}

// note adds what was asked about a person to the audit of the request (a person the token holder asked about is not audited).
func (c *authzenCaller) note(sp speaker, questions, allowed int) {
	if !sp.delegated {
		return
	}
	t := c.tally[sp.subject]
	if t == nil {
		t = &delegatedTally{}
		c.tally[sp.subject] = t
		c.delegated = append(c.delegated, sp.subject)
	}
	t.questions += questions
	t.allowed += allowed
}

// flush writes the audit of the calls made on behalf of people: a call that cannot be recorded is not answered.
func (c *authzenCaller) flush(ctx context.Context, operation string) error {
	for _, subject := range c.delegated {
		t := c.tally[subject]
		if err := c.app.RecordDelegatedCall(ctx, c.serviceID, engine.DelegatedCall{
			CallerClient: c.client, CallerSubject: c.subject, Subject: subject, Operation: operation,
			Questions: t.questions, Allowed: t.allowed,
		}); err != nil {
			return err
		}
	}
	return nil
}

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
		env := engine.NewEnv(first.sp.claims, first.attrs)
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
	return c.app.GetPermission(ctx, c.serviceID, sp.subject, engine.NewEnv(sp.claims, attrs), consistency)
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
	answers, err := c.app.CheckAccess(ctx, c.serviceID, sp.subject, engine.NewEnv(sp.claims, attrs), questions, false, consistency)
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
			answers, err := c.app.CheckAccess(ectx, c.serviceID, k.Subject, engine.NewEnv(claims, attrs), question, false, consistency)
			if err != nil {
				return err
			}
			allowed[i] = answers[0].Allowed
			return nil
		})
	}
	return allowed, eg.Wait()
}

// ---------- routes ----------

func registerAuthzen(api huma.API, app *engine.App, security []map[string][]string, authErrors []int) {
	errs := append([]int{http.StatusForbidden}, authErrors...)
	const note = `

This is the Authorization API 1.0 of OpenID AuthZEN, scoped to one service (the service is in the path, as its id or its
slug). resource.type is the name of a resource type of the service, resource.id the resource id in the external system
(empty — the type as a whole), action.name an access level. subject.id is the subject of the bearer token
(preferred_username, or sub) and the claims come from the token. A client application the service trusts (its azp, or
client_id, is in the service's trusted_clients) may ask about any person: it gives the claims of the person in
subject.properties, and every such call is recorded as the calls on behalf of a person are. Context attributes other than
explain are the attributes of the request that the rules may read.

A request that is not valid is a 400, a missing or invalid token a 401, a subject the caller may not ask about a 403.`

	Register(api, huma.Operation{
		OperationID: "authzen_evaluation",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/access/v1/evaluation",
		Summary:     "AuthZEN access evaluation",
		Description: "Decides whether the subject may perform the action on the resource." + note,
		Tags:        []string{authzenTag},
		Security:    security,
		Errors:      errs,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenEvaluationRequest
	}) (*Out[model.AuthzenEvaluationResponse], error) {
		ctx = in.freshness(ctx)
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		caller, err := newAuthzenCaller(ctx, app, serviceID, in.Authorization)
		if err != nil {
			return nil, err
		}
		evals, err := caller.evaluate(ctx, in.value(), []model.AuthzenEvaluationRequest{in.Body}, model.AuthzenEvaluationsRequest{})
		if err != nil {
			return nil, err
		}
		if evals[0].err != nil {
			return nil, evals[0].err
		}
		if err := caller.flush(ctx, "check"); err != nil {
			return nil, err
		}
		return OK(evals[0].resp)
	})

	Register(api, huma.Operation{
		OperationID: "authzen_evaluations",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/access/v1/evaluations",
		Summary:     "AuthZEN access evaluations (batch)",
		Description: `Decides up to 100 evaluations in one request; subject, resource, action and context given at the top level are defaults for the evaluations that omit them. options.evaluations_semantic chooses between answering all of them (execute_all, the default), stopping at the first denial (deny_on_first_deny) and stopping at the first permit (permit_on_first_permit). An evaluation that cannot be decided is a denial with context.error holding its status and message. Without evaluations the top-level fields are one evaluation, and the answer is its decision.` + note,
		Tags:        []string{authzenTag},
		Security:    security,
		Errors:      errs,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenEvaluationsRequest
	}) (*Out[model.AuthzenEvaluationsResponse], error) {
		ctx = in.freshness(ctx)
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		caller, err := newAuthzenCaller(ctx, app, serviceID, in.Authorization)
		if err != nil {
			return nil, err
		}
		body := in.Body
		if len(body.Evaluations) == 0 {
			single := model.AuthzenEvaluationRequest{Subject: body.Subject, Resource: body.Resource, Action: body.Action, Context: body.Context}
			evals, err := caller.evaluate(ctx, in.value(), []model.AuthzenEvaluationRequest{single}, model.AuthzenEvaluationsRequest{})
			if err != nil {
				return nil, err
			}
			if evals[0].err != nil {
				return nil, evals[0].err
			}
			if err := caller.flush(ctx, "check"); err != nil {
				return nil, err
			}
			return OK(model.AuthzenEvaluationsResponse{Decision: &evals[0].resp.Decision, Context: evals[0].resp.Context})
		}
		evals, err := caller.evaluate(ctx, in.value(), body.Evaluations, body)
		if err != nil {
			return nil, err
		}
		if err := caller.flush(ctx, "check"); err != nil {
			return nil, err
		}
		semantic := model.AuthzenExecuteAll
		if body.Options != nil && body.Options.EvaluationsSemantic != "" {
			semantic = body.Options.EvaluationsSemantic
		}
		return OK(model.AuthzenEvaluationsResponse{Evaluations: applySemantic(semantic, evals, i18n.FromContext(ctx))})
	})

	const searchNote = `

Paging: page.limit is the size of the page (at most 1000, the default), and the response carries page.next_token while
there are more results; send it back as page.token with the same request. The results are sorted.`

	Register(api, huma.Operation{
		OperationID: "authzen_search_resource",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/access/v1/search/resource",
		Summary:     "AuthZEN resource search",
		Description: "Lists the resources of the type resource.type on which the subject may perform the action; resource.id is ignored. A type without resources has none to list." + searchNote + note,
		Tags:        []string{authzenTag},
		Security:    security,
		Errors:      errs,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenSearchRequest
	}) (*Out[model.AuthzenResourceSearchResponse], error) {
		ctx = in.freshness(ctx)
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		caller, err := newAuthzenCaller(ctx, app, serviceID, in.Authorization)
		if err != nil {
			return nil, err
		}
		out, err := caller.searchResources(ctx, in.value(), in.Body)
		if err != nil {
			return nil, err
		}
		if err := caller.flush(ctx, "permission"); err != nil {
			return nil, err
		}
		return OK(*out)
	})

	Register(api, huma.Operation{
		OperationID: "authzen_search_action",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/access/v1/search/action",
		Summary:     "AuthZEN action search",
		Description: "Lists the actions (access levels) the subject may perform on the resource; without resource.id the resource is the type as a whole. The request has no action." + searchNote + note,
		Tags:        []string{authzenTag},
		Security:    security,
		Errors:      errs,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenSearchRequest
	}) (*Out[model.AuthzenActionSearchResponse], error) {
		ctx = in.freshness(ctx)
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		caller, err := newAuthzenCaller(ctx, app, serviceID, in.Authorization)
		if err != nil {
			return nil, err
		}
		out, err := caller.searchActions(ctx, in.value(), in.Body)
		if err != nil {
			return nil, err
		}
		if err := caller.flush(ctx, "permission"); err != nil {
			return nil, err
		}
		return OK(*out)
	})

	Register(api, huma.Operation{
		OperationID: "authzen_search_subject",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/access/v1/search/subject",
		Summary:     "AuthZEN subject search",
		Description: "Lists the subjects of the type subject.type that may perform the action on the resource; subject.id is ignored. Only a client application the service trusts may ask. The subjects are the known ones — the people whose tokens Licetum has verified, kept while SUBJECTS__ENABLED is on — so a person who was never seen is not listed; a request goes through at most 1000 of them, in the order of their identifiers, and page.next_token continues from where it stopped (page.total is not given)." + searchNote + note,
		Tags:        []string{authzenTag},
		Security:    security,
		Errors:      errs,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenSearchRequest
	}) (*Out[model.AuthzenSubjectSearchResponse], error) {
		ctx = in.freshness(ctx)
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		caller, err := newAuthzenCaller(ctx, app, serviceID, in.Authorization)
		if err != nil {
			return nil, err
		}
		out, err := caller.searchSubjects(ctx, in.value(), in.Body)
		if err != nil {
			return nil, err
		}
		if err := caller.flush(ctx, "check"); err != nil {
			return nil, err
		}
		return OK(*out)
	})

	Register(api, huma.Operation{
		OperationID: "authzen_metadata",
		Method:      http.MethodGet,
		Path:        "/.well-known/authzen-configuration/service/{service_id}",
		Summary:     "AuthZEN Policy Decision Point metadata",
		Description: `The discovery document of the AuthZEN API of one service (OpenID AuthZEN 1.0, "Policy Decision Point Metadata"). The Policy Decision Point of a service is <public url>/service/<id or slug>, so the document is served at the well-known path inserted between the host and that path. It needs no token. It is built from HTTP__PUBLIC_URL, and is not served (404) without it.`,
		Tags:        []string{authzenTag},
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
	}) (*authzenMetadataOut, error) {
		base := app.Cfg.HTTP.PublicURL
		if base == "" {
			return nil, apperr.NotFound(i18n.AuthzenMetadataDisabled)
		}
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		if _, err := app.GetServiceForCheck(ctx, serviceID); err != nil {
			return nil, err
		}
		return &authzenMetadataOut{CacheControl: "public, max-age=300", Body: authzenMetadata(base, in.ServiceRef)}, nil
	})
}

type authzenMetadataOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         model.AuthzenMetadata
}

// authzenMetadata builds the metadata of the service named ref (its id or its slug) behind the public URL.
func authzenMetadata(base, ref string) model.AuthzenMetadata {
	pdp := strings.TrimRight(base, "/") + "/service/" + url.PathEscape(ref)
	return model.AuthzenMetadata{
		PolicyDecisionPoint:       pdp,
		AccessEvaluationEndpoint:  pdp + "/access/v1/evaluation",
		AccessEvaluationsEndpoint: pdp + "/access/v1/evaluations",
		SearchSubjectEndpoint:     pdp + "/access/v1/search/subject",
		SearchActionEndpoint:      pdp + "/access/v1/search/action",
		SearchResourceEndpoint:    pdp + "/access/v1/search/resource",
	}
}
