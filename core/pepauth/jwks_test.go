package pepauth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// jwksServer serves a key set whose keys the test changes, and counts the requests.
type jwksServer struct {
	*httptest.Server
	mu       sync.Mutex
	kids     []string
	requests atomic.Int32
	status   int
}

func newJWKSServer(t *testing.T, kids ...string) *jwksServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &jwksServer{kids: kids}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.status != 0 {
			http.Error(w, "no", s.status)
			return
		}
		var set jose.JSONWebKeySet
		for _, kid := range s.kids {
			set.Keys = append(set.Keys, jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"})
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) setKids(kids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kids = kids
}

func jwksKeySets(ttl, minRefresh time.Duration) *KeySets {
	return &KeySets{HTTP: &http.Client{}, TTL: ttl, MinRefresh: minRefresh}
}

func TestJWKSIsFetchedOncePerTTLForManyTokens(t *testing.T) {
	srv := newJWKSServer(t, "k1")
	a := jwksKeySets(time.Hour, 0)
	for range 1000 {
		key, err := a.KeyFor(t.Context(), srv.URL, "k1", true)
		if err != nil || key == nil {
			t.Fatalf("key = %v, err = %v", key, err)
		}
	}
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("%d JWKS requests for 1000 tokens of one key, want 1", got)
	}
}

func TestJWKSExpiresAfterTheTTL(t *testing.T) {
	srv := newJWKSServer(t, "k1")
	a := jwksKeySets(50*time.Millisecond, time.Hour)
	if _, err := a.KeyFor(t.Context(), srv.URL, "k1", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := a.KeyFor(t.Context(), srv.URL, "k1", true); err != nil {
		t.Fatal(err)
	}
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("%d JWKS requests, want 2 (one per TTL)", got)
	}
}

func TestJWKSUnknownKeyRefetchesAtMostOncePerInterval(t *testing.T) {
	srv := newJWKSServer(t, "k1")
	a := jwksKeySets(time.Hour, 200*time.Millisecond)
	if _, err := a.KeyFor(t.Context(), srv.URL, "k1", true); err != nil {
		t.Fatal(err)
	}
	// A flood of tokens with random key ids: the fetch just made is the one for this interval.
	for i := range 1000 {
		key, err := a.KeyFor(t.Context(), srv.URL, "random-"+string(rune('a'+i%26))+string(rune('a'+i/26%26)), true)
		if err != nil || key != nil {
			t.Fatalf("an unknown key: %v, %v", key, err)
		}
	}
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("%d JWKS requests for 1000 unknown keys inside the interval, want 1", got)
	}

	// After the interval one token with an unknown key asks again; a rotation is then seen after exactly one refetch.
	time.Sleep(250 * time.Millisecond)
	srv.setKids("k1", "k2")
	for range 10 {
		key, err := a.KeyFor(t.Context(), srv.URL, "k2", true)
		if err != nil || key == nil {
			t.Fatalf("the rotated key: %v, %v", key, err)
		}
	}
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("%d JWKS requests after the rotation, want 2", got)
	}
}

func TestJWKSConcurrentFetchesOfOneURLAreShared(t *testing.T) {
	srv := newJWKSServer(t, "k1")
	gate := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
		http.Redirect(w, r, srv.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(slow.Close)

	a := jwksKeySets(time.Hour, 0)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if key, err := a.KeyFor(t.Context(), slow.URL, "k1", true); err != nil || key == nil {
				t.Errorf("key = %v, err = %v", key, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("%d requests for 50 concurrent tokens, want 1", got)
	}
}

func TestJWKSTransportFailureIsNotAnInvalidToken(t *testing.T) {
	srv := newJWKSServer(t, "k1")
	srv.status = http.StatusInternalServerError
	a := jwksKeySets(10*time.Minute, 30*time.Second)
	_, err := a.KeyFor(t.Context(), srv.URL, "k1", true)
	var unavailable Unavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %v, want a JWKS failure (it counts toward the breaker)", err)
	}
	// Nothing was cached: the next token asks again, and succeeds once the provider is back.
	srv.mu.Lock()
	srv.status = 0
	srv.mu.Unlock()
	if key, err := a.KeyFor(t.Context(), srv.URL, "k1", true); err != nil || key == nil {
		t.Fatalf("after the provider recovered: %v, %v", key, err)
	}
}

func TestJWKSKeyWithoutKidMatchesTokenWithoutKid(t *testing.T) {
	set := &keySet{keys: []*Key{{kid: "a", hasKid: true}, {hasKid: false}}}
	if set.find(nil, false) != set.keys[1] {
		t.Error("a token without a key id takes the first key without one")
	}
	if set.find("a", true) != set.keys[0] || set.find("b", true) != nil {
		t.Error("a token with a key id takes the key of that id")
	}
	// A key id that is not a string must not panic.
	if set.find([]any{"a"}, true) != nil {
		t.Error("a list is not a key id")
	}
}
