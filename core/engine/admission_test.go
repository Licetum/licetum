package engine

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/redisx"
)

func TestAdmitRefusesAboveTheLimitAndCountsTheRefusals(t *testing.T) {
	a := &App{Cfg: &config.Config{HTTP: config.HTTP{MaxInflight: 2}}}
	var releases []func()
	for range 2 {
		release, ok := a.Admit()
		if !ok {
			t.Fatal("a request under the limit was refused")
		}
		releases = append(releases, release)
	}
	if _, ok := a.Admit(); ok {
		t.Fatal("a third request above a limit of two was admitted")
	}
	if a.Inflight() != 2 || a.admit.shed.Load() != 1 {
		t.Fatalf("inflight %d (want 2), shed %d (want 1): a refused request must not stay counted", a.Inflight(), a.admit.shed.Load())
	}
	releases[0]()
	if release, ok := a.Admit(); !ok {
		t.Fatal("a place freed by a finished request was not given")
	} else {
		release()
	}
	releases[1]()
	if a.Inflight() != 0 {
		t.Fatalf("%d requests still counted", a.Inflight())
	}
}

func TestMaxInflightDefaultsToGOMAXPROCSTimes256AndNegativeRemovesIt(t *testing.T) {
	if got, want := (&App{Cfg: &config.Config{}}).MaxInflight(), runtime.GOMAXPROCS(0)*256; got != want {
		t.Errorf("default limit %d, want %d", got, want)
	}
	if got := (&App{Cfg: &config.Config{HTTP: config.HTTP{MaxInflight: -1}}}).MaxInflight(); got != 0 {
		t.Errorf("a negative limit must mean no limit, got %d", got)
	}
	a := &App{Cfg: &config.Config{HTTP: config.HTTP{MaxInflight: -1}}}
	for range 10_000 {
		if _, ok := a.Admit(); !ok {
			t.Fatal("a request was refused without a limit")
		}
	}
}

func TestRequestTimeoutDefaultsByRole(t *testing.T) {
	for _, c := range []struct {
		role Role
		set  time.Duration
		want time.Duration
	}{
		{RoleCheck, 0, 10 * time.Second},
		{RoleAdmin, 0, 60 * time.Second},
		{RoleWorker, 0, 60 * time.Second},
		{RoleAny, 0, 0}, // tests and tools: none unless asked for
		{RoleCheck, 3 * time.Second, 3 * time.Second},
		{RoleAdmin, 3 * time.Second, 3 * time.Second},
		{RoleCheck, -1, 0},
	} {
		a := &App{Role: c.role, Cfg: &config.Config{HTTP: config.HTTP{RequestTimeout: c.set}}}
		if got := a.RequestTimeout(); got != c.want {
			t.Errorf("role %v, set %v: %v, want %v", c.role, c.set, got, c.want)
		}
	}
}

func TestValidityUntilKeepsTheHoldPastTheCap(t *testing.T) {
	now := time.Now()
	if ttl, hold := validityUntil(nil, now); ttl != ResultCacheTTL || hold != holdUnbounded {
		t.Errorf("no window: ttl %v hold %v", ttl, hold)
	}
	far := now.Add(time.Hour)
	if ttl, hold := validityUntil(&far, now); ttl != ResultCacheTTL || hold < 59*time.Minute {
		t.Errorf("a window in an hour: ttl %v (the cap), hold %v (the hour)", ttl, hold)
	}
	soon := now.Add(5 * time.Second)
	if ttl, hold := validityUntil(&soon, now); ttl > 5*time.Second || hold > 5*time.Second || ttl != hold {
		t.Errorf("a window in 5 s: ttl %v hold %v, both at most 5 s", ttl, hold)
	}
	imminent := now.Add(100 * time.Millisecond)
	if ttl, hold := validityUntil(&imminent, now); ttl != time.Second || hold != time.Second {
		t.Errorf("a window within a second keeps the floor of a second: ttl %v hold %v", ttl, hold)
	}
}

func TestCachedEntryServingRules(t *testing.T) {
	now := time.Now()
	e := &cachedEntry[int]{Exp: now.Add(time.Minute).UnixMilli(), Hold: now.Add(time.Hour).UnixMilli(), serveUntil: now.Add(10 * time.Second)}
	if !e.fresh(now) || e.fresh(now.Add(11*time.Second)) {
		t.Error("an entry is served in the usual way until serveUntil")
	}
	if !e.stale(now.Add(30*time.Minute)) || e.stale(now.Add(2*time.Hour)) {
		t.Error("a stale entry is served until its Hold")
	}
	if (&cachedEntry[int]{Exp: now.Add(time.Minute).UnixMilli()}).stale(now) {
		t.Error("an entry without a Hold (written by an earlier release) is never served stale")
	}
}

func TestRedisDownMeansUnreachableNotAnAnswer(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{context.Canceled, false},
		{redisx.ErrSkipped, false},
		{replyErr("READONLY You can't write against a read only replica."), false},
		{replyErr("LOADING Redis is loading the dataset in memory"), true},
		{context.DeadlineExceeded, true},
		{errors.New("dial tcp: connection refused"), true},
	} {
		if got := redisDown(c.err); got != c.want {
			t.Errorf("redisDown(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestDependencyHealthIsDegradedUntilACallSucceeds(t *testing.T) {
	var h dependencyHealth
	if h.degraded() {
		t.Fatal("a dependency nobody called is not degraded")
	}
	h.note(true)
	if !h.degraded() {
		t.Fatal("a failure makes it degraded")
	}
	time.Sleep(time.Millisecond)
	h.note(false)
	if h.degraded() {
		t.Fatal("a success after the failure ends it")
	}
}
