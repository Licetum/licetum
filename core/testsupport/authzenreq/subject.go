package authzenreq

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// SubjectOf is the subject of an access token, the way the check API reads it: preferred_username, else sub. The token is
// not verified, only its payload is read. A token that cannot be read, or that names nobody, gives the empty string, so that
// a test of a refused token still builds a request and the server refuses it for the token.
func SubjectOf(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&claims) != nil {
		return ""
	}
	if s := claimText(claims["preferred_username"]); s != "" {
		return s
	}
	return claimText(claims["sub"])
}

// claimText is a claim as the server reads it: strings and numbers; anything else names nobody.
func claimText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}
