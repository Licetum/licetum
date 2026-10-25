package logx

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Sampler lets one record per key through in every interval and counts the others. A dependency that is down makes the
// same call fail again and again, hundreds of times a second; one line a second per kind of failure says as much, and the
// line that follows says how many were left out.
type Sampler struct {
	interval time.Duration
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*sampled
	// suppressed counts the records left out since the process started (see Suppressed).
	suppressed atomic.Int64
}

type sampled struct {
	last       time.Time
	suppressed int
}

// maxSampledKeys bounds the memory of the sampler: the keys are meant to be a few dozen kinds of failure, but a caller
// that builds them from varying text must not be able to grow the map for ever.
const maxSampledKeys = 4096

// NewSampler returns a Sampler that lets one record per key through in every interval.
func NewSampler(interval time.Duration) *Sampler {
	return &Sampler{interval: interval, now: time.Now, entries: map[string]*sampled{}}
}

// NewSamplerWithClock is NewSampler with the clock the intervals are measured by (for the tests).
func NewSamplerWithClock(interval time.Duration, now func() time.Time) *Sampler {
	s := NewSampler(interval)
	s.now = now
	return s
}

// Repeated is the sampler of the process for the records a failing dependency repeats (PostgreSQL, the identity
// provider, a source of resources): one record per kind of failure per second.
var Repeated = NewSampler(time.Second)

// Allow reports whether a record under key may be written now. When it may, suppressed is the number of records under that
// key left out since the previous one that was written.
func (s *Sampler) Allow(key string) (ok bool, suppressed int) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.entries[key]
	if !found {
		if len(s.entries) >= maxSampledKeys {
			clear(s.entries)
		}
		s.entries[key] = &sampled{last: now}
		return true, 0
	}
	if now.Sub(e.last) < s.interval {
		e.suppressed++
		s.suppressed.Add(1)
		return false, 0
	}
	suppressed = e.suppressed
	e.last, e.suppressed = now, 0
	return true, suppressed
}

// Suppressed is how many records the sampler has left out since the process started.
func (s *Sampler) Suppressed() int64 { return s.suppressed.Load() }

// Log writes the record at the level when the sampler lets key through, with the attribute suppressed set to the number
// of records of that key left out before it.
func (s *Sampler) Log(ctx context.Context, level slog.Level, key, msg string, args ...any) {
	if !slog.Default().Enabled(ctx, level) {
		return // a record nobody would see does not use up the turn of the key
	}
	ok, suppressed := s.Allow(key)
	if !ok {
		return
	}
	if suppressed > 0 {
		args = append(args, "suppressed", suppressed)
	}
	slog.Log(ctx, level, msg, args...)
}
