package engine

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/i18n"
)

// A PDP may read the policy from a read replica (DB__READ_URI, App.ReadDB) while everything that writes, and every
// read that must be current, goes to the primary (App.DB). The revision the process acts on is read from the replica too, so it
// is the revision of the data the process has: a region that sees revision R in its replica has the data of R.
//
// A replica that falls behind is a lag the process measures itself (WatchReplica): DB__MAX_REPLICA_LAG decides when it matters and
// DB__LAG_ACTION what happens then (readiness fails, or the reads move to the primary until the replica catches up).
// POLICY__MAX_STALENESS applies to the replica as well: a replica more behind than that fails closed.

// UseReadReplica makes the process read the policy from pool (see reader). The pool is counted like the request pool.
func (a *App) UseReadReplica(pool *pgxpool.Pool) {
	a.ReadDB = pool
	db.Observe(pool, observer{a})
}

type primaryKey struct{}

// withPrimary makes the reads of the request go to the primary.
func withPrimary(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryKey{}, true)
}

func primaryFrom(ctx context.Context) bool {
	v, _ := ctx.Value(primaryKey{}).(bool)
	return v
}

// replicaState is what WatchReplica measured.
type replicaState struct {
	measured atomic.Bool
	// lag is the larger of the replay lag the replica reports and the time its revision has been behind the primary's.
	lag atomic.Int64
	// revisionLag is how many revisions (all scopes together) the replica is behind the primary.
	revisionLag atomic.Int64
	// pendingTotal and pendingSince: the sum of the revisions of the primary when the replica was last seen behind, and when
	// that was. The replica is behind for as long as it has not reached pendingTotal; a primary that keeps moving does not
	// make a replica that follows it by a few milliseconds look late.
	pendingTotal int64
	pendingSince time.Time
	// confirmedAt is when the replica was last compared with the primary (unix nanoseconds; the start of the process before the
	// first comparison). A replica that cannot be compared - the link to the primary is cut, or the replica itself does not answer
	// - may be arbitrarily behind without the process knowing.
	confirmedAt atomic.Int64
}

// reader is the pool the reads of the request go to: the replica when there is one, the primary for a request that needs a current
// read, and while the replica lags and DB__LAG_ACTION is primary.
func (a *App) reader(ctx context.Context) *pgxpool.Pool {
	if a.ReadDB == nil || primaryFrom(ctx) || a.replicaReadsFromPrimary() {
		return a.DB
	}
	return a.ReadDB
}

func (a *App) replicaLag() time.Duration { return time.Duration(a.replica.lag.Load()) }

// replicaUnconfirmed is how long it has been since the replica was last compared with the primary.
func (a *App) replicaUnconfirmed() time.Duration {
	at := a.replica.confirmedAt.Load()
	if at == 0 {
		return 0
	}
	return time.Since(time.Unix(0, at))
}

func (a *App) replicaLagging() bool {
	return a.ReadDB != nil && a.replica.measured.Load() && a.replicaLag() > a.Cfg.DB.MaxReplicaLag
}

// replicaReadsFromPrimary: the replica lags and the action is to read from the primary meanwhile.
func (a *App) replicaReadsFromPrimary() bool {
	return a.Cfg != nil && a.Cfg.DB.LagAction == "primary" && a.replicaLagging()
}

// replicaGate fails closed while the replica is behind for longer than POLICY__MAX_STALENESS and the reads are not on the primary.
func (a *App) replicaGate() error {
	if a.ReadDB == nil {
		return nil
	}
	limit := a.tuningOrDefault().policyMaxStaleness
	// The replica is as stale as it is behind, or as long as it has gone without being compared with the primary, whichever is more.
	if limit <= 0 || max(a.replicaLag(), a.replicaUnconfirmed()) <= limit || a.replicaReadsFromPrimary() {
		return nil
	}
	return apperr.ServiceUnavailable(i18n.PolicyUnavailable).WithHeader("Retry-After", "1")
}

// WatchReplica measures the lag of the replica every DB__LAG_INTERVAL until ctx ends. It does nothing without a replica.
func (a *App) WatchReplica(ctx context.Context) {
	if a.ReadDB == nil {
		return
	}
	a.replica.confirmedAt.CompareAndSwap(0, time.Now().UnixNano())
	ticker := time.NewTicker(a.Cfg.DB.LagInterval)
	defer ticker.Stop()
	for {
		a.measureReplica(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// replayLagSQL is the lag the replica reports: zero when it has replayed everything it received (an idle primary does not make
// the timestamp of the last replayed transaction a lag), otherwise the age of the last replayed transaction.
const replayLagSQL = `
	SELECT CASE
	  WHEN NOT pg_is_in_recovery() THEN 0
	  WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
	  ELSE coalesce(extract(epoch FROM now() - pg_last_xact_replay_timestamp()), 0)
	END::float8`

func totalRevision(r db.Revisions) int64 {
	total := r.Rules + r.LogSettings
	for _, v := range r.Data {
		total += v
	}
	return total
}

func (a *App) measureReplica(ctx context.Context) {
	ctx, cancel := context.WithTimeout(db.Uncounted(context.WithoutCancel(ctx)), 2*time.Second)
	defer cancel()
	var replay float64
	if err := a.ReadDB.QueryRow(ctx, replayLagSQL).Scan(&replay); err != nil {
		// The replica does not answer: readiness reports it, and the view ages under POLICY__MAX_STALENESS.
		slog.Log(ctx, slog.LevelDebug, "failed to measure the lag of the replica", "error", err)
		return
	}
	lag := time.Duration(replay * float64(time.Second))
	st := &a.replica
	primary, perr := db.LoadRevisions(ctx, a.DB)
	replica, rerr := db.LoadRevisions(ctx, a.ReadDB)
	if perr == nil && rerr == nil {
		st.confirmedAt.Store(time.Now().UnixNano())
		p, r := totalRevision(primary), totalRevision(replica)
		switch {
		case r >= p:
			st.pendingSince = time.Time{}
			st.revisionLag.Store(0)
		default:
			if st.pendingSince.IsZero() || r >= st.pendingTotal {
				st.pendingTotal, st.pendingSince = p, time.Now()
			}
			st.revisionLag.Store(p - r)
		}
		if !st.pendingSince.IsZero() {
			lag = max(lag, time.Since(st.pendingSince))
		}
	}
	was := a.replicaLagging()
	st.lag.Store(int64(lag))
	st.measured.Store(true)
	if now := a.replicaLagging(); now != was {
		slog.WarnContext(ctx, "the lag of the read replica crossed DB__MAX_REPLICA_LAG", "lagging", now, "lag", lag.Round(time.Millisecond),
			"limit", a.Cfg.DB.MaxReplicaLag, "action", a.Cfg.DB.LagAction)
	}
}

type replicaCollector struct{ app *App }

var (
	replicaLagDesc = prometheus.NewDesc("licetum_replica_lag_seconds",
		"How far the read replica is behind the primary: the larger of its replay lag and the time its revision has been behind the primary's.", nil, nil)
	replicaRevisionDesc = prometheus.NewDesc("licetum_replica_revision_lag",
		"How many revisions (all scopes together) the read replica is behind the primary.", nil, nil)
	replicaUnconfirmedDesc = prometheus.NewDesc("licetum_replica_unconfirmed_seconds",
		"Seconds since the read replica was last compared with the primary. It grows while the primary cannot be reached; POLICY__MAX_STALENESS bounds it.", nil, nil)
	replicaLaggingDesc = prometheus.NewDesc("licetum_replica_lagging",
		"1 while the lag of the read replica is above DB__MAX_REPLICA_LAG.", nil, nil)
)

func (c *replicaCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- replicaLagDesc
	ch <- replicaRevisionDesc
	ch <- replicaLaggingDesc
	ch <- replicaUnconfirmedDesc
}

func (c *replicaCollector) Collect(ch chan<- prometheus.Metric) {
	a := c.app
	if a == nil || a.ReadDB == nil {
		return
	}
	lagging := 0.0
	if a.replicaLagging() {
		lagging = 1
	}
	ch <- prometheus.MustNewConstMetric(replicaLagDesc, prometheus.GaugeValue, a.replicaLag().Seconds())
	ch <- prometheus.MustNewConstMetric(replicaUnconfirmedDesc, prometheus.GaugeValue, a.replicaUnconfirmed().Seconds())
	ch <- prometheus.MustNewConstMetric(replicaRevisionDesc, prometheus.GaugeValue, float64(a.replica.revisionLag.Load()))
	ch <- prometheus.MustNewConstMetric(replicaLaggingDesc, prometheus.GaugeValue, lagging)
}
