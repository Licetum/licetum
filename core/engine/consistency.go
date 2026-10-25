package engine

import "context"

type consistencyKey struct{}

// WithConsistency asks that the requests made with ctx are answered with the freshness c (see ConsistencyIn). It is a lever of the process that serves the
// request, a test harness or an internal caller: no network input sets it, and a value that is none of the two
// Consistency constants is ignored.
func WithConsistency(ctx context.Context, c Consistency) context.Context {
	if c != MinimizeLatency && c != HigherConsistency {
		return ctx
	}
	return context.WithValue(ctx, consistencyKey{}, c)
}

// ConsistencyIn is the freshness WithConsistency put in ctx, and whether it did. The API layer reads it and no network input
// sets it.
func ConsistencyIn(ctx context.Context) (Consistency, bool) {
	c, ok := ctx.Value(consistencyKey{}).(Consistency)
	return c, ok
}
