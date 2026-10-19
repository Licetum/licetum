//go:build integration

package api_test

// Acceptance tests of the synchronization pipeline that need a real process (kill -9), a bounded pool, or a measurement:
// F3-4, F3-6 (p99), F3-7 and F3-12.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

// workerBinary builds `licetum worker` once per test run.
var (
	workerBuild  sync.Once
	workerPath   string
	workerBuildE error
)

func buildWorker(t *testing.T) string {
	t.Helper()
	workerBuild.Do(func() {
		dir, err := os.MkdirTemp("", "licetum-worker")
		if err != nil {
			workerBuildE = err
			return
		}
		workerPath = filepath.Join(dir, "licetum")
		cmd := exec.Command("go", "build", "-o", workerPath, "./cmd/licetum")
		cmd.Dir = "../../../../core"
		if out, err := cmd.CombinedOutput(); err != nil {
			workerBuildE = fmt.Errorf("build the worker: %v\n%s", err, out)
		}
	})
	if workerBuildE != nil {
		t.Fatal(workerBuildE)
	}
	return workerPath
}

// workerProcess starts `licetum worker` against the test database.
func (e *env) workerProcess(lease int) *exec.Cmd {
	e.t.Helper()
	cmd := exec.Command(buildWorker(e.t), "worker", "-addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(),
		"DB__URI="+e.cfg.DB.URI, "REDIS__URI="+testInfrastructure.RedisURI,
		"SYNC__LEASE="+strconv.Itoa(lease), "SYNC__CONCURRENCY=1", "SYNC__FRESH_MIN_INTERVAL=0",
		"SYNC__ENGINE="+syncEngine(), "SYNC__DBOS_SCHEMA="+e.cfg.Sync.DBOSSchema)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// bigSource serves n resources; the first call waits for firstDelay.
func bigSource(t *testing.T, n int, firstDelay time.Duration) *source {
	t.Helper()
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": "r" + strconv.Itoa(i), "name": "Resource " + strconv.Itoa(i)}
	}
	s := newSource(t, rows...)
	if firstDelay > 0 {
		s.delay.Store(int64(firstDelay))
		go func() {
			for s.calls.Load() == 0 {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(10 * time.Millisecond)
			s.delay.Store(0)
		}()
	}
	return s
}

func (e *env) waitFor(what string, d time.Duration, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) resourceCount(typ string) int {
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM resource_cache WHERE type_resource_id = $1`, typ).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// F3-4: kill -9 of the worker between fetch and commit, and during the commit: another worker finishes within twice the
// lease, the closure is consistent (no partial type), and the job ends done exactly once.
func TestSyncKillNineOfTheWorker(t *testing.T) {
	for _, phase := range []string{"fetch", "commit"} {
		t.Run(phase, func(t *testing.T) {
			e := setup(t)
			const lease = 2
			n, firstDelay := 3000, 4*time.Second // the fetch is slow: the kill lands before the commit
			if phase == "commit" {
				n, firstDelay = 120000, 0 // the commit is long: the kill lands inside the transaction
			}
			src := bigSource(t, n, firstDelay)
			f := e.syncFixture("kill-"+phase, src)
			e.stopWorker()

			victim := e.workerProcess(lease)
			r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 50 * time.Millisecond })
			r.permission(t, f) // queues the job

			switch phase {
			case "fetch":
				e.waitFor("the source to be asked", 20*time.Second, func() bool { return src.calls.Load() > 0 })
			case "commit":
				// The job of the fixture itself must be running, not a synchronization of a platform type that happens to write.
				e.waitFor("the job to be running", 60*time.Second, func() bool { return e.jobCount(f.typ, "running") > 0 })
				e.waitFor("the transaction to be writing", 60*time.Second, func() bool {
					var busy int
					_ = e.pool.QueryRow(context.Background(), `
						SELECT count(*) FROM pg_stat_activity
						WHERE datname = current_database() AND state = 'active' AND pid <> pg_backend_pid()
						  AND (query ILIKE '%INSERT INTO resource_cache%' OR query ILIKE '%resource_ancestor%')`).Scan(&busy)
					return busy > 0
				})
			}
			if err := victim.Process.Kill(); err != nil { // SIGKILL
				t.Fatal(err)
			}
			_ = victim.Wait()
			killed := time.Now()
			if phase == "commit" && e.jobCount(f.typ, "done") != 0 {
				t.Skip("the worker finished before the kill; the transaction was too short on this machine")
			}

			_, _ = e.worker(func(c *config.Config) {
				c.Sync.Lease, c.Sync.Concurrency, c.Sync.FreshMinInterval = lease*time.Second, 1, time.Millisecond
			}, engine.SyncHooks{})
			e.waitFor("another worker to finish the job", 2*lease*time.Second+60*time.Second, func() bool { return e.jobCount(f.typ, "done") > 0 })
			// The takeover itself happens within twice the lease; redoing the work takes as long as the work does.
			var reclaimed time.Time
			if syncEngine() == "dbos" {
				// The replacement of the workflow of the dead executor is queued under "<id>+r".
				var createdMs int64
				if err := e.pool.QueryRow(context.Background(), fmt.Sprintf(
					`SELECT min(created_at) FROM %s.workflow_status WHERE name = 'SyncType' AND workflow_uuid LIKE '%%+r' AND attributes->>'type' = $1`,
					e.cfg.Sync.DBOSSchema), f.typ).Scan(&createdMs); err != nil {
					t.Fatal(err)
				}
				reclaimed = time.UnixMilli(createdMs)
			} else if err := e.pool.QueryRow(context.Background(),
				`SELECT started_at FROM sync_job WHERE type_ids = ARRAY[$1::uuid] AND state = 'done'`, f.typ).Scan(&reclaimed); err != nil {
				t.Fatal(err)
			}
			if took := reclaimed.Sub(killed); took > 2*lease*time.Second {
				t.Fatalf("the job was taken over %v after the kill, want within twice the lease (%ds)", took, lease)
			}
			if got := e.resourceCount(f.typ); got != n {
				t.Fatalf("%d resources after the takeover, want %d (a partial type)", got, n)
			}
			e.requireClosureConsistent()
			if done := e.jobCount(f.typ, "done"); done != 1 {
				t.Fatalf("the job ended %d times", done)
			}
		})
	}
}

// F3-7: the synchronization pool never holds more than DB__SYNC_POOL_SIZE connections, and the request pool is not starved
// by synchronizations that wait for each other.
func TestSyncPoolsAreBounded(t *testing.T) {
	e := setup(t)
	const types = 10
	fixtures := make([]*syncFixture, types)
	for i := range fixtures {
		fixtures[i] = e.syncFixture(fmt.Sprintf("pool-%d", i), bigSource(t, 4000, 0))
	}
	e.stopWorker()
	worker, _ := e.worker(func(c *config.Config) { c.DB.SyncPoolSize, c.Sync.Concurrency = 2, 8 }, engine.SyncHooks{})
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 20 * time.Second })

	var peak atomic.Int32
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n := worker.SyncDB.Stat().AcquiredConns(); n > peak.Load() {
				peak.Store(n)
			}
			time.Sleep(time.Millisecond)
		}
	})
	var wg sync.WaitGroup
	for _, f := range fixtures {
		wg.Go(func() { r.permission(t, f) })
	}
	wg.Wait()
	close(stop)
	sampler.Wait()
	if got := worker.SyncDB.Stat().MaxConns(); got != 2 {
		t.Fatalf("the synchronization pool has %d connections, want 2", got)
	}
	if got := peak.Load(); got > 2 {
		t.Fatalf("the synchronization pool held %d connections, want at most 2", got)
	}
	for _, f := range fixtures {
		if e.jobCount(f.typ, "done") != 1 {
			t.Fatalf("type %s was not synchronized", f.typ)
		}
	}
	// The request pool of the check process never waited for a connection.
	if wait := r.app.DB.Stat().EmptyAcquireCount(); wait > 0 && r.app.DB.Stat().CanceledAcquireCount() > 0 {
		t.Fatalf("the request pool was starved: %d acquires waited, %d gave up", wait, r.app.DB.Stat().CanceledAcquireCount())
	}
}

// F3-6 (the p99 part): while a synchronization of one component is held for 30 s (LICETUM_SYNC_HOLD shortens it), the p99
// of warm check requests degrades by at most 20 %.
func TestSyncHeldSynchronizationDoesNotSlowTheChecks(t *testing.T) {
	hold := 30 * time.Second
	if v, err := time.ParseDuration(os.Getenv("LICETUM_SYNC_HOLD")); err == nil && v > 0 {
		hold = v
	}
	e := setup(t)
	held := e.syncFixture("held", newSource(t, branches("h1")...))
	warm := e.syncFixture("warm", newSource(t, branches("w1", "w2")...))
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = hold + 15*time.Second })
	r.permission(t, warm)

	measure := func(d time.Duration) time.Duration {
		var lat []time.Duration
		for end := time.Now().Add(d); time.Now().Before(end); {
			started := time.Now()
			if status, _, _ := r.permission(t, warm); status != http.StatusOK {
				t.Fatal("a check failed")
			}
			lat = append(lat, time.Since(started))
		}
		slices.Sort(lat)
		return lat[len(lat)*99/100]
	}
	measure(time.Second) // warm up
	baseline := measure(5 * time.Second)

	g, err := e.app.LoadTypeGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock($1)`, engine.ComponentKeys(g, []uuid.UUID{uuid.MustParse(held.typ)})[0]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { r.permission(t, held) }) // waits for the lock inside the worker
	time.Sleep(300 * time.Millisecond)
	during := measure(hold)
	_ = tx.Rollback(context.Background())
	wg.Wait()

	t.Logf("p99 of a warm check: %v without a held synchronization, %v with one held for %v", baseline, during, hold)
	if limit := max(baseline*12/10, baseline+3*time.Millisecond); during > limit {
		t.Fatalf("p99 degraded from %v to %v (limit %v)", baseline, during, limit)
	}
}

// F3-12: peak RSS of the worker for a type of LICETUM_SYNC_MEMORY_RESOURCES resources (default 1 000 000). It is a
// measurement, run on demand: LICETUM_SYNC_MEMORY=1 go test -tags integration -run TestSyncWorkerMemory -v
func TestSyncWorkerMemory(t *testing.T) {
	if os.Getenv("LICETUM_SYNC_MEMORY") == "" {
		t.Skip("set LICETUM_SYNC_MEMORY=1 to measure the memory of the worker")
	}
	n := 1_000_000
	if v, err := strconv.Atoi(os.Getenv("LICETUM_SYNC_MEMORY_RESOURCES")); err == nil && v > 0 {
		n = v
	}
	e := setup(t)
	// The source streams its list: the test process must not hold it in memory.
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /src", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		bw := bufio.NewWriterSize(w, 1<<20)
		_, _ = bw.WriteString("[")
		for i := range n {
			if i > 0 {
				_, _ = bw.WriteString(",")
			}
			_, _ = fmt.Fprintf(bw, `{"id":"r%d","name":"Resource %d","region":"north"}`, i, i)
		}
		_, _ = bw.WriteString("]")
		_ = bw.Flush()
	})
	mux.HandleFunc("GET /src/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "x"})
	})
	src := &source{Server: newTestServer(t, mux)}
	f := e.syncFixture("memory", src)
	e.stopWorker()

	worker := e.workerProcess(600)
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 50 * time.Millisecond })
	r.permission(t, f)
	e.waitFor("the synchronization", 30*time.Minute, func() bool { return e.jobCount(f.typ, "done") > 0 })

	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", worker.Process.Pid))
	if err != nil {
		t.Skipf("no /proc: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			t.Logf("%d resources: worker peak RSS %s", n, strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:")))
		}
	}
	if got := e.resourceCount(f.typ); got != n {
		t.Fatalf("%d resources, want %d", got, n)
	}
}

// F3 (lock timeout): a synchronization whose component lock stays busy through every attempt does not start the backoff
// of the source (it did not fail): once the lock is free the type is synchronized by the next request, not after the
// 30 s or more of a backoff. On the dbos engine; the postgres engine requeues the job, as before.
func TestSyncBusyComponentLockDoesNotStartABackoff(t *testing.T) {
	if syncEngine() != "dbos" {
		t.Skip("the table engine requeues the job instead")
	}
	e := setup(t)
	f := e.syncFixture("busy", newSource(t, branches("b1")...))
	e.stopWorker()
	_, _ = e.worker(func(c *config.Config) { c.Sync.FreshMinInterval = time.Millisecond }, engine.SyncHooks{LockTimeout: 200 * time.Millisecond})
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 20 * time.Second })

	g, err := e.app.LoadTypeGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) }) // a failed assertion must not leave the lock held
	if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock($1)`, engine.ComponentKeys(g, []uuid.UUID{uuid.MustParse(f.typ)})[0]); err != nil {
		t.Fatal(err)
	}
	// The first synchronization of the type waits for the lock, gives up, and the request is answered without the data.
	r.permission(t, f)

	var attempts int
	err = e.pool.QueryRow(context.Background(), `SELECT attempts FROM sync_state WHERE type_resource_id = $1`, f.typ).Scan(&attempts)
	if err == nil && attempts > 0 {
		t.Fatalf("a busy lock started a backoff: attempts = %d", attempts)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if status, body, _ := r.permission(t, f); status != http.StatusOK || !strings.Contains(body, `"b1"`) {
		t.Fatalf("after the lock was free: %d %s", status, body)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("the type was synchronized only after %v", took)
	}
}
