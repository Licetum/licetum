package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/db"
)

// The DBOS engine (SYNC__ENGINE=dbos) runs the synchronization jobs as DBOS workflows on a database-backed queue, in place
// of the sync_job table. The PDP and admin processes enqueue through a DBOS client and wait for the workflow; the worker
// launches the DBOS runtime and executes them. What stays the same: one job per type shared by all callers (the DBOS
// deduplication id, policy "return existing"), the state in sync_state, and the apply transaction.
//
// Fetch and apply are one DBOS step: the list of the source is never a step result, so a checkpoint is a few dozen bytes
// whatever the size of the type (measured with 100 000 resources: 52 bytes).
//
// DBOS does not hand the interrupted workflows of a dead executor to a live one (that is the job of its Conductor, or of a
// restart with the same executor id). The workers here do it themselves: every executor writes a heartbeat to sync_executor
// and every worker cancels the workflows of an executor whose heartbeat is older than the lease and queues a replacement
// under the id "<id>+r" (the waiters follow the chain). The fence of the apply transaction is the status of the workflow row,
// read FOR SHARE: a workflow that was cancelled meanwhile applies nothing, and a cancel waits for a commit that is in flight.

const (
	dbosQueue    = "licetum-sync"
	dbosWorkflow = "SyncType"
	dbosApp      = "licetum-sync"
	// dbosVersion is the application version of every worker: a rolling update of the binary must not strand the queue.
	dbosVersion = "licetum-sync-1"
	// dbosReplacement is appended to the id of a workflow that was taken over.
	dbosReplacement = "+r"
)

type dbosSyncIn struct {
	TypeID string `json:"type_id"`
	Fresh  bool   `json:"fresh"`
	Source string `json:"source"`
}

type dbosSyncOut struct {
	Unanswered []string `json:"unanswered"`
	// LeaseLost says the workflow was taken over while it ran: it applied nothing.
	LeaseLost bool `json:"lease_lost,omitempty"`
}

// dbosState is the DBOS handle of a process: a client (check, admin) or the launched runtime (worker).
type dbosState struct {
	mu      sync.Mutex
	client  dbos.Client
	tried   time.Time
	runtime dbos.Context
	// connecting is set while a client is being created, so that callers do not queue behind the lock or start a second one.
	connecting bool
	// enqueueSlots bounds the enqueues the process runs at once (see enqueueSlot).
	enqueueSlots     chan struct{}
	enqueueSlotsOnce sync.Once
	// enqueues makes the concurrent requests of the process for one job share one enqueue (see dbosBackend.enqueue).
	enqueues singleflight.Group
}

// jobName names the workflow that a request waits for after it read version v of the state of a type (the xmin of its
// sync_state row, "none" without a row): the requests that read the same state share it, whether it is queued, running or
// has ended. The deduplication id alone holds only while a job is queued or running: a request that read the state before
// a job brought the type up to date and asks after the job ended (its DBOS client was connecting, an enqueue slot was busy,
// or it was merely slower) would start a second job for data that is current already; under the name it finds the job that
// ended, and its outcome. Every job writes the state of the types it was due for, so the state read after a job has another
// name.
func jobName(dedupKey, version string) string { return dedupKey + "@" + version }

// enqueueWait bounds how long an enqueue waits for a slot when the request that asked for it does not bound it: the enqueue
// the requests of a process share (the request that started it may be gone, the others still wait for it), and the
// replacement of a workflow the janitor cancelled (it must be queued even if the worker stops).
const enqueueWait = 30 * time.Second

// enqueueSlot takes one of the slots of the DBOS enqueues of the process, waiting while ctx lasts; release gives it back.
//
// A DBOS enqueue (1.6) holds a connection of the pool in its transaction and, when the job is a duplicate (another request
// queued it, the usual case under load), takes a second connection to find the job that holds the deduplication id, with no
// deadline; and the listener of the notifications keeps one connection of the same pool for good. As many duplicate enqueues
// at once as the pool has connections left would each hold one and wait for another forever, and with them every request
// of the process that needs a synchronization. The process runs at most (pool - 1) / 2 enqueues at once: each of them finds
// its second connection. (The concurrent requests for one job share a single enqueue, see dbosBackend.enqueue: the enqueues
// at once are of different jobs, and the slots bound those.)
func (a *App) enqueueSlot(ctx context.Context) (release func(), err error) {
	s := &a.dbos
	s.enqueueSlotsOnce.Do(func() {
		size := minDBOSPool
		if raw, err := a.dbosURL(); err == nil { // pool_max_conns as the URI sets it
			if u, err := url.Parse(raw); err == nil {
				if n, err := strconv.Atoi(u.Query().Get("pool_max_conns")); err == nil && n > size {
					size = n
				}
			}
		}
		s.enqueueSlots = make(chan struct{}, (size-1)/2)
	})
	select {
	case s.enqueueSlots <- struct{}{}:
		return func() { <-s.enqueueSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *App) dbosBackend() syncBackend { return dbosBackend{a} }

type dbosBackend struct{ a *App }

func (a *App) dbosSchema() string {
	if a.Cfg != nil && a.Cfg.Sync.DBOSSchema != "" {
		return a.Cfg.Sync.DBOSSchema
	}
	return "dbos"
}

// minDBOSPool is the smallest pool a DBOS client can enqueue a duplicate job with: the listener's connection, the
// transaction's and the lookup's (see enqueueSlot).
const minDBOSPool = 3

func (a *App) dbosPoolSize() int {
	if a.Cfg != nil && a.Cfg.Sync.DBOSPoolSize > 0 {
		return max(minDBOSPool, a.Cfg.Sync.DBOSPoolSize)
	}
	return 20
}

// dbosURL is the connection string of the DBOS system database: SYNC__DBOS_URI, or the application's own with room for the
// waiters. DBOS prepares its statements, so behind PgBouncer in transaction mode it needs either PgBouncer 1.21 or newer with
// max_prepared_statements set, or a URI of its own that goes to PostgreSQL directly.
func (a *App) dbosURL() (string, error) {
	if a.Cfg == nil {
		return "", errors.New("dbos: no configuration")
	}
	raw := a.Cfg.DB.URI
	if a.Cfg.Sync.DBOSURI != "" {
		raw = a.Cfg.Sync.DBOSURI
	}
	u, err := url.Parse(db.NormalizeDSN(raw))
	if err != nil {
		return "", err
	}
	q := u.Query()
	if n, err := strconv.Atoi(q.Get("pool_max_conns")); err != nil || n < minDBOSPool {
		// The waiters of one workflow share a poller, so the pool is for the enqueues and the workflows; it is outside DB__POOL_SIZE.
		q.Set("pool_max_conns", strconv.Itoa(a.dbosPoolSize()))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// dbosClient returns the DBOS handle of the process. A client needs the system tables, which the first worker creates: until
// they exist the call fails, is not tried again for 100 ms, and the caller answers from the data that exists.
func (a *App) dbosClient(ctx context.Context) (dbos.Client, error) {
	s := &a.dbos
	s.mu.Lock()
	if s.runtime != nil {
		defer s.mu.Unlock()
		return s.runtime, nil
	}
	if s.client != nil {
		defer s.mu.Unlock()
		return s.client, nil
	}
	if s.connecting || time.Since(s.tried) < 100*time.Millisecond {
		s.mu.Unlock()
		return nil, errSyncUnavailable
	}
	s.tried = time.Now()
	s.connecting = true
	s.mu.Unlock()

	// Creating the client waits for the system tables for seconds: the lock is not held meanwhile, or every request that
	// needs a synchronization would queue behind it.
	c, err := a.newDBOSClient(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connecting = false
	if err != nil {
		slog.WarnContext(ctx, "the DBOS client is not ready (has a worker created the tables?)", "error", err)
		return nil, errSyncUnavailable
	}
	s.client = c
	return c, nil
}

func (a *App) newDBOSClient(ctx context.Context) (dbos.Client, error) {
	dsn, err := a.dbosURL()
	if err != nil {
		return nil, err
	}
	// The client lives as long as the process: its context must not end with the request that created it.
	return dbos.NewClient(context.WithoutCancel(ctx), dbos.ClientConfig{
		DatabaseURL: dsn, DatabaseSchema: a.dbosSchema(), AppName: dbosApp, SystemDBStartupTimeout: 5 * time.Second,
		Logger: slog.Default().With("component", "dbos"),
	})
}

// enqueue queues the workflow of each type, named after the state the caller read when seen has it (see jobName). The
// concurrent requests of the process for the same workflow share one enqueue: one transaction and one slot.
func (b dbosBackend) enqueue(ctx context.Context, typeIDs []uuid.UUID, fresh bool, source string, seen map[uuid.UUID]string) (map[uuid.UUID]string, error) {
	client, err := b.a.dbosClient(ctx)
	if err != nil {
		return nil, err
	}
	refs := make(map[uuid.UUID]string, len(typeIDs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	for _, id := range typeIDs {
		wg.Go(func() {
			key := dedupKey([]uuid.UUID{id}, fresh)
			opts := []dbos.EnqueueOption{
				dbos.WithEnqueueDeduplicationID(key),
				dbos.WithEnqueueDeduplicationPolicy(dbos.DeduplicationPolicyReturnExisting),
				dbos.WithEnqueueAttributes(map[string]any{"type": id.String(), "fresh": fresh}),
			}
			flight := key
			if v, ok := seen[id]; ok && !fresh {
				flight = jobName(key, v)
				opts = append(opts, dbos.WithEnqueueWorkflowID(flight))
			}
			ch := b.a.dbos.enqueues.DoChan(flight, func() (any, error) {
				// The enqueue is shared: it goes on for the others when the request that started it is gone.
				fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enqueueWait)
				defer cancel()
				release, err := b.a.enqueueSlot(fctx)
				if err != nil {
					return "", err
				}
				defer release()
				h, err := dbos.Enqueue[dbosSyncOut](client, dbosQueue, dbosWorkflow,
					dbosSyncIn{TypeID: id.String(), Fresh: fresh, Source: source}, opts...)
				if err != nil {
					return "", err
				}
				return h.GetWorkflowID(), nil
			})
			var ref string
			var err error
			select {
			case r := <-ch:
				ref, _ = r.Val.(string)
				err = r.Err
			case <-ctx.Done():
				err = ctx.Err()
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				firstErr = errors.Join(firstErr, err)
				return
			}
			refs[id] = ref
		})
	}
	wg.Wait()
	if firstErr != nil {
		slog.WarnContext(ctx, "failed to enqueue synchronization workflows", "error", firstErr)
		return nil, fmt.Errorf("%w: %w", errSyncUnavailable, firstErr)
	}
	return refs, nil
}

// poll waits for the workflow. A workflow that was cancelled because its executor died has a replacement under the id
// "<id>+r": the wait goes on there.
func (b dbosBackend) poll(ref string) (jobOutcome, error) {
	client, err := b.a.dbosClient(context.Background())
	if err != nil {
		return jobOutcome{}, err
	}
	for range 6 {
		h, err := dbos.RetrieveWorkflow[dbosSyncOut](client, ref)
		if err != nil {
			if errors.Is(err, dbos.ErrNonExistentWorkflow) {
				time.Sleep(50 * time.Millisecond) // the replacement is being queued
				continue
			}
			return jobOutcome{}, err
		}
		out, err := h.GetResult(dbos.WithHandleTimeout(jobPollCap), dbos.WithHandlePollingInterval(25*time.Millisecond))
		switch {
		case err == nil && out.LeaseLost:
			ref += dbosReplacement // it applied nothing: the replacement is the job to wait for
		case err == nil:
			o := jobOutcome{state: "done"}
			for _, s := range out.Unanswered {
				if id, perr := uuid.Parse(s); perr == nil {
					o.unanswered = append(o.unanswered, id)
				}
			}
			return o, nil
		case errors.Is(err, dbos.ErrWorkflowCancelled), errors.Is(err, dbos.ErrAwaitedWorkflowCancelled):
			ref += dbosReplacement
		case errors.Is(err, dbos.ErrTimeout):
			return jobOutcome{}, err
		default:
			return jobOutcome{state: "failed", err: err.Error()}, nil
		}
	}
	return jobOutcome{state: "failed", err: "the workflow kept being taken over"}, nil
}

// runDBOSWorker is RunWorker for the dbos engine: it launches the runtime, and keeps the heartbeat, the janitor and the
// schedule going until ctx ends.
func (a *App) runDBOSWorker(ctx context.Context, owner string) error {
	dsn, err := a.dbosURL()
	if err != nil {
		return err
	}
	executor := strings.NewReplacer("/", "-", ":", "-").Replace(owner)
	c, err := dbos.NewContext(ctx, dbos.Config{
		AppName: dbosApp, DatabaseURL: dsn, DatabaseSchema: a.dbosSchema(), ExecutorID: executor, ApplicationVersion: dbosVersion,
		Logger: slog.Default().With("component", "dbos"),
	})
	if err != nil {
		return fmt.Errorf("dbos: %w", err)
	}
	dbos.RegisterWorkflow(c, a.dbosSyncWorkflow, dbos.WithWorkflowName(dbosWorkflow))
	if _, err := dbos.RegisterQueue(c, dbosQueue,
		dbos.WithWorkerConcurrency(a.workerConcurrency()),
		dbos.WithQueueBasePollingInterval(50*time.Millisecond)); err != nil {
		return fmt.Errorf("dbos queue: %w", err)
	}
	if err := c.Launch(); err != nil {
		return fmt.Errorf("dbos launch: %w", err)
	}
	a.dbos.mu.Lock()
	a.dbos.runtime = c
	a.dbos.mu.Unlock()
	slog.InfoContext(ctx, "DBOS runtime launched", "executor", executor, "schema", a.dbosSchema())

	var wg sync.WaitGroup
	wg.Go(func() { a.dbosHeartbeat(ctx, executor) })
	wg.Go(func() { a.dbosJanitor(ctx, c, executor) })
	wg.Go(func() { a.schedule(ctx) })
	wg.Wait()
	_ = dbos.Shutdown(c, 5*time.Second)
	a.dbos.mu.Lock()
	a.dbos.runtime = nil
	a.dbos.mu.Unlock()
	return nil
}

// dbosSyncWorkflow is the workflow: one step, fetch and apply of one type.
func (a *App) dbosSyncWorkflow(ctx dbos.Context, in dbosSyncIn) (dbosSyncOut, error) {
	typeID, err := uuid.Parse(in.TypeID)
	if err != nil {
		return dbosSyncOut{}, err
	}
	workflowID, err := dbos.GetWorkflowID(ctx)
	if err != nil {
		return dbosSyncOut{}, err
	}
	started := time.Now()
	out, err := dbos.RunAsStep(ctx, func(sc context.Context) (dbosSyncOut, error) {
		var unanswered map[uuid.UUID]bool
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			unanswered, err = a.runSync(sc, []uuid.UUID{typeID}, in.Fresh, func(ctx context.Context, tx pgx.Tx, _ map[uuid.UUID]bool) error {
				// The fence: the workflow still runs. A cancel (a takeover) waits for this transaction to end.
				var status string
				q := fmt.Sprintf(`SELECT status FROM %s.workflow_status WHERE workflow_uuid = $1 FOR SHARE`, pgx.Identifier{a.dbosSchema()}.Sanitize())
				if err := tx.QueryRow(ctx, q, workflowID).Scan(&status); err != nil {
					return err
				}
				if status != string(dbos.WorkflowStatusPending) {
					a.leaseLost()
					return errLeaseLost
				}
				return nil
			})
			if !isLockTimeout(err) {
				break
			}
			time.Sleep(500 * time.Millisecond) // the graph lock was busy: try again
		}
		if errors.Is(err, errLeaseLost) {
			return dbosSyncOut{LeaseLost: true}, nil // an error type is not stored well; the flag is
		}
		if err != nil {
			return dbosSyncOut{}, err
		}
		res := dbosSyncOut{}
		for id := range unanswered {
			res.Unanswered = append(res.Unanswered, id.String())
		}
		return res, nil
	}, dbos.WithStepName("sync"))

	state := "done"
	switch {
	case err == nil && out.LeaseLost:
		slog.WarnContext(ctx, "a synchronization workflow was taken over, its result is dropped", "workflow", workflowID)
		state = "lease_lost"
	case err == nil:
	case ctx.Err() != nil:
		// The worker is shutting down: the workflow is recovered or taken over, and no backoff is due.
		return out, err
	case isLockTimeout(err):
		// The lock of the component stayed busy through every attempt. That is not a failure of the source: the type is
		// due again soon, the data stays, and the waiters are told the source did not answer, as for a job that is requeued.
		slog.WarnContext(ctx, "a synchronization workflow waited too long for the graph lock, the type is postponed", "workflow", workflowID, "type_resource_id", typeID)
		a.recordBusy(context.WithoutCancel(ctx), []uuid.UUID{typeID}, err)
		out, err = dbosSyncOut{Unanswered: []string{typeID.String()}}, nil
		state = "requeued"
	default:
		slog.ErrorContext(ctx, "a synchronization workflow failed", "workflow", workflowID, "type_resource_id", typeID, "error", err)
		a.recordFailure(context.WithoutCancel(ctx), []uuid.UUID{typeID}, err)
		state = "failed"
	}
	if a.metrics != nil {
		a.metrics.syncJobs.WithLabelValues(state).Inc()
		a.metrics.syncJobSeconds.Observe(time.Since(started).Seconds())
	}
	a.notifyJobsDone()
	return out, err
}

// dbosHeartbeat tells the other workers this executor is alive.
func (a *App) dbosHeartbeat(ctx context.Context, executor string) {
	lease := a.leaseDuration()
	beat := func() {
		if a.Hooks.NoHeartbeat != nil && a.Hooks.NoHeartbeat(nil) {
			return
		}
		if _, err := a.DB.Exec(ctx, `
			INSERT INTO sync_executor (executor_id, heartbeat_at) VALUES ($1, now())
			ON CONFLICT (executor_id) DO UPDATE SET heartbeat_at = now()`, executor); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "failed to write the heartbeat of the synchronization executor", "error", err)
		}
	}
	beat()
	ticker := time.NewTicker(lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = a.DB.Exec(context.WithoutCancel(ctx), `DELETE FROM sync_executor WHERE executor_id = $1`, executor)
			return
		case <-ticker.C:
			beat()
		}
	}
}

// dbosJanitor takes over the workflows of the executors that stopped beating: it cancels them and queues a replacement
// under "<id>+r". It also deletes the workflows that ended long ago.
func (a *App) dbosJanitor(ctx context.Context, c dbos.Context, self string) {
	lease := a.leaseDuration()
	ticker := time.NewTicker(lease / 3)
	defer ticker.Stop()
	for {
		a.dbosTakeOver(ctx, c, self, lease)
		a.dbosPrune(ctx, c)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) dbosTakeOver(ctx context.Context, c dbos.Context, self string, lease time.Duration) {
	rows, err := a.DB.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT w.executor_id FROM %s.workflow_status w
		WHERE w.status = 'PENDING' AND w.queue_name = $1 AND w.executor_id <> $2
		  AND NOT EXISTS (SELECT 1 FROM sync_executor e
		                  WHERE e.executor_id = w.executor_id AND e.heartbeat_at > now() - make_interval(secs => $3))`,
		pgx.Identifier{a.dbosSchema()}.Sanitize()), dbosQueue, self, lease.Seconds())
	if err != nil {
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "failed to look for dead synchronization executors", "error", err)
		}
		return
	}
	var dead []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			dead = append(dead, id)
		}
	}
	rows.Close()
	if len(dead) == 0 {
		return
	}
	flows, err := dbos.ListWorkflows(c, dbos.WithFilterExecutorIDs(dead...), dbos.WithFilterStatus(dbos.WorkflowStatusPending),
		dbos.WithFilterQueueName(dbosQueue), dbos.WithFilterLoadInput(true))
	if err != nil {
		slog.WarnContext(ctx, "failed to list the workflows of dead synchronization executors", "error", err)
		return
	}
	for _, w := range flows {
		if ctx.Err() != nil {
			return // the worker is stopping: a workflow cancelled now would need its replacement queued too
		}
		var in dbosSyncIn
		raw, _ := json.Marshal(w.Input)
		if inner := ""; json.Unmarshal(raw, &inner) == nil && inner != "" {
			raw = []byte(inner) // the input comes back as a JSON string holding the JSON
		}
		if json.Unmarshal(raw, &in) != nil || in.TypeID == "" {
			slog.WarnContext(ctx, "cannot read the input of a workflow of a dead synchronization executor", "workflow", w.ID, "input", string(raw))
			continue
		}
		typeID, err := uuid.Parse(in.TypeID)
		if err != nil {
			continue
		}
		if err := dbos.CancelWorkflow(c, w.ID); err != nil {
			slog.WarnContext(ctx, "failed to cancel the workflow of a dead synchronization executor", "workflow", w.ID, "error", err)
			continue
		}
		// The workflow is cancelled: its replacement is queued even if the worker stops while the janitor waits for a slot, or
		// its waiters would follow the chain to nothing until a request asks for the type again.
		qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enqueueWait)
		release, err := a.enqueueSlot(qctx)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "no enqueue slot for the replacement of a taken over workflow", "workflow", w.ID, "error", err)
			return
		}
		_, err = dbos.Enqueue[dbosSyncOut](c, dbosQueue, dbosWorkflow, in,
			dbos.WithEnqueueWorkflowID(w.ID+dbosReplacement),
			dbos.WithEnqueueDeduplicationID(dedupKey([]uuid.UUID{typeID}, in.Fresh)),
			dbos.WithEnqueueDeduplicationPolicy(dbos.DeduplicationPolicyReturnExisting),
			dbos.WithEnqueueAttributes(map[string]any{"type": typeID.String(), "fresh": in.Fresh}))
		release()
		if err != nil && !errors.Is(err, dbos.ErrConflictingWorkflowID) && !errors.Is(err, dbos.ErrWorkflowIDInUse) {
			slog.WarnContext(ctx, "failed to queue the replacement of a taken over workflow", "workflow", w.ID, "error", err)
			continue
		}
		slog.WarnContext(ctx, "took over a synchronization workflow of a dead executor", "workflow", w.ID, "executor", w.ExecutorID, "type_resource_id", typeID)
	}
}

func (a *App) dbosPrune(ctx context.Context, c dbos.Context) {
	window := 10 * time.Minute
	if a.Cfg != nil && a.Cfg.Sync.DemandWindow > 0 {
		window = a.Cfg.Sync.DemandWindow
	}
	before := time.Now().Add(-max(time.Hour, 2*window))
	old, err := dbos.ListWorkflows(c, dbos.WithFilterCompletedBefore(before), dbos.WithFilterQueueName(dbosQueue),
		dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false), dbos.WithFilterLimit(500))
	if err != nil || len(old) == 0 {
		return
	}
	ids := make([]string, len(old))
	for i, w := range old {
		ids[i] = w.ID
	}
	if err := dbos.DeleteWorkflows(c, ids); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "failed to delete the old synchronization workflows", "error", err)
	}
}

// recordBusy postpones the types whose component lock stayed busy through every attempt: they are tried again soon, with
// no backoff (see markBusy).
func (a *App) recordBusy(ctx context.Context, typeIDs []uuid.UUID, cause error) {
	err := a.syncTx(ctx, nil, func(tx pgx.Tx) error {
		for _, id := range typeIDs {
			if err := markBusy(ctx, tx, id, cause.Error()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to postpone a synchronization", "error", err)
	}
}

// recordFailure starts the backoff of types whose job failed for a reason other than their source not answering.
func (a *App) recordFailure(ctx context.Context, typeIDs []uuid.UUID, cause error) {
	err := a.syncTx(ctx, nil, func(tx pgx.Tx) error {
		for _, id := range typeIDs {
			if err := markUnanswered(ctx, tx, id, cause.Error()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record the failure of a synchronization", "error", err)
	}
}

// closeDBOS shuts the DBOS client of the process down.
func (a *App) closeDBOS() {
	a.dbos.mu.Lock()
	c := a.dbos.client
	a.dbos.client = nil
	a.dbos.mu.Unlock()
	if c != nil {
		_ = dbos.Shutdown(c, 2*time.Second)
	}
}

// useDBOS says the synchronization runs on DBOS, the default; only SYNC__ENGINE=postgres selects the sync_job table.
func (a *App) useDBOS() bool { return a.Cfg == nil || a.Cfg.Sync.Engine != "postgres" }
