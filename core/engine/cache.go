package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/redisx"
)

// The answers of the permission and check APIs are kept in Redis. An answer is current while the rules have
// not changed — the global version, part of its key, that every admin write bumps — and while the resource data
// it was computed from has not changed. The second is kept per service: a synchronization that changes the data
// of a service's types bumps that service's data version, and an answer records the data versions of the services
// it read. So a change in one service leaves in place the answers that do not read it.

const (
	// claimAttrsKey holds the claim names the rules look at, as a claimAttrsRecord.
	claimAttrsKey = "authz:claim_attrs:t4"
	claimAttrsTTL = time.Hour
	// dataVersionsKey is a hash: service id -> version of the resource data of the service.
	dataVersionsKey = "authz:data_versions"
	// computeTimeout bounds a computation that goes on after its first caller has gone away (singleflight).
	computeTimeout = 30 * time.Second
	// backgroundTimeout bounds the background work: cache writes and refreshes of the resource data.
	backgroundTimeout = 30 * time.Second
)

// claimAttrsRecord is what claimAttrsKey holds: the claim names read for one rules version.
type claimAttrsRecord struct {
	Version int64    `json:"version"`
	Attrs   []string `json:"attrs"`
}

// cacheState is what a request reads from Redis before it computes or looks up an answer: the rules version,
// the claim names of the rules for that version, and the data version of every service.
type cacheState struct {
	// ok is false when Redis could not be read: the answer is computed and not kept.
	ok      bool
	version int64
	data    map[string]int64 // service id -> data version (absent: 0)
	attrs   []string
	// attrsOK says the claim names were read for this rules version (otherwise they must be read again).
	attrsOK bool
	// tag names the data versions the state holds (see dataTag).
	tag string
}

// readCacheState reads the rules version, the claim names and the data versions of the services in one round
// trip. A Redis error leaves the state not ok.
func (a *App) readCacheState(ctx context.Context) cacheState {
	pipe := a.Redis.Pipeline()
	version := pipe.Get(ctx, redisx.VersionKey)
	attrs := pipe.Get(ctx, claimAttrsKey)
	data := pipe.HGetAll(ctx, dataVersionsKey)
	_, _ = pipe.Exec(ctx) // every reply is read below: a missing key is a reply, not a failure of the pipeline

	fail := func(err error) cacheState {
		slog.WarnContext(ctx, "failed to read the cache state", "error", err)
		return cacheState{}
	}
	state := cacheState{ok: true, data: map[string]int64{}}
	var err error
	if state.version, err = counter(version); err != nil {
		return fail(err)
	}
	if raw, err := attrs.Bytes(); err == nil {
		var record claimAttrsRecord
		if json.Unmarshal(raw, &record) == nil && record.Version == state.version {
			state.attrs, state.attrsOK = record.Attrs, true
		}
	} else if !errors.Is(err, redis.Nil) {
		return fail(err)
	}
	fields, err := data.Result()
	if err != nil {
		return fail(err)
	}
	for svc, v := range fields {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fail(err)
		}
		state.data[svc] = n
	}
	state.tag = dataTag(state.data)
	return state
}

// counter reads an integer reply; a missing key is 0.
func counter(cmd *redis.StringCmd) (int64, error) {
	raw, err := cmd.Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
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

// cachedEntry is an answer as the cache keeps it.
type cachedEntry[T any] struct {
	Value T                `json:"value"`
	Deps  map[string]int64 `json:"deps,omitempty"`
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

	if minRevisionFrom(ctx) > state.version {
		// the cache is older than the revision the caller asks for (see WithMinRevision): compute from the data
		consistency = HigherConsistency
	}

	flight := key + "|" + state.tag
	if consistency.fresh() {
		results.WithLabelValues("bypass").Inc()
		flight += ":fresh"
	} else {
		raw, err := a.Redis.Get(ctx, key).Bytes()
		if err == nil {
			var entry cachedEntry[T]
			if json.Unmarshal(raw, &entry) == nil && state.current(entry.Deps) {
				results.WithLabelValues("hit").Inc()
				return entry.Value, nil
			}
		} else if !errors.Is(err, redis.Nil) {
			slog.WarnContext(ctx, "failed to read the cached answer", "error", err)
		}
		results.WithLabelValues("miss").Inc()
	}

	shared, err, _ := a.answers.Do(flight, func() (any, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		defer cancel()
		c, err := compute(cctx)
		if err != nil {
			return nil, err
		}
		if raw, err := json.Marshal(cachedEntry[T]{Value: c.value, Deps: c.deps}); err == nil {
			a.Background(func(ctx context.Context) {
				if err := a.Redis.Set(ctx, key, raw, c.ttl).Err(); err != nil {
					slog.WarnContext(ctx, "failed to write the cached answer", "error", err)
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

// recordDataChange adds to the pipeline the bump of the data version of every service that owns one of the given
// types. Call it after the data is committed: the answers that read those services are stale from then on.
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

// Background runs fn after the request that started it has been answered. Close waits for it. Once Close has
// begun, no new Background work starts: its cache writes and refreshes are optional.
func (a *App) Background(fn func(ctx context.Context)) {
	a.closeMu.Lock()
	if a.closed {
		a.closeMu.Unlock()
		return
	}
	a.inflight.Add(1)
	a.closeMu.Unlock()
	go func() {
		defer a.inflight.Done()
		defer func() {
			// The request that started this work has been answered; a panic here must not take the process down.
			if p := recover(); p != nil {
				slog.Error("background work panicked", "panic", p, "stack", string(debug.Stack()))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
		defer cancel()
		fn(ctx)
	}()
}
