package engine

import (
	"testing"
	"time"
)

func TestLRUEvictsTheLeastRecentlyUsed(t *testing.T) {
	now := time.Now()
	c := newLRU[int](2)
	c.Add("a", 1, time.Minute, now)
	c.Add("b", 2, time.Minute, now)
	if _, ok := c.Get("a", now); !ok { // a is the most recent now
		t.Fatal("a is missing")
	}
	c.Add("c", 3, time.Minute, now) // b goes
	if _, ok := c.Get("b", now); ok {
		t.Error("b should have been evicted")
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.Get(k, now); !ok {
			t.Errorf("%s is missing", k)
		}
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d", c.Len())
	}
}

func TestLRUEntriesExpire(t *testing.T) {
	now := time.Now()
	c := newLRU[string](10)
	c.Add("k", "v", time.Second, now)
	if v, ok := c.Get("k", now.Add(500*time.Millisecond)); !ok || v != "v" {
		t.Fatalf("before the end: %q %v", v, ok)
	}
	if _, ok := c.Get("k", now.Add(time.Second)); ok {
		t.Error("an entry is gone at its end")
	}
	if c.Len() != 0 {
		t.Error("an expired entry is dropped when it is looked up")
	}
	c.Add("short", "v", 0, now) // a ttl that is not positive keeps nothing
	if _, ok := c.Get("short", now); ok {
		t.Error("an entry without a life was kept")
	}
}

func TestLRUUpdateRemoveClearAndNilSafety(t *testing.T) {
	now := time.Now()
	c := newLRU[int](3)
	c.Add("k", 1, time.Minute, now)
	c.Add("k", 2, time.Minute, now)
	if v, _ := c.Get("k", now); v != 2 || c.Len() != 1 {
		t.Errorf("update: v=%d len=%d", v, c.Len())
	}
	c.Remove("k")
	if _, ok := c.Get("k", now); ok {
		t.Error("Remove left the entry")
	}
	c.Add("x", 1, time.Minute, now)
	c.Clear()
	if c.Len() != 0 {
		t.Error("Clear left entries")
	}

	var nilCache *lruCache[int]
	nilCache.Add("k", 1, time.Minute, now)
	if _, ok := nilCache.Get("k", now); ok || nilCache.Len() != 0 {
		t.Error("a nil cache keeps nothing")
	}
	nilCache.Remove("k")
	nilCache.Clear()
	off := newLRU[int](0)
	off.Add("k", 1, time.Minute, now)
	if _, ok := off.Get("k", now); ok {
		t.Error("a cache of size 0 keeps nothing")
	}
}
