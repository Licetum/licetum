package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/redisx"
)

// logDependency logs the failure of a dependency (Redis, PostgreSQL) on the path of a request. A dependency that is down
// makes every request fail the same way, hundreds of times a second: the records go through logx.Repeated, so each kind of
// failure of each message is written once a second, and the record that follows says how many were left out
// (licetum_log_suppressed_total counts them all).
func logDependency(ctx context.Context, level slog.Level, msg string, err error) {
	logx.Repeated.Log(ctx, level, msg+"|"+errKind(err), msg, "error", err)
}

// errKind sorts a failure into a few kinds, so that failures of one kind are one repeated message although their texts
// differ (the port of the connection, the duration of the wait).
func errKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, redisx.ErrSkipped):
		return "skipped"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var reply redis.Error
	if errors.As(err, &reply) {
		word, _, _ := strings.Cut(reply.Error(), " ") // READONLY, LOADING, OOM, ...
		return "redis:" + strings.ToUpper(word)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return "postgres:" + pgErr.Code
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "connection"
	}
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return "connection"
	}
	return "other"
}
