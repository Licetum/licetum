package support

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Environment variables that point the tests at servers which are already running instead of starting
// containers (for hosts where Docker cannot create networks, or to reuse a local PostgreSQL/Redis).
// Every Start then creates its own database on the PostgreSQL server and Terminate drops it.
const (
	EnvPostgresURI = "LICETUM_TEST_POSTGRES_URI" // administrative connection string, e.g. postgres://user:pass@localhost:5432/postgres
	EnvRedisURI    = "LICETUM_TEST_REDIS_URI"    // e.g. redis://localhost:6379/0
)

// Infrastructure is the disposable PostgreSQL/Redis environment used by integration tests.
// Each test package owns one instance; individual scenarios isolate their database state with a
// PostgreSQL schema and clear the dedicated Redis database before creating the application.
type Infrastructure struct {
	Postgres *postgres.PostgresContainer
	Redis    *rediscontainer.RedisContainer
	DBURI    string
	RedisURI string

	// external is set when the tests run against servers named by EnvPostgresURI and EnvRedisURI.
	external *externalDatabase
}

type externalDatabase struct {
	adminURI, name string
}

func Start(ctx context.Context) (*Infrastructure, error) {
	if adminURI := os.Getenv(EnvPostgresURI); adminURI != "" {
		return startExternal(ctx, adminURI, os.Getenv(EnvRedisURI))
	}

	postgresContainer, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("licetum_test"),
		postgres.WithUsername("licetum"),
		postgres.WithPassword("licetum"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}
	dbURI, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(postgresContainer)
		return nil, fmt.Errorf("get postgres connection string: %w", err)
	}

	redisContainer, err := rediscontainer.Run(
		ctx,
		"redis:7-alpine",
		testcontainers.WithWaitStrategy(wait.ForListeningPort("6379/tcp").WithStartupTimeout(30*time.Second)),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(postgresContainer)
		return nil, fmt.Errorf("start redis container: %w", err)
	}
	redisURI, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(redisContainer)
		_ = testcontainers.TerminateContainer(postgresContainer)
		return nil, fmt.Errorf("get redis connection string: %w", err)
	}
	return &Infrastructure{Postgres: postgresContainer, Redis: redisContainer, DBURI: dbURI, RedisURI: redisURI}, nil
}

// startExternal creates a fresh database on the given PostgreSQL server.
func startExternal(ctx context.Context, adminURI, redisURI string) (*Infrastructure, error) {
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
		RedisURI: redisURI,
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
	if err := testcontainers.TerminateContainer(i.Redis); err != nil {
		return err
	}
	return testcontainers.TerminateContainer(i.Postgres)
}
