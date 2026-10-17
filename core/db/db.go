// Package db holds the connection pool to Licetum's own DB, the migrations and the self-service seed.
package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/config"
)

// Querier is the interface shared by the pool and a transaction: the service layer writes a query once
// and can run it either in autocommit mode or inside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NormalizeDSN strips the SQLAlchemy driver suffix from the URI scheme
// (postgresql+asyncpg://… → postgresql://…), so old DB__URI values and synchronizer DSNs
// saved by the Python version keep working.
func NormalizeDSN(dsn string) string {
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return dsn
	}
	if base, _, hasDriver := strings.Cut(scheme, "+"); hasDriver {
		return base + "://" + rest
	}
	return dsn
}

// int32Of returns v as the int32 a pool setting takes, or an error naming the setting when v does not fit.
func int32Of(name string, v int64) (int32, error) {
	if v > math.MaxInt32 || v < math.MinInt32 {
		return 0, fmt.Errorf("%s does not fit in 32 bits", name)
	}
	return int32(v), nil
}

// Connect creates the pool. pool_size + max_overflow from the old settings are added up into
// MaxConns — pgxpool does not distinguish "permanent" and "overflow" connections.
func Connect(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(NormalizeDSN(cfg.URI))
	if err != nil {
		return nil, fmt.Errorf("parse DB__URI: %w", err)
	}

	// The sum is taken in 64 bits and checked before it becomes the int32 the pool takes: a value from the
	// environment that does not fit must be an error, not a pool of a different size.
	maxConns := int64(cfg.PoolSize) + int64(cfg.MaxOverflow)
	if maxConns <= 0 {
		maxConns = 10
	}
	maxConnsN, err := int32Of("DB__POOL_SIZE + DB__MAX_OVERFLOW", maxConns)
	if err != nil {
		return nil, err
	}
	poolCfg.MaxConns = maxConnsN
	if cfg.PoolSize > 0 {
		poolCfg.MinConns = 0
		minIdle, err := int32Of("DB__POOL_SIZE", min(int64(cfg.PoolSize), maxConns))
		if err != nil {
			return nil, err
		}
		poolCfg.MinIdleConns = minIdle
	}
	if cfg.PoolRecycle > 0 {
		poolCfg.MaxConnLifetime = cfg.PoolRecycle
	}
	switch {
	case cfg.PgBouncer:
		// Behind PgBouncer in transaction mode the next statement may run on another server connection, which has not
		// prepared what this one prepared: nothing is prepared, and the statements travel by the simple protocol.
		poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		poolCfg.ConnConfig.StatementCacheCapacity = 0
		poolCfg.ConnConfig.DescriptionCacheCapacity = 0
	case cfg.StatementCacheSize > 0:
		poolCfg.ConnConfig.StatementCacheCapacity = cfg.StatementCacheSize
	}
	// The simple protocol sends the arguments as text and has no description of the statement to say their types, so pgx
	// derives the type from the Go type of the argument: the ones it does not know are told to it here.
	poolCfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		types := conn.TypeMap()
		types.RegisterDefaultPgType(uuid.UUID{}, "uuid")
		types.RegisterDefaultPgType([]uuid.UUID{}, "_uuid")
		return nil
	}
	// The tracer is always there: it counts statements for the metrics and for the tests (see Observe); DB__ECHO
	// only adds the log of every SQL text.
	poolCfg.ConnConfig.Tracer = &Tracer{echo: cfg.Echo}

	connectCtx := ctx
	if cfg.PoolTimeout > 0 {
		var cancel context.CancelFunc
		connectCtx, cancel = context.WithTimeout(ctx, cfg.PoolTimeout)
		defer cancel()
	}

	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to the DB: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the DB: %w", err)
	}
	return pool, nil
}

// InTx runs fn in a transaction: commit on nil, rollback on error.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, fn)
}

// IsUniqueViolation reports a UNIQUE constraint violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsPrimaryKeyViolation reports an insert whose primary key another row already has: a create that was given an id
// belonging to another entity (SQLSTATE 23505 on a *_pkey constraint).
func IsPrimaryKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasSuffix(pgErr.ConstraintName, "_pkey")
}

// IsUnavailable reports that the database could not be reached, dropped the connection, or had no connection to give —
// as opposed to an error of the statement itself (a constraint, a syntax error, a missing row). The caller can do nothing
// about the first kind but wait, so it answers 503 and not 500. A deadline of the caller's own context is not this kind.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"): // connection_exception
			return true
		case pgErr.Code == "53300", // too_many_connections
			pgErr.Code == "57P01", pgErr.Code == "57P02", pgErr.Code == "57P03": // admin_shutdown, crash_shutdown, cannot_connect_now
			return true
		}
		return false
	}
	var connErr *pgconn.ConnectError
	var opErr *net.OpError
	return errors.As(err, &connErr) || errors.As(err, &opErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
