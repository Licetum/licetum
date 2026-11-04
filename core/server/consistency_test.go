package server

import (
	"context"
	"testing"

	"github.com/licetum/licetum/core/engine"
)

// No network request sets the freshness: the process that serves the request does, through the context, and without it the
// answer may come from the cache.
func TestConsistencyOfTakesTheLeverOfTheContext(t *testing.T) {
	if got := consistencyOf(context.Background()); got != engine.MinimizeLatency {
		t.Errorf("without a lever: got %q, want %q", got, engine.MinimizeLatency)
	}
	for _, lever := range []engine.Consistency{engine.HigherConsistency, engine.MinimizeLatency} {
		if got := consistencyOf(engine.WithConsistency(context.Background(), lever)); got != lever {
			t.Errorf("lever %q: got %q", lever, got)
		}
	}
}
