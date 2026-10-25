package authzenreq_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// token is a JWT with the claims; the signature is not looked at.
func token(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return enc.EncodeToString(header) + "." + enc.EncodeToString(payload) + "." + enc.EncodeToString([]byte("sig"))
}

func TestSubjectOfReadsTheSubjectTheCheckAPIReads(t *testing.T) {
	for _, c := range []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{"preferred_username", map[string]any{"preferred_username": "alice", "sub": "alice-sub"}, "alice"},
		{"sub when there is no preferred_username", map[string]any{"sub": "alice-sub"}, "alice-sub"},
		{"sub when preferred_username is empty", map[string]any{"preferred_username": "", "sub": "alice-sub"}, "alice-sub"},
		{"a number", map[string]any{"sub": 42}, "42"},
		{"nobody", map[string]any{"iss": "https://idp.example.com"}, ""},
		{"a claim that is no text", map[string]any{"preferred_username": []string{"a"}, "sub": map[string]any{}}, ""},
	} {
		if got := authzenreq.SubjectOf(token(t, c.claims)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A token that is refused must still give a request, which the server then refuses for the token.
func TestSubjectOfADamagedTokenIsEmpty(t *testing.T) {
	good := token(t, map[string]any{"preferred_username": "alice"})
	for name, tok := range map[string]string{
		"empty":      "",
		"garbage":    "garbage",
		"two parts":  "a.b",
		"four parts": good + ".x",
		"not base64": "a.!!!.c",
		"not JSON":   "a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".c",
		"not object": "a." + base64.RawURLEncoding.EncodeToString([]byte(`["alice"]`)) + ".c",
	} {
		if got := authzenreq.SubjectOf(tok); got != "" {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestSubjectOfAcceptsAPaddedPayload(t *testing.T) {
	payload := base64.URLEncoding.EncodeToString([]byte(`{"preferred_username":"al"}`)) // padded
	if got := authzenreq.SubjectOf("h." + payload + ".s"); got != "al" {
		t.Errorf("got %q", got)
	}
}
