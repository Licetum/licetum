package engine

import (
	"context"
	"maps"
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
	// readRevision is the lowest revision of the policy the request's answers were read at (see noteReadRevision).
	readRevision int64
	// syncPending says the answer lacks the data of a type that was not synchronized yet (see SyncResult.Pending).
	syncPending bool
	// What the request cost: statements sent to PostgreSQL and round trips to Redis, in total and by kind.
	dbQueries, redisTrips int
	dbOps, redisOps       map[string]int
	// state is the revisions the request read, kept for the rest of the request by a process that has no policy view in memory
	// (see App.cacheState): there every reading is a statement, and the authentication of a PEP, the lookup of the answer and
	// the decision would each send one.
	state *cacheState
}

// heldState is the revisions the request has already read, when it has.
func (t *Trace) heldState() (cacheState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == nil {
		return cacheState{}, false
	}
	return *t.state, true
}

// holdState keeps the revisions for the rest of the request. A write of the request drops them (setRevision): what it reads next
// has to be read again.
func (t *Trace) holdState(s cacheState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = &s
}

type traceKey struct{}

// WithTrace returns ctx carrying a Trace, and the Trace. A ctx that already carries one keeps it, so a test (or a
// middleware in front of the API) can create the Trace itself and read what the request did afterwards.
func WithTrace(ctx context.Context) (context.Context, *Trace) {
	if t := TraceFrom(ctx); t != nil {
		return ctx, t
	}
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
	t.state = nil
}

// ReadRevision is the revision of the policy the answers of the request were read at: the lowest one of them, 0 when the
// request read no answer. An answer reflects at least this revision, so the PDP reports it as Licetum-Revision.
func (t *Trace) ReadRevision() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.readRevision
}

// noteReadRevision records the revision of the policy an answer was read at (cachedAnswer). The lowest one is kept: a
// request whose answers were read at different revisions reflects only the lowest of them for every answer.
func (t *Trace) noteReadRevision(v int64) {
	if v <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.readRevision == 0 || v < t.readRevision {
		t.readRevision = v
	}
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
// check API reports it in the X-Authz-Sync header. A test that needs to know whether an answer is complete reads it here
// (the Trace is created by the test, see WithTrace) and not from the header, which is not part of the surface to come.
func (t *Trace) SyncPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.syncPending
}

// DBQueries is how many statements the request sent to PostgreSQL (a statement of a batch counts one each; the
// BEGIN and COMMIT of a transaction are statements too). Work the request left running after its answer (the write
// of a cache entry, a refresh in the background) is not part of it; the Prometheus counters include it.
func (t *Trace) DBQueries() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dbQueries
}

// DBQueryOps is DBQueries by kind of statement (the first SQL keyword: "select", "insert", ...).
func (t *Trace) DBQueryOps() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return maps.Clone(t.dbOps)
}

// RedisRoundTrips is how many round trips the request made to Redis: a pipeline is one, whatever its size.
func (t *Trace) RedisRoundTrips() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.redisTrips
}

// RedisOps is RedisRoundTrips by command ("get", "set", ...; "pipeline" for a pipeline).
func (t *Trace) RedisOps() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return maps.Clone(t.redisOps)
}

func (t *Trace) addDB(operation string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dbOps == nil {
		t.dbOps = map[string]int{}
	}
	t.dbQueries++
	t.dbOps[operation]++
}

func (t *Trace) addRedis(operation string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.redisOps == nil {
		t.redisOps = map[string]int{}
	}
	t.redisTrips++
	t.redisOps[operation]++
}
