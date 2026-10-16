package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/redisx"
)

// The readiness of a process is what a balancer asks before it sends it requests (GET /readyz). It is not its liveness
// (GET /healthz, which depends on nothing): a process that is alive but not ready is left alone, not restarted.
//
// A process is ready when it is not draining (SetDraining, on SIGTERM), PostgreSQL answers within a second, the schema is
// at least the version this binary was built for (a newer one is fine: during a rolling update the pods of the previous
// release keep serving on the schema the new release has migrated), and, where the process keeps the policy in memory,
// that copy is loaded. Redis is not part of it: without Redis the process is degraded, not unavailable (see
// licetum_degraded).

// Why a process is not ready.
const (
	ReasonDraining = "draining"
	ReasonDatabase = "database"
	ReasonSchema   = "schema"
	ReasonSnapshot = "snapshot"
)

// NotReadyError is the reason Ready gives.
type NotReadyError struct {
	// Reason is one of the Reason constants.
	Reason string
	Err    error
}

func (e *NotReadyError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("not ready (%s): %v", e.Reason, e.Err)
	}
	return "not ready (" + e.Reason + ")"
}

func (e *NotReadyError) Unwrap() error { return e.Err }

const (
	// readyCacheTTL is how long the result of the database check is reused: a probe every second from every balancer must
	// not become a query every second per balancer.
	readyCacheTTL = time.Second
	// readyTimeout is how long PostgreSQL has to answer the readiness check.
	readyTimeout = time.Second
	// healthWindow is how long after its last failure a dependency is still reported as degraded when nothing has succeeded since.
	healthWindow = 10 * time.Second
)

// readyState is the part of the App that answers the readiness probe.
type readyState struct {
	draining atomic.Bool
	mu       sync.Mutex
	at       time.Time
	err      error
	flight   singleflight.Group
}

// SetDraining makes the process report itself not ready from now on, while it goes on serving: the balancer needs a moment
// to notice, and the requests it still sends must be answered (see cli.Serve).
func (a *App) SetDraining() { a.ready.draining.Store(true) }

// Draining reports whether SetDraining was called.
func (a *App) Draining() bool { return a.ready.draining.Load() }

// Ready returns nil when the process may take requests, and a *NotReadyError that says why when it may not.
func (a *App) Ready(ctx context.Context) error {
	if a.ready.draining.Load() {
		return &NotReadyError{Reason: ReasonDraining}
	}
	if err := a.databaseReady(); err != nil {
		return err
	}
	return a.snapshotReady(ctx)
}

// databaseReady is the database part of the check, reused for readyCacheTTL.
func (a *App) databaseReady() error {
	a.ready.mu.Lock()
	if !a.ready.at.IsZero() && time.Since(a.ready.at) < readyCacheTTL {
		err := a.ready.err
		a.ready.mu.Unlock()
		return err
	}
	a.ready.mu.Unlock()

	v, _, _ := a.ready.flight.Do("database", func() (any, error) {
		// The check does not depend on the probe that asked for it, and it is not part of the work of a request.
		ctx, cancel := context.WithTimeout(db.Uncounted(context.Background()), readyTimeout)
		defer cancel()
		err := a.checkDatabase(ctx)
		a.ready.mu.Lock()
		a.ready.at, a.ready.err = time.Now(), err
		a.ready.mu.Unlock()
		return err, nil
	})
	err, _ := v.(error)
	return err
}

func (a *App) checkDatabase(ctx context.Context) error {
	if a.DB == nil {
		return &NotReadyError{Reason: ReasonDatabase, Err: errors.New("no database")}
	}
	version, err := db.SchemaVersion(ctx, a.DB)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table: the database was never migrated
			return &NotReadyError{Reason: ReasonSchema, Err: errors.New("the database has not been migrated")}
		}
		return &NotReadyError{Reason: ReasonDatabase, Err: err}
	}
	if latest := db.LatestMigration(); version < latest {
		return &NotReadyError{Reason: ReasonSchema, Err: fmt.Errorf("the schema is at version %d, this release needs %d: run `licetum migrate`", version, latest)}
	}
	return nil
}

// snapshotReady checks that the process has its copy of the policy, where it keeps one. A policy that does not fit in
// memory is not copied by decision (the queries read it), which is not a reason to stay unready.
func (a *App) snapshotReady(ctx context.Context) error {
	if !a.localEnabled() || a.Role == RoleWorker {
		return nil
	}
	v := a.local.view.Load()
	if v == nil {
		// No reading yet (the process has just started): make it now, sharing it with the poll.
		ctx, cancel := context.WithTimeout(ctx, readyTimeout)
		defer cancel()
		v = a.renewView(ctx)
	}
	switch {
	case v == nil:
		return &NotReadyError{Reason: ReasonSnapshot, Err: errors.New("the revision of the policy has not been read")}
	case v.snap != nil, a.local.oversized.Load() == v.state.version+1:
		return nil
	}
	return &NotReadyError{Reason: ReasonSnapshot, Err: errors.New("the policy snapshot is not loaded")}
}

// dependencyHealth remembers whether the calls to a dependency have been failing lately.
type dependencyHealth struct{ failedAt, okAt atomic.Int64 }

func (h *dependencyHealth) note(down bool) {
	now := time.Now().UnixNano()
	if down {
		h.failedAt.Store(now)
		return
	}
	h.okAt.Store(now)
}

// degraded: the last call that told anything failed, and not long ago.
func (h *dependencyHealth) degraded() bool {
	failed := h.failedAt.Load()
	return failed != 0 && failed >= h.okAt.Load() && time.Since(time.Unix(0, failed)) < healthWindow
}

// redisDown: the call failed because Redis could not be reached (or is loading its dataset and answers nothing else), not
// because it answered a command with an error of that command. A read-only Redis refuses writes only; the reads that
// succeed keep it from being reported down.
func redisDown(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, redisx.ErrSkipped) {
		return false
	}
	var reply redis.Error
	return !errors.As(err, &reply) || errKind(reply) == "redis:LOADING"
}

// degradedCollector reports, at every scrape, which dependencies the process is running without, and how old its copy of
// the policy is.
type degradedCollector struct{ app *App }

var (
	degradedDesc = prometheus.NewDesc("licetum_degraded",
		"1 while the process runs without a dependency: redis (no cache: answers come from memory or are computed) or postgres "+
			"(the policy is served from the copy in memory, unconfirmed).", []string{"component"}, nil)
	snapshotAgeDesc = prometheus.NewDesc("licetum_snapshot_age_seconds",
		"Seconds since the process last confirmed its copy of the policy against PostgreSQL (the revision was read). It stays near "+
			"the poll interval while the database answers and grows while it does not; POLICY__MAX_STALENESS bounds it. Zero when "+
			"the process keeps no copy.", nil, nil)
)

func (c *degradedCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- degradedDesc
	ch <- snapshotAgeDesc
}

func (c *degradedCollector) Collect(ch chan<- prometheus.Metric) {
	a := c.app
	if a == nil {
		return
	}
	flag := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	age, stale := 0.0, false
	if v := a.local.view.Load(); v != nil {
		age = time.Since(v.readAt).Seconds()
		stale = time.Duration(age*float64(time.Second)) >= staleLimit(a.tuningOrDefault())
	}
	ch <- prometheus.MustNewConstMetric(degradedDesc, prometheus.GaugeValue, flag(a.redisDegraded()), "redis")
	ch <- prometheus.MustNewConstMetric(degradedDesc, prometheus.GaugeValue, flag(a.health.postgres.degraded() || stale), "postgres")
	ch <- prometheus.MustNewConstMetric(snapshotAgeDesc, prometheus.GaugeValue, age)
}
