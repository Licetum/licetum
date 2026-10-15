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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/model"
)

// The worker executes the synchronization jobs. Each job is claimed with FOR UPDATE SKIP LOCKED under a lease that the
// executor renews while it works; the transaction that applies the result ends with an update of the job that is
// conditioned on the lease token, so an executor that lost its lease (it was too slow, paused, partitioned) applies
// nothing: its transaction rolls back and the job is left to the one that took it over.

// SyncHooks lets the tests stop an executor at a chosen point. The zero value does nothing.
type SyncHooks struct {
	// AfterFetch runs after the sources of the given types were asked and before the result is applied.
	AfterFetch func(ctx context.Context, typeIDs []uuid.UUID)
	// NoHeartbeat, when it returns true, stops the renewal of a lease, as a paused process would. The postgres engine asks
	// per job (with the types of the job), the dbos engine for the whole executor (with nil).
	NoHeartbeat func(typeIDs []uuid.UUID) bool
}

var errLeaseLost = errors.New("the lease of the synchronization job was lost")

// syncJob is a claimed job.
type syncJob struct {
	ID       int64
	TypeIDs  []uuid.UUID
	Fresh    bool
	Source   string
	Token    int64
	Attempts int
}

// RunWorker runs the worker until ctx ends: it claims jobs, executes them, and puts the types that are due on a
// schedule. It needs a role that may apply a synchronization (the worker's, or none).
func (a *App) RunWorker(ctx context.Context) error {
	if a.Role == RoleCheck || a.Role == RoleAdmin {
		return fmt.Errorf("the %s role does not run synchronization jobs", a.Role)
	}
	owner := workerName()
	slog.InfoContext(ctx, "synchronization worker started", "owner", owner, "lease", a.leaseDuration(), "concurrency", a.workerConcurrency())
	a.importLegacyState(ctx)
	if a.useDBOS() {
		return a.runDBOSWorker(ctx, owner)
	}

	var wg sync.WaitGroup
	wake := make(chan struct{}, a.workerConcurrency())
	wg.Go(func() { a.dispatch(ctx, wake) })
	wg.Go(func() { a.schedule(ctx) })
	for range a.workerConcurrency() {
		wg.Go(func() { a.executor(ctx, owner, wake) })
	}
	wg.Wait()
	return nil
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

func (a *App) pollEvery() time.Duration {
	tick := time.Second
	if a.Cfg != nil && a.Cfg.Sync.ScheduleTick > 0 {
		tick = min(tick, a.Cfg.Sync.ScheduleTick)
	}
	return tick
}

// dispatch wakes the executors when a job is queued: through NOTIFY from any process, through the process's own channel,
// and by polling as a fallback (a missed notification, a worker started after the job).
func (a *App) dispatch(ctx context.Context, wake chan<- struct{}) {
	signal := func() {
		for range cap(wake) {
			select {
			case wake <- struct{}{}:
			default:
				return
			}
		}
	}
	var conn *pgxpool.Conn
	listen := func() bool {
		if conn != nil {
			return true
		}
		c, err := a.DB.Acquire(ctx)
		if err != nil {
			return false
		}
		if _, err := c.Exec(ctx, "LISTEN "+syncChannel); err != nil {
			c.Release()
			return false
		}
		conn = c
		return true
	}
	defer func() {
		if conn != nil {
			conn.Release()
		}
	}()

	for ctx.Err() == nil {
		if listen() {
			wctx, cancel := context.WithTimeout(ctx, a.pollEvery())
			done := make(chan struct{})
			go func() {
				select {
				case <-a.workerWake:
					signal()
				case <-wctx.Done():
				}
				close(done)
			}()
			_, err := conn.Conn().WaitForNotification(wctx)
			cancel()
			<-done
			switch {
			case err == nil:
				signal()
			case ctx.Err() != nil:
				return
			case errors.Is(err, context.DeadlineExceeded):
				signal() // poll
			default:
				// the connection is gone: take another one next time
				_ = conn.Conn().Close(context.Background())
				conn.Release()
				conn = nil
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-a.workerWake:
		case <-time.After(a.pollEvery()):
		}
		signal()
	}
}

// executor claims and runs jobs one after another.
func (a *App) executor(ctx context.Context, owner string, wake <-chan struct{}) {
	for ctx.Err() == nil {
		job, err := a.claimJob(ctx, owner)
		if err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "failed to claim a synchronization job", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		a.runJob(ctx, owner, *job)
	}
}

// claimJob takes the oldest job that is queued, or running with a lease that ran out. The lease token is new at every
// claim, so the previous owner's token no longer matches.
func (a *App) claimJob(ctx context.Context, owner string) (*syncJob, error) {
	var j syncJob
	err := a.DB.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM sync_job
			WHERE attempts < $3 AND (state = 'queued' OR (state = 'running' AND lease_until < now()))
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1)
		UPDATE sync_job j
		SET state = 'running', lease_owner = $1, lease_token = nextval('sync_job_token_seq'),
		    lease_until = now() + make_interval(secs => $2), started_at = now(), attempts = j.attempts + 1
		FROM next WHERE j.id = next.id
		RETURNING j.id, j.type_ids, j.fresh, j.source, j.lease_token, j.attempts`,
		owner, a.leaseDuration().Seconds(), maxJobClaims).
		Scan(&j.ID, &j.TypeIDs, &j.Fresh, &j.Source, &j.Token, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// runJob executes a claimed job: it renews the lease while it works, asks the sources, and applies the result.
func (a *App) runJob(ctx context.Context, owner string, job syncJob) {
	started := time.Now()
	jobCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		a.heartbeat(jobCtx, cancel, job)
	}()

	outcome, err := a.executeJob(jobCtx, job)
	cancel(nil)
	<-hbDone

	state := "done"
	switch {
	case err == nil:
	case errors.Is(err, errLeaseLost) || errors.Is(context.Cause(jobCtx), errLeaseLost):
		// Another executor owns the job now; there is nothing to write.
		slog.WarnContext(ctx, "the lease of a synchronization job was lost, its result is dropped", "job", job.ID, "owner", owner)
		state = "lease_lost"
	case ctx.Err() != nil:
		// The worker is shutting down: the lease runs out and another worker takes the job.
		return
	case isLockTimeout(err):
		slog.WarnContext(ctx, "a synchronization job waited too long for the graph lock, it is queued again", "job", job.ID, "error", err)
		a.requeueJob(ctx, job)
		state = "requeued"
		time.Sleep(500 * time.Millisecond)
	default:
		slog.ErrorContext(ctx, "a synchronization job failed", "job", job.ID, "types", job.TypeIDs, "error", err)
		a.failJob(ctx, job, err)
		state = "failed"
	}
	if a.metrics != nil {
		a.metrics.syncJobs.WithLabelValues(state).Inc()
		a.metrics.syncJobSeconds.Observe(time.Since(started).Seconds())
	}
	a.notifyJobsDone()
	_ = outcome
}

// heartbeat renews the lease every lease/3; when the renewal finds the token gone, the job is cancelled.
func (a *App) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, job syncJob) {
	if a.Hooks.NoHeartbeat != nil && a.Hooks.NoHeartbeat(job.TypeIDs) {
		<-ctx.Done()
		return
	}
	lease := a.leaseDuration()
	ticker := time.NewTicker(lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		tag, err := a.DB.Exec(ctx, `
			UPDATE sync_job SET lease_until = now() + make_interval(secs => $3)
			WHERE id = $1 AND lease_token = $2 AND state = 'running'`, job.ID, job.Token, lease.Seconds())
		if err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "failed to renew the lease of a synchronization job", "job", job.ID, "error", err)
			}
			continue
		}
		if tag.RowsAffected() == 0 {
			a.leaseLost()
			cancel(errLeaseLost)
			return
		}
	}
}

func (a *App) leaseLost() {
	if a.metrics != nil {
		a.metrics.syncLeaseLost.Inc()
	}
}

// requeueJob gives a job back (fenced by the token).
func (a *App) requeueJob(ctx context.Context, job syncJob) {
	if _, err := a.DB.Exec(ctx, `
		UPDATE sync_job SET state = 'queued', lease_owner = NULL, lease_token = NULL, lease_until = NULL
		WHERE id = $1 AND lease_token = $2 AND state = 'running'`, job.ID, job.Token); err != nil {
		slog.WarnContext(ctx, "failed to queue a synchronization job again", "job", job.ID, "error", err)
	}
}

// failJob ends a job with an error (fenced by the token) and starts the backoff of its types, so a failure that repeats
// does not become a loop.
func (a *App) failJob(ctx context.Context, job syncJob, cause error) {
	err := a.syncTx(ctx, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE sync_job SET state = 'failed', finished_at = now(), error = $3, unanswered = $4, lease_until = NULL
			WHERE id = $1 AND lease_token = $2 AND state = 'running'`, job.ID, job.Token, cause.Error(), job.TypeIDs)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errLeaseLost
		}
		for _, id := range job.TypeIDs {
			if err := markUnanswered(ctx, tx, id, cause.Error()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errLeaseLost) {
		slog.ErrorContext(ctx, "failed to record the failure of a synchronization job", "job", job.ID, "error", err)
	}
}

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

// executeJob does the work of a job: decides which of its types need the source, fetches them in parallel, and applies
// the result in one transaction that also settles the job.
func (a *App) executeJob(ctx context.Context, job syncJob) (map[uuid.UUID]bool, error) {
	return a.runSync(ctx, job.TypeIDs, job.Fresh, func(ctx context.Context, tx pgx.Tx, unanswered map[uuid.UUID]bool) error {
		// The fence: the job ends only if the lease is still ours.
		tag, err := tx.Exec(ctx, `
			UPDATE sync_job SET state = 'done', finished_at = now(), unanswered = $3, lease_until = NULL
			WHERE id = $1 AND lease_token = $2 AND state = 'running'`, job.ID, job.Token, Keys(unanswered))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			a.leaseLost()
			return errLeaseLost
		}
		return nil
	})
}

// runSync synchronizes the given types: it decides which of them need the source, fetches them in parallel, and applies the
// result in one transaction. settle runs last, in that transaction, and is the engine's fence: it returns errLeaseLost
// when the execution no longer owns the work, which rolls everything back.
func (a *App) runSync(ctx context.Context, typeIDs []uuid.UUID, fresh bool, settle func(ctx context.Context, tx pgx.Tx, unanswered map[uuid.UUID]bool) error) (map[uuid.UUID]bool, error) {
	job := syncJob{TypeIDs: typeIDs, Fresh: fresh}
	g, err := a.LoadTypeGraph(ctx)
	if err != nil {
		return nil, err
	}
	syncs, err := a.liveSynchronizers(ctx, job.TypeIDs)
	if err != nil {
		return nil, err
	}
	states, err := loadSyncStates(ctx, a.DB, job.TypeIDs)
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
		case job.Fresh:
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
	if job.Fresh {
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
			rows, err := a.Sync.FetchAll(ctx, s.Config, s.ID.String(), job.Fresh)
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
		a.Hooks.AfterFetch(ctx, job.TypeIDs)
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
// every type, with SYNC__KEEP_WARM). It also gives up jobs that were claimed too many times, and deletes the old ones.
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
		if _, err := a.enqueueSync(ctx, due, false, "schedule"); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "failed to queue the resource types that are due", "error", err)
		}
	}

	if a.useDBOS() {
		return // the DBOS janitor does this
	}
	// A job claimed too many times keeps killing its workers: give it up.
	if _, err := a.DB.Exec(ctx, `
		UPDATE sync_job SET state = 'failed', finished_at = now(), error = 'the job was claimed too many times without finishing'
		WHERE attempts >= $1 AND (state = 'queued' OR (state = 'running' AND lease_until < now()))`, maxJobClaims); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "failed to give up the jobs that never finish", "error", err)
	}
	retention := max(time.Hour, 2*window)
	if _, err := a.DB.Exec(ctx, `
		DELETE FROM sync_job WHERE state IN ('done', 'failed') AND finished_at < now() - make_interval(secs => $1)`,
		retention.Seconds()); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "failed to delete the old synchronization jobs", "error", err)
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
