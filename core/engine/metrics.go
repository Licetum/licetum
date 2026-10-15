package engine

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metrics are the Prometheus metrics of one App. They live in their own registry (so tests can
// create many Apps) together with the Go runtime and process collectors.
type metrics struct {
	registry *prometheus.Registry

	permissionCompute prometheus.Histogram
	permissionCache   *prometheus.CounterVec // result: hit | miss | bypass
	checkCache        *prometheus.CounterVec // result: hit | miss | bypass
	checks            *prometheus.CounterVec // decision: allowed | denied
	syncFetch         prometheus.Histogram
	syncResults       *prometheus.CounterVec // result: changed | unchanged | unanswered
	syncJobs          *prometheus.CounterVec // state: done | failed | requeued | lease_lost
	syncJobSeconds    prometheus.Histogram
	syncLeaseLost     prometheus.Counter
	sourceCalls       *prometheus.CounterVec // type, outcome: ok | error
	breakerOpen       *prometheus.CounterVec // dependency: source | jwks | token_endpoint
	httpRequests      *prometheus.CounterVec // operation, status
	httpDuration      *prometheus.HistogramVec
}

func newMetrics() *metrics {
	m := &metrics{registry: prometheus.NewRegistry()}
	factory := func(c prometheus.Collector) { m.registry.MustRegister(c) }
	factory(collectors.NewGoCollector())
	factory(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m.permissionCompute = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "licetum_permission_compute_seconds",
		Help:    "Time to compute the permissions of a subject in a service (cache misses only).",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 14),
	})
	m.permissionCache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_permission_cache_total",
		Help: "Permission result cache lookups: hit, miss, or bypass (higher consistency requested).",
	}, []string{"result"})
	m.checkCache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_check_cache_total",
		Help: "Check answer cache lookups: hit, miss, or bypass (higher consistency requested).",
	}, []string{"result"})
	m.checks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_check_decisions_total",
		Help: "Decisions of single access checks.",
	}, []string{"decision"})
	m.syncFetch = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "licetum_source_fetch_seconds",
		Help:    "Time to fetch the resources of the sources of one synchronization round (in parallel).",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
	})
	m.syncResults = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_source_sync_total",
		Help: "Synchronizations of a resource type: changed (written), unchanged, or unanswered (the source did not answer).",
	}, []string{"result"})
	m.syncJobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_sync_jobs_finished_total",
		Help: "Synchronization jobs a worker finished, by how they ended: done, failed, requeued (waited too long for a lock) or lease_lost.",
	}, []string{"state"})
	m.syncJobSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "licetum_sync_job_seconds",
		Help:    "Time a worker spends on one synchronization job: fetching the sources and applying the result.",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 14),
	})
	m.syncLeaseLost = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "licetum_sync_lease_lost_total",
		Help: "Times an executor found that it no longer owned its job (the lease ran out): its result was dropped.",
	})
	m.sourceCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_source_calls_total",
		Help: "Calls that asked the source of a resource type for its list, by type and outcome.",
	}, []string{"type", "outcome"})
	m.breakerOpen = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_breaker_open_total",
		Help: "Calls skipped because the circuit breaker of a dependency was open.",
	}, []string{"dependency"})
	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_http_requests_total",
		Help: "HTTP requests by API operation and status.",
	}, []string{"operation", "status"})
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "licetum_http_request_duration_seconds",
		Help:    "HTTP request duration by API operation.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 14),
	}, []string{"operation"})

	for _, c := range []prometheus.Collector{
		m.permissionCompute, m.permissionCache, m.checkCache, m.checks, m.syncFetch, m.syncResults, m.syncJobs, m.syncJobSeconds, m.syncLeaseLost, m.sourceCalls, m.breakerOpen,
		m.httpRequests, m.httpDuration,
	} {
		factory(c)
	}
	return m
}

// MetricsHandler serves the metrics in the Prometheus text format.
func (a *App) MetricsHandler() http.Handler {
	if a.metrics == nil { // an App built only to describe the API
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(a.metrics.registry, promhttp.HandlerOpts{})
}

// ObserveHTTP records one served request of an API operation ("" for routes outside the API).
func (a *App) ObserveHTTP(operation, status string, seconds float64) {
	if a.metrics == nil {
		return
	}
	if operation == "" {
		operation = "unmatched"
	}
	a.metrics.httpRequests.WithLabelValues(operation, status).Inc()
	a.metrics.httpDuration.WithLabelValues(operation).Observe(seconds)
}

// syncQueueCollector reports the synchronization queue as the database sees it, at every scrape: the jobs waiting and
// running, and the age of the oldest job waiting. The age keeps growing in a process that serves requests when every
// worker is down, which is what an alert needs.
type syncQueueCollector struct{ app *App }

var (
	syncJobsDesc = prometheus.NewDesc("licetum_sync_jobs",
		"Synchronization jobs in the queue, by state (queued, running).", []string{"state"}, nil)
	syncQueueAgeDesc = prometheus.NewDesc("licetum_sync_queue_age_seconds",
		"Age of the oldest synchronization job that is still waiting for a worker.", nil, nil)
)

func (c *syncQueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- syncJobsDesc
	ch <- syncQueueAgeDesc
}

func (c *syncQueueCollector) Collect(ch chan<- prometheus.Metric) {
	if c.app == nil || c.app.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var queued, running int64
	var age float64
	err := c.app.DB.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'queued'), count(*) FILTER (WHERE state = 'running'),
		       COALESCE(EXTRACT(EPOCH FROM now() - min(created_at) FILTER (WHERE state = 'queued')), 0)::float8
		FROM sync_job WHERE state IN ('queued', 'running')`).Scan(&queued, &running, &age)
	if err != nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(syncJobsDesc, prometheus.GaugeValue, float64(queued), "queued")
	ch <- prometheus.MustNewConstMetric(syncJobsDesc, prometheus.GaugeValue, float64(running), "running")
	ch <- prometheus.MustNewConstMetric(syncQueueAgeDesc, prometheus.GaugeValue, age)
}
