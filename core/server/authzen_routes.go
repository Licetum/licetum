package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
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

// authzenMaxBody is the largest body of an AuthZEN request. A batch of the largest size (100 evaluations) is far below it.
const authzenMaxBody int64 = 1 << 20

// ---------- routes ----------

func registerAuthzen(api huma.API, app pdpEngine, cfg *config.Config, security []map[string][]string, authErrors []int) {
	errs := append([]int{http.StatusForbidden, http.StatusTooManyRequests}, authErrors...)
	auth := newAuthzenAuth(app, cfg)
	const note = `

This is the Authorization API 1.0 of OpenID AuthZEN, scoped to one service: the service is in the path, as its id or its slug.
resource.type is the name of a resource type of the service, and resource.id the resource id in the external system; the id
"*" names the type as a whole, not a resource. action.name is an access level. subject.id is the subject of the bearer token
(preferred_username, or sub) and the claims come from the token. A client application the service trusts (its azp, or
client_id, is in the service's trusted_clients) may ask about any person: it gives the claims of the person in
subject.properties, and every such call is recorded as the calls on behalf of a person are.

A PEP client of the service authenticates with an API key, or with a token of an issuer registered for PEPs; the person of the
question is then the subject of the body, whose claims are in subject.properties. The client needs the operation of the route
(evaluate, search_resource, search_action or search_subject, and explain as well to ask for an explanation). Whatever it lacks,
an unknown service included, the answer is the same 403. A client over its rate limit gets a 429 with Retry-After.

The context holds the attributes of the request that the rules may read. Its key licetum holds the settings of Licetum, and
is read from the top-level context only: min_revision (the answer reflects every write up to that revision) and explain (the
explanation goes into context.reason_admin).

A request that is not valid is a 400, a missing or invalid token a 401, a subject or an operation the caller may not ask for a 403,
a client over its rate limit a 429. An error
has its message as a JSON string, and its code in the Licetum-Error-Code header.`

	Register(api, huma.Operation{
		OperationID:  "authzen_evaluation",
		Method:       http.MethodPost,
		Path:         "/service/{service_id}/access/v1/evaluation",
		Summary:      "AuthZEN access evaluation",
		Description:  "Decides whether the subject may perform the action on the resource." + note,
		Tags:         []string{authzenTag},
		Security:     security,
		Errors:       errs,
		MaxBodyBytes: authzenMaxBody,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenEvaluationRequest
	}) (*Out[model.AuthzenEvaluationResponse], error) {
		if err := in.Body.Validate(); err != nil {
			return nil, badRequest(err)
		}
		settings, aerr := authzenSettings(in.Body.Context)
		if aerr != nil {
			return nil, aerr
		}
		ctx, aerr = authzenFreshness(ctx, in.AtLeastAsFresh, settings)
		if aerr != nil {
			return nil, aerr
		}
		caller, err := auth.caller(ctx, in.ServiceRef, in.Authorization, engine.PEPEvaluate, settings.Explain)
		if err != nil {
			return nil, err
		}
		evals, err := caller.evaluate(ctx, in.valueIn(ctx), []model.AuthzenEvaluationRequest{in.Body}, model.AuthzenEvaluationsRequest{}, settings.Explain, model.AuthzenExecuteAll)
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
		OperationID:  "authzen_evaluations",
		Method:       http.MethodPost,
		Path:         "/service/{service_id}/access/v1/evaluations",
		Summary:      "AuthZEN access evaluations (batch)",
		Description:  `Decides up to 100 evaluations in one request; subject, resource, action and context given at the top level are defaults for the evaluations that omit them. options.evaluations_semantic chooses between answering all of them (execute_all, the default), stopping at the first denial (deny_on_first_deny) and stopping at the first permit (permit_on_first_permit). An evaluation that cannot be decided is a denial with context.error holding its status and message. Without evaluations the top-level fields are one evaluation, and the answer is its decision.` + note,
		Tags:         []string{authzenTag},
		Security:     security,
		Errors:       errs,
		MaxBodyBytes: authzenMaxBody,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenEvaluationsRequest
	}) (*Out[model.AuthzenEvaluationsResponse], error) {
		if err := in.Body.Validate(); err != nil {
			return nil, badRequest(err)
		}
		body := in.Body
		settings, aerr := authzenSettings(body.Context)
		if aerr != nil {
			return nil, aerr
		}
		ctx, aerr = authzenFreshness(ctx, in.AtLeastAsFresh, settings)
		if aerr != nil {
			return nil, aerr
		}
		caller, err := auth.caller(ctx, in.ServiceRef, in.Authorization, engine.PEPEvaluate, settings.Explain)
		if err != nil {
			return nil, err
		}
		out, err := caller.evaluations(ctx, in.valueIn(ctx), body, settings.Explain)
		if err != nil {
			return nil, err
		}
		return OK(*out)
	})

	const searchNote = `

Paging: page.limit is the size of the page (at most 1000, the default), and the response carries page.next_token while
there are more results; send it back as page.token with the same request. The results are sorted.`

	Register(api, huma.Operation{
		OperationID:  "authzen_search_resource",
		Method:       http.MethodPost,
		Path:         "/service/{service_id}/access/v1/search/resource",
		Summary:      "AuthZEN resource search",
		Description:  "Lists the resources of the type resource.type on which the subject may perform the action; resource.id is ignored. A type without resources has none to list. A page can be shorter than page.limit, even empty, and still carry page.next_token, because one request reads a bounded number of resources: keep asking until page.next_token is empty." + searchNote + note,
		Tags:         []string{authzenTag},
		Security:     security,
		Errors:       errs,
		MaxBodyBytes: authzenMaxBody,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenResourceSearchRequest
	}) (*Out[model.AuthzenResourceSearchResponse], error) {
		if err := in.Body.Validate(); err != nil {
			return nil, badRequest(err)
		}
		settings, aerr := authzenSettings(in.Body.Context)
		if aerr != nil {
			return nil, aerr
		}
		ctx, aerr = authzenFreshness(ctx, in.AtLeastAsFresh, settings)
		if aerr != nil {
			return nil, aerr
		}
		caller, err := auth.caller(ctx, in.ServiceRef, in.Authorization, engine.PEPSearchResource, false)
		if err != nil {
			return nil, err
		}
		out, err := caller.searchResources(ctx, in.valueIn(ctx), searchInput{Subject: in.Body.Subject, Resource: in.Body.Resource, Action: in.Body.Action, Context: in.Body.Context, Page: in.Body.Page})
		if err != nil {
			return nil, err
		}
		if err := caller.flush(ctx, "permission"); err != nil {
			return nil, err
		}
		return OK(*out)
	})

	Register(api, huma.Operation{
		OperationID:  "authzen_search_action",
		Method:       http.MethodPost,
		Path:         "/service/{service_id}/access/v1/search/action",
		Summary:      "AuthZEN action search",
		Description:  "Lists the actions (access levels) the subject may perform on the resource; without resource.id the resource is the type as a whole. The request has no action." + searchNote + note,
		Tags:         []string{authzenTag},
		Security:     security,
		Errors:       errs,
		MaxBodyBytes: authzenMaxBody,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenActionSearchRequest
	}) (*Out[model.AuthzenActionSearchResponse], error) {
		if err := in.Body.Validate(); err != nil {
			return nil, badRequest(err)
		}
		settings, aerr := authzenSettings(in.Body.Context)
		if aerr != nil {
			return nil, aerr
		}
		ctx, aerr = authzenFreshness(ctx, in.AtLeastAsFresh, settings)
		if aerr != nil {
			return nil, aerr
		}
		caller, err := auth.caller(ctx, in.ServiceRef, in.Authorization, engine.PEPSearchAction, false)
		if err != nil {
			return nil, err
		}
		out, err := caller.searchActions(ctx, in.valueIn(ctx), searchInput{Subject: in.Body.Subject, Resource: in.Body.Resource, Context: in.Body.Context, Page: in.Body.Page})
		if err != nil {
			return nil, err
		}
		if err := caller.flush(ctx, "permission"); err != nil {
			return nil, err
		}
		return OK(*out)
	})

	Register(api, huma.Operation{
		OperationID:  "authzen_search_subject",
		Method:       http.MethodPost,
		Path:         "/service/{service_id}/access/v1/search/subject",
		Summary:      "AuthZEN subject search",
		Description:  "Lists the subjects of the type subject.type that may perform the action on the resource; subject.id is ignored. Only a PEP client with the search_subject operation, or a client application the service trusts, may ask. The subjects are the people in the subject directory of the service — what its application sent — decided with their stored properties, so a person who is not in the directory is never listed and an empty directory gives an empty result; a request goes through at most 1000 of them, in the order of their identifiers, and page.next_token continues from where it stopped (page.total is not given)." + searchNote + note,
		Tags:         []string{authzenTag},
		Security:     security,
		Errors:       errs,
		MaxBodyBytes: authzenMaxBody,
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
		ConsistencyParam
		Authorization string `header:"Authorization" hidden:"true"`
		Body          model.AuthzenSubjectSearchRequest
	}) (*Out[model.AuthzenSubjectSearchResponse], error) {
		if err := in.Body.Validate(); err != nil {
			return nil, badRequest(err)
		}
		settings, aerr := authzenSettings(in.Body.Context)
		if aerr != nil {
			return nil, aerr
		}
		ctx, aerr = authzenFreshness(ctx, in.AtLeastAsFresh, settings)
		if aerr != nil {
			return nil, aerr
		}
		caller, err := auth.caller(ctx, in.ServiceRef, in.Authorization, engine.PEPSearchSubject, false)
		if err != nil {
			return nil, err
		}
		out, err := caller.searchSubjects(ctx, in.valueIn(ctx), searchInput{Subject: in.Body.Subject, Resource: in.Body.Resource, Action: in.Body.Action, Context: in.Body.Context, Page: in.Body.Page})
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
		Description: `The discovery document of the AuthZEN API of one service (OpenID AuthZEN 1.0, "Policy Decision Point Metadata"). The Policy Decision Point of a service is <PDP__PUBLIC_URL>/service/<slug>, so the document is served at the well-known path inserted between the host and that path. It needs no token. It is built from PDP__PUBLIC_URL, and is not served (404) without it.`,
		Tags:        []string{authzenTag},
	}, func(ctx context.Context, in *struct {
		ServiceRefPath
	}) (*authzenMetadataOut, error) {
		base := cfg.PDP.PublicURL
		if base == "" {
			return nil, apperr.NotFound(i18n.AuthzenMetadataDisabled)
		}
		serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
		if err != nil {
			return nil, err
		}
		svc, err := app.GetServiceForCheck(ctx, serviceID)
		if err != nil {
			return nil, err
		}
		return &authzenMetadataOut{CacheControl: "public, max-age=300", Body: authzenMetadata(base, svc.Slug)}, nil
	})
}

// authzenSettings reads the settings of Licetum (context.licetum) from the top-level context of a request. The settings are
// read there and nowhere else: an item of a batch cannot set them (see prepare).
func authzenSettings(top map[string]any) (model.AuthzenLicetum, *apperr.Error) {
	_, settings, err := model.ParseAuthzenContext(top)
	if err != nil {
		return model.AuthzenLicetum{}, badRequest(err)
	}
	return settings, nil
}

// authzenFreshness carries into the request the revision its answers must reflect: the later of at_least_as_fresh and
// context.licetum.min_revision. The decision is then computed from the data when the view has not reached the revision
// (see WithMinRevision), so no answer older than it is given.
func authzenFreshness(ctx context.Context, atLeastAsFresh int64, settings model.AuthzenLicetum) (context.Context, *apperr.Error) {
	rev := atLeastAsFresh
	if settings.MinRevision != "" {
		v, err := strconv.ParseInt(settings.MinRevision, 10, 64)
		if err != nil {
			return ctx, apperr.BadRequest(i18n.AuthzenLicetumInvalid)
		}
		rev = max(rev, v)
	}
	return engine.WithMinRevision(ctx, rev), nil
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
