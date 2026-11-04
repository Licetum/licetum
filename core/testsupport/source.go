package support

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// SourceCall is one request the fake source received.
type SourceCall struct {
	Kind string // the resource type: the {kind} of /sync/{kind}
	ID   string // empty for the list of the type
	At   time.Time
}

// Source is a fake resource source for synchronizers of kind http: it serves a list of resources per type at
// GET /sync/{kind} (and one at GET /sync/{kind}/{id}) and counts what is asked of it, so a test can say how often a
// source was called. The answer can be slowed down, made to fail, made large, or held until the test releases it.
type Source struct {
	server *httptest.Server

	mu     sync.Mutex
	rows   map[string][]map[string]any
	calls  []SourceCall
	delay  time.Duration
	status int
	size   int
	hold   chan struct{} // non-nil while the answers are held

	total       atomic.Int64
	inflight    atomic.Int64
	maxInflight atomic.Int64
}

// NewSource starts a fake source; it stops with the test.
func NewSource(t testing.TB) *Source {
	t.Helper()
	s := &Source{rows: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sync/{kind}", s.serve)
	mux.HandleFunc("GET /sync/{kind}/{id}", s.serve)
	s.server = httptest.NewServer(mux)
	t.Cleanup(func() {
		s.Release() // a request held at the end of the test must not block the shutdown
		s.server.Close()
	})
	return s
}

// URL is the base address; the list of a type is at URL()+"/sync/<kind>".
func (s *Source) URL() string { return s.server.URL }

// ListURL is the address of the list of a type, as a synchronizer of kind http takes it.
func (s *Source) ListURL(kind string) string { return s.server.URL + "/sync/" + kind }

// Set replaces the resources of a type. A row is {"id": ..., "name": ..., "parent_id": ...} plus any attributes.
func (s *Source) Set(kind string, rows ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[kind] = rows
}

// SetDelay makes every answer wait d first.
func (s *Source) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// SetStatus makes every request fail with the status (0 turns it off: the source answers again).
func (s *Source) SetStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

// SetBodySize pads every list to at least n bytes (with white space after the JSON, so it stays valid); 0 turns it off.
func (s *Source) SetBodySize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = n
}

// Hold makes every request wait, once it is counted, until Release. Requests that arrive while held pile up:
// WaitForCalls tells when enough of them have, InFlight how many wait.
func (s *Source) Hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hold == nil {
		s.hold = make(chan struct{})
	}
}

// Release lets the held requests go and stops holding new ones.
func (s *Source) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hold != nil {
		close(s.hold)
		s.hold = nil
	}
}

// Calls is how many requests the source received since the start or the last Reset.
func (s *Source) Calls() int { return int(s.total.Load()) }

// CallsTo is how many of them were for the type (the list and single resources together).
func (s *Source) CallsTo(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// History is the requests received, oldest first.
func (s *Source) History() []SourceCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SourceCall(nil), s.calls...)
}

// InFlight is how many requests are being answered now; MaxInFlight the most there were at once.
func (s *Source) InFlight() int    { return int(s.inflight.Load()) }
func (s *Source) MaxInFlight() int { return int(s.maxInflight.Load()) }

// Reset forgets the requests counted so far (not the resources or the settings).
func (s *Source) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
	s.total.Store(0)
	s.maxInflight.Store(s.inflight.Load())
}

// WaitForCalls waits until the source has received at least n requests and reports whether it did within d.
func (s *Source) WaitForCalls(n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s.Calls() >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return s.Calls() >= n
}

func (s *Source) serve(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("kind"), r.PathValue("id")
	n := s.inflight.Add(1)
	defer s.inflight.Add(-1)
	for {
		max := s.maxInflight.Load()
		if n <= max || s.maxInflight.CompareAndSwap(max, n) {
			break
		}
	}
	s.total.Add(1)

	s.mu.Lock()
	s.calls = append(s.calls, SourceCall{Kind: kind, ID: id, At: time.Now()})
	delay, status, size, hold := s.delay, s.status, s.size, s.hold
	rows := append([]map[string]any(nil), s.rows[kind]...)
	s.mu.Unlock()

	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}

	var body []byte
	if id != "" {
		for _, row := range rows {
			if row["id"] == id {
				body, _ = json.Marshal(row)
			}
		}
		if body == nil {
			http.NotFound(w, r)
			return
		}
	} else {
		if rows == nil {
			rows = []map[string]any{}
		}
		body, _ = json.Marshal(rows)
	}
	if pad := size - len(body); pad > 0 {
		body = append(body, strings.Repeat(" ", pad)...)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
