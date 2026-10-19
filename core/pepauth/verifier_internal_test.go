package pepauth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"strconv"
	"testing"
	"time"
)

// TestContract_PEP01_AKeyMustBeOfTheTypeOfItsAlgorithm checks the key of a token against its algorithm: the same algorithm
// on another key type is refused before the signature is looked at.
func TestContract_PEP01_AKeyMustBeOfTheTypeOfItsAlgorithm(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		alg string
		key any
		ok  bool
	}{
		{"RS256", &rsaKey.PublicKey, true},
		{"PS256", &rsaKey.PublicKey, true},
		{"RS256", p256.Public(), false},
		{"ES256", p256.Public(), true},
		{"ES256", p384.Public(), false},
		{"ES384", p384.Public(), true},
		{"ES256", edPub, false},
		{"EdDSA", edPub, true},
		{"EdDSA", &rsaKey.PublicKey, false},
		{"none", &rsaKey.PublicKey, false},
		{"HS256", &rsaKey.PublicKey, false},
	}
	for _, c := range cases {
		if got := keyFits(c.alg, c.key); got != c.ok {
			t.Errorf("keyFits(%s, %T) = %v, want %v", c.alg, c.key, got, c.ok)
		}
	}
}

// TestTokenCacheIsBounded: the verified tokens are kept up to the size cap, and the expired ones make room for new ones.
func TestTokenCacheIsBounded(t *testing.T) {
	var c tokenCache
	now := time.Now()
	digest := func(s string) [sha256.Size]byte { return sha256.Sum256([]byte(s)) }
	for i := range tokenCacheSize + 100 {
		c.put(digest(strconv.Itoa(i)), Verified{}, now.Add(time.Minute), now)
	}
	if n := len(c.entries); n != tokenCacheSize {
		t.Fatalf("%d entries, want the cap of %d", n, tokenCacheSize)
	}
	if _, ok := c.get(digest("0"), now); !ok {
		t.Fatal("a live entry is gone")
	}
	later := now.Add(2 * time.Minute)
	c.put(digest("new"), Verified{}, later.Add(time.Minute), later)
	if n := len(c.entries); n != 1 {
		t.Fatalf("%d entries after the expired ones were swept, want 1", n)
	}
	if _, ok := c.get(digest("0"), later); ok {
		t.Fatal("an expired entry is served")
	}
}
