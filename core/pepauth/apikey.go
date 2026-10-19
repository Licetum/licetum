package pepauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// An API key of a PEP is "lct_" + 16 lowercase hex characters (the key id, which is public) + "_" + the 43 base64url
// characters of a 32-byte secret, without padding. Only the key id and the SHA-256 of the secret are stored; the secret is
// shown once, when the key is made, and is never logged.
const (
	apiKeyPrefix    = "lct_"
	apiKeyIDLen     = 16
	apiKeySecretLen = 43
	apiKeyLen       = len(apiKeyPrefix) + apiKeyIDLen + 1 + apiKeySecretLen
)

// ErrAPIKeyMalformed is the rejection of a string that is not an API key in the format above.
var ErrAPIKeyMalformed = errors.New("pepauth: malformed API key")

// dummyHash is what DummyCompare compares a secret with.
var dummyHash = HashSecret("")

// NewAPIKey makes an API key: the full string its holder presents, its key id, and the hash of its secret, which is what
// the store keeps.
func NewAPIKey() (full string, keyID string, hash []byte, err error) {
	id := make([]byte, apiKeyIDLen/2)
	if _, err := rand.Read(id); err != nil {
		return "", "", nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, err
	}
	keyID = hex.EncodeToString(id)
	random := base64.RawURLEncoding.EncodeToString(raw)
	return apiKeyPrefix + keyID + "_" + random, keyID, HashSecret(random), nil
}

// ParseAPIKey splits an API key into its key id and its secret. It is strict: the length, the prefix, the separator, the
// lowercase hex of the key id and the base64url alphabet of the secret must all be as the format says, and the secret must
// decode to exactly 32 bytes with no bits of padding set. Anything else is ErrAPIKeyMalformed.
func ParseAPIKey(s string) (keyID, random string, err error) {
	if len(s) != apiKeyLen || !strings.HasPrefix(s, apiKeyPrefix) || s[len(apiKeyPrefix)+apiKeyIDLen] != '_' {
		return "", "", ErrAPIKeyMalformed
	}
	keyID = s[len(apiKeyPrefix) : len(apiKeyPrefix)+apiKeyIDLen]
	random = s[len(apiKeyPrefix)+apiKeyIDLen+1:]
	if !isLowerHex(keyID) || !isBase64URL(random) {
		return "", "", ErrAPIKeyMalformed
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(random)
	if err != nil || len(raw) != 32 {
		return "", "", ErrAPIKeyMalformed
	}
	return keyID, random, nil
}

// HashSecret is the hash that is stored for the random part of an API key: its SHA-256. A fast hash is the right one here: the
// input is 32 bytes from a cryptographic random source, so there is nothing to guess and nothing to gain from a slow, salted hash;
// those are for passwords that people choose.
func HashSecret(random string) []byte {
	sum := sha256.Sum256([]byte(random))
	return sum[:]
}

// CompareSecret reports whether the secret is the one the stored hash was made of. The comparison takes the same time
// whatever the secret.
func CompareSecret(stored []byte, random string) bool {
	return subtle.ConstantTimeCompare(stored, HashSecret(random)) == 1
}

// DummyCompare does the work of CompareSecret for a key id that is unknown, so that a request with an unknown key takes as
// long as one with a known key and the timing does not tell the two apart. Its result is not used.
func DummyCompare(random string) {
	_ = CompareSecret(dummyHash, random)
}

// isLowerHex reports whether s is made of the characters 0-9 and a-f only.
func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isBase64URL reports whether s is made of the characters of the base64url alphabet only (no padding).
func isBase64URL(s string) bool {
	for _, c := range s {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
