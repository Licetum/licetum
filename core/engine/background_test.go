package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/licetum/licetum/core/config"
)

func newBackgroundApp(workers, queue int) *App {
	return &App{
		Cfg:     &config.Config{Background: config.Background{Workers: workers, Queue: queue}},
		metrics: newMetrics(),
	}
}

func TestBackgroundRunsOnABoundedNumberOfWorkers(t *testing.T) {
	a := newBackgroundApp(3, 100)
	var running, peak, done atomic.Int32
	release := make(chan struct{})
	for range 20 {
		if !a.Background(func(context.Context) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-release
			running.Add(-1)
			done.Add(1)
		}) {
			t.Fatal("work was dropped though the queue has room")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for running.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := running.Load(); got != 3 {
		t.Errorf("%d tasks run at once, want the 3 workers", got)
	}
	close(release)
	a.Close()
	if done.Load() != 20 {
		t.Errorf("Close returned with %d of 20 tasks done: it waits for the queued work", done.Load())
	}
	if peak.Load() > 3 {
		t.Errorf("peak concurrency %d exceeds the workers", peak.Load())
	}
}

func TestBackgroundDropsWhenTheQueueIsFullAndNeverBlocks(t *testing.T) {
	a := newBackgroundApp(1, 2)
	block := make(chan struct{})
	started := make(chan struct{})
	a.Background(func(context.Context) { close(started); <-block })
	<-started // the only worker is busy
	accepted := 0
	begin := time.Now()
	for range 10 {
		if a.Background(func(context.Context) {}) {
			accepted++
		}
	}
	if took := time.Since(begin); took > time.Second {
		t.Errorf("submitting blocked for %v", took)
	}
	if accepted != 2 {
		t.Errorf("%d tasks accepted, want the 2 the queue holds", accepted)
	}
	if got := counterValue(t, a.metrics, "licetum_background_dropped_total"); got != 8 {
		t.Errorf("licetum_background_dropped_total = %v, want 8", got)
	}
	close(block)
	a.Close()
}

func TestBackgroundSurvivesAPanicAndRefusesWorkAfterClose(t *testing.T) {
	a := newBackgroundApp(1, 10)
	var after sync.WaitGroup
	after.Add(1)
	a.Background(func(context.Context) { panic("boom") })
	a.Background(func(context.Context) { after.Done() })
	after.Wait() // the worker went on after the panic
	a.Close()
	if a.Background(func(context.Context) {}) {
		t.Error("work was accepted after Close")
	}
	if got := counterValue(t, a.metrics, "licetum_background_dropped_total"); got != 0 {
		t.Errorf("work refused by Close counted as dropped: %v", got)
	}
}

// counterValue is the value of an unlabelled counter of the metrics registry.
func counterValue(t *testing.T, m *metrics, name string) float64 {
	t.Helper()
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			var sum float64
			for _, metric := range f.GetMetric() {
				sum += metric.GetCounter().GetValue()
			}
			return sum
		}
	}
	return 0
}
