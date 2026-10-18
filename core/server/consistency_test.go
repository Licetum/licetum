package server

import (
	"context"
	"testing"

	"github.com/licetum/licetum/core/engine"
)

func TestConsistencyParamTakesTheQueryWhenTheContextHasNoLever(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		query string
		want  engine.Consistency
	}{
		{"", engine.MinimizeLatency},
		{"minimize_latency", engine.MinimizeLatency},
		{"higher", engine.HigherConsistency},
		{"anything else", engine.MinimizeLatency},
	} {
		if got := (ConsistencyParam{Consistency: c.query}).valueIn(ctx); got != c.want {
			t.Errorf("?consistency=%q: got %q, want %q", c.query, got, c.want)
		}
	}
}

func TestConsistencyParamLetsTheContextWin(t *testing.T) {
	for _, c := range []struct {
		lever, query, want engine.Consistency
	}{
		{engine.HigherConsistency, "", engine.HigherConsistency},
		{engine.HigherConsistency, engine.MinimizeLatency, engine.HigherConsistency},
		{engine.HigherConsistency, engine.HigherConsistency, engine.HigherConsistency},
		{engine.MinimizeLatency, engine.HigherConsistency, engine.MinimizeLatency},
	} {
		ctx := engine.WithConsistency(context.Background(), c.lever)
		if got := (ConsistencyParam{Consistency: string(c.query)}).valueIn(ctx); got != c.want {
			t.Errorf("lever %q with ?consistency=%q: got %q, want %q", c.lever, c.query, got, c.want)
		}
	}
}

// The revision a caller asks for is a separate lever and keeps its own carrier.
func TestConsistencyParamFreshnessLeavesTheConsistencyAlone(t *testing.T) {
	ctx := engine.WithConsistency(context.Background(), engine.HigherConsistency)
	p := ConsistencyParam{AtLeastAsFresh: 12}
	ctx = p.freshness(ctx)
	if got := p.valueIn(ctx); got != engine.HigherConsistency {
		t.Fatalf("got %q", got)
	}
}
