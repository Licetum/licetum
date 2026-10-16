//go:build integration

package redisx_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/redisx"
	support "github.com/licetum/licetum/core/testsupport"
)

func connect(t *testing.T) *redis.Client {
	t.Helper()
	ctx := context.Background()
	uri, stop, err := support.StartRedis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	rdb, err := redisx.Connect(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// A breaker whose window and cooldown are tens of milliseconds, so that a test can let them pass.
var fast = redisx.Breaker{Threshold: 3, Window: 30 * time.Millisecond, Cooldown: 20 * time.Millisecond}

// uniqueKey names a dependency for one test, so that a Redis shared by several runs does not carry counters between them.
func uniqueKey(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

func failKey(key string) string { return "authz:breaker:" + key + ":fail" }
func openKey(key string) string { return "authz:breaker:" + key + ":open" }

// requireTTLWhenPresent is the property of F5-12: a key of the breaker that exists has an expiry.
func requireTTLWhenPresent(t *testing.T, rdb *redis.Client, key, history string) {
	t.Helper()
	ctx := context.Background()
	for _, k := range []string{failKey(key), openKey(key)} {
		// PTTL is -2 for a key that does not exist and -1 for one that exists without an expiry (the client returns them
		// as they are, as durations of -2 and -1 nanoseconds). A key that expires between the steps is fine.
		ttl, err := rdb.PTTL(ctx, k).Result()
		if err != nil {
			t.Fatal(err)
		}
		if ttl == -1 {
			t.Fatalf("%s exists without an expiry after%s", k, history)
		}
	}
}

// TestF5_12_TheFailureKeyAlwaysHasATTL runs random sequences of failures, successes and the passing of time against
// the breaker and checks after every step that the failure counter, when it exists, expires. The seed is printed;
// LICETUM_BREAKER_SEED repeats a run.
func TestF5_12_TheFailureKeyAlwaysHasATTL(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()

	seed := time.Now().UnixNano()
	if v := os.Getenv("LICETUM_BREAKER_SEED"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("LICETUM_BREAKER_SEED: %v", err)
		}
		seed = n
	}
	t.Logf("seed %d (LICETUM_BREAKER_SEED repeats the run)", seed)
	rng := rand.New(rand.NewPCG(uint64(seed), 7))

	const sequences, steps = 12, 50
	for s := range sequences {
		key := fmt.Sprintf("f5-12-%d-%d", seed, s)
		history := ""
		for range steps {
			switch op := rng.IntN(10); {
			case op < 6: // most steps are failures, so the threshold is crossed often
				fast.RecordFailure(ctx, rdb, key)
				history += " F"
			case op < 7:
				redisx.RecordSuccess(ctx, rdb, key)
				history += " S"
			default: // time passes: the window or the cooldown may end
				time.Sleep(time.Duration(5+rng.IntN(40)) * time.Millisecond)
				history += " T"
			}
			requireTTLWhenPresent(t, rdb, key, history)
		}
		rdb.Del(ctx, failKey(key), openKey(key))
	}
}

// A counter without an expiry, as a process that died between INCR and EXPIRE left it in the previous release, is given
// one by the next failure; it would otherwise count failures for ever and open the breaker on the first one of every day.
func TestF5_12_ACounterWithoutAnExpiryIsRepaired(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()
	key := uniqueKey("f5-12-repair")
	if err := rdb.Set(ctx, failKey(key), 1, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if ttl := rdb.PTTL(ctx, failKey(key)).Val(); ttl != -1 {
		t.Fatalf("the fixture must have no expiry, PTTL is %v", ttl)
	}

	redisx.RecordFailure(ctx, rdb, key)

	ttl := rdb.PTTL(ctx, failKey(key)).Val()
	if ttl <= 0 || ttl > redisx.FailureWindow {
		t.Fatalf("PTTL after the failure is %v, want a positive time of at most %v", ttl, redisx.FailureWindow)
	}
}

// The breaker opens at the threshold, for the cooldown, and a success closes it.
func TestF5_12_TheBreakerOpensAtTheThresholdAndClosesOnSuccess(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()
	key := uniqueKey("f5-12-open")
	b := redisx.Breaker{Threshold: redisx.FailureThreshold, Window: redisx.FailureWindow, Cooldown: redisx.Cooldown}

	for i := range b.Threshold - 1 {
		b.RecordFailure(ctx, rdb, key)
		if redisx.BreakerOpen(ctx, rdb, key) {
			t.Fatalf("open after %d failures, the threshold is %d", i+1, b.Threshold)
		}
	}
	b.RecordFailure(ctx, rdb, key)
	if !redisx.BreakerOpen(ctx, rdb, key) {
		t.Fatalf("closed after %d failures", b.Threshold)
	}
	if ttl := rdb.PTTL(ctx, openKey(key)).Val(); ttl <= 0 || ttl > b.Cooldown {
		t.Fatalf("the open flag lives %v, want at most the cooldown %v", ttl, b.Cooldown)
	}

	redisx.RecordSuccess(ctx, rdb, key)
	if redisx.BreakerOpen(ctx, rdb, key) {
		t.Fatal("still open after a success")
	}
	if n := rdb.Exists(ctx, failKey(key)).Val(); n != 0 {
		t.Fatal("the counter survived a success")
	}
}

// Concurrent failures are all counted: no increment is lost between the clients.
func TestF5_12_ConcurrentFailuresAreAllCounted(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()
	key := uniqueKey("f5-12-concurrent")
	b := redisx.Breaker{Threshold: 1000, Window: time.Minute, Cooldown: time.Minute}

	done := make(chan struct{})
	const workers, each = 8, 25
	for range workers {
		go func() {
			defer func() { done <- struct{}{} }()
			for range each {
				b.RecordFailure(ctx, rdb, key)
			}
		}()
	}
	for range workers {
		<-done
	}
	if got, _ := rdb.Get(ctx, failKey(key)).Int(); got != workers*each {
		t.Fatalf("counted %d failures, want %d", got, workers*each)
	}
	requireTTLWhenPresent(t, rdb, key, "concurrent failures")
}
