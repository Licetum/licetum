package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/model"
)

// The synchronization pipeline has one executor per job. The processes that serve requests (check, admin) only ask for
// a synchronization — a row in sync_job — and wait for the result for a bounded time; the worker (sync_worker.go) claims
// the job under a lease, fetches the sources and applies the result in one transaction that ends with a fence.

// syncBackend is what runs the jobs: the sync_job table (default) or DBOS workflows. Both give the same guarantees: one
// execution per type shared by all callers, recovery after the death of an executor, and a result the callers wait for.
type syncBackend interface {
	// enqueue makes sure there is an active job for each type and returns a reference to it.
	enqueue(ctx context.Context, typeIDs []uuid.UUID, fresh bool, source string) (map[uuid.UUID]string, error)
	// poll waits for the job to end (for at most jobPollCap) and says how it ended.
	poll(ref string) (jobOutcome, error)
}

// errSyncUnavailable says the backend cannot be reached or is not ready (the DBOS tables are created by the first worker):
// the request answers from the data that exists.
var errSyncUnavailable = errors.New("the synchronization backend is not available")

func (a *App) backend() syncBackend {
	if a.useDBOS() {
		return a.dbosBackend()
	}
	return pgBackend{a}
}

// SyncOptions tells Ensure what the caller needs.
type SyncOptions struct {
	// Fresh asks the source regardless of the throttle (consistency=higher): the sources are asked even if the data is
	// current. Concurrent fresh requests for a type share one job, and the worker spaces the source calls out.
	Fresh bool
	// Wait is how long to wait for the synchronization; when it passes the caller gets what exists (SyncResult.Pending).
	// Zero asks and does not wait.
	Wait time.Duration
}

// SyncResult is what Ensure found out.
type SyncResult struct {
	// Synced holds the types that have a live synchronizer: only their data is current.
	Synced map[uuid.UUID]bool
	// Unanswered holds the types whose source did not answer in the job that was waited for (their data stays as it was).
	Unanswered map[uuid.UUID]bool
	// Pending says the data of some type is not there yet (never synchronized: the wait ran out, or its source does
	// not answer) or the synchronization asked for was not finished in time. The answer is made of the data that exists:
	// a type without data contributes no resources.
	Pending bool
}

// SyncCoordinator makes sure a set of types is synchronized no older than the request needs. Concurrent callers, in any
// process, share one execution.
type SyncCoordinator interface {
	Ensure(ctx context.Context, typeIDs []uuid.UUID, opts SyncOptions) (SyncResult, error)
}

// Coordinator is the PostgreSQL job table implementation of SyncCoordinator.
func (a *App) Coordinator() SyncCoordinator { return coordinator{a} }

type coordinator struct{ a *App }

func (c coordinator) Ensure(ctx context.Context, typeIDs []uuid.UUID, opts SyncOptions) (SyncResult, error) {
	g, err := c.a.LoadTypeGraph(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	return c.a.ensure(ctx, g, typeIDs, opts, "request")
}

// EnsureSynced brings the given types up to date through the coordinator, for the admin paths (they wait for the worker
// for SYNC__ADMIN_WAIT), and returns the types that have a live synchronizer.
func (a *App) EnsureSynced(ctx context.Context, g *TypeGraph, typeIDs []uuid.UUID, fresh bool) (map[uuid.UUID]bool, error) {
	res, err := a.ensure(ctx, g, typeIDs, SyncOptions{Fresh: fresh, Wait: a.adminWait()}, "admin")
	return res.Synced, err
}

// dedupKey names the job of a set of types: the same set has the same key in every process; a fresh job has its own.
func dedupKey(typeIDs []uuid.UUID, fresh bool) string {
	ids := make([]string, len(typeIDs))
	for i, id := range typeIDs {
		ids[i] = id.String()
	}
	slices.Sort(ids)
	h := sha256.New()
	for _, id := range ids {
		writeString(h, id)
		writeString(h, ",")
	}
	key := hex.EncodeToString(h.Sum(nil))
	if fresh {
		key += ":fresh"
	}
	return key
}

// ensure is Ensure with the type graph in hand. source says who asked (request, admin).
//
// The types are looked at one by one: a type with no data under the current structure (never synchronized, or the graph
// above it changed) is waited for, and so is a type of the platform that a write made due — a write must show at once;
// a type whose throttle ran out is refreshed in the background, the request answers from the data that exists. A type
// whose source failed lately is in backoff: nobody asks the source until it ends, the data stays.
func (a *App) ensure(ctx context.Context, g *TypeGraph, typeIDs []uuid.UUID, opts SyncOptions, source string) (SyncResult, error) {
	res := SyncResult{Synced: map[uuid.UUID]bool{}, Unanswered: map[uuid.UUID]bool{}}
	syncs, err := a.liveSynchronizers(ctx, typeIDs)
	if err != nil {
		return res, err
	}
	if len(syncs) == 0 {
		return res, nil
	}
	ids := make([]uuid.UUID, len(syncs))
	for i, s := range syncs {
		ids[i] = s.TypeResourceID
		res.Synced[s.TypeResourceID] = true
	}

	deadline := time.Now().Add(opts.Wait)
	const maxRounds = 3
	for round := 0; ; round++ {
		states, err := loadSyncStates(ctx, a.DB, ids)
		if err != nil {
			return res, err
		}
		var block, async []uuid.UUID
		res.Pending = false
		for _, s := range syncs {
			id := s.TypeResourceID
			st, ok := states[id]
			switch {
			case !ok || !st.usable() || st.GraphSig != structureOf(g, id, s.Config):
				// There is no data under the current structure. In backoff nobody asks the source: the type stays empty.
				if ok && st.Backoff {
					res.Pending = true
				} else {
					block = append(block, id)
				}
			case st.Due && isPlatformType(g, id):
				block = append(block, id)
			case st.Due:
				async = append(async, id)
			}
		}
		if opts.Fresh {
			block, async = ids, nil
		}
		if len(async) > 0 {
			a.requestInBackground(async, source)
		}
		if len(block) == 0 {
			return res, nil
		}
		// A write may land while the job is running: the job then leaves the type due, and it is looked at again. After a few
		// rounds the request answers with what there is.
		remaining := time.Until(deadline)
		if round >= maxRounds {
			res.Pending = true
			return res, nil
		}
		if remaining <= 0 {
			// A caller that does not wait only asks (the worker finds the job), and answers from what exists.
			res.Pending = true
			if _, err := a.enqueueSync(ctx, block, opts.Fresh, source); err != nil && !errors.Is(err, errSyncUnavailable) {
				return res, err
			}
			return res, nil
		}

		jobs, err := a.enqueueSync(ctx, block, opts.Fresh, source)
		if errors.Is(err, errSyncUnavailable) {
			// The backend is not ready (the first worker has not created its tables yet): try again while the wait lasts.
			resume := pauseComputeSlot(ctx)
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(min(remaining, 100*time.Millisecond)):
			}
			if err := resume(ctx); err != nil {
				return res, err
			}
			round--
			continue
		}
		if err != nil {
			return res, err
		}
		// The wait is the worker's: the computation gives its slot back meanwhile (see computeSlot).
		resume := pauseComputeSlot(ctx)
		unanswered, finished := a.waitJobs(ctx, jobs, remaining)
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := resume(ctx); err != nil {
			return res, err
		}
		if !finished {
			res.Pending = true
			return res, nil
		}
		for id := range unanswered {
			res.Unanswered[id] = true
		}
		if opts.Fresh {
			return res, nil
		}
	}
}

func isPlatformType(g *TypeGraph, id uuid.UUID) bool {
	n, ok := g.Node(id)
	return ok && n.ServiceID == model.PlatformServiceID
}

// requestInBackground asks for the synchronization of the given types without waiting (and off the request goroutine). A process asks for a type at most
// once a second: a burst of requests that all find the type due is one row in the table, not one write each.
func (a *App) requestInBackground(typeIDs []uuid.UUID, source string) {
	now := time.Now()
	var ask []uuid.UUID
	for _, id := range typeIDs {
		if last, ok := a.syncAsked.Load(id); ok && now.Sub(last.(time.Time)) < time.Second {
			continue
		}
		ask = append(ask, id)
	}
	if len(ask) == 0 {
		return
	}
	// The writes (the demand, the job, the notification) are not part of the answer: they run on the background pool, and a
	// full queue drops them — the next request asks again.
	accepted := a.Background(func(bg context.Context) {
		if _, err := a.enqueueSync(bg, ask, false, source); err != nil {
			level := slog.LevelWarn
			if errors.Is(err, errSyncUnavailable) {
				level = slog.LevelDebug // the first worker has not created its tables yet: not worth a line per type
			}
			slog.Log(bg, level, "failed to ask for the refresh of the resource types", "error", err)
		}
	})
	if accepted {
		for _, id := range ask {
			a.syncAsked.Store(id, now)
		}
	}
}

// enqueueSync makes sure there is an active job for each type and returns a reference to each. A type that has one (queued
// or running) shares it. Each type has its own job, so that two requests that involve different sets of types still share
// the source call of a type they have in common. Who asked is remembered: the worker keeps the types in demand fresh.
func (a *App) enqueueSync(ctx context.Context, typeIDs []uuid.UUID, fresh bool, source string) (map[uuid.UUID]string, error) {
	if source != "schedule" {
		if _, err := a.DB.Exec(ctx, `
			INSERT INTO sync_demand (type_resource_id, last_asked_at) SELECT unnest($1::uuid[]), now()
			ON CONFLICT (type_resource_id) DO UPDATE SET last_asked_at = now()`, typeIDs); err != nil {
			// Only the schedule's view of the demand is lost; the job itself must still be asked for.
			logDependency(ctx, slog.LevelWarn, "failed to remember the demand for the resource types", err)
		}
	}
	return a.backend().enqueue(ctx, typeIDs, fresh, source)
}

type pgBackend struct{ a *App }

func (b pgBackend) enqueue(ctx context.Context, typeIDs []uuid.UUID, fresh bool, source string) (map[uuid.UUID]string, error) {
	a := b.a
	keys := make([]string, len(typeIDs))
	for i, id := range typeIDs {
		keys[i] = dedupKey([]uuid.UUID{id}, fresh)
	}
	rows, err := a.DB.Query(ctx, `
		INSERT INTO sync_job (dedup_key, type_ids, fresh, source)
		SELECT u.key, ARRAY[u.type_id], $3, $4
		FROM unnest($1::text[], $2::uuid[]) AS u(key, type_id)
		ON CONFLICT (dedup_key) WHERE state IN ('queued', 'running') DO UPDATE SET dedup_key = EXCLUDED.dedup_key
		RETURNING id, type_ids[1], (xmax = 0)`, keys, typeIDs, fresh, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := map[uuid.UUID]string{}
	created := false
	for rows.Next() {
		var id int64
		var typeID uuid.UUID
		var inserted bool
		if err := rows.Scan(&id, &typeID, &inserted); err != nil {
			return nil, err
		}
		jobs[typeID] = strconv.FormatInt(id, 10)
		created = created || inserted
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if created {
		a.wakeWorkers(ctx)
	}
	return jobs, nil
}

// wakeWorkers tells the workers there is a job: this process's own, directly, the others through NOTIFY. A worker that
// misses it finds the job on its next poll.
func (a *App) wakeWorkers(ctx context.Context) {
	select {
	case a.workerWake <- struct{}{}:
	default:
	}
	if _, err := a.DB.Exec(ctx, `SELECT pg_notify($1, '')`, syncChannel); err != nil {
		slog.DebugContext(ctx, "failed to notify the synchronization workers", "error", err)
	}
}

const syncChannel = "licetum_sync"

// jobOutcome is what a finished job says: the types whose source did not answer.
type jobOutcome struct {
	state      string
	err        string
	unanswered []uuid.UUID
}

// waitJobs waits for the jobs to finish for at most d. It returns the types that did not get an answer from their source
// (including those of a job that failed) and whether every job finished.
func (a *App) waitJobs(ctx context.Context, jobs map[uuid.UUID]string, d time.Duration) (map[uuid.UUID]bool, bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	type waited struct {
		typeID  uuid.UUID
		outcome jobOutcome
		ok      bool
	}
	results := make(chan waited, len(jobs))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for typeID, jobID := range jobs {
		wg.Go(func() {
			ch := a.jobFlights.DoChan("job:"+jobID, func() (any, error) { return a.backend().poll(jobID) })
			select {
			case r := <-ch:
				o, _ := r.Val.(jobOutcome)
				results <- waited{typeID, o, r.Err == nil}
			case <-stop:
			}
		})
	}
	finished := true
	unanswered := map[uuid.UUID]bool{}
	for range jobs {
		select {
		case w := <-results:
			if !w.ok {
				finished = false // a poll that failed: the wait is over, the caller answers with what exists
				break
			}
			if w.outcome.state == "failed" {
				slog.WarnContext(ctx, "a resource synchronization job failed", "type_resource_id", w.typeID, "error", w.outcome.err)
				unanswered[w.typeID] = true
			}
			for _, id := range w.outcome.unanswered {
				unanswered[id] = true
			}
		case <-timer.C:
			finished = false
		case <-ctx.Done():
			finished = false
		}
		if !finished {
			break
		}
	}
	close(stop)
	wg.Wait()
	return unanswered, finished
}

// jobPollCap bounds how long one poller watches a job: a job still unfinished by then is somebody else's problem, and
// the waiters have given up long before.
const jobPollCap = 2 * time.Minute

// pollJob watches a job until it ends, with the interval growing from 25 ms to 500 ms. One poller serves every waiter of
// the job in this process (singleflight). A job that ended in this very process wakes it at once.
func (b pgBackend) poll(ref string) (jobOutcome, error) {
	a := b.a
	jobID, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return jobOutcome{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobPollCap)
	defer cancel()
	delay := 25 * time.Millisecond
	for {
		done := a.jobsDone()
		var o jobOutcome
		err := a.DB.QueryRow(ctx, `SELECT state, COALESCE(error, ''), unanswered FROM sync_job WHERE id = $1`, jobID).
			Scan(&o.state, &o.err, &o.unanswered)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return jobOutcome{state: "done"}, nil // pruned: it ended long ago
		case err != nil:
			return o, err
		case o.state == "done" || o.state == "failed":
			return o, nil
		}
		select {
		case <-done:
		case <-time.After(delay):
		case <-ctx.Done():
			return o, ctx.Err()
		}
		delay = min(delay*2, 500*time.Millisecond)
	}
}

// jobsDone returns a channel that is closed when a job ends in this process.
func (a *App) jobsDone() <-chan struct{} {
	a.doneMu.Lock()
	defer a.doneMu.Unlock()
	if a.doneCh == nil {
		a.doneCh = make(chan struct{})
	}
	return a.doneCh
}

func (a *App) notifyJobsDone() {
	a.doneMu.Lock()
	defer a.doneMu.Unlock()
	if a.doneCh != nil {
		close(a.doneCh)
		a.doneCh = nil
	}
}

func (a *App) requestWait() time.Duration {
	if a.Cfg == nil || a.Cfg.Sync.RequestWait <= 0 {
		return 5 * time.Second
	}
	return a.Cfg.Sync.RequestWait
}

func (a *App) adminWait() time.Duration {
	if a.Cfg == nil || a.Cfg.Sync.AdminWait <= 0 {
		return 30 * time.Second
	}
	return a.Cfg.Sync.AdminWait
}

func (a *App) statementTimeout() time.Duration {
	if a.Cfg == nil || a.Cfg.Sync.StatementTimeout <= 0 {
		return 120 * time.Second
	}
	return a.Cfg.Sync.StatementTimeout
}

func (a *App) leaseDuration() time.Duration {
	if a.Cfg == nil || a.Cfg.Sync.Lease <= 0 {
		return 30 * time.Second
	}
	return a.Cfg.Sync.Lease
}
