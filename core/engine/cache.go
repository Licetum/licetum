package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/redisx"
)

// The answers of the permission and check APIs are kept in Redis (and in memory). An answer is current while the rules
// have not changed — the revision of the policy, part of its key — and while the resource data it was computed from has
// not changed. Both numbers live in PostgreSQL, in the table revision_state, and change in the transaction that changes
// the data they number: a write of an administrator bumps the revision 'rules', a synchronization that changed the data
// of a service's types bumps 'data:<service>'. An answer records the data revisions of the services it read, so a change
// in one service leaves in place the answers that do not read it.

const (
	// dataVersionsKey is a hash: service id -> version of the resource data of the service. Since the revision moved to
	// PostgreSQL only the replicas of the previous release read it; the synchronizations still write it for them.
	dataVersionsKey = "authz:data_versions"
	// computeTimeout bounds a computation that goes on after its first caller has gone away (singleflight).
	computeTimeout = 30 * time.Second
)

// cacheState is what a request reads before it computes or looks up an answer: the revision of the rules, the revision
// of the resource data of every service, and the names of the request attributes the rules look at.
type cacheState struct {
	// ok is false when the revisions could not be read: the answer is computed and not kept.
	ok      bool
	version int64            // the revision of the rules
	data    map[string]int64 // service id -> revision of its resource data (absent: 0)
	attrs   []string
	// attrsOK says the attribute names are those of this revision (otherwise they are read from the rules).
	attrsOK bool
	// tag names the data versions the state holds (see dataTag).
	tag string
	// snap is the policy snapshot of the revision, when the process keeps the policy in memory (see localView).
	snap *snapshot
	// degraded says the revisions could not be read lately and the state is the last one this process read: the answers
	// are kept in memory only.
	degraded bool
}

// readCacheState reads the revisions from PostgreSQL, in one statement. An error leaves the state not ok.
func (a *App) readCacheState(ctx context.Context) cacheState {
	rev, err := db.LoadRevisions(ctx, a.DB)
	if err != nil {
		slog.WarnContext(ctx, "failed to read the revision of the policy", "error", err)
		return cacheState{}
	}
	state := cacheState{ok: true, version: rev.Rules, data: rev.Data}
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

// logRedis logs a failed use of Redis. A command that was skipped because Redis is being skipped (redisx.FastFail) is
// the consequence of a failure already logged, and at 600 requests a second would flood the log: it is a debug line.
func logRedis(ctx context.Context, level slog.Level, msg string, err error) {
	if errors.Is(err, redisx.ErrSkipped) {
		level = slog.LevelDebug
	}
	slog.Log(ctx, level, msg, "error", err)
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

// computed is an answer with what it depends on: the data versions it read, and how long it stays current.
type computed[T any] struct {
	value T
	deps  map[string]int64
	ttl   time.Duration
}

// cachedEntry is an answer as the cache keeps it. Exp is when it stops being current (unix milliseconds), so that a
// process that reads it from Redis knows how long it may keep it in memory; entries written before it was added have none.
type cachedEntry[T any] struct {
	Value T                `json:"value"`
	Deps  map[string]int64 `json:"deps,omitempty"`
	Exp   int64            `json:"exp,omitempty"`
}

// cachedAnswer returns the answer of compute for key: from the cache while the entry there is current, otherwise
// computed. Concurrent misses for the same key share one computation (singleflight), and the answer is written
// to the cache in the background. consistency=higher skips the read but still refreshes the entry. Without Redis
// (state.ok false) the answer is computed each time and nothing is kept.
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
		now := time.Now()
		if v, ok := a.answersL1.Get(key, now); ok {
			if entry, ok := v.(*cachedEntry[T]); ok && state.current(entry.Deps) {
				a.metrics.l1Lookup("answer", true)
				results.WithLabelValues("hit").Inc()
				return entry.Value, nil
			}
		}
		a.metrics.l1Lookup("answer", false)
		if !state.degraded {
			raw, err := a.Redis.Get(ctx, key).Bytes()
			if err == nil {
				var entry cachedEntry[T]
				if json.Unmarshal(raw, &entry) == nil && state.current(entry.Deps) {
					a.keepAnswerInMemory(key, &entry, now)
					results.WithLabelValues("hit").Inc()
					return entry.Value, nil
				}
			} else if !errors.Is(err, redis.Nil) {
				logRedis(ctx, slog.LevelWarn, "failed to read the cached answer", err)
			}
		}
		results.WithLabelValues("miss").Inc()
	}

	shared, err, _ := a.answers.Do(flight, func() (any, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		defer cancel()
		// A wave of misses (a revision moved under many hot keys) queues here instead of exhausting the database pool.
		if a.computeSlots != nil {
			select {
			case a.computeSlots <- struct{}{}:
				defer func() { <-a.computeSlots }()
			case <-cctx.Done():
				return nil, cctx.Err()
			}
		}
		c, err := compute(cctx)
		if err != nil {
			return nil, err
		}
		if !store {
			return c.value, nil
		}
		entry := cachedEntry[T]{Value: c.value, Deps: c.deps, Exp: time.Now().Add(c.ttl).UnixMilli()}
		a.keepAnswerInMemory(key, &entry, time.Now())
		if raw, err := json.Marshal(entry); err == nil && !state.degraded {
			a.Background(func(ctx context.Context) {
				if err := a.Redis.Set(ctx, key, raw, c.ttl).Err(); err != nil {
					logRedis(ctx, slog.LevelWarn, "failed to write the cached answer", err)
				}
			})
		}
		return c.value, nil
	})
	if err != nil {
		return zero, err
	}
	return shared.(T), nil
}

// keepAnswerInMemory keeps an answer in the first level of the answer cache until it stops being current, and at most
// ten seconds: the key holds the revision, so a write makes the entry unreachable, and the data versions it read are
// compared at every lookup. An entry whose end is unknown (written by an older version) is not kept.
func (a *App) keepAnswerInMemory(key string, entry any, now time.Time) {
	var exp int64
	switch e := entry.(type) {
	case interface{ expiry() int64 }:
		exp = e.expiry()
	}
	if exp == 0 {
		return
	}
	a.answersL1.Add(key, entry, min(maxAnswerL1TTL, time.UnixMilli(exp).Sub(now)), now)
}

// recordDataChange adds to the pipeline the bump of the data version of every service that owns one of the given types,
// in the Redis hash the replicas of the previous release read. The authoritative revision is the one bumpDataRevisions
// moves in PostgreSQL, in the transaction that wrote the data; this one is for a rolling update.
func recordDataChange(ctx context.Context, pipe redis.Pipeliner, g *TypeGraph, typeIDs []uuid.UUID) {
	seen := map[uuid.UUID]bool{}
	for _, id := range typeIDs {
		n, ok := g.Node(id)
		if !ok || seen[n.ServiceID] {
			continue
		}
		seen[n.ServiceID] = true
		pipe.HIncrBy(ctx, dataVersionsKey, n.ServiceID.String(), 1)
	}
}

func (e *cachedEntry[T]) expiry() int64 { return e.Exp }

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
