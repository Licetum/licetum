package pepauth

import (
	"crypto/sha256"
	"sync"
	"time"
)

// A verified token is kept for a short time, so that the PEPs that present the same token for every decision do not verify
// it again. The entry lives for the shorter of verifiedTTL and the time left to the token's exp. The cache holds at most
// tokenCacheSize entries: when it is full, the expired ones are swept away, and a token that still finds it full is not kept
// (it is verified again next time). Only verified tokens are kept, so a flood of junk tokens never fills it.
const (
	verifiedTTL    = time.Minute
	tokenCacheSize = 1024
)

// tokenCache is the set of verified tokens of one verifier, keyed by the sha256 of the token. The zero value is empty.
type tokenCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]cachedToken
}

type cachedToken struct {
	verified Verified
	until    time.Time
}

// get returns the token kept under the digest while it is alive at now. The claims it returns are the cache's own: the
// caller copies them before it changes anything.
func (c *tokenCache) get(digest [sha256.Size]byte, now time.Time) (Verified, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[digest]
	if !ok {
		return Verified{}, false
	}
	if !now.Before(e.until) {
		delete(c.entries, digest)
		return Verified{}, false
	}
	return e.verified, true
}

// put keeps the verified token under the digest until the given time, if there is room for it at now.
func (c *tokenCache) put(digest [sha256.Size]byte, v Verified, until, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[digest]; !ok && len(c.entries) >= tokenCacheSize {
		for d, e := range c.entries {
			if !now.Before(e.until) {
				delete(c.entries, d)
			}
		}
		if len(c.entries) >= tokenCacheSize {
			return
		}
	}
	if c.entries == nil {
		c.entries = map[[sha256.Size]byte]cachedToken{}
	}
	c.entries[digest] = cachedToken{verified: v, until: until}
}
