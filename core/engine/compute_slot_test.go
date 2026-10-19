package engine

import (
	"context"
	"testing"
	"time"
)

// A computation that waits for the worker gives its slot back for the wait, and takes one again, queueing like any other
// computation, before it reads the database again.
func TestAComputationGivesItsSlotBackWhileItWaitsForTheWorker(t *testing.T) {
	slots := make(chan struct{}, 1)
	waiting := &computeSlot{slots: slots}
	if err := waiting.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	resume := pauseComputeSlot(context.WithValue(context.Background(), computeSlotKey{}, waiting))

	other := &computeSlot{slots: slots}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := other.acquire(ctx); err != nil {
		t.Fatalf("the only slot stayed held through the wait: %v", err)
	}

	resumed := make(chan error, 1)
	go func() { resumed <- resume(context.Background()) }()
	select {
	case err := <-resumed:
		t.Fatalf("the computation went on while the only slot was held by another (%v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	if !other.release() {
		t.Fatal("the other computation did not hold its slot")
	}
	if err := <-resumed; err != nil {
		t.Fatal(err)
	}
	if !waiting.release() || waiting.release() {
		t.Fatal("the slot taken again must be released once, and only once")
	}
	if len(slots) != 0 {
		t.Fatalf("%d slots left held", len(slots))
	}
}

// Outside a computation that holds a slot (a cached answer without slots, a tool), the pause does nothing.
func TestPausingWithoutASlotDoesNothing(t *testing.T) {
	if err := pauseComputeSlot(context.Background())(context.Background()); err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	idle := &computeSlot{slots: slots}
	if err := pauseComputeSlot(context.WithValue(context.Background(), computeSlotKey{}, idle))(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Fatal("resuming a computation that held no slot took one")
	}
}
