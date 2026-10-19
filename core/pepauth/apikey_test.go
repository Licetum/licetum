package pepauth

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
)

var apiKeyShape = regexp.MustCompile(`^lct_[0-9a-f]{16}_[A-Za-z0-9_-]{43}$`)

// TestAPIKeyIsMadeAndParsedBackInItsFormat: a made key has the format, parses to its key id and secret, and the hash stored
// for the secret is the one the secret is compared with.
func TestAPIKeyIsMadeAndParsedBackInItsFormat(t *testing.T) {
	full, keyID, hash, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !apiKeyShape.MatchString(full) {
		t.Fatalf("the key %q is not in the format", full)
	}
	gotID, secret, err := ParseAPIKey(full)
	if err != nil {
		t.Fatalf("the key does not parse: %v", err)
	}
	if gotID != keyID || !strings.HasPrefix(full, "lct_"+keyID+"_") {
		t.Fatalf("parsed id %q, made id %q", gotID, keyID)
	}
	if !bytes.Equal(hash, HashSecret(secret)) {
		t.Fatal("the stored hash is not the hash of the secret")
	}
	if !CompareSecret(hash, secret) {
		t.Fatal("the secret does not match its hash")
	}
	if CompareSecret(hash, secret+"x") || CompareSecret(hash[:31], secret) || CompareSecret(nil, secret) {
		t.Fatal("a different secret, or a hash of another length, matches")
	}

	other, _, _, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if other == full {
		t.Fatal("two keys are the same")
	}
}

// TestAPIKeyFormatIsStrict: the strings of the format are parsed, and every other string is refused.
func TestAPIKeyFormatIsStrict(t *testing.T) {
	id := "0123456789abcdef"
	canonical := strings.Repeat("A", 42) + "A" // 43 characters, 32 zero bytes
	cases := []struct {
		name string
		key  string
		ok   bool
	}{
		{"the format", "lct_" + id + "_" + canonical, true},
		{"no separator", "lct_" + id + canonical, false},
		{"another prefix", "lcx_" + id + "_" + canonical, false},
		{"no prefix", id + "_" + canonical, false},
		{"upper-case hex in the key id", "lct_0123456789ABCDEF_" + canonical, false},
		{"a character that is not hex in the key id", "lct_0123456789abcdeg_" + canonical, false},
		{"a short key id", "lct_0123456789abcde_" + canonical, false},
		{"a dash for the separator", "lct_" + id + "-" + canonical, false},
		{"a secret of 42 characters", "lct_" + id + "_" + strings.Repeat("A", 42), false},
		{"a secret of 44 characters", "lct_" + id + "_" + canonical + "A", false},
		{"a standard base64 character", "lct_" + id + "_" + strings.Repeat("A", 42) + "+", false},
		{"base64 padding", "lct_" + id + "_" + strings.Repeat("A", 42) + "=", false},
		{"a newline in the secret", "lct_" + id + "_" + strings.Repeat("A", 42) + "\n", false},
		{"bits of padding set in the last character", "lct_" + id + "_" + strings.Repeat("A", 42) + "B", false},
		{"a leading space", " lct_" + id + "_" + canonical, false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keyID, secret, err := ParseAPIKey(c.key)
			if c.ok {
				if err != nil || keyID != id || secret != canonical {
					t.Fatalf("parsed %q, %q, %v", keyID, secret, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the key %q is accepted as %q", c.key, keyID)
			}
			if !errors.Is(err, ErrAPIKeyMalformed) {
				t.Fatalf("err = %v, want ErrAPIKeyMalformed", err)
			}
		})
	}
}

// TestAPIKeyDummyCompareDoesNotPanic: the comparison for an unknown key id runs for any secret, and says nothing.
func TestAPIKeyDummyCompareDoesNotPanic(t *testing.T) {
	for _, secret := range []string{"", "x", strings.Repeat("A", 43), "lct_not-a-key"} {
		DummyCompare(secret)
	}
	if !bytes.Equal(dummyHash, HashSecret("")) {
		t.Fatal("the dummy hash is not the hash of the empty secret")
	}
}
