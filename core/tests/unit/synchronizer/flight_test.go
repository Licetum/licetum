package synchronizer_test

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/synchronizer"
)

// heldSource is an HTTP source whose answers wait until the test lets them go.
type heldSource struct {
	calls   atomic.Int32
	release chan struct{}
}

func newHeldSource() *heldSource { return &heldSource{release: make(chan struct{})} }

func (s *heldSource) client() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		s.calls.Add(1)
		select {
		case <-s.release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"id":"a","name":"A"}]`)), Request: r}, nil
	})}
}

func (s *heldSource) waitForCalls(t *testing.T, n int32) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); s.calls.Load() < n; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the source was asked %d times, want %d", s.calls.Load(), n)
		}
	}
}

// waitForJoined waits until n goroutines are inside singleflight's Do: the one whose request is in flight and the n-1 that have
// joined it. A goroutine that has not reached Do yet is not counted, so the test lets the source answer only after every
// caller has joined, however slow the scheduler is.
func waitForJoined(t *testing.T, n int) {
	t.Helper()
	buf := make([]byte, 1<<20)
	count := func() int {
		stacks := string(buf[:runtime.Stack(buf, true)])
		in := 0
		for _, g := range strings.Split(stacks, "\n\n") {
			if strings.Contains(g, "singleflight.(*Group).Do(") {
				in++
			}
		}
		return in
	}
	for deadline := time.Now().Add(5 * time.Second); count() < n; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d callers are inside the shared request, want %d", count(), n)
		}
	}
}

var listConfig = model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/list"}

// Concurrent requests for the list of one synchronizer share one request to its source.
func TestConcurrentFetchesOfOneSourceShareOneRequest(t *testing.T) {
	src := newHeldSource()
	adapter := synchronizer.NewAdapterWithHTTPClient(src.client())
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			rows, err := adapter.FetchAll(context.Background(), listConfig, "sync-1", false)
			if err != nil || len(rows) != 1 {
				t.Errorf("rows = %v, err = %v", rows, err)
			}
		})
	}
	src.waitForCalls(t, 1)
	waitForJoined(t, 5) // the others have joined the request that is in flight
	close(src.release)
	wg.Wait()
	if got := src.calls.Load(); got != 1 {
		t.Fatalf("the source was asked %d times by concurrent fetches, want 1", got)
	}
}

// A fresh fetch (a request that needs the source as it is now) never joins a fetch that is not fresh: that one may have started
// before, and read the source before the write the fresh request is waiting for. It asks the source again.
func TestAFreshFetchDoesNotJoinAFetchThatStartedBeforeIt(t *testing.T) {
	src := newHeldSource()
	adapter := synchronizer.NewAdapterWithHTTPClient(src.client())
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := adapter.FetchAll(context.Background(), listConfig, "sync-1", false); err != nil {
			t.Errorf("the earlier fetch: %v", err)
		}
	})
	src.waitForCalls(t, 1)
	wg.Go(func() {
		if _, err := adapter.FetchAll(context.Background(), listConfig, "sync-1", true); err != nil {
			t.Errorf("the fresh fetch: %v", err)
		}
	})
	src.waitForCalls(t, 2) // the fresh fetch asked the source while the first was still waiting for its answer
	close(src.release)
	wg.Wait()
	if got := src.calls.Load(); got != 2 {
		t.Fatalf("the source was asked %d times, want 2 (one fetch and one fresh fetch)", got)
	}
}

// Fresh fetches of one synchronizer share a request among themselves, as the others do.
func TestConcurrentFreshFetchesShareOneRequest(t *testing.T) {
	src := newHeldSource()
	adapter := synchronizer.NewAdapterWithHTTPClient(src.client())
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := adapter.FetchAll(context.Background(), listConfig, "sync-1", true); err != nil {
				t.Errorf("fresh fetch: %v", err)
			}
		})
	}
	src.waitForCalls(t, 1)
	waitForJoined(t, 3)
	close(src.release)
	wg.Wait()
	if got := src.calls.Load(); got != 1 {
		t.Fatalf("the source was asked %d times by concurrent fresh fetches, want 1", got)
	}
}

// A check before saving has no key and shares nothing: every call asks the source.
func TestACheckBeforeSavingAsksTheSourceEveryTime(t *testing.T) {
	src := newHeldSource()
	close(src.release)
	adapter := synchronizer.NewAdapterWithHTTPClient(src.client())
	for range 3 {
		if _, err := adapter.FetchAll(context.Background(), listConfig, "", true); err != nil {
			t.Fatal(err)
		}
	}
	if got := src.calls.Load(); got != 3 {
		t.Fatalf("the source was asked %d times, want 3", got)
	}
}

// A source that fails is asked again by the next call: the adapter keeps no memory of failures (the backoff is the
// engine's, in sync_state).
func TestTheAdapterDoesNotHoldBackAFailingSource(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("boom")), Request: r}, nil
	})}
	adapter := synchronizer.NewAdapterWithHTTPClient(client)
	for range 8 {
		if _, err := adapter.FetchAll(context.Background(), listConfig, "sync-1", false); err == nil {
			t.Fatal("a failing source answered")
		}
	}
	if got := calls.Load(); got != 8 {
		t.Fatalf("the source was asked %d times in 8 sequential fetches, want 8", got)
	}
}
