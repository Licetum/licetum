package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/httpx"
	"github.com/licetum/licetum/core/model"
)

// pdpEngine is what the AuthZEN handlers ask of the engine: the decisions and the searches, the verification of a token and
// the audit of the calls on behalf of a person. *engine.App is the implementation; the handler tests give a fake of it
// (authzen_fake_test.go), so that they run without a database. It lists the methods the handlers call, and no more.
type pdpEngine interface {
	ServiceIDByRef(ctx context.Context, ref string) (uuid.UUID, error)
	GetServiceForCheck(ctx context.Context, serviceID uuid.UUID) (*model.Service, error)
	VerifyToken(ctx context.Context, svc *model.Service, token string) (engine.Claims, error)
	AuthenticatePEP(ctx context.Context, credential, serviceRef, operation string) (*engine.PEPCaller, error)
	NewEnv(claims engine.Claims, context map[string]any) engine.Env
	CheckAccess(ctx context.Context, serviceID uuid.UUID, subject string, env engine.Env, requests []model.CheckRequest, explain bool, consistency engine.Consistency) ([]model.CheckResult, error)
	LogDecisions(ctx context.Context, serviceID uuid.UUID, subject string, checks []model.CheckRequest, results []model.CheckResult, consistency engine.Consistency)
	GetPermission(ctx context.Context, serviceID uuid.UUID, subject string, env engine.Env, consistency engine.Consistency) (engine.PermissionResult, error)
	SearchResources(ctx context.Context, serviceID uuid.UUID, subject string, env engine.Env, q engine.ResourceSearch, consistency engine.Consistency) (engine.ResourcePage, error)
	DirectoryAfter(ctx context.Context, serviceID uuid.UUID, subjectType string, after *db.DirectoryKey, limit int) ([]db.DirectoryEntry, error)
	RecordDelegatedCall(ctx context.Context, serviceID uuid.UUID, call engine.DelegatedCall) error
}

var _ pdpEngine = (*engine.App)(nil)

// NewPDPHandler serves the regional PDP (licetum pdp), the process client services call on every request: the AuthZEN API of
// each service and, until P2-23, the routes the check API served before it (see registerLegacy). It is a separate app/port
// (8001) so that it can be scaled and deployed independently of the admin API. Its public port serves these routes and
// nothing else: the probes and the metrics are on the ops listener (NewOpsHandler), and the PDP has no docs or OpenAPI document
// on either listener (`licetum openapi pdp` prints the document).
func NewPDPHandler(app *engine.App) http.Handler {
	h, _ := NewPDPAPI(app)
	return h
}

// NewPDPAPI is the same but also returns the huma.API (to generate OpenAPI without running a server).
func NewPDPAPI(app *engine.App) (http.Handler, huma.API) {
	tags := []*huma.Tag{
		{Name: "Permission", Description: "Permission check: what the user holding this token may do with the service resources."},
		{Name: "Check", Description: "Single access checks with explanations."},
		{Name: authzenTag, Description: "The OpenID AuthZEN Authorization API 1.0 of a service: access evaluation and evaluations, the subject, resource and action searches, and the discovery document."},
		{Name: "Delegated", Description: "The same questions asked on behalf of a person by a client application the service trusts, without the person's token. Every call is recorded."},
		{Name: "System", Description: "Service endpoints."},
	}
	mux, api := newRouteTable("Licetum PDP", tags, false, func(api huma.API) {
		httpx.AddBearerScheme(api, "Access token issued by the IdP of the service whose permissions are being checked.")
		security := []map[string][]string{{httpx.BearerScheme: {}}}
		authErrors := []int{http.StatusUnauthorized, http.StatusFailedDependency}
		registerLegacy(api, app, security, authErrors)
		registerAuthzen(api, app, app.Cfg, security, authErrors)
	})
	return withStack(app, mux), api
}
