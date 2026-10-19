package loadtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	adminsvc "github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/redisx"
	support "github.com/licetum/licetum/core/testsupport"
)

// env is the process side of a run: the database and the cache the harness seeds through the admin application. The
// traffic needs nothing else from the process: it asks as a PEP client with an API key the seeding issues (seed.go).
type env struct {
	pool     *pgxpool.Pool
	app      *engine.App
	admin    *adminsvc.App
	dbURI    string
	redisURI string
	// checkDBURI and checkRedisURI are what the check servers connect to: the same servers, or, when the run adds latency,
	// the Toxiproxy links in front of them. The harness itself (seeding, the admin writes) always goes direct.
	checkDBURI, checkRedisURI string
	// links are the proxies in front of PostgreSQL and Redis (nil without added latency).
	links       []*support.Link
	stateSecret string
	closers     []func() error
}

// newEnv connects to the infrastructure the same way the binaries do (config from the environment, migrations, the
// engine), so the seeding goes through the same code the admin API runs.
func newEnv(ctx context.Context, infra *support.Infrastructure) (*env, error) {
	secret, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	e := &env{dbURI: infra.DBURI, redisURI: infra.RedisURI, checkDBURI: infra.DBURI, checkRedisURI: infra.RedisURI, stateSecret: secret}
	for name, value := range map[string]string{"DB__URI": e.dbURI, "REDIS__URI": e.redisURI, "AUTH__STATE_SECRET": secret} {
		if err := os.Setenv(name, value); err != nil {
			return nil, fmt.Errorf("set %s: %w", name, err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	pool, err := db.Connect(ctx, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	e.pool = pool
	e.closers = append(e.closers, func() error { pool.Close(); return nil })
	if err := db.Migrate(ctx, pool, cfg); err != nil {
		e.close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	rdb, err := redisx.Connect(ctx, cfg.Redis.URI)
	if err != nil {
		e.close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	e.closers = append(e.closers, rdb.Close)
	e.app = engine.New(pool, rdb, cfg)
	e.closers = append(e.closers, func() error { e.app.Close(); return nil })
	// The synchronization jobs are executed by a worker: the harness runs one in-process next to the APIs under test.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = e.app.RunWorker(workerCtx)
	}()
	e.closers = append(e.closers, func() error { stopWorker(); <-workerDone; return nil })
	e.admin = adminsvc.New(e.app)
	return e, nil
}

// insertLinks puts a Toxiproxy link in front of PostgreSQL and in front of Redis for the check servers. The links add no
// latency yet (see addLatency): the warm-up runs on the plain link, since the first synchronization is not what is measured.
func (e *env) insertLinks() error {
	for _, l := range []struct {
		name string
		uri  *string
		to   string
	}{{"postgres", &e.checkDBURI, e.dbURI}, {"redis", &e.checkRedisURI, e.redisURI}} {
		u, err := url.Parse(db.NormalizeDSN(l.to))
		if err != nil {
			return fmt.Errorf("parse the %s address: %w", l.name, err)
		}
		link, err := support.StartLink(l.name, u.Host)
		if err != nil {
			return fmt.Errorf("start the %s link: %w", l.name, err)
		}
		e.links = append(e.links, link)
		if *l.uri, err = link.Rewrite(l.to); err != nil {
			return err
		}
	}
	return nil
}

// addLatency makes every round trip to PostgreSQL and to Redis take rtt longer: half of it in each direction.
func (e *env) addLatency(rtt time.Duration) error {
	for _, l := range e.links {
		if err := l.SetLatency(rtt/2, 0); err != nil {
			return fmt.Errorf("add latency: %w", err)
		}
	}
	return nil
}

// close releases the connections, last opened first.
func (e *env) close() {
	for _, l := range e.links {
		l.Close()
	}
	for i := len(e.closers) - 1; i >= 0; i-- {
		_ = e.closers[i]()
	}
}

func subjectName(i int) string { return fmt.Sprintf("user-%d", i) }

func departmentName(g int) string { return fmt.Sprintf("dept-%d", g) }

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
