// Package harness builds the multi-replica environments of the integration tests (tier M of the production-load
// plan): several check API replicas, each with its own database pool and Redis client, on one shared PostgreSQL and
// one shared Redis, together with an admin API to set the policy up, a fake identity provider and a fake source of
// resources. The call counters of the fakes and the per-request counters of the replicas (engine.Trace) are what the
// tests assert against; with WithFaultProxies the replicas reach PostgreSQL and Redis through Toxiproxy, so a test can
// slow, cut or black-hole either of them.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/admin-api/api"
	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/cli"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
	"github.com/licetum/licetum/core/server"
	support "github.com/licetum/licetum/core/testsupport"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// Cluster is one environment: the shared state, the fakes, the admin API and the check replicas.
type Cluster struct {
	T      testing.TB
	Infra  *support.Infrastructure
	IdP    *support.IdP
	Source *support.Source
	// Cfg is the configuration of the admin API (it reaches PostgreSQL and Redis directly).
	Cfg *config.Config
	// Pool and Redis are the test's own direct connections, for fixtures and for looking at the state.
	Pool  *pgxpool.Pool
	Redis *redis.Client
	// Admin is the admin API in process (AdminServer serves it); Replicas are the check API replicas.
	Admin       *service.App
	AdminServer *httptest.Server
	AdminToken  string
	Replicas    []*Replica
	// PostgresFault and RedisFault are the proxies the replicas connect through (nil without WithFaultProxies).
	PostgresFault, RedisFault *support.FaultProxy
	// ReplicaFault is the proxy in front of the read replica (WithReadReplica together with WithFaultProxies).
	ReplicaFault *support.FaultProxy
	// ReplicaPool is the test's own direct connection to the read replica (WithReadReplica), for pausing the replay and for
	// looking at its state.
	ReplicaPool *pgxpool.Pool

	// LastRevision is the revision (X-Authz-Revision) of the last write made through AdminDo or Must. The replicas
	// follow the revision with a delay of up to REVISION__POLL_INTERVAL; Replica.Permission asks for at least this one, so
	// that it sees the writes of the test, as a client that passes at_least_as_fresh does (authzenreq.AtLeast).
	LastRevision atomic.Int64

	proxied      bool
	replicaRedis string
	// replicaDBURI and replicaRedisURI are what the replicas connect to (behind the proxies when there are any): AddReplica
	// starts another one the same way.
	replicaDBURI, replicaRedisURI string
	hotRedis                      time.Duration
	// withReplica makes the check replicas read the policy from the streaming replica (see WithReadReplica); replicaReadURI is
	// what they connect to (behind ReplicaFault when there are proxies).
	withReplica    bool
	replicaReadURI string
	client         *http.Client
	configure      []func(*config.Config)
}

// Replica is one check API instance: its own App, database pool, Redis client and HTTP server.
type Replica struct {
	Index  int
	App    *engine.App
	Pool   *pgxpool.Pool
	Redis  *redis.Client
	Server *httptest.Server

	traces sync.Map // request id -> *engine.Trace
	seq    atomic.Int64
}

// Option changes how StartReplicas builds the cluster.
type Option func(*Cluster)

// WithFaultProxies makes the replicas reach PostgreSQL and Redis through Toxiproxy (Cluster.PostgresFault and
// Cluster.RedisFault). The admin API and the test's own connections stay direct.
func WithFaultProxies() Option { return func(c *Cluster) { c.proxied = true } }

// EnvReplicaAddr names the host:port of a streaming replica of the PostgreSQL of the infrastructure (scripts/pg-replica.sh
// starts one). Without it the tests that need a read replica are skipped.
const EnvReplicaAddr = "LICETUM_TEST_REPLICA_ADDR"

// WithReadReplica makes the check replicas read the policy from a real streaming replica of the infrastructure's PostgreSQL
// (App.ReadDB), the way a region does; the admin API, the worker and the test's own Pool use the primary. The test is skipped
// when there is no replica (EnvReplicaAddr) or the infrastructure goes through PgBouncer. Cluster.PauseReplay and ResumeReplay
// make the replica lag.
func WithReadReplica() Option { return func(c *Cluster) { c.withReplica = true } }

// WithReplicaRedis makes the replicas use the Redis at uri instead of the one of the infrastructure (the admin API keeps
// the infrastructure's). It is how a test gives the replicas a Redis that is read-only, or a different one.
func WithReplicaRedis(uri string) Option { return func(c *Cluster) { c.replicaRedis = uri } }

// WithConfig changes the configuration of the cluster (the admin API and the replicas) before they start.
func WithConfig(f func(*config.Config)) Option {
	return func(c *Cluster) { c.configure = append(c.configure, f) }
}

// WithHotRedis gives the replicas the Redis client of the check API in production: commands time out after timeout, are
// not retried, and Redis is skipped for a while after a few failures in a row (redisx.Options).
func WithHotRedis(timeout time.Duration) Option { return func(c *Cluster) { c.hotRedis = timeout } }

// StartReplicas starts n check API replicas on the PostgreSQL and Redis of infra, with an admin API next to them.
// Each call gets its own database schema and clears Redis, so the tests of a package must not run in parallel.
func StartReplicas(t testing.TB, infra *support.Infrastructure, n int, opts ...Option) *Cluster {
	t.Helper()
	ctx := context.Background()
	c := &Cluster{T: t, Infra: infra, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
	for _, o := range opts {
		o(c)
	}
	c.IdP = support.NewIdP(t)
	c.Source = support.NewSource(t)

	// An isolated schema per cluster (a database of its own behind PgBouncer, see Infrastructure.Isolate).
	iso := infra.Isolate(ctx, t)
	dbURI := iso.URI

	c.Cfg = &config.Config{
		DB: config.Database{URI: dbURI, PoolSize: 5, PgBouncer: iso.PgBouncer, MigrateURI: iso.MigrateURI},
		// The DBOS tables of the cluster live in a schema of their own, dropped with it.
		Sync:     config.Sync{DBOSSchema: "dbos_" + iso.Name},
		Auth:     config.Auth{StateSecret: "test-secret", StateTTL: time.Minute},
		Subjects: config.Subjects{Enabled: true, RetentionDays: 90},
		Platform: config.Platform{
			Name: "Licetum", URL: c.IdP.AppURL(), Icon: c.IdP.IconURL(),
			AuthorizationURL: c.IdP.AuthorizationURL(), TokenURL: c.IdP.TokenURL(), JwksURL: c.IdP.JWKSURL(),
			Issuer: c.IdP.Issuer(), ClientID: "licetum", ClientSecret: "s",
			AdminGroupName: "Platform administrator", AdminClaim: "preferred_username", AdminClaimValues: []string{"admin"},
		},
	}
	for _, f := range c.configure {
		f(c.Cfg)
	}
	// The migrations connect as `licetum migrate` does: directly to PostgreSQL where the application goes through a pooler.
	if err := db.RunMigrate(ctx, c.Cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var err error
	c.Pool, err = db.Connect(ctx, c.Cfg.DB)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(c.Pool.Close)
	c.Redis, err = redisx.Connect(ctx, infra.RedisURI)
	if err != nil {
		t.Fatalf("connect to Redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Redis.Close() })
	if err := c.Redis.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush Redis: %v", err)
	}

	// The admin API writes through its own App; it reaches both servers directly.
	adminPool, err := db.Connect(ctx, c.Cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	adminRedis, err := redisx.Connect(ctx, infra.RedisURI)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminRedis.Close() })
	c.Admin = service.New(engine.New(adminPool, adminRedis, c.Cfg))
	t.Cleanup(c.Admin.Close)
	c.startWorker()
	c.AdminServer = serve(t, api.NewAdminHandler(c.Admin))
	c.AdminToken = c.IdP.Token(jwt.MapClaims{"preferred_username": "admin", "sub": "admin-sub"})

	replicaDB, replicaRedis := c.Cfg.DB.URI, infra.RedisURI
	if c.proxied {
		pgURL, err := url.Parse(dbURI)
		if err != nil {
			t.Fatal(err)
		}
		redisURL, err := url.Parse(infra.RedisURI)
		if err != nil {
			t.Fatal(err)
		}
		c.PostgresFault = support.NewFaultProxy(t, "postgres", pgURL.Host)
		c.RedisFault = support.NewFaultProxy(t, "redis", redisURL.Host)
		replicaDB, replicaRedis = c.PostgresFault.Rewrite(dbURI), c.RedisFault.Rewrite(infra.RedisURI)
	}
	if c.withReplica {
		c.startReadReplica(dbURI)
	}
	if c.replicaRedis != "" {
		replicaRedis = c.replicaRedis
		if c.proxied {
			t.Fatal("WithReplicaRedis and WithFaultProxies cannot be combined: the proxy would not be in front of the replicas' Redis")
		}
	}
	c.replicaDBURI, c.replicaRedisURI = replicaDB, replicaRedis
	for i := 0; i < n; i++ {
		c.Replicas = append(c.Replicas, c.startReplica(i, replicaDB, replicaRedis))
	}
	return c
}

// AddReplica starts one more check API replica, as a pod that replaces another one in a rolling update does.
func (c *Cluster) AddReplica() *Replica {
	c.T.Helper()
	r := c.startReplica(len(c.Replicas), c.replicaDBURI, c.replicaRedisURI)
	c.Replicas = append(c.Replicas, r)
	return r
}

// Stop ends the replica as the process does on SIGTERM (cli.StopServing): it says it is not ready, serves for the drain,
// stops listening and waits for the requests in progress.
func (r *Replica) Stop(drain, timeout time.Duration) error {
	return cli.StopServing(r.Server.Config, r.App, config.Shutdown{Drain: drain, Timeout: timeout}, nil)
}

// startWorker runs the synchronization worker of the cluster: the check and admin processes only ask for synchronizations.
func (c *Cluster) startWorker() {
	t := c.T
	ctx := context.Background()
	pool, err := db.Connect(ctx, c.Cfg.DB)
	if err != nil {
		t.Fatalf("worker: connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	rdb, err := redisx.Connect(ctx, c.Infra.RedisURI)
	if err != nil {
		t.Fatalf("worker: connect to Redis: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	app := engine.New(pool, rdb, c.Cfg)
	app.Role = engine.RoleWorker
	t.Cleanup(app.Close)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = app.RunWorker(runCtx)
	}()
	t.Cleanup(func() { stop(); <-done })
}

func (c *Cluster) startReplica(index int, dbURI, redisURI string) *Replica {
	t := c.T
	ctx := context.Background()
	cfg := *c.Cfg
	cfg.DB.URI = dbURI
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		t.Fatalf("replica %d: connect to PostgreSQL: %v", index, err)
	}
	t.Cleanup(pool.Close)
	rdb, err := redisx.ConnectWith(ctx, redisURI, redisx.Options{HotTimeout: c.hotRedis})
	if err != nil {
		t.Fatalf("replica %d: connect to Redis: %v", index, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	app := engine.New(pool, rdb, &cfg)
	t.Cleanup(app.Close)
	if c.replicaReadURI != "" {
		readCfg := cfg.DB
		readCfg.URI = c.replicaReadURI
		readPool, err := db.Connect(ctx, readCfg)
		if err != nil {
			t.Fatalf("replica %d: connect to the read replica: %v", index, err)
		}
		t.Cleanup(readPool.Close)
		app.UseReadReplica(readPool)
	}
	// As in the check process: the replica follows the revision of the policy by itself, whether requests come or not.
	watchCtx, stopWatching := context.WithCancel(ctx)
	go app.WatchRevision(watchCtx)
	go app.WatchReplica(watchCtx)
	t.Cleanup(stopWatching)

	r := &Replica{Index: index, App: app, Pool: pool, Redis: rdb}
	// Levers: a test sets the freshness of a request with a header (Higher), not with a parameter of the API.
	handler := Levers(server.NewCheckHandler(app))
	// The Trace of a request is created here, so the test can read what the request cost; the server keeps it.
	r.Server = serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, trace := engine.WithTrace(req.Context())
		if id := req.Header.Get("X-Request-ID"); id != "" {
			r.traces.Store(id, trace)
		}
		handler.ServeHTTP(w, req.WithContext(ctx))
	}))
	return r
}

func serve(t testing.TB, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := httptest.NewUnstartedServer(h)
	s.Listener = ln
	s.Start()
	t.Cleanup(s.Close)
	return s
}

// Revision is the revision of the rules in the database, as the replicas will read it.
func (c *Cluster) Revision() int64 {
	c.T.Helper()
	rev, err := db.LoadRevisions(context.Background(), c.Pool)
	if err != nil {
		c.T.Fatalf("read the revisions: %v", err)
	}
	return rev.Rules
}

// DataRevision is the revision of the resource data of a service in the database.
func (c *Cluster) DataRevision(serviceID string) int64 {
	c.T.Helper()
	rev, err := db.LoadRevisions(context.Background(), c.Pool)
	if err != nil {
		c.T.Fatalf("read the revisions: %v", err)
	}
	return rev.Data[serviceID]
}

// Token signs a token of the fake identity provider.
func (c *Cluster) Token(claims jwt.MapClaims) string { return c.IdP.Token(claims) }

// UserToken is the token of a user with the given preferred_username.
func (c *Cluster) UserToken(name string) string {
	return c.IdP.Token(jwt.MapClaims{"preferred_username": name, "sub": name + "-sub"})
}

// AdminDo sends a request to the admin API as the platform administrator and returns the status and the body.
func (c *Cluster) AdminDo(method, path string, body any) (int, []byte) {
	c.T.Helper()
	status, raw, header := c.do(method, c.AdminServer.URL+path, c.AdminToken, body, "", nil)
	if rev, err := strconv.ParseInt(header.Get("X-Authz-Revision"), 10, 64); err == nil && rev > c.LastRevision.Load() {
		c.LastRevision.Store(rev)
	}
	return status, raw
}

// AdminWrite is AdminDo that also returns the revision the write took (X-Authz-Revision; 0 when the request wrote nothing).
func (c *Cluster) AdminWrite(method, path string, body any) (status int, raw []byte, revision int64) {
	c.T.Helper()
	status, raw, header := c.do(method, c.AdminServer.URL+path, c.AdminToken, body, "", nil)
	revision, _ = strconv.ParseInt(header.Get("X-Authz-Revision"), 10, 64)
	if revision > c.LastRevision.Load() {
		c.LastRevision.Store(revision)
	}
	return status, raw, revision
}

// Must is AdminDo for a request that must succeed (200); it returns the JSON object of the answer.
func (c *Cluster) Must(method, path string, body any) map[string]any {
	c.T.Helper()
	status, raw := c.AdminDo(method, path, body)
	if status != http.StatusOK {
		c.T.Fatalf("%s %s: %d %s", method, path, status, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		var id string // a DELETE answers with the id as a JSON string
		if json.Unmarshal(raw, &id) == nil {
			return map[string]any{"id": id}
		}
		c.T.Fatalf("decode %s %s: %v (%s)", method, path, err, raw)
	}
	return out
}

// do sends a request; requestID, when not empty, names it for the Trace lookup, and header adds headers. A failure to get an
// answer fails the test.
func (c *Cluster) do(method, target, token string, body any, requestID string, header http.Header) (int, []byte, http.Header) {
	c.T.Helper()
	status, raw, answered, err := c.send(c.client, method, target, token, body, requestID, header)
	if err != nil {
		c.T.Fatalf("%s %s: %v", method, target, err)
	}
	return status, raw, answered
}

func (c *Cluster) send(client *http.Client, method, target, token string, body any, requestID string, header http.Header) (int, []byte, http.Header, error) {
	req, err := newRequest(context.Background(), method, target, token, body, header)
	if err != nil {
		return 0, nil, nil, err
	}
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, raw, resp.Header, nil
}

// Result is an answer of a replica with what the request cost.
type Result struct {
	Status int
	Body   []byte
	Header http.Header
	// Trace holds the counters of the request: Trace.DBQueries(), Trace.RedisRoundTrips(). Work the request left in
	// the background (writing the answer to the cache) is not in it.
	Trace *engine.Trace
}

// JSON decodes the body into a map.
func (r *Result) JSON(t testing.TB) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("decode the answer: %v (%s)", err, r.Body)
	}
	return out
}

// IDs are the ids of the resources that the answer to a search of resources lists (see Branches), in the order the API gives
// them. An answer that is not one, an error status included, fails the test.
func (r *Result) IDs(t testing.TB) []string {
	t.Helper()
	ids, _, err := authzenreq.ResourceIDs(r.Status, r.Body)
	if err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	return ids
}

// Decision is the decision of the answer to the evaluation of one question. An answer that is not a decision fails the test,
// an evaluation that could not be decided (the API answers false with context.error) included: it is not a denial.
func (r *Result) Decision(t testing.TB) bool {
	t.Helper()
	allowed, err := authzenreq.Allowed(r.Status, r.Body)
	if err != nil {
		t.Fatalf("decode the answer: %v", err)
	}
	return allowed
}

// Sees is whether the answer to a search of resources (see Branches) lists at least one: the person has the access it asked
// for. An answer that is not a 200, or that cannot be read, sees nothing, so a test that waits for a grant to appear or to go
// tells that from a refusal by the status.
func (r *Result) Sees() bool {
	ids, _, err := authzenreq.ResourceIDs(r.Status, r.Body)
	return err == nil && len(ids) > 0
}

// Do sends a request to the replica with the token (empty: none) and returns the answer with the Trace of the request.
func (r *Replica) Do(c *Cluster, method, path, token string, body any) *Result {
	c.T.Helper()
	return r.request(c, method, path, token, body, nil)
}

// Send sends an AuthZEN request (see authzenreq) to the replica with the token (empty: none), under the levers, and returns the
// answer with the Trace of the request.
func (r *Replica) Send(c *Cluster, req authzenreq.Request, token string, levers ...Lever) *Result {
	c.T.Helper()
	return r.request(c, req.Method, req.Path, token, req.Body, questionHeader(levers))
}

func (r *Replica) request(c *Cluster, method, path, token string, body any, header http.Header) *Result {
	c.T.Helper()
	id := "harness-" + strconv.Itoa(r.Index) + "-" + strconv.FormatInt(r.seq.Add(1), 10)
	status, raw, answered := c.do(method, r.Server.URL+path, token, body, id, header)
	res := &Result{Status: status, Body: raw, Header: answered}
	if v, ok := r.traces.LoadAndDelete(id); ok {
		res.Trace = v.(*engine.Trace)
	}
	return res
}

// Try is Send for a replica that may not answer: the client gives up after timeout and the error says so (a black-holed
// dependency makes the replica wait for it). The Trace is not returned: the request may still run.
func (r *Replica) Try(c *Cluster, req authzenreq.Request, token string, timeout time.Duration, levers ...Lever) (status int, body []byte, err error) {
	c.T.Helper()
	client := &http.Client{Timeout: timeout}
	status, body, _, err = c.send(client, req.Method, r.Server.URL+req.Path, token, req.Body, "", questionHeader(levers))
	return status, body, err
}

// TryPermission is Try of the request that Permission sends, without asking for a revision.
func (r *Replica) TryPermission(c *Cluster, serviceRef, token string, timeout time.Duration) (status int, body []byte, err error) {
	c.T.Helper()
	return r.Try(c, Branches(serviceRef, token), token, timeout)
}

// Fresh asks what Permission asks under the lever Higher: the policy and the sources are read again, so the request needs
// PostgreSQL and Redis even when everything is cached.
func (r *Replica) Fresh(c *Cluster, serviceRef, token string) *Result {
	c.T.Helper()
	return r.Send(c, Branches(serviceRef, token), token, Higher())
}

// Permission asks the service for the branches the holder of the token may read, for an answer that reflects the last write
// made through AdminDo or Must (PermissionRequest).
func (r *Replica) Permission(c *Cluster, serviceRef, token string) *Result {
	c.T.Helper()
	return r.Send(c, c.PermissionRequest(serviceRef, token), token)
}

// Metrics is the text of GET /metrics of the replica.
func (r *Replica) Metrics(c *Cluster) string {
	c.T.Helper()
	_, raw, _ := c.do(http.MethodGet, r.Server.URL+"/metrics", "", nil, "", nil)
	return string(raw)
}

// WaitForSyncIdle waits until the synchronization worker has nothing queued or running, and the replicas have had time to
// see the revisions it moved (two polls of the revision): a synchronization that ends meanwhile makes a replica read the
// policy again, which would be counted against the request being measured.
func (c *Cluster) WaitForSyncIdle() {
	c.T.Helper()
	ctx := context.Background()
	q := fmt.Sprintf(`SELECT count(*) FROM %s.workflow_status WHERE status IN ('ENQUEUED', 'PENDING')`, pgx.Identifier{c.Cfg.Sync.DBOSSchema}.Sanitize())
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var active int
		if err := c.Pool.QueryRow(db.Uncounted(ctx), q).Scan(&active); err != nil || active == 0 {
			break // no table yet, or nothing active
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
}

// Warm sends the request until the replica is in a steady state for it: the answer and the token are cached and the
// background work of the earlier requests (writing the cache, refreshing a source, the batch of known subjects) has ended, which is when two
// requests in a row cost the same. It returns the last of them. The cost of a request is meaningful only then.
func (r *Replica) Warm(c *Cluster, req authzenreq.Request, token string) *Result {
	c.T.Helper()
	var last *Result
	same := 0
	for i := 0; i < 40; i++ {
		res := r.Send(c, req, token)
		if res.Status != http.StatusOK {
			c.T.Fatalf("warm %s %s: %d %s", req.Method, req.Path, res.Status, res.Body)
		}
		if last != nil && last.Trace.DBQueries() == res.Trace.DBQueries() && last.Trace.RedisRoundTrips() == res.Trace.RedisRoundTrips() {
			same++
		} else {
			same = 0
		}
		last = res
		if same >= 2 {
			c.WaitForSyncIdle()
			// The subjects the requests noted are written in batches once a second: write them now, so that the
			// batch does not fall into the request a test measures next.
			r.App.FlushSubjects(context.Background())
			time.Sleep(100 * time.Millisecond) // let the background work of the last request end
			return r.Send(c, req, token)
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.T.Fatalf("%s %s did not settle: the last request cost db=%d redis=%d", req.Method, req.Path, last.Trace.DBQueries(), last.Trace.RedisRoundTrips())
	return nil
}

// startReadReplica connects the cluster to the streaming replica: the check replicas get its URI (behind a proxy when there
// are proxies), the test gets a direct pool, and the call returns once the replica has replayed the migrations.
func (c *Cluster) startReadReplica(dbURI string) {
	t := c.T
	t.Helper()
	addr := os.Getenv(EnvReplicaAddr)
	if addr == "" {
		t.Skipf("no read replica: set %s (scripts/pg-replica.sh up)", EnvReplicaAddr)
	}
	u, err := url.Parse(dbURI)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cfg.DB.PgBouncer {
		t.Skip("the read replica tests do not run through PgBouncer")
	}
	direct := *u
	direct.Host = addr
	readURI := direct.String()
	cfg := c.Cfg.DB
	cfg.URI = readURI
	c.ReplicaPool, err = db.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect to the read replica: %v", err)
	}
	t.Cleanup(func() {
		c.ResumeReplay()
		c.ReplicaPool.Close()
	})
	c.replicaReadURI = readURI
	if c.proxied {
		c.ReplicaFault = support.NewFaultProxy(t, "replica", addr)
		c.replicaReadURI = c.ReplicaFault.Rewrite(readURI)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := db.LoadRevisions(context.Background(), c.ReplicaPool); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the read replica did not replay the migrations: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// PauseReplay stops the read replica applying the changes it receives: from now on it lags behind the primary.
func (c *Cluster) PauseReplay() {
	c.T.Helper()
	if _, err := c.ReplicaPool.Exec(context.Background(), `SELECT pg_wal_replay_pause()`); err != nil {
		c.T.Fatalf("pause the replay: %v", err)
	}
}

// ResumeReplay lets the read replica apply what it has received; it is safe to call when the replay is not paused.
func (c *Cluster) ResumeReplay() {
	if c.ReplicaPool == nil {
		return
	}
	_, _ = c.ReplicaPool.Exec(context.Background(), `SELECT pg_wal_replay_resume()`)
}

// ReplicaRevision is the revision of the rules the read replica has replayed.
func (c *Cluster) ReplicaRevision() int64 {
	c.T.Helper()
	rev, err := db.LoadRevisions(context.Background(), c.ReplicaPool)
	if err != nil {
		c.T.Fatalf("read the revisions of the replica: %v", err)
	}
	return rev.Rules
}
