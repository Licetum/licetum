package engine

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestUnverifiedIssuerReadsOnlyTheIssOfAWellFormedToken(t *testing.T) {
	segment := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	token := func(payload string) string {
		return segment(`{"alg":"RS256"}`) + "." + segment(payload) + "." + segment("signature")
	}

	cases := []struct {
		name  string
		token string
		want  string
		ok    bool
	}{
		{"an issuer", token(`{"iss":"https://idp.example.com","sub":"x"}`), "https://idp.example.com", true},
		{"no issuer", token(`{"sub":"x"}`), "", false},
		{"an empty issuer", token(`{"iss":""}`), "", false},
		{"an issuer that is not a string", token(`{"iss":["a","b"]}`), "", false},
		{"a payload that is not JSON", token(`not json`), "", false},
		{"a payload that is not base64", segment("{}") + ".@@@." + segment("s"), "", false},
		{"two segments", segment("{}") + "." + segment(`{"iss":"a"}`), "", false},
		{"four segments", token(`{"iss":"a"}`) + ".x", "", false},
		{"an API key", "lct_0123456789abcdef_" + strings.Repeat("A", 43), "", false},
		{"nothing", "", "", false},
	}
	for _, c := range cases {
		got, ok := unverifiedIssuer(c.token)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}
