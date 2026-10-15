package engine

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// backgroundTimeout bounds one piece of background work: a cache write or a refresh of the resource data.
const backgroundTimeout = 30 * time.Second

// closeWait is how long Close waits for the queued background work.
const closeWait = 10 * time.Second

// backgroundPool runs the work that is done after a request has been answered, on a fixed number of workers fed by a
// bounded queue. Nothing in it blocks the request: when the queue is full the work is dropped (it is optional: a cache
// write, a refresh that the next request will start again).
type backgroundPool struct {
	queue   chan func(ctx context.Context)
	pending sync.WaitGroup // queued or running work; Close waits for it
	workers sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

func newBackgroundPool(workers, queue int, run func(fn func(ctx context.Context))) *backgroundPool {
	p := &backgroundPool{queue: make(chan func(ctx context.Context), queue)}
	for range workers {
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			for fn := range p.queue {
				run(fn)
				p.pending.Done()
			}
		}()
	}
	return p
}

// submit queues fn. It reports false when the pool is closed or the queue is full.
func (p *backgroundPool) submit(fn func(ctx context.Context)) (accepted, closed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, true
	}
	p.pending.Add(1)
	select {
	case p.queue <- fn:
		return true, false
	default:
		p.pending.Done()
		return false, false
	}
}

// close refuses new work, waits up to wait for the queued work, then stops the workers (the ones still busy finish on
// their own). It reports whether all the work ended in time.
func (p *backgroundPool) close(wait time.Duration) bool {
	p.mu.Lock()
	already := p.closed
	p.closed = true
	if !already {
		close(p.queue)
	}
	p.mu.Unlock()
	done := make(chan struct{})
	go func() {
		p.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(wait):
		return false
	}
}

// Background runs fn after the request that started it has been answered, on the background workers. Close waits for
// the queued work. It reports whether fn was accepted: once Close has begun, or when the queue is full, the work is
// dropped (a full queue is counted in licetum_background_dropped_total) and the caller goes on. The work is optional:
// its cache writes and refreshes are never a reason to make a request wait.
func (a *App) Background(fn func(ctx context.Context)) bool {
	pool := a.background()
	accepted, closed := pool.submit(fn)
	if !accepted && !closed && a.metrics != nil {
		a.metrics.backgroundDropped.Inc()
	}
	return accepted
}

// background returns the pool of the App, starting it on first use.
func (a *App) background() *backgroundPool {
	a.bgOnce.Do(func() {
		t := newTuning(a.Cfg)
		a.bg = newBackgroundPool(t.workers, t.queue, func(fn func(ctx context.Context)) {
			defer func() {
				// The request that started this work has been answered; a panic here must not take the process down.
				if p := recover(); p != nil {
					slog.Error("background work panicked", "panic", p, "stack", string(debug.Stack()))
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
			defer cancel()
			fn(ctx)
		})
	})
	return a.bg
}
