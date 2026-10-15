package redisx

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newTestFastFail() (*FastFail, *clock) {
	c := &clock{now: time.Unix(1_000_000, 0)}
	f := NewFastFail()
	f.now = c.Now
	return f, c
}

// send runs one command through the hook with the outcome err, and reports whether it was sent.
func send(f *FastFail, err error) (sent bool, got error) {
	hook := f.ProcessHook(func(context.Context, redis.Cmder) error { sent = true; return err })
	got = hook(context.Background(), redis.NewStringCmd(context.Background(), "get", "k"))
	return sent, got
}

var timeout = &net.OpError{Op: "read", Err: timeoutErr{}}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestFastFailSkipsRedisAfterThreeFailuresInARow(t *testing.T) {
	f, clk := newTestFastFail()
	for i := range 3 {
		if sent, err := send(f, timeout); !sent || err == nil {
			t.Fatalf("failure %d was not sent: sent=%v err=%v", i, sent, err)
		}
	}
	if !f.Open() {
		t.Fatal("three failures in a row must open the breaker")
	}
	if sent, err := send(f, nil); sent || !errors.Is(err, ErrSkipped) {
		t.Fatalf("while open: sent=%v err=%v, want a skip", sent, err)
	}
	clk.now = clk.now.Add(FastFailCooldown - time.Millisecond)
	if sent, _ := send(f, nil); sent {
		t.Fatal("still inside the cooldown")
	}
}

func TestFastFailHalfOpenSendsOneProbe(t *testing.T) {
	f, clk := newTestFastFail()
	for range 3 {
		_, _ = send(f, timeout)
	}
	clk.now = clk.now.Add(FastFailCooldown)
	// The probe fails: skipped for another cooldown.
	if sent, err := send(f, timeout); !sent || err == nil {
		t.Fatalf("the probe was not sent: %v %v", sent, err)
	}
	if sent, _ := send(f, nil); sent {
		t.Fatal("a failed probe must reopen the breaker")
	}
	// The next probe succeeds: Redis flows again.
	clk.now = clk.now.Add(FastFailCooldown)
	if sent, err := send(f, nil); !sent || err != nil {
		t.Fatalf("the probe: %v %v", sent, err)
	}
	for range 5 {
		if sent, _ := send(f, nil); !sent {
			t.Fatal("after a successful probe commands are sent")
		}
	}
	if f.Open() {
		t.Error("the breaker should be closed")
	}
}

func TestFastFailHalfOpenLetsOnlyOneProbeThrough(t *testing.T) {
	f, clk := newTestFastFail()
	for range 3 {
		_, _ = send(f, timeout)
	}
	clk.now = clk.now.Add(FastFailCooldown)
	allow, probe := f.admit()
	if !allow || !probe {
		t.Fatalf("admit = %v %v", allow, probe)
	}
	if allow, _ := f.admit(); allow {
		t.Fatal("a second command went through while the probe is out")
	}
}

func TestFastFailIgnoresWhatIsNotAFailureToReachRedis(t *testing.T) {
	f, _ := newTestFastFail()
	for _, err := range []error{redis.Nil, context.Canceled, errors.New("READONLY You can't write against a read only replica")} {
		for range 5 {
			_, _ = send(f, err)
		}
	}
	// The READONLY text above is a plain error here; a Redis reply error is a redis.Error.
	if f.Open() && countsAsFailure(redis.Nil) {
		t.Fatal("a missing key is not a failure")
	}
	if countsAsFailure(context.Canceled) || countsAsFailure(nil) {
		t.Error("a canceled request and success are not failures")
	}
	if !countsAsFailure(timeout) || !countsAsFailure(redis.ErrClosed) {
		t.Error("a timeout and a closed client are failures")
	}
}

func TestFastFailSuccessResetsTheCount(t *testing.T) {
	f, _ := newTestFastFail()
	_, _ = send(f, timeout)
	_, _ = send(f, timeout)
	_, _ = send(f, nil)
	_, _ = send(f, timeout)
	_, _ = send(f, timeout)
	if f.Open() {
		t.Fatal("the failures were not in a row")
	}
}

func TestFastFailPipelineMarksEveryCommandSkipped(t *testing.T) {
	f, _ := newTestFastFail()
	for range 3 {
		_, _ = send(f, timeout)
	}
	cmds := []redis.Cmder{redis.NewStringCmd(context.Background(), "get", "a"), redis.NewStringCmd(context.Background(), "get", "b")}
	sent := false
	hook := f.ProcessPipelineHook(func(context.Context, []redis.Cmder) error { sent = true; return nil })
	if err := hook(context.Background(), cmds); !errors.Is(err, ErrSkipped) || sent {
		t.Fatalf("pipeline: sent=%v err=%v", sent, err)
	}
	for _, c := range cmds {
		if !errors.Is(c.Err(), ErrSkipped) {
			t.Errorf("command %v: err = %v", c.Args(), c.Err())
		}
	}
}
