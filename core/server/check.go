package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// NewCheckHandler serves the runtime endpoints client services call on every request
// (GET /service/{service_id}/permission and the single checks). It is a separate app/port (8001) so
// that it can be scaled and deployed independently of the admin API.
func NewCheckHandler(app *engine.App) http.Handler {
	h, _ := NewCheckAPI(app)
	return h
}

// ConsistencyParam is the freshness a caller asks for.
// ServiceRefPath names the service by its id or by its slug (see engine.App.ServiceIDByRef).
type ServiceRefPath struct {
	ServiceRef string `path:"service_id" doc:"Service id or slug"`
}

type ConsistencyParam struct {
	Consistency    string `query:"consistency" enum:"minimize_latency,higher" default:"minimize_latency" doc:"minimize_latency (default) may answer from the result cache, and from the resource data as of the last synchronization: a source is asked again in the background once its throttle has run out, so an answer can lag the source by one refresh; higher skips the cache and fetches every involved source again, at the price of latency"`
	AtLeastAsFresh int64  `query:"at_least_as_fresh" required:"false" minimum:"0" doc:"The revision of an admin write, from the X-Authz-Revision header of its response. The answer then reflects that write and every write before it: when the cache is older than the revision, the answer is computed from the data"`
}

func (c ConsistencyParam) value() engine.Consistency {
	if c.Consistency == string(engine.HigherConsistency) {
		return engine.HigherConsistency
	}
	return engine.MinimizeLatency
}

// freshness carries the revision the caller asks for into the request.
func (c ConsistencyParam) freshness(ctx context.Context) context.Context {
	return engine.WithMinRevision(ctx, c.AtLeastAsFresh)
}

// PermissionQuery is the filter of GET /service/{service_id}/permission (see engine.PermissionFilter).
type PermissionQuery struct {
	Type    string `query:"type" required:"false" doc:"Only this resource type"`
	Access  string `query:"access" required:"false" doc:"Only the resources (and types) that have this access level"`
	Limit   int    `query:"limit" required:"false" minimum:"1" maximum:"1000" doc:"At most this many resources of the type; needs type. Absent: all of them"`
	After   string `query:"after" required:"false" doc:"The resource id after which the page starts: the next_after of the previous page"`
	Context string `query:"context" required:"false" doc:"The attributes of this request that the rules may read (conditions with source context), as a JSON object, e.g. {\"ip\":\"10.0.0.7\"}"`
}

func (q PermissionQuery) filter() engine.PermissionFilter {
	return engine.PermissionFilter{Type: q.Type, Access: q.Access, Limit: q.Limit, After: q.After}
}

// verifiedCaller verifies the bearer token against the service and returns the service with the token's claims.
func verifiedCaller(ctx context.Context, app *engine.App, serviceID uuid.UUID, authorization string) (*model.Service, engine.Claims, error) {
	if authorization == "" {
		return nil, nil, apperr.Unauthorized(i18n.AuthHeaderRequired)
	}
	token, isBearer := strings.CutPrefix(authorization, "Bearer ")
	if !isBearer {
		return nil, nil, apperr.BadRequest(i18n.AuthHeaderRequired)
	}

	svc, err := app.GetServiceForCheck(ctx, serviceID)
	if err != nil {
		return nil, nil, err
	}
	claims, err := app.VerifyToken(ctx, svc, token)
	if err != nil {
		return nil, nil, err
	}
	return svc, claims, nil
}

// authenticateDelegate verifies the token of a client that asks on behalf of a person, and checks that the service trusts
// the client (the azp of the token, see engine.AuthorizeDelegate). It returns the client and the subject of its token.
// The log line names the client as the user, and the call names the person (setOnBehalfOf).
func authenticateDelegate(ctx context.Context, app *engine.App, serviceID uuid.UUID, authorization string) (client, callerSubject string, err error) {
	svc, claims, err := verifiedCaller(ctx, app, serviceID, authorization)
	if err != nil {
		return "", "", err
	}
	if client, err = engine.AuthorizeDelegate(svc, claims); err != nil {
		return "", "", err
	}
	callerSubject, _ = engine.SubjectFromClaims(claims)
	SetRequestUser(ctx, "client:"+client)
	return client, callerSubject, nil
}

// authenticateCaller verifies the bearer token against the service and returns the caller's subject
// and claims. The subject is preferred_username, or sub if it is missing.
func authenticateCaller(ctx context.Context, app *engine.App, serviceID uuid.UUID, authorization string) (string, engine.Claims, error) {
	_, claims, err := verifiedCaller(ctx, app, serviceID, authorization)
	if err != nil {
		return "", nil, err
	}
	subject, err := engine.SubjectFromClaims(claims)
	if err != nil {
		return "", nil, err
	}
	SetRequestUser(ctx, subject)
	app.NoteSubject(subject, claims)
	return subject, claims, nil
}

// NewCheckAPI is the same but also returns the huma.API (to generate OpenAPI without running a server).
func NewCheckAPI(app *engine.App) (http.Handler, huma.API) {
	tags := []*huma.Tag{
		{Name: "Permission", Description: "Permission check: what the user holding this token may do with the service resources."},
		{Name: "Check", Description: "Single access checks with explanations."},
		{Name: authzenTag, Description: "The OpenID AuthZEN Authorization API 1.0 of a service: access evaluation and evaluations, the subject, resource and action searches, and the discovery document."},
		{Name: "Delegated", Description: "The same questions asked on behalf of a person by a client application the service trusts, without the person's token. Every call is recorded."},
		{Name: "System", Description: "Service endpoints."},
	}
	return NewServer(app, "Licetum — check", tags, func(api huma.API) {
		AddBearerScheme(api, "Access token issued by the IdP of the service whose permissions are being checked.")
		security := []map[string][]string{{BearerScheme: {}}}
		authErrors := []int{http.StatusUnauthorized, http.StatusFailedDependency}

		Register(api, huma.Operation{
			OperationID: "get_permission",
			Method:      http.MethodGet,
			Path:        "/service/{service_id}/permission",
			Summary:     "User permissions in a service",
			Description: `The token is verified against the service JWKS (signature, exp/nbf/iat, issuer). The subject is the
preferred_username claim, or sub if it is missing.

The response is keyed by resource type name, and the shape of each entry depends on the type mode, which is
derived from its synchronizer:

- ` + "`sync`" + ` — the type has a synchronizer: ` + "`{\"type\": \"sync\", \"resources\": [{id, name, access}]}`" + ` lists every resource
  of the type the user has access to;
- ` + "`not_sync`" + ` — the type has no synchronizer (a permission without resources, e.g. a feature flag):
  ` + "`{\"type\": \"not_sync\", \"access\": [...]}`" + ` are the access levels on the type as a whole.

A type without applicable rules is not in the response. Direct rules, claim-based rules, groups (transitively), roles and
grants cascading down the resource hierarchy — including from types of other services — are all taken into
account, and so are denials: a denial takes its access levels away from everything it covers unless an
exception below it lifts it. Rules outside their validity period do not count.

The result is cached for up to 60 seconds (less when a rule or a group membership starts or stops being in force
sooner) per subject and per the claims the rules look at; any change in the admin API resets the cache. Pass
consistency=higher to skip the cache.

Filters and paging: type keeps one resource type, access keeps the resources with one access level, and limit with
after pages the resources of that type, sorted by id. A page that is not the last carries next_after; pass it as after
to read the next one. Paging needs type.

Errors: 401 — the token is missing, invalid or expired; 424 — the service JWKS is unavailable.`,
			Tags:     []string{"Permission"},
			Security: security,
			Errors:   authErrors,
		}, func(ctx context.Context, in *struct {
			ServiceRefPath
			ConsistencyParam
			PermissionQuery
			Authorization string `header:"Authorization" hidden:"true"`
		}) (*Out[engine.PermissionResult], error) {
			ctx = in.freshness(ctx)
			serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
			if err != nil {
				return nil, err
			}
			subject, claims, err := authenticateCaller(ctx, app, serviceID, in.Authorization)
			if err != nil {
				return nil, err
			}
			env, err := requestEnv(claims, in.Context)
			if err != nil {
				return nil, err
			}
			out, err := app.PermissionPage(ctx, serviceID, subject, env, in.value(), in.filter())
			if err != nil {
				return nil, err
			}
			return OK(out)
		})

		Register(api, huma.Operation{
			OperationID: "check_access",
			Method:      http.MethodPost,
			Path:        "/service/{service_id}/check",
			Summary:     "Check access to resources",
			Description: `Answers up to 100 questions "may I do X with Y" in one request, for the subject of the token.

Every question names a resource type of the service, optionally a resource id, and an access level. Without a
resource id the question is about the type as a whole (creating resources, a permission without resources): only the
rules anchored on the type itself count, and a denial always wins there. With a resource id the rules covering the
resource through the hierarchy count.

With ` + "`explain`" + ` every answer carries an explanation: which rules grant the level, which denials apply, and which
exceptions lift them. The decision is the same as in GET /permission, only computed for the asked resources and not for
all of them.

Errors: 401 — the token is missing, invalid or expired; 424 — the service JWKS is unavailable.`,
			Tags:     []string{"Check"},
			Security: security,
			Errors:   authErrors,
		}, func(ctx context.Context, in *struct {
			ServiceRefPath
			ConsistencyParam
			Authorization string `header:"Authorization" hidden:"true"`
			Body          model.CheckInput
		}) (*Out[model.CheckResponse], error) {
			ctx = in.freshness(ctx)
			serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
			if err != nil {
				return nil, err
			}
			subject, claims, err := authenticateCaller(ctx, app, serviceID, in.Authorization)
			if err != nil {
				return nil, err
			}
			if err := in.Body.Context.Validate(); err != nil {
				return nil, err
			}
			env := engine.NewEnv(claims, in.Body.Context)
			results, err := app.CheckAccess(ctx, serviceID, subject, env, in.Body.Checks, in.Body.Explain, in.value())
			if err != nil {
				return nil, err
			}
			app.LogDecisions(ctx, serviceID, subject, in.Body.Checks, results, in.value())
			return OK(model.CheckResponse{Results: results})
		})

		delegateErrors := append([]int{http.StatusForbidden, http.StatusUnprocessableEntity}, authErrors...)
		const delegateNote = `

The caller is a client application the service trusts (its azp, or client_id, is in the service's trusted_clients), and it
authenticates with its own token — from the same IdP and keys as a user's, typically a client-credentials token. The person is
named in the body, with the claims their token would carry: the caller vouches for them, so a trusted client has to send the
claims the rules of the service read (the admin API lists them: GET /service/{id}/policy/claims). preferred_username
defaults to the subject. Every call is recorded: who asked, for whom, when and how many questions were allowed.
A token of a trusted client may ask about anybody: trust a backend client, not the one your users sign in with.

Errors: 401 — the token is missing, invalid or expired; 403 — the client is not trusted by the service; 422 — the subject
or the claims are not acceptable; 424 — the service JWKS is unavailable.`

		Register(api, huma.Operation{
			OperationID: "delegated_check",
			Method:      http.MethodPost,
			Path:        "/service/{service_id}/on_behalf/check",
			Summary:     "Check access on behalf of a person",
			Description: `Answers up to 100 questions "may this person do X with Y" as POST /service/{id}/check does, for the person named in the body instead of the holder of the token.` + delegateNote,
			Tags:        []string{"Delegated"},
			Security:    security,
			Errors:      delegateErrors,
		}, func(ctx context.Context, in *struct {
			ServiceRefPath
			ConsistencyParam
			Authorization string `header:"Authorization" hidden:"true"`
			Body          model.DelegatedCheckInput
		}) (*Out[model.CheckResponse], error) {
			ctx = in.freshness(ctx)
			serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
			if err != nil {
				return nil, err
			}
			client, callerSubject, err := authenticateDelegate(ctx, app, serviceID, in.Authorization)
			if err != nil {
				return nil, err
			}
			claims, err := engine.PrepareDelegated(in.Body.Subject, in.Body.Claims)
			if err != nil {
				return nil, err
			}
			if err := in.Body.Context.Validate(); err != nil {
				return nil, err
			}
			SetOnBehalfOf(ctx, in.Body.Subject)
			env := engine.NewEnv(claims, in.Body.Context)
			results, err := app.CheckAccess(ctx, serviceID, in.Body.Subject, env, in.Body.Checks, in.Body.Explain, in.value())
			if err != nil {
				return nil, err
			}
			allowed := 0
			for _, r := range results {
				if r.Allowed {
					allowed++
				}
			}
			if err := app.RecordDelegatedCall(ctx, serviceID, engine.DelegatedCall{
				CallerClient: client, CallerSubject: callerSubject, Subject: in.Body.Subject, Operation: "check",
				Questions: len(results), Allowed: allowed,
			}); err != nil {
				return nil, err
			}
			app.LogDecisions(ctx, serviceID, in.Body.Subject, in.Body.Checks, results, in.value())
			return OK(model.CheckResponse{Results: results})
		})

		Register(api, huma.Operation{
			OperationID: "delegated_permission",
			Method:      http.MethodPost,
			Path:        "/service/{service_id}/on_behalf/permission",
			Summary:     "A person's permissions in a service, asked on their behalf",
			Description: `What the person named in the body may do with the resources of the service, per resource type, as GET /service/{id}/permission answers it for the holder of a token. The filters and the paging are the same (type, access, limit, after), given in the body.` + delegateNote,
			Tags:        []string{"Delegated"},
			Security:    security,
			Errors:      delegateErrors,
		}, func(ctx context.Context, in *struct {
			ServiceRefPath
			ConsistencyParam
			Authorization string `header:"Authorization" hidden:"true"`
			Body          model.DelegatedPermissionInput
		}) (*Out[engine.PermissionResult], error) {
			ctx = in.freshness(ctx)
			serviceID, err := app.ServiceIDByRef(ctx, in.ServiceRef)
			if err != nil {
				return nil, err
			}
			client, callerSubject, err := authenticateDelegate(ctx, app, serviceID, in.Authorization)
			if err != nil {
				return nil, err
			}
			claims, err := engine.PrepareDelegated(in.Body.Subject, in.Body.Claims)
			if err != nil {
				return nil, err
			}
			if err := in.Body.Context.Validate(); err != nil {
				return nil, err
			}
			SetOnBehalfOf(ctx, in.Body.Subject)
			env := engine.NewEnv(claims, in.Body.Context)
			filter := engine.PermissionFilter{Type: in.Body.Type, Access: in.Body.Access, Limit: in.Body.Limit, After: in.Body.After}
			out, err := app.PermissionPage(ctx, serviceID, in.Body.Subject, env, in.value(), filter)
			if err != nil {
				return nil, err
			}
			if err := app.RecordDelegatedCall(ctx, serviceID, engine.DelegatedCall{
				CallerClient: client, CallerSubject: callerSubject, Subject: in.Body.Subject, Operation: "permission",
			}); err != nil {
				return nil, err
			}
			return OK(out)
		})

		registerAuthzen(api, app, security, authErrors)
	})
}

// requestEnv is the environment of a request: the claims of its token and the attributes given as a JSON object.
func requestEnv(claims engine.Claims, raw string) (engine.Env, error) {
	attrs := model.RequestContext{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &attrs); err != nil || attrs == nil {
			return engine.Env{}, apperr.Unprocessable(i18n.ContextInvalid)
		}
	}
	if err := attrs.Validate(); err != nil {
		return engine.Env{}, err
	}
	return engine.NewEnv(claims, attrs), nil
}
