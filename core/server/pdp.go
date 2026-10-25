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

// pdpEngine is what the AuthZEN handlers ask of the engine: the authentication of a PEP client, the decisions and the searches.
// *engine.App is the implementation; the handler tests give a fake of it
// (authzen_fake_test.go), so that they run without a database. It lists the methods the handlers call, and no more.
type pdpEngine interface {
	ServiceIDByRef(ctx context.Context, ref string) (uuid.UUID, error)
	AuthenticatePEP(ctx context.Context, credential, serviceRef, operation string) (*engine.PEPCaller, error)
	NewEnv(claims engine.Claims, context map[string]any) engine.Env
	CheckAccess(ctx context.Context, serviceID uuid.UUID, subject string, env engine.Env, requests []model.CheckRequest, explain bool, consistency engine.Consistency) ([]model.CheckResult, error)
	LogDecisions(ctx context.Context, serviceID uuid.UUID, subject string, checks []model.CheckRequest, results []model.CheckResult, consistency engine.Consistency)
	GetPermission(ctx context.Context, serviceID uuid.UUID, subject string, env engine.Env, consistency engine.Consistency) (engine.PermissionResult, error)
	SearchResources(ctx context.Context, serviceID uuid.UUID, subject string, env engine.Env, q engine.ResourceSearch, consistency engine.Consistency) (engine.ResourcePage, error)
	DirectoryAfter(ctx context.Context, serviceID uuid.UUID, subjectType string, after *db.DirectoryKey, limit int) ([]db.DirectoryEntry, error)
}

var _ pdpEngine = (*engine.App)(nil)

// NewPDPHandler serves the regional PDP (licetum pdp), the process client services call on every request: the AuthZEN API of
// each service. It is a separate app/port (8001) so that it can be scaled and deployed independently of the admin API. Its public
// port serves these routes and
// nothing else: the probes and the metrics are on the ops listener (NewOpsHandler), and the PDP has no docs or OpenAPI document
// on either listener (`licetum openapi pdp` prints the document).
func NewPDPHandler(app *engine.App) http.Handler {
	h, _ := NewPDPAPI(app)
	return h
}

// NewPDPAPI is the same but also returns the huma.API (to generate OpenAPI without running a server).
func NewPDPAPI(app *engine.App) (http.Handler, huma.API) {
	tags := []*huma.Tag{
		{Name: authzenTag, Description: "The OpenID AuthZEN Authorization API 1.0 of a service: access evaluation and evaluations, the subject, resource and action searches, and the discovery document."},
		{Name: "System", Description: "Service endpoints."},
	}
	mux, api := newRouteTable("Licetum PDP", tags, false, func(api huma.API) {
		httpx.AddBearerScheme(api, "The API key of a PEP client of the service, or the access token of an issuer registered for PEPs.")
		security := []map[string][]string{{httpx.BearerScheme: {}}}
		authErrors := []int{http.StatusUnauthorized, http.StatusFailedDependency}
		registerAuthzen(api, app, app.Cfg, security, authErrors)
	})
	return withStack(app, mux), api
}
