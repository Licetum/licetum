package server

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/httpx"
)

// NewPDPHandler serves the regional PDP (licetum pdp), the process client services call on every request: the AuthZEN API of
// each service and, until P2-23, the routes the check API served before it (see registerLegacy). It is a separate app/port
// (8001) so that it can be scaled and deployed independently of the admin API.
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
	return NewServer(app, "Licetum PDP", tags, func(api huma.API) {
		httpx.AddBearerScheme(api, "Access token issued by the IdP of the service whose permissions are being checked.")
		security := []map[string][]string{{httpx.BearerScheme: {}}}
		authErrors := []int{http.StatusUnauthorized, http.StatusFailedDependency}
		registerLegacy(api, app, security, authErrors)
		registerAuthzen(api, app, security, authErrors)
	})
}
