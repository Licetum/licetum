// Package redisx holds the Redis client and small primitives on top of it: the permission cache
// version (invalidation without scanning keys) and a distributed circuit breaker.
// Key formats (authz:cache_version, authz:permission:*, authz:token:*, authz:breaker:*,
// authz:pkce:*) are kept from the legacy Python version.
package redisx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Connect parses REDIS__URI (redis://, rediss://, unix://) and checks the connection.
func Connect(ctx context.Context, uri string) (*redis.Client, error) {
	opts, err := redis.ParseURL(uri)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS__URI: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to Redis: %w", err)
	}
	return client, nil
}

// VersionKey is the permission cache version counter. Every admin write bumps it, and all
// authz:permission:v{old}:… keys stop being read (and expire by TTL).
const VersionKey = "authz:cache_version"

// BumpVersion invalidates the permission cache and returns the new version: the revision of the write
// that caused it (see service.Trace).
func BumpVersion(ctx context.Context, rdb *redis.Client) (int64, error) {
	return rdb.Incr(ctx, VersionKey).Result()
}

func GetVersion(ctx context.Context, rdb *redis.Client) (int64, error) {
	v, err := rdb.Get(ctx, VersionKey).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

// Circuit breaker parameters: FailureThreshold failures within FailureWindow open it for Cooldown.
const (
	FailureThreshold = 5
	FailureWindow    = 60 * time.Second
	Cooldown         = 30 * time.Second
)

func failKey(key string) string { return "authz:breaker:" + key + ":fail" }
func openKey(key string) string { return "authz:breaker:" + key + ":open" }

// BreakerOpen reports true when the source is considered unavailable and a live call should be skipped.
// A Redis error counts as "closed" — cache unavailability must not block checks.
func BreakerOpen(ctx context.Context, rdb *redis.Client, key string) bool {
	n, err := rdb.Exists(ctx, openKey(key)).Result()
	return err == nil && n > 0
}

func RecordFailure(ctx context.Context, rdb *redis.Client, key string) {
	failures, err := rdb.Incr(ctx, failKey(key)).Result()
	if err != nil {
		return
	}
	if failures == 1 {
		rdb.Expire(ctx, failKey(key), FailureWindow)
	}
	if failures >= FailureThreshold {
		rdb.Set(ctx, openKey(key), "1", Cooldown)
	}
}

func RecordSuccess(ctx context.Context, rdb *redis.Client, key string) {
	rdb.Del(ctx, failKey(key), openKey(key))
}
