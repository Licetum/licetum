package engine

import (
	"context"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/licetum/licetum/core/logx"
)

// Admission control keeps a process that is asked for more than it can do from doing all of it badly: it serves at most
// HTTP__MAX_INFLIGHT requests at once, and refuses the others at once with 503 and Retry-After. Without a limit every
// request is taken, they queue for the CPU, the pool and the lock, the latency of all of them grows until the clients time
// out, and the work already done is thrown away. A refused request costs a counter increment.

// defaultInflightPerCPU is HTTP__MAX_INFLIGHT per GOMAXPROCS when the setting is zero: requests mostly wait for the
// database and Redis, so many more than the cores can be served at once.
const defaultInflightPerCPU = 256

// Default request deadlines, when HTTP__REQUEST_TIMEOUT is zero: the PDP answers within a budget; the admin API
// waits for synchronizations (SYNC__ADMIN_WAIT) and keeps the 60 s the server always allowed a response.
const (
	defaultPDPRequestTimeout   = 10 * time.Second
	defaultAdminRequestTimeout = 60 * time.Second
)

// MaxInflight is the number of requests the process serves at once (0: no limit).
func (a *App) MaxInflight() int {
	n := 0
	if a.Cfg != nil {
		n = a.Cfg.HTTP.MaxInflight
	}
	switch {
	case n < 0:
		return 0
	case n == 0:
		return runtime.GOMAXPROCS(0) * defaultInflightPerCPU
	}
	return n
}

// RequestTimeout is the deadline of a request (0: none).
func (a *App) RequestTimeout() time.Duration {
	var d time.Duration
	if a.Cfg != nil {
		d = a.Cfg.HTTP.RequestTimeout
	}
	switch {
	case d < 0:
		return 0
	case d == 0 && a.Role == RolePDP:
		return defaultPDPRequestTimeout
	case d == 0 && (a.Role == RoleAdmin || a.Role == RoleWorker):
		return defaultAdminRequestTimeout
	}
	return d // an engine without a role (tests, tools) has a deadline only when one is set
}

// admission counts the requests in flight and the ones refused.
type admission struct {
	inflight atomic.Int64
	shed     atomic.Int64
}

// Admit takes a place for a request. When the limit is reached it returns false (and counts the refusal); otherwise the
// caller must call release when the request is done.
func (a *App) Admit() (release func(), ok bool) {
	limit := int64(a.MaxInflight())
	if n := a.admit.inflight.Add(1); limit > 0 && n > limit {
		a.admit.inflight.Add(-1)
		a.admit.shed.Add(1)
		logx.Repeated.Log(context.Background(), slog.LevelWarn, "shed",
			"requests are being refused: the limit of requests in flight is reached (HTTP__MAX_INFLIGHT)", "limit", limit)
		return nil, false
	}
	return func() { a.admit.inflight.Add(-1) }, true
}

// Inflight is the number of requests being served.
func (a *App) Inflight() int64 { return a.admit.inflight.Load() }

func (a *App) registerAdmissionMetrics(m *metrics) {
	m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "licetum_shed_total",
		Help: "Requests refused with 503 and Retry-After because the process already served HTTP__MAX_INFLIGHT requests.",
	}, func() float64 { return float64(a.admit.shed.Load()) }))
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "licetum_http_inflight",
		Help: "Requests being served right now that count against HTTP__MAX_INFLIGHT (not the health, readiness and metrics requests).",
	}, func() float64 { return float64(a.admit.inflight.Load()) }))
}
