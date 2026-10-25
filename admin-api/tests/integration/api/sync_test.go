//go:build integration

package api_test

// Acceptance tests of the synchronization pipeline (issue F3): one executor per job, state in PostgreSQL, no graph writes
// in the check path. The "replicas" are several engine.App values on the same database and Redis, each with its own pools
// and HTTP server, as separate pods would be.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
	"github.com/licetum/licetum/core/server"
	support "github.com/licetum/licetum/core/testsupport"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// source is an HTTP source of resources whose answers the test controls.
type source struct {
	*httptest.Server
	calls atomic.Int32 // requests for the list
	// pointCalls counts the requests for one resource (the admin inline refresh); they answer even while the list fails.
	pointCalls atomic.Int32
	mu         sync.Mutex
	list       []map[string]any
	fail       atomic.Bool
	delay      atomic.Int64 // nanoseconds
}

func newSource(t *testing.T, resources ...map[string]any) *source {
	t.Helper()
	s := &source{list: resources}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /src", func(w http.ResponseWriter, _ *http.Request) {
		s.calls.Add(1)
		if d := time.Duration(s.delay.Load()); d > 0 {
			time.Sleep(d)
		}
		if s.fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(s.list)
	})
	mux.HandleFunc("GET /src/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.pointCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "x"})
	})
	s.Server = newTestServer(t, mux)
	return s
}

func branches(ids ...string) []map[string]any {
	out := make([]map[string]any, len(ids))
	for i, id := range ids {
		out[i] = map[string]any{"id": id, "name": "Branch " + id}
	}
	return out
}

// syncFixture is a service with one synchronized type, readable by alice. The check API knows the service by its slug.
type syncFixture struct {
	service, slug, typ string
	src                *source
	alice              string
}

func (e *env) syncFixture(name string, src *source) *syncFixture {
	e.t.Helper()
	org := e.must("POST", "/service", servicePayload(e, name))["id"].(string)
	typ := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	e.must("POST", "/service/"+org+"/type_resource/"+typ+"/resource/synchronizer", map[string]any{
		"config":            map[string]any{"kind": "http", "url": src.URL + "/src"},
		"probe_resource_id": "b1",
	})
	e.must("POST", "/assignment_rule", map[string]any{"type_resource_id": typ, "access": []string{"read"}, "subject": "alice"})
	// The probe of the synchronizer asked the source once; the tests count the calls of the synchronization.
	src.calls.Store(0)
	return &syncFixture{service: org, slug: name, typ: typ, src: src, alice: e.token(jwt.MapClaims{"preferred_username": "alice", "sub": "alice-sub"})}
}

// replica is another process on the same database: a check API (or a worker) with its own pools. url is its public port;
// ops is its operations listener (the probes and the metrics).
type replica struct {
	env *env
	app *engine.App
	url string
	ops string
}

func (e *env) newApp(role engine.Role, tune func(*config.Config)) *engine.App {
	e.t.Helper()
	cfg := *e.cfg
	if tune != nil {
		tune(&cfg)
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(pool.Close)
	rdb, err := redisx.Connect(ctx, testInfrastructure.RedisURI)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = rdb.Close() })
	app := engine.New(pool, rdb, &cfg)
	app.Role = role
	if role == engine.RoleWorker {
		// The worker applies on its own pool of DB__SYNC_POOL_SIZE connections, as the binary does.
		c := cfg.DB
		c.PoolSize, c.MaxOverflow = max(cfg.DB.SyncPoolSize, 1), 0
		syncPool, err := db.Connect(ctx, c)
		if err != nil {
			e.t.Fatal(err)
		}
		e.t.Cleanup(syncPool.Close)
		app.SyncDB = syncPool
	}
	e.t.Cleanup(app.Close)
	return e.track(app)
}

func (e *env) checkReplica(tune func(*config.Config)) *replica {
	app := e.newApp(engine.RolePDP, tune)
	srv := newTestServer(e.t, harness.Levers(server.NewPDPHandler(app)))
	ops := newTestServer(e.t, server.NewOpsHandler(app))
	return &replica{env: e, app: app, url: srv.URL, ops: ops.URL}
}

// worker starts another worker; the default one of the test is stopped first when replace is set.
func (e *env) worker(tune func(*config.Config), hooks engine.SyncHooks) (*engine.App, func()) {
	app := e.newApp(engine.RoleWorker, tune)
	app.Hooks = hooks
	return app, startWorker(e.t, app)
}

// permission asks the replica, as alice, which branches of the fixture's service she may read (the resource search of branch
// and read), and returns the status, the body and the headers of the answer.
func (r *replica) permission(t *testing.T, f *syncFixture, opts ...authzenreq.Option) (int, string, http.Header) {
	t.Helper()
	search := authzenreq.SearchResource(f.slug, authzenreq.SubjectOf(f.alice), "branch", "read", opts...)
	resp, err := http.DefaultClient.Do(r.env.pdpRequest(r.url, search, f.alice))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

// jobCount counts the DBOS SyncType workflows of a type in a state (done, queued, running).
func (e *env) jobCount(typ string, state string) int {
	e.t.Helper()
	var n int
	status := map[string]string{"done": "SUCCESS", "queued": "ENQUEUED", "running": "PENDING"}[state]
	q := fmt.Sprintf(`SELECT count(*) FROM %s.workflow_status WHERE name = 'SyncType' AND status = $1 AND attributes->>'type' = $2`,
		e.cfg.Sync.DBOSSchema)
	if err := e.pool.QueryRow(context.Background(), q, status, typ).Scan(&n); err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return 0
		}
		e.t.Fatal(err)
	}
	return n
}

func (e *env) makeDue(typ string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE sync_state SET next_due_at = now() WHERE type_resource_id = $1`, typ); err != nil {
		e.t.Fatal(err)
	}
}

// burst sends n requests at once, spread over the replicas.
func burst(t *testing.T, replicas []*replica, f *syncFixture, n int, opts ...authzenreq.Option) []string {
	t.Helper()
	out := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			status, body, _ := replicas[i%len(replicas)].permission(t, f, opts...)
			if status != http.StatusOK {
				t.Errorf("permission: %d %s", status, body)
			}
			out[i] = body
		})
	}
	wg.Wait()
	return out
}

func replicas(e *env, n int, tune func(*config.Config)) []*replica {
	out := make([]*replica, n)
	for i := range out {
		out[i] = e.checkReplica(tune)
	}
	return out
}

// F3-1: 5 replicas, 100 concurrent requests for a type that was never synchronized: one source call and one job.
func TestSyncOneSourceCallForAConcurrentFirstRequest(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2", "b3")...)
	src.delay.Store(int64(150 * time.Millisecond))
	f := e.syncFixture("first", src)

	for _, body := range burst(t, replicas(e, 5, nil), f, 100) {
		if !strings.Contains(body, `"b3"`) {
			t.Fatalf("an answer lacks the resources: %s", body)
		}
	}
	if got := src.calls.Load(); got != 1 {
		t.Fatalf("the source was asked %d times, want 1", got)
	}
	if got := e.jobCount(f.typ, "done"); got != 1 {
		t.Fatalf("%d jobs ran, want 1", got)
	}
}

// F3-2: concurrent consistency=higher requests share the source call, and the calls are spaced out.
func TestSyncFreshRequestsShareTheSourceCall(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("fresh", src)
	e.stopWorker()
	_, _ = e.worker(func(c *config.Config) { c.Sync.FreshMinInterval = time.Second }, engine.SyncHooks{})
	rs := replicas(e, 5, nil)
	burst(t, rs, f, 1)
	src.calls.Store(0)

	src.delay.Store(int64(100 * time.Millisecond))
	started := time.Now()
	burst(t, rs, f, 50, authzenreq.Fresh())
	elapsed := time.Since(started)
	limit := int32(1 + (elapsed / time.Second) + 1)
	if got := src.calls.Load(); got > limit {
		t.Fatalf("the source was asked %d times in %v, want at most %d (one per second)", got, elapsed, limit)
	}
	if got := src.calls.Load(); got == 0 {
		t.Fatal("a fresh request did not ask the source")
	}
}

// A computation that waits for a synchronization job gives its slot of COMPUTE__CONCURRENCY back for the wait: concurrent
// consistency=higher requests of different people share the fresh job instead of queueing one behind the other, each for
// a job of its own SYNC__FRESH_MIN_INTERVAL after the last one (the cold load test collapsed at COMPUTE__CONCURRENCY / 1 s).
func TestSyncWaitingForTheWorkerHoldsNoComputeSlot(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("slots", src)
	e.stopWorker()
	_, _ = e.worker(func(c *config.Config) { c.Sync.FreshMinInterval = time.Second }, engine.SyncHooks{})
	r := e.checkReplica(func(c *config.Config) { c.Compute.Concurrency = 1 })
	if status, body, _ := r.permission(t, f); status != http.StatusOK || !strings.Contains(body, `"b2"`) {
		t.Fatalf("first synchronization: %d %s", status, body)
	}

	const people = 8
	tokens := make([]string, people)
	for i := range tokens {
		name := fmt.Sprintf("person-%d", i)
		e.must("POST", "/assignment_rule", map[string]any{"type_resource_id": f.typ, "access": []string{"read"}, "subject": name})
		tokens[i] = e.token(jwt.MapClaims{"preferred_username": name, "sub": name + "-sub"})
	}
	started := time.Now()
	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Go(func() {
			status, body, _ := r.permission(t, &syncFixture{service: f.service, slug: f.slug, alice: token}, authzenreq.Fresh())
			if status != http.StatusOK || !strings.Contains(body, `"b2"`) {
				t.Errorf("fresh permission: %d %s", status, body)
			}
		})
	}
	wg.Wait()
	// One slot held through each wait would put the requests one fresh job (a second) apart: 7 s and more.
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("%d concurrent fresh requests with one compute slot took %v: they waited for the worker one at a time", people, elapsed)
	}
}

// Concurrent requests that ask for jobs which are already queued do not wedge the DBOS client of the process. A DBOS enqueue
// holds a connection of the client's pool in its transaction and, when the job is a duplicate, takes a second one to find the
// job that holds the deduplication id, with no deadline, while the listener of the client keeps one connection for good: as
// many concurrent duplicate enqueues as the pool had connections left each held one and waited for another forever (TestF2_2
// hung so for 39 minutes, 20 concurrent admin writes enqueueing the six platform types each). The concurrent requests for one
// job share one enqueue, and the process runs at most (pool - 1) / 2 enqueues of different jobs at once (engine.enqueueSlot).
func TestSyncConcurrentDuplicateEnqueuesDoNotExhaustTheDBOSPool(t *testing.T) {
	e := setup(t)
	const types = 4
	fixtures := make([]*syncFixture, types)
	typeIDs := make([]uuid.UUID, types)
	for i := range fixtures {
		// The admin requests of the first one made the worker create the DBOS tables.
		fixtures[i] = e.syncFixture(fmt.Sprintf("enqueues-%d", i), newSource(t, branches("b1")...))
		typeIDs[i] = uuid.MustParse(fixtures[i].typ)
	}
	e.stopWorker()                                                          // the jobs stay queued: every later enqueue is a duplicate
	r := e.checkReplica(func(c *config.Config) { c.Sync.DBOSPoolSize = 3 }) // the listener's connection and two more
	// The first requests create the DBOS client of the process (the requests meanwhile answer without it) and queue the jobs.
	deadline := time.Now().Add(10 * time.Second)
	for _, f := range fixtures {
		for e.jobCount(f.typ, "queued") == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("the job of the type %s was not queued", f.typ)
			}
			if _, err := r.app.Coordinator().Ensure(context.Background(), typeIDs, engine.SyncOptions{}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := range 16 {
			wg.Go(func() {
				// No wait: the request only asks for the job (and answers from the data that exists).
				if _, err := r.app.Coordinator().Ensure(context.Background(), typeIDs[i%types:i%types+1], engine.SyncOptions{}); err != nil {
					t.Errorf("ensure: %v", err)
				}
			})
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("16 concurrent requests for %d queued jobs did not get through the enqueue in 20 s: the DBOS pool of 3 is wedged", types)
	}
	for _, f := range fixtures {
		if got := e.jobCount(f.typ, "queued"); got != 1 {
			t.Fatalf("%d queued jobs for the type %s, want the one they share", got, f.typ)
		}
	}
}

// F3-3: a source that fails is asked once per backoff window by the whole cluster, and the answers come from the data
// that exists.
func TestSyncFailingSourceIsAskedOncePerBackoff(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("failing", src)
	rs := replicas(e, 4, nil)
	burst(t, rs, f, 4) // synchronized

	src.fail.Store(true)
	src.calls.Store(0)
	e.makeDue(f.typ)
	for _, body := range burst(t, rs, f, 60) {
		if !strings.Contains(body, `"b2"`) {
			t.Fatalf("an answer does not come from the existing data: %s", body)
		}
	}
	// Let the refresh job run, then ask again many times inside the backoff window.
	time.Sleep(500 * time.Millisecond)
	burst(t, rs, f, 60)
	time.Sleep(300 * time.Millisecond)
	if got := src.calls.Load(); got != 1 {
		t.Fatalf("the failing source was asked %d times inside one backoff window, want 1", got)
	}
	var attempts int
	var lastError string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT attempts, COALESCE(last_error, '') FROM sync_state WHERE type_resource_id = $1`, f.typ).Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastError == "" {
		t.Fatalf("the failure is not recorded: attempts=%d error=%q", attempts, lastError)
	}
}

// The backoff of a source lives in sync_state, so it holds back the admin's inline refresh of one resource too: while the
// type is in backoff the cached value is returned and the source is not asked; outside it the source is asked.
func TestSyncAdminResolveDoesNotAskASourceInBackoff(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("resolve", src)
	rs := replicas(e, 1, nil)
	burst(t, rs, f, 2) // synchronized
	url := e.admin.URL + "/service/" + f.service + "/type_resource/" + f.typ + "/resource?resource_id=b2"

	src.pointCalls.Store(0)
	if status, body := e.do(http.MethodGet, url, nil); status != http.StatusOK {
		t.Fatalf("resolve outside the backoff: %d %s", status, body)
	}
	if got := src.pointCalls.Load(); got != 1 {
		t.Fatalf("outside the backoff the source was asked %d times for the resource, want 1", got)
	}

	src.fail.Store(true)
	e.makeDue(f.typ)
	burst(t, rs, f, 4)
	waitFor(t, "the type to be in backoff", func() bool {
		var attempts int
		_ = e.pool.QueryRow(context.Background(), `SELECT attempts FROM sync_state WHERE type_resource_id = $1`, f.typ).Scan(&attempts)
		return attempts > 0
	})

	src.pointCalls.Store(0)
	status, body := e.do(http.MethodGet, url, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"b2"`) {
		t.Fatalf("resolve in the backoff does not answer from the cache: %d %s", status, body)
	}
	if got := src.pointCalls.Load(); got != 0 {
		t.Fatalf("in the backoff the source was asked %d times for the resource, want none", got)
	}
}

// F3-11: a request for a type that was never synchronized, with no worker, answers within the wait, marked pending, with
// no resources of that type. F3-15: with the workers stopped the check API keeps answering from the existing data and the
// queue age grows.
func TestSyncWithoutWorkersTheCheckKeepsAnswering(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("noworker", src)
	e.stopWorker()
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 400 * time.Millisecond })
	r.permission(t, &syncFixture{service: f.service, slug: f.slug, alice: f.alice}) // warm the token cache

	// consistency=higher skips the result cache, so the pending answer is computed (and marked) by this very request.
	started := time.Now()
	status, body, header := r.permission(t, f, authzenreq.Fresh())
	if took := time.Since(started); status != http.StatusOK || took > 400*time.Millisecond+250*time.Millisecond {
		t.Fatalf("status %d after %v: %s", status, took, body)
	}
	if header.Get("X-Authz-Sync") != "pending" {
		t.Fatalf("no X-Authz-Sync: pending header: %v", header)
	}
	if strings.Contains(body, `"b1"`) {
		t.Fatalf("a type without data contributes resources: %s", body)
	}
	if src.calls.Load() != 0 {
		t.Fatal("a check process asked the source")
	}

	// A worker comes, the data arrives; then the workers stop again and the type becomes due.
	stop := func() func() {
		_, stop := e.worker(nil, engine.SyncHooks{})
		return stop
	}()
	if _, body, _ := r.permission(t, f, authzenreq.Fresh()); !strings.Contains(body, `"b2"`) {
		t.Fatalf("the worker did not synchronize: %s", body)
	}
	stop()
	e.makeDue(f.typ)
	for range 3 {
		if _, body, _ := r.permission(t, f); !strings.Contains(body, `"b2"`) {
			t.Fatalf("an answer does not come from the existing data: %s", body)
		}
		time.Sleep(1100 * time.Millisecond)
	}
	_, metrics := e.do(http.MethodGet, r.ops+"/metrics", nil)
	age := metricValue(string(metrics), "licetum_sync_queue_age_seconds")
	if age < 1 {
		t.Fatalf("licetum_sync_queue_age_seconds = %v, want it to grow while no worker runs\n%s", age, metrics)
	}
}

func metricValue(text, name string) float64 {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, name+" ") {
			var v float64
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, name+" "), "%g", &v)
			return v
		}
	}
	return -1
}

// F3-5: an executor that outlives its lease is fenced: another executor takes the job, and the late one applies nothing.
// F3-4: the job ends done exactly once, and the closure is consistent.
func TestSyncLateExecutorIsFenced(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2", "b3")...)
	f := e.syncFixture("fenced", src)
	e.stopWorker()

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	isFixture := func(ids []uuid.UUID) bool { return slices.Contains(ids, uuid.MustParse(f.typ)) }
	// One executor: a paused executor's own siblings would take its expired job over as well.
	late, _ := e.worker(func(c *config.Config) { c.Sync.Lease, c.Sync.Concurrency = 600*time.Millisecond, 1 }, engine.SyncHooks{
		// The DBOS executor asks for its heartbeat with nil; the heartbeat stops for the whole executor.
		NoHeartbeat: func(ids []uuid.UUID) bool { return ids == nil },
		AfterFetch: func(ctx context.Context, ids []uuid.UUID) {
			if !isFixture(ids) {
				return // only the job of the fixture is held
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		},
	})
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 200 * time.Millisecond })
	r.permission(t, f) // asks for the job; times out (pending) while the late executor holds it
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first executor never took the job")
	}

	// A second worker takes the job over once the lease has run out.
	_, _ = e.worker(func(c *config.Config) { c.Sync.Lease = 600 * time.Millisecond }, engine.SyncHooks{})
	deadline := time.Now().Add(5 * time.Second)
	for e.jobCount(f.typ, "done") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the job was not taken over after the lease ran out")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var resources int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM resource_cache WHERE type_resource_id = $1`, f.typ).Scan(&resources); err != nil {
		t.Fatal(err)
	}
	if resources != 3 {
		t.Fatalf("%d resources after the takeover, want 3", resources)
	}
	e.requireClosureConsistent()

	// The late executor wakes up: its transaction must not apply anything.
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM resource_cache WHERE type_resource_id = $1 AND external_id = 'b3'`, f.typ); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(time.Second)
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM resource_cache WHERE type_resource_id = $1`, f.typ).Scan(&resources); err != nil {
		t.Fatal(err)
	}
	if resources != 2 {
		t.Fatalf("the late executor applied its result again (%d resources)", resources)
	}
	if got := metricValue(scrape(late), "licetum_sync_lease_lost_total"); got != 1 {
		t.Fatalf("licetum_sync_lease_lost_total = %v, want 1", got)
	}
	if n := e.jobCount(f.typ, "done"); n != 1 {
		t.Fatalf("the job ended %d times", n)
	}
}

func scrape(app *engine.App) string {
	rec := httptest.NewRecorder()
	app.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

// requireClosureConsistent checks that every row of resource_ancestor is derivable from resource_edge and the other way
// round: no partial type, no stale ancestor.
func (e *env) requireClosureConsistent() {
	e.t.Helper()
	var missing, extra int
	err := e.pool.QueryRow(context.Background(), `
		WITH RECURSIVE r(d, a) AS (
			SELECT id, id FROM resource_cache WHERE active
			UNION
			SELECT r.d, ed.parent_id FROM r JOIN resource_edge ed ON ed.child_id = r.a WHERE ed.parent_id IS NOT NULL
		)
		SELECT (SELECT count(*) FROM (SELECT d, a FROM r EXCEPT SELECT descendant_id, ancestor_id FROM resource_ancestor) x),
		       (SELECT count(*) FROM (SELECT descendant_id, ancestor_id FROM resource_ancestor EXCEPT SELECT d, a FROM r) y)`).Scan(&missing, &extra)
	if err != nil {
		e.t.Fatal(err)
	}
	if missing != 0 || extra != 0 {
		e.t.Fatalf("the closure is not derivable from the edges: %d missing, %d extra", missing, extra)
	}
}

// F3-6: a synchronization of one component does not wait for the lock of an unrelated one.
func TestSyncComponentsDoNotBlockEachOther(t *testing.T) {
	e := setup(t)
	slow := e.syncFixture("slow", newSource(t, branches("b1")...))
	quick := e.syncFixture("quick", newSource(t, branches("q1")...))
	g, err := e.app.LoadTypeGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	slowKey := engine.ComponentKeys(g, []uuid.UUID{uuid.MustParse(slow.typ)})[0]

	// Somebody holds the lock of the slow type's component.
	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock($1)`, slowKey); err != nil {
		t.Fatal(err)
	}
	r := e.checkReplica(func(c *config.Config) { c.Sync.RequestWait = 10 * time.Second })
	var wg sync.WaitGroup
	wg.Go(func() { r.permission(t, slow) })
	time.Sleep(300 * time.Millisecond)

	started := time.Now()
	if _, body, _ := r.permission(t, quick); !strings.Contains(body, `"q1"`) {
		t.Fatalf("the unrelated type was not synchronized: %s", body)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("an unrelated synchronization waited %v for the lock", took)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if e.jobCount(slow.typ, "done") != 1 {
		t.Fatal("the held synchronization did not finish after the lock was released")
	}
}

// F3-8: the check process runs under a database role that cannot write the resource graph or the sync state.
func TestSyncPDPRoleCannotWriteTheGraph(t *testing.T) {
	if os.Getenv(support.EnvPgBouncerAddr) != "" {
		// The restricted role is session state (SET ROLE on every connection), which transaction pooling hands from one
		// client to the next: the owner's statements would run as the restricted role.
		t.Skip("SET ROLE is not carried by PgBouncer in transaction mode")
	}
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("readonly", src)
	ctx := context.Background()

	role := fmt.Sprintf("check_%d", time.Now().UnixNano())
	var schema string
	if err := e.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE ROLE ` + role,
		`GRANT USAGE ON SCHEMA ` + schema + `, public TO ` + role,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ` + schema + ` TO ` + role,
		`GRANT USAGE ON ALL SEQUENCES IN SCHEMA ` + schema + ` TO ` + role,
		`REVOKE INSERT, UPDATE, DELETE ON resource_cache, resource_edge, resource_ancestor, sync_state FROM ` + role,
	} {
		if _, err := e.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{`DROP OWNED BY ` + role, `DROP ROLE ` + role} {
			_, _ = e.pool.Exec(context.Background(), stmt)
		}
	})

	restricted := e.restrictedApp(role)
	srv := newTestServer(t, harness.Levers(server.NewPDPHandler(restricted)))
	ops := newTestServer(t, server.NewOpsHandler(restricted))
	r := &replica{env: e, app: restricted, url: srv.URL, ops: ops.URL}

	// The first request asks for the synchronization; the worker (the owner) applies it; the restricted role only reads.
	if status, body, _ := r.permission(t, f); status != http.StatusOK || !strings.Contains(body, `"b2"`) {
		t.Fatalf("%d %s", status, body)
	}
	e.makeDue(f.typ)
	if status, body, _ := r.permission(t, f); status != http.StatusOK || !strings.Contains(body, `"b2"`) {
		t.Fatalf("%d %s", status, body)
	}
	if _, err := restricted.DB.Exec(ctx, `DELETE FROM resource_cache`); err == nil {
		t.Fatal("the pdp role can delete from resource_cache")
	}
}

// restrictedApp is a check process whose connections run as the given database role.
func (e *env) restrictedApp(role string) *engine.App {
	e.t.Helper()
	cfg := *e.cfg
	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(db.NormalizeDSN(cfg.DB.URI))
	if err != nil {
		e.t.Fatal(err)
	}
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE `+role)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(pool.Close)
	rdb, err := redisx.Connect(ctx, testInfrastructure.RedisURI)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = rdb.Close() })
	app := engine.New(pool, rdb, &cfg)
	app.Role = engine.RolePDP
	e.t.Cleanup(app.Close)
	return e.track(app)
}

// F3-9: a write in the admin API does not make the worker ask the sources of other services.
func TestSyncAdminWriteDoesNotAskOtherSources(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("other", src)
	r := e.checkReplica(nil)
	r.permission(t, f)
	src.calls.Store(0)

	for i := range 5 {
		e.must("POST", "/service", servicePayload(e, fmt.Sprintf("writer-%d", i)))
		r.permission(t, f)
	}
	time.Sleep(time.Second)
	if got := src.calls.Load(); got != 0 {
		t.Fatalf("admin writes made the worker ask an unrelated source %d times", got)
	}
}

// F3-10: running the same synchronization again changes nothing and does not touch the data revision.
func TestSyncRepeatedSynchronizationChangesNothing(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2", "b3")...)
	f := e.syncFixture("idempotent", src)
	r := e.checkReplica(nil)
	r.permission(t, f)

	snapshot := func() string {
		var s string
		err := e.pool.QueryRow(context.Background(), `
			SELECT (SELECT md5(string_agg(id::text || external_id || name || meta::text || active::text || synced_at::text, ',' ORDER BY id)) FROM resource_cache)
			    || (SELECT md5(coalesce(string_agg(child_id::text || parent_external_id, ',' ORDER BY child_id, parent_external_id), '')) FROM resource_edge)
			    || (SELECT md5(coalesce(string_agg(ancestor_id::text || descendant_id::text, ',' ORDER BY ancestor_id, descendant_id), '')) FROM resource_ancestor)`).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	versions := func() string {
		rows, err := e.pool.Query(context.Background(), `SELECT scope || '=' || revision FROM revision_state WHERE scope LIKE 'data:%' ORDER BY scope`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var all []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			all = append(all, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(all, ",")
	}
	before, beforeVersions := snapshot(), versions()
	for range 3 {
		e.makeDue(f.typ)
		r.permission(t, f, authzenreq.Fresh())
	}
	if got := snapshot(); got != before {
		t.Fatal("the database changed after repeated synchronizations of an unchanged source")
	}
	if got := versions(); got != beforeVersions {
		t.Fatalf("the data revision changed: %s -> %s", beforeVersions, got)
	}
}

// F3-13: a Redis flush starts no synchronization and asks no source.
func TestSyncRedisFlushStartsNothing(t *testing.T) {
	e := setup(t)
	src := newSource(t, branches("b1", "b2")...)
	f := e.syncFixture("flush", src)
	r := e.checkReplica(nil)
	r.permission(t, f)
	jobs, calls := e.jobCount(f.typ, "done"), src.calls.Load()

	if err := e.app.Redis.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, body, _ := r.permission(t, f); !strings.Contains(body, `"b2"`) {
			t.Fatalf("%s", body)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if got := e.jobCount(f.typ, "done"); got != jobs {
		t.Fatalf("a Redis flush started %d synchronizations", got-jobs)
	}
	if got := src.calls.Load(); got != calls {
		t.Fatalf("a Redis flush made %d source calls", got-calls)
	}
}

// A body that still carries cache_ttl (the list of a source is no longer kept) is accepted, the field is dropped, and
// neither the answer, the list of the type's synchronizers nor the stored configuration has it.
func TestSynchronizerBodyWithCacheTTLIsAcceptedAndIgnored(t *testing.T) {
	e := setup(t)
	src := newSource(t)
	org := e.must("POST", "/service", servicePayload(e, "ttl-org"))["id"].(string)
	typ := e.must("POST", "/service/"+org+"/type_resource", map[string]any{"name": "branch"})["id"].(string)
	path := "/service/" + org + "/type_resource/" + typ + "/resource/synchronizer"
	created := e.must("POST", path, map[string]any{
		"config":            map[string]any{"kind": "http", "url": src.URL + "/src", "cache_ttl": 30},
		"probe_resource_id": "b1",
	})
	if raw, _ := json.Marshal(created); strings.Contains(string(raw), "cache_ttl") {
		t.Fatalf("the answer of the create shows cache_ttl: %s", raw)
	}
	status, raw := e.do("GET", e.admin.URL+path, nil)
	if status != http.StatusOK || strings.Contains(string(raw), "cache_ttl") {
		t.Fatalf("GET %s: %d %s", path, status, raw)
	}
	var stored bool
	if err := e.pool.QueryRow(context.Background(),
		`SELECT config ? 'cache_ttl' FROM synchronizer WHERE type_resource_id = $1`, typ).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored {
		t.Fatal("cache_ttl was stored")
	}
}
