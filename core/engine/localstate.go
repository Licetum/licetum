package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// The revision of the policy and the data versions of the services live in Redis, and every answer is keyed by them. A
// process does not read them for every request: it keeps the last reading and the policy snapshot of that revision as
// one localView, and renews both in the background once the reading is older than SNAPSHOT__REVISION_POLL. So a request
// that finds its answer in memory does not touch Redis or PostgreSQL, and a write made through another replica reaches
// the process within the poll interval (twice that, when the process serves no traffic and nothing renews the view
// in the background: the next request renews it before it is answered).
//
// A view is replaced as a whole and never changed. A write made through this process (BumpVersion) and a change of the
// resource data it recorded (recordDataChange) invalidate the view, so the next request reads Redis again and sees them.
// A request that asks for at least revision N (WithMinRevision) renews a view older than N before it is answered.

// localView is the revision, the data versions and the snapshot of that revision, as of readAt.
type localView struct {
	// state is what readCacheState returned, with the snapshot and its request attributes in place.
	state  cacheState
	snap   *snapshot
	readAt time.Time
	// epoch is the invalidation count the view was read at: a later count means the view predates a local write.
	epoch int64
}

// snapshotRetry is how long the process waits before it tries to build a snapshot again after a failure.
const snapshotRetry = 5 * time.Second

// viewState holds the fields of the App that keep the view.
type viewState struct {
	view        atomic.Pointer[localView]
	epoch       atomic.Int64
	renewing    atomic.Bool
	failedAt    atomic.Int64 // unix nanoseconds of the last failed build of a snapshot
	failedRenew atomic.Int64 // unix nanoseconds of the last reading of Redis that failed
	oversized   atomic.Int64 // revision + 1 of a policy that did not fit, 0 for none
	renewFlight singleflight.Group
}

// localEnabled reports whether this process keeps the revision and the policy in memory.
func (a *App) localEnabled() bool {
	return a.DB != nil && a.Redis != nil && a.tuningOrDefault().snapshotMaxBytes > 0
}

// invalidateView makes the next request read the revision and the data versions again. Call it after a change the
// process made to either.
func (a *App) invalidateView() { a.local.epoch.Add(1) }

// cacheState is the state a request decides its answer key and its freshness from: the revision of the rules, the
// data versions, the attribute names and, when the policy is kept in memory, the snapshot. It is the view of the process
// when there is one, otherwise it is read from Redis. A state that is not ok means Redis could not be read.
func (a *App) cacheState(ctx context.Context) cacheState {
	if !a.localEnabled() {
		return a.readCacheState(ctx)
	}
	v := a.currentView(ctx)
	if v == nil {
		return cacheState{}
	}
	if v.snap == nil && a.metrics != nil {
		a.metrics.snapshotFallback.Inc()
	}
	return v.state
}

// policySnapshot is the snapshot of the current view, nil when the policy is not kept in memory (or Redis could not be
// read, so the revision is not known).
func (a *App) policySnapshot(ctx context.Context) *snapshot {
	if !a.localEnabled() {
		return nil
	}
	if v := a.currentView(ctx); v != nil {
		return v.snap
	}
	return nil
}

// currentView returns the view, renewed first when it predates a local write, predates the revision the request asks for
// or is too old to trust without a reading (staleLimit). It is nil when Redis cannot be read and the process has no view
// it may fall back to.
//
// When Redis cannot be read, the last view is served for a while longer (degradedLimit) and marked degraded, so that the
// requests go on from memory instead of each waiting for Redis; the answers are not kept in Redis then.
func (a *App) currentView(ctx context.Context) *localView {
	t := a.tuningOrDefault()
	v := a.local.view.Load()
	now := time.Now()
	ageStale := false
	if v != nil {
		age := now.Sub(v.readAt)
		ageStale = age >= staleLimit(t)
		if v.epoch == a.local.epoch.Load() && minRevisionFrom(ctx) <= v.state.version && !ageStale {
			if age >= t.revisionPoll {
				a.renewViewInBackground()
			}
			return v
		}
	}
	// A view that is only old, after a failed reading a moment ago: do not ask again for every request.
	if v != nil && ageStale && v.epoch == a.local.epoch.Load() && minRevisionFrom(ctx) <= v.state.version &&
		now.Sub(time.Unix(0, a.local.failedRenew.Load())) < t.revisionPoll {
		return degraded(v, now, t)
	}
	if nv := a.renewView(ctx); nv != nil {
		return nv
	}
	a.local.failedRenew.Store(time.Now().UnixNano())
	if v != nil && ageStale && v.epoch == a.local.epoch.Load() && minRevisionFrom(ctx) <= v.state.version {
		return degraded(v, now, t)
	}
	return nil
}

// staleLimit is the age at which a request no longer trusts the view without reading Redis first. The view is renewed in
// the background long before (every poll interval, while requests come), so this is the age of a view that nothing
// has renewed, or whose renewals fail.
func staleLimit(t tuning) time.Duration { return max(8*t.revisionPoll, 2*time.Second) }

// degraded returns the view marked degraded when it is not too old, otherwise nil.
func degraded(v *localView, now time.Time, t tuning) *localView {
	limit := t.snapshotMaxAge
	if limit <= 0 {
		limit = defaultSnapshotMaxAge
	}
	if now.Sub(v.readAt) >= limit {
		return nil
	}
	d := *v
	d.state.degraded = true
	return &d
}

func (a *App) renewViewInBackground() {
	if time.Since(time.Unix(0, a.local.failedRenew.Load())) < a.tuningOrDefault().revisionPoll {
		return // Redis could not be read a moment ago
	}
	if !a.local.renewing.CompareAndSwap(false, true) {
		return
	}
	if !a.Background(func(ctx context.Context) {
		defer a.local.renewing.Store(false)
		if a.renewView(ctx) == nil {
			a.local.failedRenew.Store(time.Now().UnixNano())
		}
	}) {
		a.local.renewing.Store(false)
	}
}

// renewView reads the revision and the data versions from Redis and, when the revision moved, builds the snapshot of
// the new one. Concurrent renewals share one reading. A view that still predates the revision a request asked for is
// read once more.
func (a *App) renewView(ctx context.Context) *localView {
	asked := minRevisionFrom(ctx)
	var v *localView
	for attempt := range 2 {
		got, _, _ := a.local.renewFlight.Do("view", func() (any, error) { return a.readView(ctx), nil })
		v, _ = got.(*localView)
		if v == nil || asked <= v.state.version || attempt == 1 {
			break
		}
		a.local.renewFlight.Forget("view")
	}
	return v
}

// readView does the reading. A failure leaves the previous view in place (and returns nil: the caller cannot trust it).
func (a *App) readView(ctx context.Context) *localView {
	ctx = context.WithoutCancel(ctx)
	epoch := a.local.epoch.Load()
	state := a.readCacheState(ctx)
	if !state.ok {
		return nil
	}
	snap := a.snapshotFor(ctx, state.version, a.local.view.Load())
	if snap != nil {
		state.snap, state.attrs, state.attrsOK = snap, snap.attrs, true
	}
	v := &localView{state: state, snap: snap, readAt: time.Now(), epoch: epoch}
	a.local.view.Store(v)
	return v
}

// snapshotFor returns the snapshot of the revision: the one the previous view holds when it is of that revision and not
// too old, otherwise a new one. It is nil when the policy does not fit, or cannot be read just now.
func (a *App) snapshotFor(ctx context.Context, revision int64, previous *localView) *snapshot {
	t := a.tuningOrDefault()
	if previous != nil && previous.snap != nil && previous.snap.revision == revision &&
		(t.snapshotMaxAge <= 0 || time.Since(previous.snap.loadedAt) < t.snapshotMaxAge) {
		return previous.snap
	}
	if a.local.oversized.Load() == revision+1 {
		return nil
	}
	if failed := a.local.failedAt.Load(); failed != 0 && time.Since(time.Unix(0, failed)) < snapshotRetry {
		return nil
	}
	snap, err := a.loadSnapshot(ctx, revision, t.snapshotMaxBytes)
	switch {
	case err == nil:
		a.local.failedAt.Store(0)
		return snap
	case isTooLarge(err):
		a.local.oversized.Store(revision + 1)
		a.logSnapshotFallback(revision, err)
	default:
		a.local.failedAt.Store(time.Now().UnixNano())
		slog.WarnContext(ctx, "failed to build the policy snapshot: the queries read the policy until the next try", "error", err)
	}
	return nil
}

func isTooLarge(err error) bool { return errors.Is(err, errSnapshotTooLarge) }

// ClearMemoryCaches empties the in-process levels of the answer cache and of the token cache (and forgets the tokens
// remembered as invalid). Redis keeps what it holds, so the next request is served from it: this is how a test, or an
// operator, tells the cost of the second level from that of the first.
func (a *App) ClearMemoryCaches() {
	a.answersL1.Clear()
	a.tokensL1.Clear()
	a.invalidTokens.Clear()
}

// ClearAnswerMemoryCache empties the in-process level of the answer cache only.
func (a *App) ClearAnswerMemoryCache() { a.answersL1.Clear() }

// PolicySnapshotLoaded reports whether the last view of this process holds a policy snapshot, which the answers that are
// not cached are decided from. It is false when the policy does not fit SNAPSHOT__MAX_BYTES or the snapshot is off.
func (a *App) PolicySnapshotLoaded() bool {
	v := a.local.view.Load()
	return v != nil && v.snap != nil
}

// PolicyRevision is the revision of the policy this process serves from (the one answers are keyed by), 0 when it has
// not read one yet or does not keep the policy in memory.
func (a *App) PolicyRevision() int64 {
	if v := a.local.view.Load(); v != nil {
		return v.state.version
	}
	return 0
}
