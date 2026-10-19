package server

import (
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
)

// clientLimits is the rate limit of the PEP clients of one PDP pod (PEP__RATE_LIMIT, PEP__RATE_BURST): a token bucket per client.
// Pods share no state, so the limit applies per pod and the limit of the fleet grows with the number of pods; a global limit per
// client belongs to the ingress or the gateway in front of the PDPs (ADR 0001, 7.11).
type clientLimits struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newClientLimits makes the limits of cfg, or nil when the rate is 0: the limit is off.
func newClientLimits(cfg config.PEP, now func() time.Time) *clientLimits {
	if cfg.RateLimit <= 0 {
		return nil
	}
	burst := float64(cfg.Burst)
	if burst <= 0 {
		burst = float64(cfg.RateLimit)
	}
	return &clientLimits{rate: float64(cfg.RateLimit), burst: max(burst, 1), now: now, buckets: map[uuid.UUID]*bucket{}}
}

// allow takes a token of the client's bucket. When the bucket is empty it says how long until a token is there.
func (l *clientLimits) allow(client uuid.UUID) (ok bool, wait time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[client]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[client] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration(math.Ceil((1 - b.tokens) / l.rate * float64(time.Second)))
}
