package engine

import (
	"sync"
	"time"
)

// flusher calls flush every interval, and at once when kicked; closing it calls flush a last time (final = true) and
// waits for it. flush runs in one goroutine only, so it needs no lock against itself.
type flusher struct {
	kickCh chan struct{}
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func startFlusher(interval time.Duration, flush func(final bool)) *flusher {
	f := &flusher{kickCh: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(f.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-f.stop:
				flush(true)
				return
			case <-ticker.C:
			case <-f.kickCh:
			}
			flush(false)
		}
	}()
	return f
}

// kick asks for a flush before the next tick; it never blocks.
func (f *flusher) kick() {
	select {
	case f.kickCh <- struct{}{}:
	default:
	}
}

// close stops the flusher after a final flush, waiting at most wait for it.
func (f *flusher) close(wait time.Duration) {
	f.once.Do(func() { close(f.stop) })
	select {
	case <-f.done:
	case <-time.After(wait):
	}
}
