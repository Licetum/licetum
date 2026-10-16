package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/redisx"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// replyErr is an error reply of Redis (go-redis marks them with RedisError).
type replyErr string

func (e replyErr) Error() string { return string(e) }
func (replyErr) RedisError()     {}

var _ redis.Error = replyErr("")

func TestErrKindGroupsFailuresByTheirKindNotTheirText(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, "none"},
		{redisx.ErrSkipped, "skipped"},
		{fmt.Errorf("wrapped: %w", redisx.ErrSkipped), "skipped"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "read", Net: "tcp", Err: timeoutErr{}}, "timeout"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection"},
		{fmt.Errorf("read tcp 127.0.0.1:50234->127.0.0.1:6379: %w", syscall.ECONNRESET), "connection"},
		{replyErr("READONLY You can't write against a read only replica."), "redis:READONLY"},
		{fmt.Errorf("set: %w", replyErr("LOADING Redis is loading the dataset in memory")), "redis:LOADING"},
		{&pgconn.PgError{Code: "57P01"}, "postgres:57P01"},
		{errors.New("something else"), "other"},
	} {
		if got := errKind(c.err); got != c.want {
			t.Errorf("errKind(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	// Two timeouts that differ in the port of the connection are the same kind.
	a := errKind(&net.OpError{Op: "read", Net: "tcp", Addr: &net.TCPAddr{Port: 1}, Err: timeoutErr{}})
	b := errKind(&net.OpError{Op: "read", Net: "tcp", Addr: &net.TCPAddr{Port: 2}, Err: timeoutErr{}})
	if a != b {
		t.Errorf("timeouts on different ports are different kinds: %q and %q", a, b)
	}
}
