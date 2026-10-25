package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestErrKindGroupsFailuresByTheirKindNotTheirText(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, "none"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "read", Net: "tcp", Err: timeoutErr{}}, "timeout"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection"},
		{fmt.Errorf("read tcp 127.0.0.1:50234->127.0.0.1:5432: %w", syscall.ECONNRESET), "connection"},
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
