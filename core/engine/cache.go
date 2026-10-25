package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/licetum/licetum/core/db"
)

// The answers of the permission and check APIs are kept in the memory of the process. An answer is current while the rules
// have not changed — the revision of the policy, part of its key — and while the resource data it was computed from has
// not changed. Both numbers live in PostgreSQL, in the table revision_state, and change in the transaction that changes
// the data they number: a write of an administrator bumps the revision 'rules', a synchronization that changed the data
// of a service's types bumps 'data:<service>'. An answer records the data revisions of the services it read, so a change
// in one service leaves in place the answers that do not read it.

const (
	// computeTimeout bounds a computation that goes on after its first caller has gone away (singleflight).
	computeTimeout = 30 * time.Second
)

// cacheState is what a request reads before it computes or looks up an answer: the revision of the rules, the revision
// of the resource data of every service, and the names of the request attributes the rules look at.
type cacheState struct {
	// ok is false when the revisions could not be read: the answer is computed and not kept.
	ok      bool
	version int64            // the revision of the rules
	pep     int64            // the revision of the PEP directory (see PEPDirectory); no answer key depends on it
	data    map[string]int64 // service id -> revision of its resource data (absent: 0)
	attrs   []string
	// attrsOK says the attribute names are those of this revision (otherwise they are read from the rules).
	attrsOK bool
	// tag names the data versions the state holds (see dataTag).
	tag string
	// snap is the policy snapshot of the revision, when the process keeps the policy in memory (see localView).
	snap *snapshot
	// degraded says the revisions could not be read lately and the state is the last one this process read.
	degraded bool
}

// readCacheState reads the revisions from PostgreSQL, in one statement. An error leaves the state not ok.
func (a *App) readCacheState(ctx context.Context) cacheState {
	rev, err := db.LoadRevisions(ctx, a.DB)
	if err != nil {
		logDependency(ctx, slog.LevelWarn, "failed to read the revision of the policy", err)
		return cacheState{}
	}
	a.noteLogSettingsRevision(rev.LogSettings)
	state := cacheState{ok: true, version: rev.Rules, data: rev.Data, pep: rev.PEP}
	state.tag = dataTag(state.data)
	if memo := a.attrsMemo.Load(); memo != nil && memo.revision == rev.Rules {
		state.attrs, state.attrsOK = memo.attrs, true
	}
	return state
}

// attrsMemo remembers the attribute names of one revision of the rules, for the processes that do not keep the snapshot.
type attrsMemo struct {
	revision int64
	attrs    []string
}

// forConsistency is the state a request of the given consistency decides from. consistency=higher asks for the freshest
// answer there is: it reads the policy from the database, as it reads the sources again, and not from the snapshot.
func (s cacheState) forConsistency(ctx context.Context, c Consistency) cacheState {
	// A request that asks for a revision the view has not reached cannot be decided from the snapshot of the view either.
	if c.fresh() || minRevisionFrom(ctx) > s.version {
		s.snap = nil
	}
	return s
}

// current reports whether an answer computed from the data versions deps is still current.
func (s cacheState) current(deps map[string]int64) bool {
	for svc, v := range deps {
		if s.data[svc] != v {
			return false
		}
	}
	return true
}

// computed is an answer with what it depends on: the data versions it read, how long it is kept (ttl), and how long it stays
// correct with respect to validity windows (hold, at least ttl: see validityUntil).
type computed[T any] struct {
	value T
	deps  map[string]int64
	ttl   time.Duration
	hold  time.Duration
}

// cachedEntry is an answer as the cache keeps it. Exp is when it stops being current (unix milliseconds): the entry is
// served from memory until then, while PostgreSQL answers. Hold is when it stops being correct at all (no validity window
// starts or ends before it): a process that cannot reach PostgreSQL serves the answer until then (see stale).
type cachedEntry[T any] struct {
	Value T
	Deps  map[string]int64
	Exp   int64
	Hold  int64

	// serveUntil is the end of the time this process serves the entry from memory while PostgreSQL answers.
	serveUntil time.Time
}

// fresh: the entry may be served in the usual way.
func (e *cachedEntry[T]) fresh(now time.Time) bool { return now.Before(e.serveUntil) }

// stale: the entry is past its time, but nothing that could make it wrong has happened as far as this process can tell:
// the view of the policy it was made under is the one the process still holds, and no validity window has started or ended.
// It is served only while the process cannot read the revisions (a degraded view), for the time POLICY__MAX_STALENESS allows.
func (e *cachedEntry[T]) stale(now time.Time) bool {
	return e.Hold > 0 && now.Before(time.UnixMilli(e.Hold))
}

// cachedAnswer returns the answer of compute for key: from the cache while the entry there is current, otherwise
// computed. Concurrent misses for the same key share one computation (singleflight), and the answer is kept in memory.
// consistency=higher skips the read but still refreshes the entry. When the revisions cannot be read at all (state.ok
// false) the answer is computed each time and nothing is kept.
func cachedAnswer[T any](ctx context.Context, a *App, key string, state cacheState, consistency Consistency,
	results *prometheus.CounterVec, compute func(ctx context.Context) (computed[T], error)) (T, error) {
	var zero T
	if !state.ok {
		c, err := compute(ctx)
		if err != nil {
			return zero, err
		}
		return c.value, nil
	}
	// The revision the answer is read at is the one the PDP reports (Licetum-Revision), even when the answer is computed from the
	// data below: it then reflects at least this revision.
	if t := TraceFrom(ctx); t != nil {
		t.noteReadRevision(state.version)
	}

	store := true
	if minRevisionFrom(ctx) > state.version {
		// This process did not reach the revision the caller asks for (see WithMinRevision) within REVISION__WAIT_MAX:
		// the answer is computed from the data, and not kept under a revision it could not be confirmed to reflect.
		consistency = HigherConsistency
		store = false
	}

	flight := key + "|" + state.tag
	if consistency.fresh() {
		results.WithLabelValues("bypass").Inc()
		flight += ":fresh"
	} else {
		now := a.now()
		if v, ok := a.answersL1.Get(key, now); ok {
			// While the revisions cannot be read an answer is served past its time, as long as no validity window has started or
			// ended since: the process cannot compute another (the data it needs is in PostgreSQL), and the policy it holds is
			// the one it was told to trust for POLICY__MAX_STALENESS.
			if entry, ok := v.(*cachedEntry[T]); ok && state.current(entry.Deps) {
				switch {
				case entry.fresh(now):
					a.metrics.l1Lookup("answer", true)
					results.WithLabelValues("hit").Inc()
					return entry.Value, nil
				case state.degraded && entry.stale(now):
					a.metrics.l1Lookup("answer", true)
					results.WithLabelValues("stale").Inc()
					return entry.Value, nil
				}
			}
		}
		a.metrics.l1Lookup("answer", false)
		results.WithLabelValues("miss").Inc()
	}

	// The computation is shared and goes on for the others, but the caller does not wait for it longer than its own request
	// lasts (HTTP__REQUEST_TIMEOUT): a request that ran out of time is answered, not held until the computation ends.
	flightResult := a.answers.DoChan(flight, func() (any, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		defer cancel()
		// A wave of misses (a revision moved under many hot keys) queues here instead of exhausting the database pool.
		if a.computeSlots != nil {
			slot := &computeSlot{slots: a.computeSlots}
			if err := slot.acquire(cctx); err != nil {
				return nil, err
			}
			defer slot.release()
			cctx = context.WithValue(cctx, computeSlotKey{}, slot)
		}
		started := a.now()
		c, err := compute(cctx)
		if err != nil {
			return nil, err
		}
		if !store {
			return c.value, nil
		}
		written := a.now()
		// Hold is counted from the start of the computation, which is not later than the moment its hold was measured from:
		// an answer is never served stale after the boundary it was made to stop at, however long the computation took.
		entry := cachedEntry[T]{Value: c.value, Deps: c.deps, Exp: written.Add(c.ttl).UnixMilli(),
			Hold: started.Add(c.hold).UnixMilli()}
		a.keepAnswerInMemory(key, &entry, written)
		return c.value, nil
	})
	select {
	case res := <-flightResult:
		if res.Err != nil {
			return zero, res.Err
		}
		return res.Val.(T), nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// computeSlot is the slot of COMPUTE__CONCURRENCY one computation of an answer holds (see cachedAnswer). The slots bound
// the computations that read the database. A computation that waits for something else — a synchronization job, which
// the worker runs (see ensure) — gives its slot back for the wait (pauseComputeSlot): otherwise the requests that wait for
// the worker, consistency=higher ones above all (each waits for a fresh job, up to SYNC__FRESH_MIN_INTERVAL apart), hold
// every slot while the database is idle, and the requests behind them queue until they time out.
type computeSlot struct {
	slots chan struct{}
	mu    sync.Mutex
	held  bool
}

type computeSlotKey struct{}

// acquire takes a slot, waiting while all are held; it does nothing when the computation holds one.
func (s *computeSlot) acquire(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held {
		return nil
	}
	select {
	case s.slots <- struct{}{}:
		s.held = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release gives the slot back, if the computation holds it, and says whether it did.
func (s *computeSlot) release() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.held {
		return false
	}
	<-s.slots
	s.held = false
	return true
}

// pauseComputeSlot gives back the slot of the computation of ctx, if it holds one, for a wait that reads nothing from the
// database pool. The function it returns takes a slot again (queueing like any computation) before the computation goes on.
func pauseComputeSlot(ctx context.Context) (resume func(context.Context) error) {
	if s, ok := ctx.Value(computeSlotKey{}).(*computeSlot); ok && s.release() {
		return s.acquire
	}
	return func(context.Context) error { return nil }
}

// keepAnswerInMemory keeps an answer in the answer cache. It is served from there until it stops being current (the key
// holds the revision, so a write makes the entry unreachable, and the data versions it read are compared at every lookup):
// a minute at most, or the next moment a rule or a membership starts or stops being in force. An entry whose end is
// unknown is not kept.
//
// The entry is kept in memory longer than it is served (until Hold, at most maxStaleAnswerTTL): if PostgreSQL cannot be
// reached and the revisions cannot be read, it is the only answer there is (see cachedEntry.stale).
func (a *App) keepAnswerInMemory(key string, entry any, now time.Time) {
	e, ok := entry.(interface {
		expiry() int64
		holdTime() int64
		serveFor(time.Time)
	})
	if !ok || e.expiry() == 0 {
		return
	}
	serve := time.UnixMilli(e.expiry()).Sub(now)
	e.serveFor(now.Add(serve))
	keep := serve
	if hold := time.UnixMilli(e.holdTime()).Sub(now); hold > keep {
		keep = min(hold, maxStaleAnswerTTL)
	}
	a.answersL1.Add(key, entry, keep, now)
}

func (e *cachedEntry[T]) expiry() int64 { return e.Exp }

func (e *cachedEntry[T]) holdTime() int64 { return e.Hold }

func (e *cachedEntry[T]) serveFor(until time.Time) { e.serveUntil = until }

// dataTag names the data versions of a state. Two requests share a computation only when they saw the same
// versions: a computation that started before a change of the data must not answer a request that started after it.
func dataTag(data map[string]int64) string {
	svcs := make([]string, 0, len(data))
	for svc := range data {
		svcs = append(svcs, svc)
	}
	slices.Sort(svcs)
	var b strings.Builder
	for _, svc := range svcs {
		b.WriteString(svc + "=" + strconv.FormatInt(data[svc], 10) + ";")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// dataServices are the services that own the given types, each once, in a fixed order.
func dataServices(g *TypeGraph, typeIDs []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, id := range typeIDs {
		if n, ok := g.Node(id); ok && !seen[n.ServiceID] {
			seen[n.ServiceID] = true
			out = append(out, n.ServiceID)
		}
	}
	slices.SortFunc(out, func(x, y uuid.UUID) int { return compareUUID(x, y) })
	return out
}

// bumpDataRevisions moves the revision of the resource data of every service that owns one of the given types. It runs in
// the transaction that wrote the data, so the revision cannot reach a reader before the data does.
func bumpDataRevisions(ctx context.Context, q db.Querier, g *TypeGraph, typeIDs []uuid.UUID) error {
	for _, service := range dataServices(g, typeIDs) {
		if _, err := db.BumpRevision(ctx, q, db.DataScope(service)); err != nil {
			return err
		}
	}
	return nil
}
