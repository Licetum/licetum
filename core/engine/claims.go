package engine

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Claims are the claims of a verified JWT. Numbers are json.Number (decoded with UseNumber), so
// comparing "5" == claim gives the same result as in the Python version.
type Claims map[string]any

func decodeClaims(raw []byte) (Claims, error) {
	var claims Claims
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	err := dec.Decode(&claims)
	return claims, err
}

func claimInt(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(x), true
	case string:
		// Some IdPs send numbers as strings (pydantic accepted them too).
		if i, err := strconv.ParseInt(x, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}
