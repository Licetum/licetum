package engine

import (
	"context"
	"sync"
)

// Trace is what one request leaves for the layers below the API: the request id and the subject that makes
// the request (both for the logs), and the revision of the last write it made. The API creates a Trace per
// request (see WithTrace); the service layer only reports into it.
type Trace struct {
	mu        sync.Mutex
	requestID string
	actor     string
	revision  int64
	// syncPending says the answer lacks the data of a type that was not synchronized yet (see SyncResult.Pending).
	syncPending bool
}

type traceKey struct{}

// WithTrace returns ctx carrying a fresh Trace, and the Trace.
func WithTrace(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

func TraceFrom(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

// SetRequestID records the request id the logs carry.
func (t *Trace) SetRequestID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requestID = id
}

// SetActor records the subject that makes the request. It is called once the token is verified, so an
// unverified token never becomes the actor of a change.
func (t *Trace) SetActor(subject string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.actor = subject
}

// Revision is the revision of the last write of the request, 0 when it wrote nothing (or Redis failed, so
// the write has no revision to report). A client that passes it as at_least_as_fresh gets answers that
// reflect the write.
func (t *Trace) Revision() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.revision
}

func (t *Trace) setRevision(v int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revision = v
}

func (t *Trace) who() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.actor
}

func (t *Trace) Request() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requestID
}

// ActorFrom is the subject the change log names for a write (empty outside a request).
func ActorFrom(ctx context.Context) string {
	if t := TraceFrom(ctx); t != nil {
		return t.who()
	}
	return ""
}

type minRevisionKey struct{}

// WithMinRevision asks that the answers reflect every write up to revision v (see Trace.Revision). When the
// cache version is older than v, the answer is computed from the data instead of the cache.
func WithMinRevision(ctx context.Context, v int64) context.Context {
	if v <= 0 {
		return ctx
	}
	return context.WithValue(ctx, minRevisionKey{}, v)
}

func minRevisionFrom(ctx context.Context) int64 {
	v, _ := ctx.Value(minRevisionKey{}).(int64)
	return v
}

func (t *Trace) setSyncPending() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncPending = true
}

// SyncPending says the answer was made without the data of some type: its synchronization had not finished in time. The
// check API reports it in the X-Authz-Sync header.
func (t *Trace) SyncPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.syncPending
}
