package db

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationsFS is the embedded migrations rooted at the directory (also used by tests).
func migrationsFS() (fs.FS, error) { return fs.Sub(migrationFS, "migrations") }

// migrateLockTries is how many times, one a second, a migration run tries to take the lock held by another run (a
// parallel `licetum migrate`, as the pods of a release start) before it gives up.
const migrateLockTries = 600

// newProvider is the goose provider of the embedded migrations. It takes the session-level advisory lock of goose for
// the whole run, so two parallel runs never apply the same migration: the second one waits, then finds nothing to do.
// The lock belongs to the connection that took it, so a run needs a connection that stays the same for its whole
// duration: PostgreSQL itself, or PgBouncer in session mode, not in transaction mode (see config.Database.MigrateURI).
func newProvider(sqlDB *sql.DB, migrations fs.FS) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, migrateLockTries))
	if err != nil {
		return nil, fmt.Errorf("init the migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("init goose: %w", err)
	}
	return provider, nil
}

// latestMigration is the highest version among the embedded migrations (they are named NNNNN_name.sql).
var latestMigration = sync.OnceValue(func() int64 {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return 0
	}
	var latest int64
	for _, e := range entries {
		version, _, _ := strings.Cut(e.Name(), "_")
		if n, err := strconv.ParseInt(version, 10, 64); err == nil && n > latest {
			latest = n
		}
	}
	return latest
})

// LatestMigration is the version of the schema this binary was built for: the highest embedded migration.
func LatestMigration() int64 { return latestMigration() }

// SchemaVersion is the version of the schema the database is at: the highest migration applied. A database that was
// never migrated has no version table, which is an error.
func SchemaVersion(ctx context.Context, q Querier) (int64, error) {
	var version int64
	err := q.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&version)
	return version, err
}

// MigrateUpTo applies the migrations up to and including version, without seeding the self-service. It
// lets a test bring a database to the schema of an earlier release, fill it, and migrate it on.
func MigrateUpTo(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	migrations, err := migrationsFS()
	if err != nil {
		return err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := newProvider(sqlDB, migrations)
	if err != nil {
		return err
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("apply migrations up to %d: %w", version, err)
	}
	return nil
}

// MigrateDownTo rolls the schema back to version (the migrations above it are reverted). Tests use it to check
// that the down migrations still reach the schema of the previous release.
func MigrateDownTo(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	migrations, err := migrationsFS()
	if err != nil {
		return err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := newProvider(sqlDB, migrations)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("roll back to %d: %w", version, err)
	}
	return nil
}

// RunMigrate is what `licetum migrate` does: it connects as DB__MIGRATE_URI says (DB__URI when that is empty), applies the
// migrations and seeds the self-service. The migrations hold a session-level advisory lock for their whole run, so they need
// a connection that stays the same: behind PgBouncer in transaction mode that is PostgreSQL itself, and never the simple
// protocol of the pooler.
func RunMigrate(ctx context.Context, cfg *config.Config) error {
	conn := cfg.DB
	if conn.MigrateURI != "" {
		conn.URI, conn.PgBouncer = conn.MigrateURI, false
	} else if conn.PgBouncer {
		slog.Warn("DB__PGBOUNCER is set and DB__MIGRATE_URI is not: the migrations lock the database for their run, which " +
			"PgBouncer in transaction mode cannot hold; point DB__MIGRATE_URI at PostgreSQL itself")
	}
	pool, err := Connect(ctx, conn)
	if err != nil {
		return err
	}
	defer pool.Close()
	return Migrate(ctx, pool, cfg)
}

// Migrate applies every embedded migration (goose) and then idempotently seeds the self-service.
func Migrate(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	migrations, err := migrationsFS()
	if err != nil {
		return err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := newProvider(sqlDB, migrations)
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	for _, r := range results {
		slog.Info("migration applied", "version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
	}
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return SeedPlatform(ctx, pool, cfg)
}

// seedLockKey is the advisory lock key the seed of the self-service runs under.
var seedLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("licetum.seed.platform"))
	return int64(h.Sum64()) // #nosec G115 -- a hash, wrapping is fine
}()

// platformSource describes where a platform policy's entities live: the table, the expression used
// as the resource name and the condition of a live row.
type platformSource struct {
	table, name, alive string
}

var platformSources = map[model.PlatformType]platformSource{
	model.PlatformService:        {"service", "name", "deleted_at IS NULL"},
	model.PlatformTypeResource:   {"type_resource", "name", "deleted_at IS NULL"},
	model.PlatformGroup:          {"user_group", "name", "true"},
	model.PlatformGroupMember:    {"group_member", "id::text", "true"},
	model.PlatformRole:           {"role", "name", "true"},
	model.PlatformAssignmentRule: {"assignment_rule", "id::text", "true"},
}

var adminAccess = []string{string(model.ActionRead), string(model.ActionWrite)}

// SeedPlatform registers Licetum as a Service within itself and describes the access policies for
// the platform itself, so managing Licetum goes through the same permission mechanism as any other
// service:
//
//   - a resource type per policy (model.PlatformTypes), each with a Synchronizer(kind=sql) reading
//     its own table of this very instance — without it a type would have no resources and hence no
//     whole-type access (that is expanded into entries per resource);
//   - the platform admin group (PLATFORM__ADMIN_GROUP_NAME), whose members are matched by a predicate
//     over claims (PLATFORM__ADMIN_CLAIM in PLATFORM__ADMIN_CLAIM_VALUES);
//   - rules granting that group read+write on every policy type as a whole.
//
// Silently skipped if PLATFORM__* OAuth2 is not configured. Safe to re-run: existing rows are never
// touched, and the admin group and its rules are only seeded in the run that creates the policy
// types — once an admin edits or removes them, a later deploy does not bring them back.
func SeedPlatform(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	p := cfg.Platform
	if !p.IsConfigured() {
		slog.Info("PLATFORM__* OAuth2 is not configured (authorization_url/token_url/jwks_url/" +
			"issuer/client_id/client_secret) — skipping the self-service seed. " +
			"Set the variables and run migrate again — the operation is idempotent.")
		return nil
	}

	var created []model.PlatformType
	err := InTx(ctx, pool, func(tx pgx.Tx) error {
		created = nil
		// Parallel runs of the seed (the pods of a release start together) take turns: the one that comes second finds the
		// rows of the first and creates nothing. The lock is released with the transaction.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, seedLockKey); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO service (id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO NOTHING`,
			model.PlatformServiceID, p.Name, model.PlatformSlug, p.URL, p.Icon, p.AuthorizationURL, p.TokenURL, p.JwksURL,
			p.Issuer, p.ClientID, p.ClientSecret,
		); err != nil {
			return err
		}

		for _, t := range model.PlatformTypes {
			ok, err := seedPlatformType(ctx, tx, cfg.DB.URI, t)
			if err != nil {
				return fmt.Errorf("policy %s: %w", t, err)
			}
			if ok {
				created = append(created, t)
			}
		}
		if len(created) == 0 {
			return nil
		}
		if err := seedAdminGroup(ctx, tx, p); err != nil {
			return err
		}
		// New policies and an admin group: the replicas that run already must read the rules again.
		_, err := BumpRevision(ctx, tx, ScopeRules)
		return err
	})
	if err != nil {
		return fmt.Errorf("seed self-service: %w", err)
	}
	if len(created) > 0 {
		slog.Info("self-service seeded", "service_id", model.PlatformServiceID, "new_policies", created)
	}
	return nil
}

// seedPlatformType creates the resource type of a policy and its synchronizer; reports whether the
// type was created now (false if it already existed, even soft-deleted).
func seedPlatformType(ctx context.Context, tx pgx.Tx, dsn string, t model.PlatformType) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO type_resource (id, service_id, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`,
		model.PlatformTypeResourceID(t), model.PlatformServiceID, string(t),
	)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}

	// A synchronizer pointing at itself: kind=sql, the dsn is our own DB__URI, the query reads our
	// own table.
	src := platformSources[t]
	list := fmt.Sprintf("SELECT id::text AS id, %s AS name, NULL::text AS parent_id FROM %s WHERE %s",
		src.name, src.table, src.alive)
	// cache_ttl 0: the lists of the platform's own entities are never kept, or an entity created a moment ago would
	// be missing from them (and answered with 403) until the cached list ran out.
	syncConfig, err := json.Marshal(map[string]any{
		"dsn":        dsn,
		"query":      list + " AND id::text = :id",
		"list_query": list,
		"cache_ttl":  0,
	})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO synchronizer (id, type_resource_id, kind, config)
		VALUES ($1, $2, 'SQL', $3::jsonb)
		ON CONFLICT (id) DO NOTHING`,
		model.PlatformSynchronizerID(t), model.PlatformTypeResourceID(t), string(syncConfig),
	)
	return err == nil, err
}

func seedAdminGroup(ctx context.Context, tx pgx.Tx, p config.Platform) error {
	if _, err := tx.Exec(ctx, `INSERT INTO user_group (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		model.PlatformAdminGroupID, p.AdminGroupName); err != nil {
		if IsUniqueViolation(err) {
			return fmt.Errorf("a group named %q already exists — set another PLATFORM__ADMIN_GROUP_NAME", p.AdminGroupName)
		}
		return err
	}

	if len(p.AdminClaimValues) > 0 {
		// Who belongs to the group: the claim takes one of the configured values.
		predicate, err := json.Marshal([]model.PredicateCondition{
			{Attr: p.AdminClaim, Op: model.OpIn, Value: model.PredicateValue{List: p.AdminClaimValues, IsList: true}},
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_member (id, group_id, member_group_id, predicate, enabled)
			VALUES ($1, $2, NULL, $3::jsonb, true)
			ON CONFLICT (id) DO NOTHING`,
			model.PlatformAdminGroupMemberID, model.PlatformAdminGroupID, string(predicate),
		); err != nil {
			return err
		}
	} else {
		slog.Warn("PLATFORM__ADMIN_CLAIM_VALUES is not set — the admin group is created without members, "+
			"nobody can manage the platform yet", "group", p.AdminGroupName)
	}

	// The group gets access to every platform type as a whole (resource_id = NULL).
	for _, t := range model.PlatformTypes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO assignment_rule (id, type_resource_id, resource_id, access, subject, predicate, enabled)
			VALUES ($1, $2, NULL, $3, $4, '[]'::jsonb, true)
			ON CONFLICT (id) DO NOTHING`,
			model.PlatformAdminRuleID(t), model.PlatformTypeResourceID(t), adminAccess,
			model.GroupSubject(model.PlatformAdminGroupID),
		); err != nil {
			return err
		}
	}
	return nil
}
