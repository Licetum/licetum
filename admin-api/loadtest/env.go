package loadtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	adminsvc "github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	support "github.com/licetum/licetum/core/testsupport"
)

// env is the process side of a run: the database the harness seeds through the admin application. The traffic needs
// nothing else from the process: it asks as a PEP client with an API key the seeding issues (seed.go).
type env struct {
	pool  *pgxpool.Pool
	app   *engine.App
	admin *adminsvc.App
	dbURI string
	// checkDBURI is what the check servers connect to: the same server, or, when the run adds latency, the Toxiproxy link
	// in front of it. The harness itself (seeding, the admin writes) always goes direct.
	checkDBURI string
	// links are the proxies in front of PostgreSQL (nil without added latency).
	links   []*support.Link
	closers []func() error
}

// newEnv connects to the infrastructure the same way the binaries do (config from the environment, migrations, the
// engine), so the seeding goes through the same code the admin API runs.
func newEnv(ctx context.Context, infra *support.Infrastructure) (*env, error) {
	e := &env{dbURI: infra.DBURI, checkDBURI: infra.DBURI}
	if err := os.Setenv("DB__URI", e.dbURI); err != nil {
		return nil, fmt.Errorf("set DB__URI: %w", err)
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
	e.app = engine.New(pool, cfg)
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

// insertLinks puts a Toxiproxy link in front of PostgreSQL for the check servers. The link adds no latency yet (see
// addLatency): the warm-up runs on the plain link, since the first synchronization is not what is measured.
func (e *env) insertLinks() error {
	u, err := url.Parse(db.NormalizeDSN(e.dbURI))
	if err != nil {
		return fmt.Errorf("parse the postgres address: %w", err)
	}
	link, err := support.StartLink("postgres", u.Host)
	if err != nil {
		return fmt.Errorf("start the postgres link: %w", err)
	}
	e.links = append(e.links, link)
	if e.checkDBURI, err = link.Rewrite(e.dbURI); err != nil {
		return err
	}
	return nil
}

// addLatency makes every round trip to PostgreSQL take rtt longer: half of it in each direction.
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
