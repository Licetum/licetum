// Package harness builds the multi-replica environments of the integration tests (tier M of the production-load
// plan): several check API replicas, each with its own database pool and Redis client, on one shared PostgreSQL and
// one shared Redis, together with an admin API to set the policy up, a fake identity provider and a fake source of
// resources. The call counters of the fakes and the per-request counters of the replicas (engine.Trace) are what the
// tests assert against; with WithFaultProxies the replicas reach PostgreSQL and Redis through Toxiproxy, so a test can
// slow, cut or black-hole either of them.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
	"github.com/licetum/licetum/core/server"
	support "github.com/licetum/licetum/core/testsupport"
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

	// LastRevision is the revision (X-Authz-Revision) of the last write made through AdminDo or Must. The replicas
	// follow the revision with a delay of up to REVISION__POLL_INTERVAL; Replica.Permission asks for at least this one, so
	// that it sees the writes of the test, as a client that passes at_least_as_fresh does.
	LastRevision atomic.Int64

	proxied      bool
	replicaRedis string
	hotRedis     time.Duration
	client       *http.Client
	configure    []func(*config.Config)
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

	// An isolated schema per cluster; the public schema stays on the search path for the extensions installed there.
	schema := fmt.Sprintf("it_%d", time.Now().UnixNano())
	dbURI := db.NormalizeDSN(infra.DBURI)
	conn, err := pgx.Connect(ctx, dbURI)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	for _, stmt := range []string{"CREATE EXTENSION IF NOT EXISTS ltree SCHEMA public", "CREATE SCHEMA " + schema} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = conn.Close(context.Background())
	})
	withSchema := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		q := u.Query()
		q.Set("search_path", schema+",public")
		u.RawQuery = q.Encode()
		return u.String()
	}

	c.Cfg = &config.Config{
		DB:       config.Database{URI: withSchema(dbURI), PoolSize: 5},
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
	c.Pool, err = db.Connect(ctx, c.Cfg.DB)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(c.Pool.Close)
	if err := db.Migrate(ctx, c.Pool, c.Cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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
		replicaDB, replicaRedis = withSchema(c.PostgresFault.Rewrite(dbURI)), c.RedisFault.Rewrite(infra.RedisURI)
	}
	if c.replicaRedis != "" {
		replicaRedis = c.replicaRedis
		if c.proxied {
			t.Fatal("WithReplicaRedis and WithFaultProxies cannot be combined: the proxy would not be in front of the replicas' Redis")
		}
	}
	for i := 0; i < n; i++ {
		c.Replicas = append(c.Replicas, c.startReplica(i, replicaDB, replicaRedis))
	}
	return c
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

	r := &Replica{Index: index, App: app, Pool: pool, Redis: rdb}
	handler := server.NewCheckHandler(app)
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

// Token signs a token of the fake identity provider.
func (c *Cluster) Token(claims jwt.MapClaims) string { return c.IdP.Token(claims) }

// UserToken is the token of a user with the given preferred_username.
func (c *Cluster) UserToken(name string) string {
	return c.IdP.Token(jwt.MapClaims{"preferred_username": name, "sub": name + "-sub"})
}

// AdminDo sends a request to the admin API as the platform administrator and returns the status and the body.
func (c *Cluster) AdminDo(method, path string, body any) (int, []byte) {
	c.T.Helper()
	status, raw, header := c.do(method, c.AdminServer.URL+path, c.AdminToken, body, "")
	if rev, err := strconv.ParseInt(header.Get("X-Authz-Revision"), 10, 64); err == nil && rev > c.LastRevision.Load() {
		c.LastRevision.Store(rev)
	}
	return status, raw
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

// do sends a request; requestID, when not empty, names it for the Trace lookup. A failure to get an answer fails the test.
func (c *Cluster) do(method, target, token string, body any, requestID string) (int, []byte, http.Header) {
	c.T.Helper()
	status, raw, header, err := c.send(c.client, method, target, token, body, requestID)
	if err != nil {
		c.T.Fatalf("%s %s: %v", method, target, err)
	}
	return status, raw, header
}

func (c *Cluster) send(client *http.Client, method, target, token string, body any, requestID string) (int, []byte, http.Header, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, target, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

// Do sends a request to the replica with the token (empty: none) and returns the answer with the Trace of the request.
func (r *Replica) Do(c *Cluster, method, path, token string, body any) *Result {
	c.T.Helper()
	id := "harness-" + strconv.Itoa(r.Index) + "-" + strconv.FormatInt(r.seq.Add(1), 10)
	status, raw, header := c.do(method, r.Server.URL+path, token, body, id)
	res := &Result{Status: status, Body: raw, Header: header}
	if v, ok := r.traces.LoadAndDelete(id); ok {
		res.Trace = v.(*engine.Trace)
	}
	return res
}

// TryPermission is Permission for a replica that may not answer: the client gives up after timeout and the error says
// so (a black-holed dependency makes the replica wait for it). The Trace is not returned: the request may still run.
func (r *Replica) TryPermission(c *Cluster, serviceRef, token string, timeout time.Duration) (status int, body []byte, err error) {
	c.T.Helper()
	return r.TryGet(c, "/service/"+serviceRef+"/permission", token, timeout)
}

// TryGet is TryPermission for any path of the replica.
func (r *Replica) TryGet(c *Cluster, path, token string, timeout time.Duration) (status int, body []byte, err error) {
	c.T.Helper()
	client := &http.Client{Timeout: timeout}
	status, body, _, err = c.send(client, http.MethodGet, r.Server.URL+path, token, nil, "")
	return status, body, err
}

// Fresh asks GET /service/{ref}/permission with consistency=higher: the policy and the sources are read again, so the
// request needs PostgreSQL and Redis even when everything is cached.
func (r *Replica) Fresh(c *Cluster, serviceRef, token string) *Result {
	c.T.Helper()
	return r.Do(c, http.MethodGet, "/service/"+serviceRef+"/permission?consistency=higher", token, nil)
}

// Permission asks GET /service/{ref}/permission.
func (r *Replica) Permission(c *Cluster, serviceRef, token string) *Result {
	c.T.Helper()
	path := "/service/" + serviceRef + "/permission"
	if rev := c.LastRevision.Load(); rev > 0 {
		path += "?at_least_as_fresh=" + strconv.FormatInt(rev, 10)
	}
	return r.Do(c, http.MethodGet, path, token, nil)
}

// Metrics is the text of GET /metrics of the replica.
func (r *Replica) Metrics(c *Cluster) string {
	c.T.Helper()
	_, raw, _ := c.do(http.MethodGet, r.Server.URL+"/metrics", "", nil, "")
	return string(raw)
}

// Warm sends the request until the replica is in a steady state for it: the answer and the token are cached and the
// background work of the earlier requests (writing the cache, refreshing a source) has ended, which is when two
// requests in a row cost the same. It returns the last of them. The cost of a request is meaningful only then.
func (r *Replica) Warm(c *Cluster, method, path, token string) *Result {
	c.T.Helper()
	var last *Result
	same := 0
	for i := 0; i < 40; i++ {
		res := r.Do(c, method, path, token, nil)
		if res.Status != http.StatusOK {
			c.T.Fatalf("warm %s %s: %d %s", method, path, res.Status, res.Body)
		}
		if last != nil && last.Trace.DBQueries() == res.Trace.DBQueries() && last.Trace.RedisRoundTrips() == res.Trace.RedisRoundTrips() {
			same++
		} else {
			same = 0
		}
		last = res
		if same >= 2 {
			time.Sleep(100 * time.Millisecond) // let the background work of the last request end
			return r.Do(c, method, path, token, nil)
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.T.Fatalf("%s %s did not settle: the last request cost db=%d redis=%d", method, path, last.Trace.DBQueries(), last.Trace.RedisRoundTrips())
	return nil
}
