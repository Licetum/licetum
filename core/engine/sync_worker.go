package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/model"
)

// The worker executes the synchronization jobs, which are DBOS workflows (sync_dbos.go). This file holds what the
// executor of a job does whatever runs it: fetching the sources and applying the result in one transaction that ends with
// the fence of the engine, so an executor that lost its work (it was too slow, paused, partitioned) applies nothing: its
// transaction rolls back and the job is left to the one that took it over.

// SyncHooks lets the tests stop an executor at a chosen point. The zero value does nothing.
type SyncHooks struct {
	// AfterFetch runs after the sources of the given types were asked and before the result is applied.
	AfterFetch func(ctx context.Context, typeIDs []uuid.UUID)
	// NoHeartbeat, when it returns true, stops the heartbeat of the executor, as a paused process would. It is asked for the
	// whole executor, with nil (the DBOS engine has no per-job lease).
	NoHeartbeat func(typeIDs []uuid.UUID) bool
	// LockTimeout replaces syncLockTimeout when it is positive: how long a synchronization waits for the lock of its
	// component.
	LockTimeout time.Duration
}

var errLeaseLost = errors.New("the lease of the synchronization job was lost")

// RunWorker runs the worker until ctx ends: it runs the DBOS runtime that executes the jobs, and puts the types that are due
// on a schedule. It needs a role that may apply a synchronization (the worker's, or none).
func (a *App) RunWorker(ctx context.Context) error {
	if a.Role == RolePDP || a.Role == RoleAdmin {
		return fmt.Errorf("the %s role does not run synchronization jobs", a.Role)
	}
	owner := workerName()
	slog.InfoContext(ctx, "synchronization worker started", "owner", owner, "lease", a.leaseDuration(), "concurrency", a.workerConcurrency())
	a.importLegacyState(ctx)
	return a.runDBOSWorker(ctx, owner)
}

func workerName() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), uuid.NewString()[:8])
}

func (a *App) workerConcurrency() int {
	if a.Cfg != nil && a.Cfg.Sync.Concurrency > 0 {
		return a.Cfg.Sync.Concurrency
	}
	if a.Cfg != nil && a.Cfg.DB.SyncPoolSize > 0 {
		return a.Cfg.DB.SyncPoolSize
	}
	return 4
}

func (a *App) leaseLost() {
	if a.metrics != nil {
		a.metrics.syncLeaseLost.Inc()
	}
}

// markBusy postpones a type whose component lock was held too long: it is due again in a few seconds, and the attempts
// (and so the backoff) are untouched, because the source did not fail. A type that has no state row is left alone: it has
// no data yet, and whoever needs it asks again.
func markBusy(ctx context.Context, tx pgx.Tx, typeID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE sync_state SET next_due_at = now() + make_interval(secs => $2), last_error = $3
		WHERE type_resource_id = $1 AND next_due_at < now() + make_interval(secs => $2)`,
		typeID, busyRetry.Seconds(), reason)
	return err
}

// busyRetry is when a type whose component lock was busy is due again.
const busyRetry = 5 * time.Second

// markUnanswered starts or lengthens the backoff of a type whose source did not answer: min(30 s · 2^attempts, 10 min),
// for all replicas at once, because the state is in the database.
func markUnanswered(ctx context.Context, tx pgx.Tx, typeID uuid.UUID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO sync_state (type_resource_id, fetched_at, next_due_at, attempts, last_error)
		VALUES ($1, now(), now() + make_interval(secs => 30), 1, $2)
		ON CONFLICT (type_resource_id) DO UPDATE SET
		    fetched_at = now(), attempts = sync_state.attempts + 1, last_error = $2,
		    next_due_at = now() + make_interval(secs => LEAST(30 * power(2, LEAST(sync_state.attempts, 10)), 600))`,
		typeID, reason)
	return err
}

// runSync synchronizes the given types: it decides which of them need the source, fetches them in parallel, and applies the
// result in one transaction. settle runs last, in that transaction, and is the engine's fence: it returns errLeaseLost
// when the execution no longer owns the work, which rolls everything back.
func (a *App) runSync(ctx context.Context, typeIDs []uuid.UUID, fresh bool, settle func(ctx context.Context, tx pgx.Tx, unanswered map[uuid.UUID]bool) error) (map[uuid.UUID]bool, error) {
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	syncs, err := a.liveSynchronizers(ctx, typeIDs)
	if err != nil {
		return nil, err
	}
	states, err := loadSyncStates(ctx, a.DB, typeIDs)
	if err != nil {
		return nil, err
	}

	// A job that was queued when the type needed the source may meet a type that another job brought up to date since:
	// only the types that still need the source are asked. A fresh job asks regardless of the throttle.
	var due []model.Synchronizer
	for _, s := range syncs {
		id := s.TypeResourceID
		st, ok := states[id]
		structure := structureOf(g, id, s.Config)
		switch {
		case fresh:
			due = append(due, s)
		case !ok || !st.usable() || st.GraphSig != structure:
			if !ok || !st.Backoff {
				due = append(due, s)
			}
		case st.Due:
			due = append(due, s)
		}
	}

	var fetchStarted time.Time
	if err := a.DB.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&fetchStarted); err != nil {
		return nil, err
	}
	if fresh {
		if err := a.spaceOutFresh(ctx, due); err != nil {
			return nil, err
		}
	}

	started := time.Now()
	fetched := make([][]model.Resource, len(due))
	failure := make([]error, len(due))
	var wg sync.WaitGroup
	for i, s := range due {
		wg.Go(func() {
			rows, err := a.Sync.FetchAll(ctx, s.Config, s.ID.String(), fresh)
			fetched[i], failure[i] = rows, err
			outcome := "ok"
			if err != nil {
				outcome = "error"
			}
			if a.metrics != nil {
				a.metrics.sourceCalls.WithLabelValues(typeLabel(g, s.TypeResourceID), outcome).Inc()
			}
		})
	}
	wg.Wait()
	fetchDuration := time.Since(started)
	if a.metrics != nil {
		a.metrics.syncFetch.Observe(fetchDuration.Seconds())
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if a.Hooks.AfterFetch != nil {
		a.Hooks.AfterFetch(ctx, typeIDs)
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
	}

	dueIDs := make([]uuid.UUID, len(due))
	for i, s := range due {
		dueIDs[i] = s.TypeResourceID
	}
	counts, err := a.ActiveCounts(ctx, dueIDs)
	if err != nil {
		return nil, err
	}

	unanswered := map[uuid.UUID]bool{}
	reasons := map[uuid.UUID]string{}
	type settledType struct {
		typeID  uuid.UUID
		digest  string
		changed *changedType
	}
	var settled []settledType
	var changed []changedType
	for i, s := range due {
		typeID := s.TypeResourceID
		if failure[i] != nil {
			unanswered[typeID] = true
			reasons[typeID] = failure[i].Error()
			slog.WarnContext(ctx, "the source of a resource type did not answer, sync skipped", "type_resource_id", typeID)
			if a.metrics != nil {
				a.metrics.syncResults.WithLabelValues("unanswered").Inc()
			}
			continue
		}
		normalized, dropped := NormalizeResources(g, typeID, fetched[i])
		fetched[i] = nil
		if dropped > 0 {
			slog.WarnContext(ctx, "the source reports parents that cannot be resolved to a parent type of the resource type: "+
				"the parent type is unknown or ambiguous (use parents[].type, service/name if needed)",
				"type_resource_id", typeID, "links", dropped)
		}
		unique := make(map[string]IncomingResource, len(normalized))
		for _, r := range normalized {
			unique[r.ExternalID] = r
		}
		data, graph := dataDigest(unique), structureOf(g, typeID, s.Config)
		st := states[typeID]
		if st.usable() && st.DataHash == data && st.GraphSig == graph && counts[typeID] == len(unique) {
			settled = append(settled, settledType{typeID: typeID, digest: data})
			if a.metrics != nil {
				a.metrics.syncResults.WithLabelValues("unchanged").Inc()
			}
			continue
		}
		rows := make([]IncomingResource, 0, len(unique))
		for _, r := range unique {
			rows = append(rows, r)
		}
		c := changedType{typeID: typeID, rows: rows, digest: data, touchAll: !st.usable() || st.GraphSig != graph}
		changed = append(changed, c)
		settled = append(settled, settledType{typeID: typeID, digest: data, changed: &c})
		if a.metrics != nil {
			a.metrics.syncResults.WithLabelValues("changed").Inc()
		}
	}

	changedIDs := make([]uuid.UUID, len(changed))
	for i, c := range changed {
		changedIDs[i] = c.typeID
	}
	var keys []int64
	if len(changed) > 0 {
		keys = ComponentKeys(g, changedIDs)
	}
	writeStarted := time.Now()
	var movedTypes []uuid.UUID
	err = a.syncTx(ctx, keys, func(tx pgx.Tx) error {
		touched := map[uuid.UUID]struct{}{}
		movedTypes = nil
		for _, c := range changed {
			// A hash that differs does not mean the rows do: the database says what changed.
			var rowsChanged bool
			if err := applyType(ctx, tx, c.typeID, c.rows, c.touchAll, false, touched, &rowsChanged); err != nil {
				return fmt.Errorf("sync type %s: %w", c.typeID, err)
			}
			if rowsChanged {
				movedTypes = append(movedTypes, c.typeID)
			}
		}
		if err := rebuildClosure(ctx, tx, touched); err != nil {
			return err
		}
		// The revision of the data of these services moves in the transaction that moved the data, and only when it moved.
		if err := bumpDataRevisions(ctx, tx, g, movedTypes); err != nil {
			return err
		}
		for _, st := range settled {
			structure := ""
			for _, s := range due {
				if s.TypeResourceID == st.typeID {
					structure = structureOf(g, st.typeID, s.Config)
				}
			}
			if err := markApplied(ctx, tx, st.typeID, st.digest, structure, fetchStarted); err != nil {
				return err
			}
		}
		for id := range unanswered {
			if err := markUnanswered(ctx, tx, id, reasons[id]); err != nil {
				return err
			}
		}
		return settle(ctx, tx, unanswered)
	})
	if err != nil {
		return nil, err
	}
	if len(changed) > 0 {
		slog.InfoContext(ctx, "resource graph synchronized", "changed", len(changed), "types", len(due),
			"fetch_ms", fetchDuration.Milliseconds(), "write_ms", time.Since(writeStarted).Milliseconds())
	}
	if len(movedTypes) > 0 {
		a.invalidateView() // the data revisions moved: the next request reads them again
		// The replicas of the previous release follow the data versions in Redis.
		pipe := a.Redis.Pipeline()
		recordDataChange(ctx, pipe, g, movedTypes)
		if _, err := pipe.Exec(ctx); err != nil {
			logRedis(ctx, slog.LevelWarn, "failed to record the resource data change for the previous release", err)
		}
	}
	return unanswered, nil
}

// markApplied records that a type was brought up to the state of its source. The throttle starts, unless a write
// invalidated the type after the source was asked (the type then stays due).
func markApplied(ctx context.Context, tx pgx.Tx, typeID uuid.UUID, data, graph string, fetchStarted time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO sync_state (type_resource_id, format, data_hash, graph_sig, applied_at, fetched_at, next_due_at, attempts, last_error)
		VALUES ($1, $2, $3, $4, now(), now(), now() + make_interval(secs => $5), 0, NULL)
		ON CONFLICT (type_resource_id) DO UPDATE SET
		    format = EXCLUDED.format, data_hash = EXCLUDED.data_hash, graph_sig = EXCLUDED.graph_sig,
		    applied_at = now(), fetched_at = now(), attempts = 0, last_error = NULL,
		    next_due_at = CASE WHEN sync_state.invalidated_at IS NOT NULL AND sync_state.invalidated_at > $6
		                       THEN sync_state.next_due_at ELSE EXCLUDED.next_due_at END`,
		typeID, syncFormat, data, graph, syncTTL.Seconds(), fetchStarted)
	return err
}

// spaceOutFresh waits until the sources of the given types may be asked again: a fresh job asks a source at most once per
// SYNC__FRESH_MIN_INTERVAL. The lease keeps being renewed meanwhile.
func (a *App) spaceOutFresh(ctx context.Context, due []model.Synchronizer) error {
	interval := time.Second
	if a.Cfg != nil && a.Cfg.Sync.FreshMinInterval > 0 {
		interval = a.Cfg.Sync.FreshMinInterval
	}
	ids := make([]uuid.UUID, len(due))
	for i, s := range due {
		ids[i] = s.TypeResourceID
	}
	var wait float64
	err := a.DB.QueryRow(ctx, `
		SELECT COALESCE(MAX(GREATEST(0, EXTRACT(EPOCH FROM (fetched_at + make_interval(secs => $2) - clock_timestamp())))), 0)::float8
		FROM sync_state WHERE type_resource_id = ANY($1) AND fetched_at IS NOT NULL`, ids, interval.Seconds()).Scan(&wait)
	if err != nil {
		return err
	}
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-time.After(time.Duration(math.Ceil(wait*1000)) * time.Millisecond):
		return nil
	}
}

func typeLabel(g *TypeGraph, id uuid.UUID) string {
	if n, ok := g.Node(id); ok {
		return n.ServiceName + "/" + n.Name
	}
	return id.String()
}

// schedule puts the types that are due on the queue, every SYNC__SCHEDULE_TICK: those that someone asked for lately (or
// every type, with SYNC__KEEP_WARM). The janitor of the DBOS engine takes over the jobs of dead executors and prunes the
// old ones (dbosJanitor).
func (a *App) schedule(ctx context.Context) {
	tick := 5 * time.Second
	if a.Cfg != nil && a.Cfg.Sync.ScheduleTick > 0 {
		tick = a.Cfg.Sync.ScheduleTick
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		a.scheduleOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) scheduleOnce(ctx context.Context) {
	keepWarm, window := false, 10*time.Minute
	if a.Cfg != nil {
		keepWarm = a.Cfg.Sync.KeepWarm
		if a.Cfg.Sync.DemandWindow > 0 {
			window = a.Cfg.Sync.DemandWindow
		}
	}
	rows, err := a.DB.Query(ctx, `
		SELECT st.type_resource_id
		FROM sync_state st
		JOIN type_resource t ON t.id = st.type_resource_id AND t.deleted_at IS NULL
		WHERE st.next_due_at <= now()
		  AND EXISTS (SELECT 1 FROM synchronizer s WHERE s.type_resource_id = t.id AND s.deleted_at IS NULL)
		  AND ($1 OR EXISTS (SELECT 1 FROM sync_demand d
		                     WHERE d.type_resource_id = st.type_resource_id AND d.last_asked_at > now() - make_interval(secs => $2)))
		UNION
		SELECT s.type_resource_id FROM synchronizer s
		JOIN type_resource t ON t.id = s.type_resource_id AND t.deleted_at IS NULL
		WHERE $1 AND s.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM sync_state st WHERE st.type_resource_id = s.type_resource_id)`,
		keepWarm, window.Seconds())
	if err != nil {
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "failed to look for the resource types that are due", "error", err)
		}
		return
	}
	var due []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		due = append(due, id)
	}
	rows.Close()
	if len(due) > 0 {
		if _, err := a.enqueueSync(ctx, due, false, "schedule", nil); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "failed to queue the resource types that are due", "error", err)
		}
	}
}

// importLegacyState copies the stored hashes of the previous release (Redis authz:resource_sync:hash:<type>) into
// sync_state for the types that have no row yet, so that the first start after the upgrade rebuilds nothing. The Redis
// keys are left alone: pods of the previous release may still use them while they roll out.
func (a *App) importLegacyState(ctx context.Context) {
	if a.Redis == nil {
		return
	}
	rows, err := a.DB.Query(ctx, `
		SELECT DISTINCT s.type_resource_id FROM synchronizer s
		WHERE s.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM sync_state st WHERE st.type_resource_id = s.type_resource_id)`)
	if err != nil {
		slog.WarnContext(ctx, "failed to look for the stored synchronization hashes of the previous release", "error", err)
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	imported := 0
	for _, id := range ids {
		digest, err := a.Redis.Get(ctx, legacyHashPrefix+id.String()).Result()
		if err != nil {
			continue
		}
		parts := strings.Split(digest, ":")
		if len(parts) != 3 || parts[0] != "v4" {
			continue
		}
		tag, err := a.DB.Exec(ctx, `
			INSERT INTO sync_state (type_resource_id, format, data_hash, graph_sig, applied_at, next_due_at)
			SELECT $1, $2, $3, $4, now(), now() WHERE EXISTS (SELECT 1 FROM type_resource WHERE id = $1)
			ON CONFLICT DO NOTHING`, id, syncFormatPrevious, parts[1], parts[2])
		if err == nil && tag.RowsAffected() > 0 {
			imported++
		}
	}
	if imported > 0 {
		slog.InfoContext(ctx, "imported the synchronization state of the previous release", "types", imported)
	}
}

// legacyHashPrefix is where the previous release kept the hash of the last synchronization of a type.
const legacyHashPrefix = "authz:resource_sync:hash:"
