package engine

import (
	"context"
	"testing"
)

func TestConsistencyInIsEmptyUntilSet(t *testing.T) {
	if c, ok := ConsistencyIn(context.Background()); ok || c != "" {
		t.Fatalf("a context without the lever must say so: %q %v", c, ok)
	}
}

func TestWithConsistencyCarriesBothLevels(t *testing.T) {
	for _, want := range []Consistency{MinimizeLatency, HigherConsistency} {
		got, ok := ConsistencyIn(WithConsistency(context.Background(), want))
		if !ok || got != want {
			t.Errorf("%q came back as %q (set: %v)", want, got, ok)
		}
	}
}

func TestWithConsistencyIgnoresWhatIsNoLevel(t *testing.T) {
	base := WithConsistency(context.Background(), HigherConsistency)
	for _, bad := range []Consistency{"", "HIGHER", "eventual"} {
		ctx := WithConsistency(base, bad)
		if got, ok := ConsistencyIn(ctx); !ok || got != HigherConsistency {
			t.Errorf("%q must leave the level of the context as it was: %q %v", bad, got, ok)
		}
		if _, ok := ConsistencyIn(WithConsistency(context.Background(), bad)); ok {
			t.Errorf("%q must not set a level", bad)
		}
	}
}

func TestWithConsistencyTheInnermostWins(t *testing.T) {
	ctx := WithConsistency(WithConsistency(context.Background(), HigherConsistency), MinimizeLatency)
	if got, _ := ConsistencyIn(ctx); got != MinimizeLatency {
		t.Fatalf("got %q", got)
	}
}

// The levers of a request are independent: the revision it asks for does not touch its consistency, nor the reverse.
func TestWithConsistencyAndWithMinRevisionDoNotMeet(t *testing.T) {
	ctx := WithMinRevision(WithConsistency(context.Background(), HigherConsistency), 7)
	if got, ok := ConsistencyIn(ctx); !ok || got != HigherConsistency {
		t.Errorf("consistency: %q %v", got, ok)
	}
	if got := minRevisionFrom(ctx); got != 7 {
		t.Errorf("min revision: %d", got)
	}
}
