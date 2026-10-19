package logx_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/licetum/licetum/core/logx"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestSamplerLetsOneRecordPerKeyPerIntervalThrough(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	s := logx.NewSamplerWithClock(time.Second, clock.now)

	if ok, n := s.Allow("redis|timeout"); !ok || n != 0 {
		t.Fatalf("the first record: ok=%v suppressed=%d", ok, n)
	}
	for i := range 599 {
		clock.t = clock.t.Add(time.Millisecond)
		if ok, _ := s.Allow("redis|timeout"); ok {
			t.Fatalf("record %d within the interval was let through", i+2)
		}
	}
	// Another kind of failure has its own turn.
	if ok, _ := s.Allow("postgres|refused"); !ok {
		t.Fatal("a different key must not be suppressed by the first")
	}
	clock.t = clock.t.Add(time.Second)
	ok, n := s.Allow("redis|timeout")
	if !ok || n != 599 {
		t.Fatalf("after the interval: ok=%v suppressed=%d, want true and 599", ok, n)
	}
	if got := s.Suppressed(); got != 599 {
		t.Fatalf("Suppressed() = %d, want 599", got)
	}
	// The count starts again after it was reported.
	if ok, _ := s.Allow("redis|timeout"); ok {
		t.Fatal("the record right after a written one must wait for the interval")
	}
	clock.t = clock.t.Add(time.Second)
	if ok, n := s.Allow("redis|timeout"); !ok || n != 1 {
		t.Fatalf("second window: ok=%v suppressed=%d, want true and 1", ok, n)
	}
}

func TestSamplerLogWritesTheCountOfSuppressedRecords(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	s := logx.NewSamplerWithClock(time.Second, clock.now)
	for range 10 {
		s.Log(context.Background(), slog.LevelWarn, "k", "failed to read the cached answer", "error", "i/o timeout")
	}
	clock.t = clock.t.Add(2 * time.Second)
	s.Log(context.Background(), slog.LevelWarn, "k", "failed to read the cached answer", "error", "i/o timeout")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines written for 11 records in two intervals, want 2:\n%s", len(lines), buf.String())
	}
	if strings.Contains(lines[0], "suppressed") || !strings.Contains(lines[1], "suppressed=9") {
		t.Fatalf("the first line must carry no count and the second suppressed=9:\n%s", buf.String())
	}
}

func TestSamplerDoesNotSpendTheTurnOnADisabledLevel(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := logx.NewSampler(time.Hour)
	s.Log(context.Background(), slog.LevelDebug, "k", "skipped command")
	s.Log(context.Background(), slog.LevelWarn, "k", "failed")
	if !strings.Contains(buf.String(), "failed") {
		t.Fatalf("the warning was swallowed by a debug record that nobody sees:\n%s", buf.String())
	}
}

func TestSamplerForgetsKeysWhenThereAreTooMany(t *testing.T) {
	s := logx.NewSampler(time.Hour)
	for i := range 10_000 {
		s.Allow(strings.Repeat("x", 1+i%7) + string(rune('a'+i%26)) + time.Duration(i).String())
	}
	if ok, _ := s.Allow("a-new-key"); !ok {
		t.Fatal("a new key must always get its first record")
	}
}
