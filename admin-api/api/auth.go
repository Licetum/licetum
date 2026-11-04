package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/licetum/licetum/core/httpx"
	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// The admin API is protected by the platform's own policies: the token is verified against the
// self-service (PLATFORM__*) and permissions come from the same AssignmentRules as for any other
// service, on the resource types in model.PlatformTypes. An operation declares its policy with
// withPolicy; platformAuth enforces it before the request body is even parsed.

type policyScope int

const (
	// scopeSignedIn: any valid platform token, no particular permission.
	scopeSignedIn policyScope = iota
	// scopeAny: a collection (list) — access to any entity of the type is enough; the handler must
	// filter the response with service.FilterAllowed.
	scopeAny
	// scopeWholeType: creating a new entity — needs access to the whole type; a point grant on one
	// existing entity does not allow creating new ones.
	scopeWholeType
	// scopeResource: a specific entity whose id is the path parameter `param`.
	scopeResource
)

type policy struct {
	typ    model.PlatformType
	action model.Action
	scope  policyScope
	param  string
}

func signedIn() policy { return policy{scope: scopeSignedIn} }

func requireAny(t model.PlatformType, a model.Action) policy {
	return policy{typ: t, action: a, scope: scopeAny}
}

func requireWholeType(t model.PlatformType, a model.Action) policy {
	return policy{typ: t, action: a, scope: scopeWholeType}
}

func requireResource(t model.PlatformType, a model.Action, param string) policy {
	return policy{typ: t, action: a, scope: scopeResource, param: param}
}

func (p policy) allows(access *service.Access, ctx huma.Context) bool {
	switch p.scope {
	case scopeAny:
		return access.CanAny(p.typ, p.action)
	case scopeWholeType:
		return access.CanWholeType(p.typ, p.action)
	case scopeResource:
		return access.Can(p.typ, p.action, ctx.Param(p.param))
	default:
		return true
	}
}

// withPolicy attaches a platform policy to an operation and documents the bearer auth and the
// errors it adds.
func withPolicy(op huma.Operation, p policy) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[httpx.PolicyMetadataKey] = p
	op.Security = []map[string][]string{{httpx.BearerScheme: {}}}
	op.Errors = append(op.Errors, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusFailedDependency, http.StatusServiceUnavailable)
	return op
}

type accessKey struct{}

// accessFrom returns the caller's platform access checked by platformAuth.
func accessFrom(ctx context.Context) *service.Access {
	access, _ := ctx.Value(accessKey{}).(*service.Access)
	return access
}

// platformAuth is the Huma middleware enforcing withPolicy. It has to be installed before the
// operations are registered.
func platformAuth(app *service.App) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		p, ok := op.Metadata[httpx.PolicyMetadataKey].(policy)
		if !ok {
			next(ctx)
			return
		}
		access, err := app.Authenticate(ctx.Context(), ctx.Header("Authorization"))
		if err == nil && !p.allows(access, ctx) {
			err = apperr.Forbidden(i18n.AccessDenied, p.action, p.typ)
		}
		if err != nil {
			apiErr := server.LocalizeError(ctx.Context(), op.OperationID, err)
			for name, values := range apiErr.GetHeaders() {
				for _, v := range values {
					ctx.AppendHeader(name, v)
				}
			}
			ctx.SetHeader("Content-Type", "application/json")
			ctx.SetStatus(apiErr.Status)
			_ = json.NewEncoder(ctx.BodyWriter()).Encode(apiErr)
			return
		}
		server.SetRequestUser(ctx.Context(), access.Subject)
		next(huma.WithValue(ctx, accessKey{}, access))
	}
}

// filterAllowed keeps the list items the caller may read.
func filterAllowed[T any](ctx context.Context, t model.PlatformType, items []T, id func(T) uuid.UUID) []T {
	return service.FilterAllowed(accessFrom(ctx), t, model.ActionRead, items, id)
}
