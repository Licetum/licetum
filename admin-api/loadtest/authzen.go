package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// documentType and readAction are what the traffic asks: whether a subject may read a document.
const (
	documentType = "document"
	readAction   = "read"
)

// newRequest turns a request of the AuthZEN API, as authzenreq builds it, into an HTTP request to the check server at
// base with the token of the caller. The warm-up and the oracle build their requests this way; k6 sends the same ones
// from the script, which spells them by hand.
func newRequest(ctx context.Context, base string, r authzenreq.Request, token string) (*http.Request, error) {
	raw, err := json.Marshal(r.Body)
	if err != nil {
		return nil, fmt.Errorf("encode the request to %s: %w", r.Path, err)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, base+r.Path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
