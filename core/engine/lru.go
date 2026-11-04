package engine

import (
	"container/list"
	"sync"
	"time"
)

// lruCache is a bounded in-memory cache: the least recently used entry goes first, and an entry expires at its own time.
// It is safe for concurrent use. A cache of size 0 keeps nothing.
type lruCache[V any] struct {
	mu    sync.Mutex
	max   int
	order *list.List // front: most recently used
	items map[string]*list.Element
}

type lruEntry[V any] struct {
	key     string
	value   V
	expires time.Time
}

func newLRU[V any](max int) *lruCache[V] {
	return &lruCache[V]{max: max, order: list.New(), items: map[string]*list.Element{}}
}

// Get returns the entry of key if it is there and has not expired at now.
func (c *lruCache[V]) Get(key string, now time.Time) (V, bool) {
	var zero V
	if c == nil || c.max <= 0 {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*lruEntry[V])
	if !now.Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.value, true
}

// Add keeps value under key for ttl; a ttl that is not positive keeps nothing.
func (c *lruCache[V]) Add(key string, value V, ttl time.Duration, now time.Time) {
	if c == nil || c.max <= 0 || ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*lruEntry[V])
		e.value, e.expires = value, now.Add(ttl)
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&lruEntry[V]{key: key, value: value, expires: now.Add(ttl)})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*lruEntry[V]).key)
	}
}

// Remove drops key.
func (c *lruCache[V]) Remove(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
		delete(c.items, key)
	}
}

// Clear drops everything.
func (c *lruCache[V]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order.Init()
	c.items = map[string]*list.Element{}
}

// Len is how many entries are kept (expired ones included until they are looked up).
func (c *lruCache[V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
