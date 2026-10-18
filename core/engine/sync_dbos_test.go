//go:build integration

package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	support "github.com/licetum/licetum/core/testsupport"
)

// dbosFixture is a database, a process on it whose DBOS client has a pool of 3 (one enqueue slot, see enqueueSlot), and a
// DBOS executor. Given a workflow, the executor runs the synchronization jobs with it; without one no queue is registered,
// and the workflows stay where the test puts them.
type dbosFixture struct {
	a    *App
	pool *pgxpool.Pool
	cfg  *config.Config
	c    dbos.Context
}

func newDBOSFixture(t *testing.T, workflow func(dbos.Context, dbosSyncIn) (dbosSyncOut, error)) *dbosFixture {
	t.Helper()
	ctx := context.Background()
	infra, err := support.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = infra.Terminate(context.Background()) })
	pool, err := db.Connect(ctx, config.Database{URI: infra.DBURI, PoolSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateUpTo(ctx, pool, db.LatestMigration()); err != nil {
		t.Fatal(err)
	}
	f := &dbosFixture{pool: pool, cfg: &config.Config{DB: config.Database{URI: infra.DBURI}, Sync: config.Sync{DBOSPoolSize: 3}}}
	f.a = f.process(t)
	dsn, err := f.a.dbosURL()
	if err != nil {
		t.Fatal(err)
	}
	f.c, err = dbos.NewContext(ctx, dbos.Config{AppName: dbosApp, DatabaseURL: dsn, DatabaseSchema: f.a.dbosSchema(),
		ExecutorID: "worker", ApplicationVersion: dbosVersion})
	if err != nil {
		t.Fatal(err)
	}
	if workflow != nil {
		dbos.RegisterWorkflow(f.c, workflow, dbos.WithWorkflowName(dbosWorkflow))
		if _, err := dbos.RegisterQueue(f.c, dbosQueue, dbos.WithQueueBasePollingInterval(20*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.c.Launch(); err != nil { // creates the system tables
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbos.Shutdown(f.c, 5*time.Second) })
	return f
}

// process is a process on the database of the fixture, with a DBOS client of its own.
func (f *dbosFixture) process(t *testing.T) *App {
	a := New(f.pool, nil, f.cfg)
	t.Cleanup(a.Close)
	return a
}

// status is the status of a workflow, "" when there is none.
func (f *dbosFixture) status(id string) string {
	var s string
	_ = f.pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT status FROM %s.workflow_status WHERE workflow_uuid = $1`, f.a.dbosSchema()), id).Scan(&s)
	return s
}

// jobs counts the workflows of a type, in a status or in any ("").
func (f *dbosFixture) jobs(typ uuid.UUID, status string) int {
	var n int
	_ = f.pool.QueryRow(context.Background(), fmt.Sprintf(`SELECT count(*) FROM %s.workflow_status
		WHERE attributes->>'type' = $1 AND ($2 = '' OR status = $2)`, f.a.dbosSchema()), typ.String(), status).Scan(&n)
	return n
}

func (f *dbosFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// A request that read the state of a type before a job brought it up to date, and asks for the job after the job has ended
// (here its enqueue slot was busy; under load its DBOS client was still connecting, or it was merely slower), finds that job
// and its outcome: the deduplication id was freed when the job ended, and a second job would start for data that is current
// already. TestSyncOneSourceCallForAConcurrentFirstRequest saw "2 jobs ran" so under load.
func TestDBOSARequestThatReadTheStateBeforeAJobEndedSharesTheJob(t *testing.T) {
	var sig atomic.Value // the structure the job writes
	running, end := make(chan struct{}), make(chan struct{})
	var runs atomic.Int32
	var f *dbosFixture
	f = newDBOSFixture(t, func(ctx dbos.Context, in dbosSyncIn) (dbosSyncOut, error) {
		if runs.Add(1) == 1 {
			close(running)
			<-end
		}
		// What a job writes at its end: the state of the type.
		_, err := f.pool.Exec(ctx, `
			INSERT INTO sync_state (type_resource_id, format, data_hash, graph_sig, applied_at, next_due_at)
			VALUES ($1, $2, 'applied', $3, now(), now() + interval '30 seconds')
			ON CONFLICT (type_resource_id) DO UPDATE SET applied_at = now()`, in.TypeID, syncFormat, sig.Load())
		return dbosSyncOut{}, err
	})
	ctx := context.Background()
	service, typ := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO service (id, name, url, icon, slug) VALUES ($1, 'enqueue', '', '', 'enqueue')`, service)
	f.exec(t, `INSERT INTO type_resource (id, service_id, name) VALUES ($1, $2, 'branch')`, typ, service)
	f.exec(t, `INSERT INTO synchronizer (type_resource_id, kind, config) VALUES ($1, 'HTTP', '{"kind": "http", "url": "http://127.0.0.1:1/src"}')`, typ)
	g, err := f.a.LoadTypeGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	syncs, err := f.a.liveSynchronizers(ctx, []uuid.UUID{typ})
	if err != nil || len(syncs) != 1 {
		t.Fatalf("the synchronizer: %v %v", syncs, err)
	}
	sig.Store(structureOf(g, typ, syncs[0].Config))
	ensure := func(a *App) chan error {
		done := make(chan error, 1)
		go func() {
			res, err := a.ensure(ctx, g, []uuid.UUID{typ}, SyncOptions{Wait: 10 * time.Second}, "request")
			if err == nil && res.Pending {
				err = fmt.Errorf("the data is pending: %+v", res)
			}
			done <- err
		}()
		return done
	}
	asked := func() (at time.Time) {
		_ = f.pool.QueryRow(ctx, `SELECT last_asked_at FROM sync_demand WHERE type_resource_id = $1`, typ).Scan(&at)
		return at
	}

	first := ensure(f.a) // finds no data and waits for the job, which runs until the test ends it
	<-running

	other := f.process(t) // another process: its request also finds no data, and its only enqueue slot is busy
	if _, err := other.dbosClient(ctx); err != nil {
		t.Fatal(err)
	}
	release, err := other.enqueueSlot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := asked()
	late := ensure(other)
	waitUntil(t, "the other request to ask for the job", func() bool { return asked().After(before) }) // it has read the state

	close(end)
	waitUntil(t, "the job to end", func() bool { return f.jobs(typ, string(dbos.WorkflowStatusSuccess)) == 1 })
	release() // the other request queues now, after the job ended

	for _, done := range []chan error{first, late} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("a request did not finish")
		}
	}
	if jobs := f.jobs(typ, ""); jobs != 1 || runs.Load() != 1 {
		t.Fatalf("%d jobs were queued and %d ran for one state of the type, want one", jobs, runs.Load())
	}
}

// The janitor cancels the workflow of a dead executor and then queues its replacement "<id>+r". A worker that stops between
// the two (its context ends while the janitor waits for an enqueue slot) still queues the replacement: the waiters of the
// cancelled workflow follow the chain to it.
func TestDBOSTakeOverQueuesTheReplacementWhenTheWorkerStops(t *testing.T) {
	f := newDBOSFixture(t, nil)
	ctx := context.Background()
	typeID := uuid.New()
	h, err := dbos.Enqueue[dbosSyncOut](f.c, dbosQueue, dbosWorkflow, dbosSyncIn{TypeID: typeID.String(), Source: "request"},
		dbos.WithEnqueueDeduplicationID(dedupKey([]uuid.UUID{typeID}, false)),
		dbos.WithEnqueueAttributes(map[string]any{"type": typeID.String(), "fresh": false}))
	if err != nil {
		t.Fatal(err)
	}
	orphan := h.GetWorkflowID()
	// It was running on an executor that died: no heartbeat in sync_executor.
	f.exec(t, fmt.Sprintf(`UPDATE %s.workflow_status SET status = 'PENDING', executor_id = 'dead' WHERE workflow_uuid = $1`,
		f.a.dbosSchema()), orphan)

	release, err := f.a.enqueueSlot(ctx) // the only slot is busy
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.a.dbosTakeOver(workerCtx, f.c, "worker", time.Second)
	}()
	waitUntil(t, "the workflow of the dead executor to be cancelled", func() bool {
		return f.status(orphan) == string(dbos.WorkflowStatusCancelled)
	})
	stop() // the worker stops while the janitor waits for the slot
	select {
	case <-done:
		t.Fatal("the janitor gave up the replacement of a workflow it had cancelled when the worker stopped")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the janitor did not finish once the slot was free")
	}
	if got := f.status(orphan + dbosReplacement); got != string(dbos.WorkflowStatusEnqueued) {
		t.Fatalf("the replacement of the cancelled workflow is %q, want it queued", got)
	}
}
