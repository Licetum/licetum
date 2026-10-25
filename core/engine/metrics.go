package engine

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"

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
	searchCache       *prometheus.CounterVec // result: hit | miss | bypass
	checks            *prometheus.CounterVec // decision: allowed | denied
	syncFetch         prometheus.Histogram
	syncResults       *prometheus.CounterVec // result: changed | unchanged | unanswered
	syncJobs          *prometheus.CounterVec // state: done | failed | requeued | lease_lost
	syncJobSeconds    prometheus.Histogram
	syncLeaseLost     prometheus.Counter
	sourceCalls       *prometheus.CounterVec // type, outcome: ok | error
	httpRequests      *prometheus.CounterVec // operation, status
	httpDuration      *prometheus.HistogramVec
	dbQueries         *prometheus.CounterVec   // operation, result
	dbQueryDuration   *prometheus.HistogramVec // operation
	backgroundDropped prometheus.Counter
	snapshotFallback  prometheus.Counter
	revisionRegress   prometheus.Counter
	l1Cache           *prometheus.CounterVec // cache: answer, result: hit | miss
	jwksFetches       *prometheus.CounterVec // result: ok | error
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
		Help: "Permission result cache lookups: hit, miss, bypass (higher consistency requested), or stale (an answer served past its time because the revisions of the policy cannot be read).",
	}, []string{"result"})
	m.checkCache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_check_cache_total",
		Help: "Check answer cache lookups: hit, miss, bypass (higher consistency requested), or stale (an answer served past its time because the revisions of the policy cannot be read).",
	}, []string{"result"})
	m.searchCache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_search_cache_total",
		Help: "Resource search page cache lookups: hit, miss, bypass (higher consistency requested), or stale (a page served past its time because the revisions of the policy cannot be read).",
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
	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_http_requests_total",
		Help: "HTTP requests by API operation and status.",
	}, []string{"operation", "status"})
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "licetum_http_request_duration_seconds",
		Help:    "HTTP request duration by API operation.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 14),
	}, []string{"operation"})

	m.dbQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_db_queries_total",
		Help: "Statements sent to PostgreSQL by the first SQL keyword (operation) and result (ok | error).",
	}, []string{"operation", "result"})
	m.dbQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "licetum_db_query_seconds",
		Help:    "Time a PostgreSQL statement took, by the first SQL keyword.",
		Buckets: prometheus.ExponentialBuckets(0.0002, 2, 16),
	}, []string{"operation"})

	m.backgroundDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "licetum_background_dropped_total",
		Help: "Background tasks (cache writes, refreshes) dropped because the queue was full.",
	})
	m.snapshotFallback = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "licetum_snapshot_fallback_total",
		Help: "Policy snapshots that were not used because the policy does not fit SNAPSHOT__MAX_BYTES: the queries read the policy.",
	})
	m.revisionRegress = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "licetum_revision_regressions_total",
		Help: "Times the revision of the policy read from the database was lower than the one the process had: the database was restored or a replica promoted after a failover. Run `licetum failover`.",
	})
	m.l1Cache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_l1_cache_total",
		Help: "Lookups in the in-process answer cache, by cache (answer) and result (hit | miss).",
	}, []string{"cache", "result"})
	m.jwksFetches = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "licetum_jwks_fetch_total",
		Help: "Requests for a JWKS to an identity provider, by result (ok | error).",
	}, []string{"result"})

	for _, c := range []prometheus.Collector{
		m.permissionCompute, m.permissionCache, m.checkCache, m.searchCache, m.checks, m.syncFetch, m.syncResults, m.syncJobs, m.syncJobSeconds, m.syncLeaseLost, m.sourceCalls,
		m.backgroundDropped, m.snapshotFallback, m.revisionRegress, m.l1Cache, m.jwksFetches,
		m.dbQueries, m.dbQueryDuration,
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
	ctx, cancel := context.WithTimeout(db.Uncounted(context.Background()), 2*time.Second)
	defer cancel()
	var queued, running int64
	var age float64
	err := c.app.DB.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FILTER (WHERE status = 'ENQUEUED'), count(*) FILTER (WHERE status = 'PENDING'),
		       COALESCE((EXTRACT(EPOCH FROM now()) * 1000 - min(created_at) FILTER (WHERE status = 'ENQUEUED')) / 1000, 0)::float8
		FROM %s.workflow_status WHERE queue_name = $1 AND status IN ('ENQUEUED', 'PENDING')`,
		pgx.Identifier{c.app.dbosSchema()}.Sanitize()), dbosQueue).Scan(&queued, &running, &age)
	if err != nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(syncJobsDesc, prometheus.GaugeValue, float64(queued), "queued")
	ch <- prometheus.MustNewConstMetric(syncJobsDesc, prometheus.GaugeValue, float64(running), "running")
	ch <- prometheus.MustNewConstMetric(syncQueueAgeDesc, prometheus.GaugeValue, age)
}

// poolCollector reports the connection pools of the process at every scrape: the request pool and, in the processes that
// have one, the pool of the synchronization transactions. The acquire counters are what shows a pool that is too small
// (requests wait for a connection: the empty acquires and the time they waited) or too large for the server
// (licetum_db_pool_connections summed over the replicas against max_connections).
type poolCollector struct{ app *App }

var (
	poolConnsDesc = prometheus.NewDesc("licetum_db_pool_connections",
		"Connections of a pool by state: acquired (in use), idle, constructing (being opened).", []string{"pool", "state"}, nil)
	poolMaxDesc = prometheus.NewDesc("licetum_db_pool_max_connections",
		"The most connections a pool may hold (DB__POOL_SIZE + DB__MAX_OVERFLOW for the request pool).", []string{"pool"}, nil)
	poolAcquireDesc = prometheus.NewDesc("licetum_db_pool_acquire_total",
		"Connections taken from a pool.", []string{"pool"}, nil)
	poolEmptyAcquireDesc = prometheus.NewDesc("licetum_db_pool_empty_acquire_total",
		"Acquires that found no idle connection and had to wait for one or to open one.", []string{"pool"}, nil)
	poolCanceledDesc = prometheus.NewDesc("licetum_db_pool_canceled_acquire_total",
		"Acquires that were given up (the request ended) before a connection came.", []string{"pool"}, nil)
	poolAcquireSecondsDesc = prometheus.NewDesc("licetum_db_pool_acquire_seconds_total",
		"Time spent waiting for a connection of a pool, summed over the acquires that had to wait.", []string{"pool"}, nil)
	poolNewConnsDesc = prometheus.NewDesc("licetum_db_pool_new_connections_total",
		"Connections a pool has opened.", []string{"pool"}, nil)
)

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{poolConnsDesc, poolMaxDesc, poolAcquireDesc, poolEmptyAcquireDesc, poolCanceledDesc,
		poolAcquireSecondsDesc, poolNewConnsDesc} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	if c.app == nil {
		return
	}
	pools := []struct {
		name string
		pool *pgxpool.Pool
	}{{"request", c.app.DB}}
	if c.app.SyncDB != nil && c.app.SyncDB != c.app.DB {
		pools = append(pools, struct {
			name string
			pool *pgxpool.Pool
		}{"sync", c.app.SyncDB})
	}
	for _, p := range pools {
		if p.pool == nil {
			continue
		}
		st := p.pool.Stat()
		ch <- prometheus.MustNewConstMetric(poolConnsDesc, prometheus.GaugeValue, float64(st.AcquiredConns()), p.name, "acquired")
		ch <- prometheus.MustNewConstMetric(poolConnsDesc, prometheus.GaugeValue, float64(st.IdleConns()), p.name, "idle")
		ch <- prometheus.MustNewConstMetric(poolConnsDesc, prometheus.GaugeValue, float64(st.ConstructingConns()), p.name, "constructing")
		ch <- prometheus.MustNewConstMetric(poolMaxDesc, prometheus.GaugeValue, float64(st.MaxConns()), p.name)
		ch <- prometheus.MustNewConstMetric(poolAcquireDesc, prometheus.CounterValue, float64(st.AcquireCount()), p.name)
		ch <- prometheus.MustNewConstMetric(poolEmptyAcquireDesc, prometheus.CounterValue, float64(st.EmptyAcquireCount()), p.name)
		ch <- prometheus.MustNewConstMetric(poolCanceledDesc, prometheus.CounterValue, float64(st.CanceledAcquireCount()), p.name)
		ch <- prometheus.MustNewConstMetric(poolAcquireSecondsDesc, prometheus.CounterValue, st.EmptyAcquireWaitTime().Seconds(), p.name)
		ch <- prometheus.MustNewConstMetric(poolNewConnsDesc, prometheus.CounterValue, float64(st.NewConnsCount()), p.name)
	}
}

// l1Lookup counts a lookup in an in-process cache (nil-safe).
func (m *metrics) l1Lookup(cache string, hit bool) {
	if m == nil {
		return
	}
	result := "miss"
	if hit {
		result = "hit"
	}
	m.l1Cache.WithLabelValues(cache, result).Inc()
}
