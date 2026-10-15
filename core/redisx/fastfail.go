package redisx

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrSkipped is the error of a command that was not sent because Redis is being skipped (see FastFail). A caller treats
// it as any other Redis error: the cache is optional.
var ErrSkipped = errors.New("redis is skipped after repeated failures")

// FastFail is a circuit breaker in front of one Redis client, kept in this process only: after Threshold failures in a
// row (timeouts, connection errors) the commands are not sent for Cooldown, and fail at once with ErrSkipped; then one
// command is let through as a probe (half-open): if it succeeds the commands flow again, if it fails Redis is skipped for
// another Cooldown. A request thus pays the timeout of Redis a few times, not once per command, while Redis is down or
// black-holed.
//
// An answer from Redis that is an error (READONLY, LOADING, a wrong type) is Redis talking, not Redis missing: it does
// not count. A missing key is not an error.
type FastFail struct {
	Threshold int
	Cooldown  time.Duration

	mu          sync.Mutex
	consecutive int
	openUntil   time.Time
	probing     bool
	now         func() time.Time
	skipped     atomic.Int64
}

// Default parameters of the fast-fail of the check API: three failures in a row skip Redis for five seconds.
const (
	FastFailThreshold = 3
	FastFailCooldown  = 5 * time.Second
)

// NewFastFail makes a FastFail with the default parameters.
func NewFastFail() *FastFail {
	return &FastFail{Threshold: FastFailThreshold, Cooldown: FastFailCooldown, now: time.Now}
}

// Open reports whether Redis is being skipped right now.
func (f *FastFail) Open() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.openUntil.IsZero() && f.clock().Before(f.openUntil)
}

func (f *FastFail) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// admit says whether a command may be sent, and whether it is the probe of a half-open breaker.
func (f *FastFail) admit() (allow, probe bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openUntil.IsZero() {
		return true, false
	}
	if f.clock().Before(f.openUntil) || f.probing {
		return false, false
	}
	f.probing = true
	return true, true
}

// record takes the outcome of a command that was sent.
func (f *FastFail) record(err error, probe bool) {
	failed := countsAsFailure(err)
	f.mu.Lock()
	defer f.mu.Unlock()
	if probe {
		f.probing = false
	}
	if errors.Is(err, context.Canceled) {
		// The caller gave up: nothing was learnt about Redis. It neither proves it healthy (a canceled probe must not close
		// the breaker) nor counts as a failure.
		return
	}
	switch {
	case !failed:
		f.consecutive = 0
		if probe || !f.openUntil.IsZero() {
			f.openUntil = time.Time{}
		}
	case probe:
		f.openUntil = f.clock().Add(f.Cooldown)
	default:
		f.consecutive++
		if f.consecutive >= f.Threshold {
			f.openUntil = f.clock().Add(f.Cooldown)
			f.consecutive = 0
		}
	}
}

// countsAsFailure is true for a failure to reach Redis: a timeout, a broken or refused connection, an exhausted pool.
func countsAsFailure(err error) bool {
	if err == nil || errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
		return false
	}
	var reply redis.Error
	return !errors.As(err, &reply)
}

func (f *FastFail) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (f *FastFail) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		allow, probe := f.admit()
		if !allow {
			f.skipped.Add(1)
			return ErrSkipped
		}
		err := next(ctx, cmd)
		f.record(err, probe)
		return err
	}
}

func (f *FastFail) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		allow, probe := f.admit()
		if !allow {
			f.skipped.Add(int64(len(cmds)))
			for _, c := range cmds {
				c.SetErr(ErrSkipped)
			}
			return ErrSkipped
		}
		err := next(ctx, cmds)
		f.record(err, probe)
		return err
	}
}
