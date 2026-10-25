package support

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/licetum/licetum/core/db"
)

// Environment variables that point the tests at servers which are already running instead of starting
// containers (for hosts where Docker cannot create networks, or to reuse a local PostgreSQL).
// Every Start then creates its own database on the PostgreSQL server and Terminate drops it.
const (
	EnvPostgresURI = "LICETUM_TEST_POSTGRES_URI" // administrative connection string, e.g. postgres://user:pass@localhost:5432/postgres
	// EnvPgBouncerAddr is the address (host:port) of a PgBouncer in transaction mode in front of the PostgreSQL of
	// EnvPostgresURI. With it the tests connect through the pooler, as DB__PGBOUNCER says (see Infrastructure.Isolate).
	EnvPgBouncerAddr = "LICETUM_TEST_PGBOUNCER_ADDR"
)

// Infrastructure is the disposable PostgreSQL environment used by integration tests.
// Each test package owns one instance; individual scenarios isolate their database state with a
// PostgreSQL schema (Isolate).
type Infrastructure struct {
	Postgres *postgres.PostgresContainer
	DBURI    string

	// external is set when the tests run against the server named by EnvPostgresURI.
	external *externalDatabase
}

type externalDatabase struct {
	adminURI, name string
}

func Start(ctx context.Context) (*Infrastructure, error) {
	return StartWith(ctx)
}

// StartWith is Start with more options for the PostgreSQL container, such as the server arguments a benchmark needs
// (the statistics extension). They do not apply to the servers named by EnvPostgresURI, which are configured already.
func StartWith(ctx context.Context, postgresOptions ...testcontainers.ContainerCustomizer) (*Infrastructure, error) {
	if adminURI := os.Getenv(EnvPostgresURI); adminURI != "" {
		return startExternal(ctx, adminURI)
	}

	options := append([]testcontainers.ContainerCustomizer{
		postgres.WithDatabase("licetum_test"),
		postgres.WithUsername("licetum"),
		postgres.WithPassword("licetum"),
		postgres.BasicWaitStrategies(),
	}, postgresOptions...)
	postgresContainer, err := postgres.Run(ctx, "postgres:16-alpine", options...)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}
	dbURI, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(postgresContainer)
		return nil, fmt.Errorf("get postgres connection string: %w", err)
	}

	return &Infrastructure{Postgres: postgresContainer, DBURI: dbURI}, nil
}

// startExternal creates a fresh database on the given PostgreSQL server.
func startExternal(ctx context.Context, adminURI string) (*Infrastructure, error) {
	name := "licetum_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	conn, err := pgx.Connect(ctx, adminURI)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", EnvPostgresURI, err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return nil, fmt.Errorf("create test database: %w", err)
	}

	u, err := url.Parse(adminURI)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvPostgresURI, err)
	}
	u.Path = "/" + name
	return &Infrastructure{
		DBURI:    u.String(),
		external: &externalDatabase{adminURI: adminURI, name: name},
	}, nil
}

func (i *Infrastructure) Terminate(ctx context.Context) error {
	if i == nil {
		return nil
	}
	if i.external != nil {
		conn, err := pgx.Connect(ctx, i.external.adminURI)
		if err != nil {
			return err
		}
		defer conn.Close(ctx)
		_, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+i.external.name+" WITH (FORCE)")
		return err
	}
	return testcontainers.TerminateContainer(i.Postgres)
}

// Isolation is the database a test works in, apart from the others that share the server.
type Isolation struct {
	// Name is unique to the isolation: the name of its schema or of its database.
	Name string
	// URI is what the application connects with.
	URI string
	// MigrateURI is what the migrations connect with when that must be another connection (empty: URI): PostgreSQL itself
	// when URI goes through PgBouncer.
	MigrateURI string
	// PgBouncer says URI goes through a PgBouncer in transaction mode (config.Database.PgBouncer).
	PgBouncer bool
}

// Isolate gives a test a database of its own, dropped when the test ends. Without a pooler that is a schema of the
// infrastructure's database, selected by the search_path of the connection. With one (EnvPgBouncerAddr) that is a database:
// PgBouncer in transaction mode does not carry the search_path of a connection from one transaction to the next, and the
// application connects through it, while the migrations go to PostgreSQL directly.
func (i *Infrastructure) Isolate(ctx context.Context, t testing.TB) Isolation {
	t.Helper()
	name := fmt.Sprintf("it_%d", time.Now().UnixNano())
	direct := db.NormalizeDSN(i.DBURI)
	admin, err := pgx.Connect(ctx, direct)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	u, err := url.Parse(direct)
	if err != nil {
		t.Fatalf("parse the database URI: %v", err)
	}

	if bouncer := os.Getenv(EnvPgBouncerAddr); bouncer != "" {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatalf("create the database %s: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = admin.Close(context.Background())
		})
		viaPooler, migrate := *u, *u
		viaPooler.Host, viaPooler.Path = bouncer, "/"+name
		migrate.Path = "/" + name
		return Isolation{Name: name, URI: viaPooler.String(), MigrateURI: migrate.String(), PgBouncer: true}
	}

	// The public schema stays on the search path for the extensions installed there.
	for _, stmt := range []string{"CREATE EXTENSION IF NOT EXISTS ltree SCHEMA public", "CREATE SCHEMA " + name} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS dbos_"+name+" CASCADE")
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE")
		_ = admin.Close(context.Background())
	})
	q := u.Query()
	q.Set("search_path", name+",public")
	u.RawQuery = q.Encode()
	return Isolation{Name: name, URI: u.String()}
}
