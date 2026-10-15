package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// syncPollInterval is how often a replica checks whether another replica has released its lock, at first; the
// interval doubles up to syncPollMax while the lock is held, so that many waiting requests do not poll Redis hard.
const (
	syncPollInterval = 50 * time.Millisecond
	syncPollMax      = 500 * time.Millisecond
)

// defaultSyncLockTTL keeps a lock from living forever if its holder dies, when no configuration is set.
const defaultSyncLockTTL = 60 * time.Second

// releaseSyncLock deletes the lock only if it is still ours: after its TTL another replica may hold it.
var releaseSyncLock = redis.NewScript(`
	if redis.call('get', KEYS[1]) == ARGV[1] then
		return redis.call('del', KEYS[1])
	end
	return 0`)

// typesLockName names the synchronization of a set of types: the same set has the same name in every replica.
func typesLockName(typeIDs []uuid.UUID) string {
	ids := make([]string, len(typeIDs))
	for i, id := range typeIDs {
		ids[i] = id.String()
	}
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, ",")))
	return hex.EncodeToString(sum[:])
}

// syncTypesOnce runs the synchronization of a set of types once at a time: concurrent calls in this process
// share one run (singleflight), and across replicas the replica that takes the Redis lock does the work while
// the others wait for the lock to be released and then find the answer cached. fresh (consistency=higher)
// runs are kept apart from the others. The work runs under a context that the first caller's cancellation
// does not cancel, because the other callers wait for its answer. Fail-open: without Redis, or when the lock
// cannot be used, the work goes on without it — the lock is only an optimization.
func (a *App) syncTypesOnce(ctx context.Context, typeIDs []uuid.UUID, fresh bool, work func(context.Context) (map[uuid.UUID]bool, error)) (map[uuid.UUID]bool, error) {
	name := typesLockName(typeIDs)
	flight := name
	if fresh {
		flight += ":fresh"
	}
	v, err, _ := a.syncFlights.Do(flight, func() (any, error) {
		return a.syncWithRedisLock(context.WithoutCancel(ctx), name, work)
	})
	if err != nil {
		return nil, err
	}
	synced, _ := v.(map[uuid.UUID]bool)
	return synced, nil
}

// syncWithRedisLock is the cross-replica part of syncTypesOnce.
func (a *App) syncWithRedisLock(ctx context.Context, name string, work func(context.Context) (map[uuid.UUID]bool, error)) (map[uuid.UUID]bool, error) {
	if a.Redis == nil {
		return work(ctx)
	}
	key := "authz:resource_sync:lock:" + name

	token := uuid.NewString()
	acquired, err := a.Redis.SetNX(ctx, key, token, a.syncLockTTL()).Result()
	if err != nil {
		logRedis(ctx, slog.LevelWarn, "the resource sync lock is unavailable, synchronizing without it", err)
		return work(ctx)
	}
	if !acquired {
		a.waitSyncLock(ctx, key)
		return work(ctx)
	}
	defer releaseSyncLock.Run(context.WithoutCancel(ctx), a.Redis, []string{key}, token)
	return work(ctx)
}

// waitSyncLock waits until another replica releases the lock, for at most Sync.LockWait.
func (a *App) waitSyncLock(ctx context.Context, key string) {
	deadline := time.Now().Add(a.syncLockWait())
	delay := syncPollInterval
	for time.Now().Before(deadline) {
		held, err := a.Redis.Exists(ctx, key).Result()
		if err != nil || held == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, syncPollMax)
	}
}

func (a *App) syncLockTTL() time.Duration {
	if a.Cfg == nil || a.Cfg.Sync.LockTTL <= 0 {
		return defaultSyncLockTTL
	}
	return a.Cfg.Sync.LockTTL
}

func (a *App) syncLockWait() time.Duration {
	if a.Cfg == nil {
		return 0
	}
	return a.Cfg.Sync.LockWait
}
