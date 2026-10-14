package engine

import (
	"context"
	"strconv"
	"time"

	"github.com/licetum/licetum/core/redisx"
)

// observer feeds what the database pool and the Redis client report into the metrics of the App and into the
// Trace of the request the call belongs to (when the call ran with the context of a request).
type observer struct{ a *App }

func (o observer) ObserveQuery(ctx context.Context, operation string, took time.Duration, err error) {
	if m := o.a.metrics; m != nil {
		m.dbQueries.WithLabelValues(operation, resultOf(err)).Inc()
		m.dbQueryDuration.WithLabelValues(operation).Observe(took.Seconds())
	}
	if t := TraceFrom(ctx); t != nil {
		t.addDB(operation)
	}
}

// ObserveRedis counts a round trip in the metrics and, unless it is connection setup (redisx.Setup: traffic, not what the
// request asked for), in the Trace of its request.
func (o observer) ObserveRedis(ctx context.Context, operation string, commands int, took time.Duration, err error) {
	if m := o.a.metrics; m != nil {
		m.redisOps.WithLabelValues(operation, commandsLabel(commands), resultOf(err)).Inc()
		m.redisOpDuration.WithLabelValues(operation).Observe(took.Seconds())
	}
	if t := TraceFrom(ctx); t != nil && operation != redisx.Setup {
		t.addRedis(operation)
	}
}

func resultOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// commandsLabel keeps the number of commands of a round trip a label of bounded cardinality: exact up to 16, then
// "17-64", "65-256" and "257+".
func commandsLabel(n int) string {
	switch {
	case n <= 16:
		return strconv.Itoa(n)
	case n <= 64:
		return "17-64"
	case n <= 256:
		return "65-256"
	}
	return "257+"
}
