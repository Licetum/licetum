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

// asPerson gives the claims of subject u of the profile as the properties of the subject of a request: the department, which
// picks the groups of the person. The traffic asks as the PEP client of the service, so the person is the subject of the body
// and the claims come with it; preferred_username is the subject itself. k6 spells the same properties in the script.
func (p Profile) asPerson(u int) authzenreq.Option {
	return authzenreq.WithProperties(map[string]any{"department": departmentName(u % p.Groups)})
}

// newRequest turns a request of the AuthZEN API, as authzenreq builds it, into an HTTP request to the check server at
// base, with the API key of the PEP client as the bearer. The warm-up and the oracle build their requests this way; k6 sends
// the same ones from the script, which spells them by hand.
func newRequest(ctx context.Context, base string, r authzenreq.Request, apiKey string) (*http.Request, error) {
	raw, err := json.Marshal(r.Body)
	if err != nil {
		return nil, fmt.Errorf("encode the request to %s: %w", r.Path, err)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, base+r.Path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
