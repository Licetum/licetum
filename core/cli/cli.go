// Package cli holds what the Licetum binaries share: reading the configuration, applying the migrations, serving an
// HTTP application with its settings and printing the OpenAPI document. The binaries themselves (the PDP in
// core/cmd/licetum, the admin API in admin-api/cmd/licetum-admin) only choose the subcommands.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/redisx"
	"github.com/licetum/licetum/core/server"
)

// LoadConfig reads the environment; with requireAll the Redis and the auth settings must be set too.
func LoadConfig(requireAll bool) (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.RequireDB(); err != nil {
		return nil, err
	}
	if requireAll {
		if err := cfg.RequireRedis(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// Migrate applies the database migrations and seeds the self-service.
func Migrate(ctx context.Context) error {
	cfg, err := LoadConfig(false)
	if err != nil {
		return err
	}
	return db.RunMigrate(ctx, cfg)
}

// Failover raises every revision of the policy by a jump (-jump, default db.DefaultFailoverJump), in one transaction, against the
// primary the process connects to. Run it once after a replica has been promoted, before the traffic is sent to it (see
// docs/deployment.md): the regions then see revisions above everything they used, and drop what they kept under older ones.
func Failover(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("failover", flag.ExitOnError)
	jump := fs.Int64("jump", db.DefaultFailoverJump, "how much to add to every revision")
	_ = fs.Parse(args)
	cfg, err := LoadConfig(false)
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	var revisions map[string]int64
	if err := db.InTx(ctx, pool, func(tx pgx.Tx) (err error) {
		revisions, err = db.BumpAllRevisions(ctx, tx, *jump)
		return err
	}); err != nil {
		return err
	}
	for scope, revision := range revisions {
		slog.InfoContext(ctx, "revision raised", "scope", scope, "revision", revision)
	}
	slog.InfoContext(ctx, "failover done: the regions replace their views and their cached answers within REVISION__POLL_INTERVAL", "jump", *jump)
	return nil
}

// Serve runs an HTTP application until the context ends. name is the application's name in the logs and its role; the
// address is the -addr flag, else the application's own setting (PDP__ADDR for the PDP, HTTP__ADDR for the admin API), else
// defaultAddr. build creates the handler from the engine. The PDP also serves its operations endpoints on PDP__OPS_ADDR
// (server.NewOpsHandler).
func Serve(ctx context.Context, name string, args []string, defaultAddr string, build func(*engine.App) http.Handler, logs *logx.Switch) error {
	cfg, err := LoadConfig(true)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet(name, flag.ExitOnError)
	addr := fs.String("addr", "", "HTTP server address (overrides PDP__ADDR or HTTP__ADDR)")
	_ = fs.Parse(args)
	own := cfg.HTTP.Addr
	if name == "pdp" {
		own = cfg.PDP.Addr
	}
	switch {
	case *addr != "":
	case own != "":
		*addr = own
	default:
		*addr = defaultAddr
	}

	if name == "admin" {
		if err := cfg.RequireAdminAuth(); err != nil {
			return err
		}
	}
	if name == "pdp" && cfg.PDP.PublicURL == "" {
		return errors.New("PDP__PUBLIC_URL is required: the PEPs name the PDP by it, for example https://licetum.example.com")
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	logConnectionBudget(ctx, pool, cfg, name, name == "admin")

	// The PDP answers within a budget: its Redis client times out fast, does not retry, and skips Redis for a while
	// after repeated failures (see redisx.FastFail). The admin API waits for Redis as long as the default allows.
	var redisOptions redisx.Options
	if name == "pdp" && cfg.Redis.HotTimeout > 0 {
		redisOptions.HotTimeout = cfg.Redis.HotTimeout
	}
	rdb, err := redisx.ConnectWith(ctx, cfg.Redis.URI, redisOptions)
	if err != nil {
		return err
	}
	defer rdb.Close()

	app := engine.New(pool, rdb, cfg)
	defer app.Close()
	app.Logs = logs
	app.Role = roleOf(name)
	warnDeprecated(slog.Default())
	if app.Role == engine.RolePDP && cfg.DB.ReadURI != "" && cfg.DB.ReadURI != cfg.DB.URI {
		// The PDP reads the policy from the read replica of its region; everything else, and every read that must be
		// current, goes to the primary (see engine.App.ReadDB).
		readCfg := cfg.DB
		readCfg.URI = cfg.DB.ReadURI
		readPool, err := db.Connect(ctx, readCfg)
		if err != nil {
			return fmt.Errorf("connect to DB__READ_URI: %w", err)
		}
		defer readPool.Close()
		app.UseReadReplica(readPool)
		slog.InfoContext(ctx, "reading the policy from a read replica", "max_lag", cfg.DB.MaxReplicaLag, "action", cfg.DB.LagAction)
	}
	if app.Role == engine.RoleAdmin {
		// The admin API refreshes a single resource inline; it does so on the synchronization pool, not on the request pool.
		syncPool, err := connectSyncPool(ctx, cfg)
		if err != nil {
			return err
		}
		defer syncPool.Close()
		app.SyncDB = syncPool
	}
	if err := app.LoadLogSettings(ctx); err != nil {
		slog.WarnContext(ctx, "the stored log settings are not applied, the defaults stay in force", "error", err)
	}
	// The revision is watched until the process has finished serving, not until SIGTERM: during the drain and the wait for
	// the requests in progress the process answers, and its copy of the policy must keep being confirmed.
	watchCtx, stopWatching := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWatching()
	go app.WatchRevision(watchCtx)
	go app.WatchReplica(watchCtx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           build(app),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// The PDP's operations endpoints (the probes and the metrics) have a listener of their own. It is bound before the public one,
	// so that a port already in use fails the process at start, and it is shut down last (StopServing).
	var ops *http.Server
	if name == "pdp" {
		ops = &http.Server{
			Addr:              cfg.PDP.OpsAddr,
			Handler:           server.NewOpsHandler(app),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		ln, err := new(net.ListenConfig).Listen(ctx, "tcp", ops.Addr)
		if err != nil {
			return fmt.Errorf("operations listener: %w", err)
		}
		go func() {
			slog.Info("listening", "app", name, "addr", ln.Addr().String(), "role", "operations")
			if err := ops.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("the operations listener stopped", "app", name, "error", err)
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "app", name, "addr", *addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	return StopServing(srv, ops, app, cfg.Shutdown, errCh)
}

// StopServing is the shutdown after SIGTERM, in the order that lets the balancer take the pod out without a request lost:
//
//  1. readiness turns false (GET /readyz is 503) and the responses carry Connection: close, so the clients move to other
//     pods; the process goes on serving what still arrives;
//  2. it waits SHUTDOWN__DRAIN, the time the balancer needs to notice;
//  3. it stops listening and waits SHUTDOWN__TIMEOUT for the requests in progress (http.Server.Shutdown); then the operations
//     listener of the PDP (ops, nil for the admin API) stops, last, so that its probes answer through the whole drain;
//  4. the caller closes the engine (the background work, the buffers) and the connections.
//
// The pod needs terminationGracePeriodSeconds of at least drain + timeout + 10, and a preStop sleep of the drain when the
// balancer is told of the pod's end only by the pod's deletion.
func StopServing(srv, ops *http.Server, app *engine.App, cfg config.Shutdown, errCh <-chan error) error {
	slog.Info("shutting down: not ready, serving while the balancer stops sending requests", "drain", cfg.Drain, "timeout", cfg.Timeout)
	app.SetDraining()
	if cfg.Drain > 0 {
		select {
		case err := <-errCh: // the listener died while draining
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		case <-time.After(cfg.Drain):
		}
	}
	slog.Info("shutting down: stopped listening, waiting for the requests in progress")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		_ = srv.Close() // the requests that did not finish in time are cut off
	}
	// The operations listener stops after the public one: its probes answer through the drain, /readyz says 503 from SetDraining on.
	if ops != nil {
		if opsErr := ops.Shutdown(shutdownCtx); opsErr != nil {
			_ = ops.Close()
		}
	}
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// roleOf is the role of the application of that name in the synchronization pipeline.
func roleOf(name string) engine.Role {
	switch name {
	case "pdp":
		return engine.RolePDP
	case "admin":
		return engine.RoleAdmin
	}
	return engine.RoleAny
}

// warnDeprecated logs one warning per removed setting that is still set, with the reason the catalog gives. Serve (the pdp
// and admin processes) and Worker warn: they are the long-running processes whose logs a deployment watches. migrate,
// failover, restore-admin and openapi do not: they are one-shot tools, migrate and failover run from a job with the same
// environment as the serving processes, which already tell the operator, and a warning on the output of openapi would
// corrupt what a script reads (the logger writes to stdout, like the specification).
func warnDeprecated(log *slog.Logger) {
	for _, name := range config.DeprecatedSettings() {
		reason, _ := config.DeprecatedReason(name)
		log.Warn("the setting no longer has an effect", "setting", name, "reason", reason)
	}
}

// connectionBudget is the most connections to PostgreSQL a process may hold: the request pool (DB__POOL_SIZE +
// DB__MAX_OVERFLOW), the pool of the synchronization transactions where the process has one (DB__SYNC_POOL_SIZE: the
// worker and the admin API) and the pool of the DBOS client (SYNC__DBOS_POOL_SIZE, outside the others). The connections of
// all the replicas add up against max_connections of the server, or against default_pool_size of PgBouncer.
func connectionBudget(cfg *config.Config, withSyncPool, withReadPool bool) (total int, parts []any) {
	request := cfg.DB.MaxConns()
	total = request
	parts = []any{"request_pool", request}
	if withReadPool {
		// The pool of the read replica (DB__READ_URI) has the size of the request pool, on the replica's own server.
		parts = append(parts, "read_pool_on_replica", request)
	}
	if withSyncPool {
		syncPool := max(cfg.DB.SyncPoolSize, 1)
		total += syncPool
		parts = append(parts, "sync_pool", syncPool)
	}
	dbosPool := cfg.Sync.DBOSPoolSize
	if dbosPool <= 0 {
		dbosPool = 20
	}
	total += dbosPool
	parts = append(parts, "dbos_pool", dbosPool)
	return total, parts
}

// logConnectionBudget tells the operator how many connections the process may take, and warns when PostgreSQL would not
// give even one process that many: many replicas with the default pool settings exhaust max_connections.
func logConnectionBudget(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, app string, withSyncPool bool) {
	total, parts := connectionBudget(cfg, withSyncPool, app == "pdp" && cfg.DB.ReadURI != "" && cfg.DB.ReadURI != cfg.DB.URI)
	slog.InfoContext(ctx, "database connections: the most this process may hold, to be multiplied by the replicas against "+
		"max_connections of PostgreSQL (or default_pool_size of PgBouncer)", append([]any{"app", app, "total", total}, parts...)...)
	if cfg.DB.PgBouncer {
		return // SHOW max_connections would be answered by the pooler, not by the server the connections end at
	}
	var limit int
	if err := pool.QueryRow(db.Uncounted(ctx), `SELECT current_setting('max_connections')::int`).Scan(&limit); err != nil {
		return
	}
	if total >= limit {
		slog.WarnContext(ctx, "the connections of one process alone reach max_connections of PostgreSQL: lower DB__POOL_SIZE, "+
			"DB__MAX_OVERFLOW and SYNC__DBOS_POOL_SIZE, or raise max_connections, or use PgBouncer (DB__PGBOUNCER)",
			"app", app, "total", total, "max_connections", limit)
	}
}

// connectSyncPool opens the pool of the synchronization transactions: DB__SYNC_POOL_SIZE connections, no overflow.
func connectSyncPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	c := cfg.DB
	c.PoolSize, c.MaxOverflow = max(cfg.DB.SyncPoolSize, 1), 0
	return db.Connect(ctx, c)
}

// Worker runs the synchronization worker: it executes the jobs the PDP and admin processes queue and keeps the types
// that are being used fresh. It serves /metrics and /healthz on its address (default :8002, -addr or HTTP__ADDR).
func Worker(ctx context.Context, args []string, defaultAddr string, logs *logx.Switch) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := errors.Join(cfg.RequireDB(), cfg.RequireRedis()); err != nil {
		return err
	}
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	addr := fs.String("addr", "", "address of the metrics endpoint (overrides HTTP__ADDR)")
	_ = fs.Parse(args)
	switch {
	case *addr != "":
	case cfg.HTTP.Addr != "":
		*addr = cfg.HTTP.Addr
	default:
		*addr = defaultAddr
	}

	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	logConnectionBudget(ctx, pool, cfg, "worker", true)
	syncPool, err := connectSyncPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer syncPool.Close()
	rdb, err := redisx.Connect(ctx, cfg.Redis.URI)
	if err != nil {
		return err
	}
	defer rdb.Close()

	app := engine.New(pool, rdb, cfg)
	defer app.Close()
	app.Logs = logs
	app.Role = engine.RoleWorker
	app.SyncDB = syncPool
	warnDeprecated(slog.Default())
	if err := app.LoadLogSettings(ctx); err != nil {
		slog.WarnContext(ctx, "the stored log settings are not applied, the defaults stay in force", "error", err)
	}
	go app.WatchRevision(ctx)
	// The retention runs here only: the PDP and the admin processes do not delete.
	go app.WatchRetention(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", app.MetricsHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// Ready: PostgreSQL answers and the schema is the release's (the worker keeps no copy of the policy).
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := app.Ready(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("listening", "app", "worker", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("the worker's metrics endpoint stopped", "error", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	return app.RunWorker(ctx)
}

// PrintOpenAPI prints on the standard output the document OpenAPI returns for the same arguments.
func PrintOpenAPI(args []string, apps map[string]func() huma.API) error {
	out, err := OpenAPI(args, apps)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

// OpenAPI is the spec of an application, as the openapi command prints it, without connecting to the DB or Redis:
// registering routes does not need those dependencies. args is "<name> [-yaml] [-lang en|ru|zh]"; apps maps each name to
// the function that builds its API. The document ends with a newline.
func OpenAPI(args []string, apps map[string]func() huma.API) ([]byte, error) {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	asYAML := fs.Bool("yaml", false, "print YAML instead of JSON")
	langCode := fs.String("lang", string(i18n.Default), "language of descriptions: en, ru or zh")
	build, ok := apps[firstArg(args)]
	if !ok {
		return nil, fmt.Errorf("usage: openapi <%s> [-yaml] [-lang en|ru|zh]", joinNames(apps))
	}
	_ = fs.Parse(args[1:])
	lang, ok := i18n.FromCode(*langCode)
	if !ok {
		return nil, fmt.Errorf("unsupported language %q (supported: en, ru, zh)", *langCode)
	}

	out, err := server.LocalizedSpec(build(), lang, *asYAML)
	if err != nil {
		return nil, err
	}
	if !*asYAML {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, out, "", "  "); err != nil {
			return nil, err
		}
		out = pretty.Bytes()
	}
	return append(out, '\n'), nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func joinNames(apps map[string]func() huma.API) string {
	out := ""
	for name := range apps {
		if out != "" {
			out += "|"
		}
		out += name
	}
	return out
}
