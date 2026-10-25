package pepauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/sync/singleflight"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// The key sets (JWKS) of the identity providers are kept per URL. A key set is fetched when none is held or the held one
// is older than AUTH__JWKS_TTL; a token that names a key the held set lacks (a rotation) triggers one more fetch, but at
// most one per AUTH__JWKS_MIN_REFRESH per URL, whoever asks. The fetches of one URL are shared through singleflight, so
// a thousand tokens at once cost the provider one request, and a flood of tokens with random key ids costs it one per
// interval.

// Key is one key of a key set. The key is parsed when a token first names it.
type Key struct {
	kid    any
	hasKid bool
	raw    json.RawMessage

	once sync.Once
	jwk  jose.JSONWebKey
	err  error
}

// Parsed returns the key; an unusable key is an error that the caller answers as a key set it cannot read (a 424), not as an
// invalid token.
func (k *Key) Parsed() (*jose.JSONWebKey, error) {
	k.once.Do(func() { k.err = k.jwk.UnmarshalJSON(k.raw) })
	if k.err != nil {
		return nil, Unavailable{apperr.FailedDependency(i18n.JWKSKeyInvalid).Wrap(k.err)}
	}
	return &k.jwk, nil
}

// keySet is a fetched key set.
type keySet struct {
	keys      []*Key
	fetchedAt time.Time
}

// find returns the first key whose id is the given one; a token without a key id takes the first key that has none.
func (s *keySet) find(kid any, hasKid bool) *Key {
	for _, k := range s.keys {
		if k.hasKid == hasKid && (!hasKid || sameKid(k.kid, kid)) {
			return k
		}
	}
	return nil
}

func sameKid(a, b any) bool {
	if x, ok := a.(string); ok {
		y, ok := b.(string)
		return ok && x == y
	}
	return reflect.DeepEqual(a, b)
}

// KeySets holds the key set of every URL seen and fetches them. It belongs to no App: the settings and the
// observer are given by the owner, so the verifiers of a process share one set of keys.
type KeySets struct {
	// HTTP is the client of the fetches; each call bounds its own time.
	HTTP *http.Client
	// TTL is how long a key set is trusted before it is fetched again; MinRefresh is how often an unknown key id may
	// make it fetch again. A zero TTL fetches on every call.
	TTL, MinRefresh time.Duration
	// Observe, when set, is told the result of every fetch: "ok" or "error".
	Observe func(result string)

	mu     sync.Mutex
	sets   map[string]*keySet
	flight singleflight.Group
}

// Unavailable is a failure to read a key set, or a key of it. The caller answers it with a 424 (the token is neither accepted
// nor refused), unlike an invalid token, which is a 401.
type Unavailable struct{ err *apperr.Error }

func (e Unavailable) Error() string { return e.err.Error() }
func (e Unavailable) Unwrap() error { return e.err }

// ErrJWKSUnavailable is matched by every Unavailable (errors.Is): the key set of an issuer could not be read, which says
// nothing about the token that was presented.
var ErrJWKSUnavailable = errors.New("pepauth: key set unavailable")

func (e Unavailable) Is(target error) bool { return target == ErrJWKSUnavailable }

// resultOf is the result of a fetch as Observe is told it: "ok" or "error".
func resultOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// jwksTimeout bounds one fetch of a key set.
const jwksTimeout = 5 * time.Second

func (c *KeySets) get(url string) *keySet {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sets[url]
}

func (c *KeySets) put(url string, set *keySet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sets == nil {
		c.sets = map[string]*keySet{}
	}
	c.sets[url] = set
}

// KeyFor returns the key of the key set at jwksURL that signed a token with the given key id, or nil when the
// provider has no such key. A transport failure is an Unavailable.
func (ks *KeySets) KeyFor(ctx context.Context, jwksURL string, kid any, hasKid bool) (*Key, error) {
	now := time.Now()
	set := ks.get(jwksURL)
	if set != nil && now.Sub(set.fetchedAt) < ks.TTL {
		if key := set.find(kid, hasKid); key != nil {
			return key, nil
		}
		// An unknown key: the provider may have rotated. Ask again, but not more often than once per interval.
		if now.Sub(set.fetchedAt) < ks.MinRefresh {
			return nil, nil
		}
	}
	fresh, err := ks.fetchKeySet(ctx, jwksURL, now)
	if err != nil {
		return nil, err
	}
	return fresh.find(kid, hasKid), nil
}

// fetchKeySet fetches the key set of jwksURL, once for all the callers that wait for it. A caller that decided to fetch
// at asked does not fetch again when a fetch has completed since.
func (ks *KeySets) fetchKeySet(ctx context.Context, jwksURL string, asked time.Time) (*keySet, error) {
	v, err, _ := ks.flight.Do(jwksURL, func() (any, error) {
		if set := ks.get(jwksURL); set != nil && set.fetchedAt.After(asked) {
			return set, nil
		}
		doc, err := ks.fetchJWKS(context.WithoutCancel(ctx), jwksURL)
		if ks.Observe != nil {
			ks.Observe(resultOf(err))
		}
		if err != nil {
			return nil, err
		}
		set := &keySet{fetchedAt: time.Now()}
		for _, raw := range doc.Keys {
			var meta map[string]any
			if json.Unmarshal(raw, &meta) != nil {
				continue
			}
			kid, hasKid := meta["kid"]
			set.keys = append(set.keys, &Key{kid: kid, hasKid: hasKid, raw: raw})
		}
		ks.put(jwksURL, set)
		return set, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*keySet), nil
}

type jwksDocument struct {
	Keys []json.RawMessage `json:"keys"`
}

// fetchJWKS asks the provider for its key set.
func (ks *KeySets) fetchJWKS(ctx context.Context, jwksURL string) (*jwksDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, jwksTimeout)
	defer cancel()

	// The answer to a caller does not say where the key set is or why it could not be read (the URL and the transport error
	// would tell a caller who presents a token of an unregistered issuer's look-alike what the PDP can reach): the cause is
	// kept for the log only.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, Unavailable{apperr.FailedDependency(i18n.JWKSUnavailable).Wrap(err)}
	}
	resp, err := ks.HTTP.Do(req)
	if err != nil {
		return nil, Unavailable{apperr.FailedDependency(i18n.JWKSUnavailable).Wrap(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, Unavailable{apperr.FailedDependency(i18n.JWKSRejected).Wrap(fmt.Errorf("the key set answered %d", resp.StatusCode))}
	}
	var doc jwksDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, Unavailable{apperr.FailedDependency(i18n.JWKSBadJSON).Wrap(err)}
	}
	return &doc, nil
}
