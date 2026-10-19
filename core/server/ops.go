package server

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/httpx"
)

// NewOpsHandler serves the operations endpoints of the PDP on its ops listener (PDP__OPS_ADDR): the liveness probe
// (GET /healthz), the readiness probe (GET /readyz, the same answer as the admin API's) and the metrics (GET /metrics). Nothing
// else is on this listener, and none of these is on the public port (NewPDPHandler). The listener has no admission control
// and no request timeout: a probe is answered however loaded the public port is.
func NewOpsHandler(app *engine.App) http.Handler {
	mux, _ := newRouteTable("Licetum PDP operations", nil, false, func(api huma.API) {
		registerProbes(api, app)
	})
	mux.Handle("GET /metrics", app.MetricsHandler())
	return logAndRecover(app, httpx.WithGzip(stripTrailingSlash(withLang(mux))))
}
