package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// Branches is what the tests of the replicas ask a service: the resources of the type branch on which the holder of the token
// may do read (the search of AuthZEN, authzenreq.SearchResource). SeedPolicy gives alice read on every branch. It is the request
// of Replica.Permission, Fresh and TryPermission; the options are those of authzenreq (AtLeast, Page).
func Branches(ref, token string, opts ...authzenreq.Option) authzenreq.Request {
	// A token that names nobody (a refused token) still gets a well-formed request: the subject id is required, and the
	// server refuses the token itself with 401.
	subject := authzenreq.SubjectOf(token)
	if subject == "" {
		subject = "nobody"
	}
	return authzenreq.SearchResource(ref, subject, "branch", "read", opts...)
}

// PermissionRequest is the request of Replica.Permission: Branches, asking for an answer that reflects the last write made through
// AdminDo or Must (LastRevision, authzenreq.AtLeast). A test that measures the cost of that request warms it with this one.
func (c *Cluster) PermissionRequest(ref, token string) authzenreq.Request {
	return Branches(ref, token, authzenreq.AtLeast(c.LastRevision.Load()))
}

// A Lever sets how a replica serves a request, through a header that Levers turns into a lever of the engine: a test asks for
// the freshest answer without a parameter of the API, which a later pull request removes.
type Lever func(http.Header)

// Higher is the lever of the freshest answer: the answer cache is skipped and the sources are asked again (what
// ?consistency=higher asks of the API).
func Higher() Lever {
	return func(h http.Header) { h.Set(ConsistencyHeader, string(engine.HigherConsistency)) }
}

// questionHeader is what the harness adds to a question to the AuthZEN API: the levers, and the mark that the question may be sent
// again. A question changes nothing, and a client retries it on a connection the server closed while it was idle (a replica that is
// stopped or draining closes them) as it retries a GET; Go does that for a POST only when it holds an Idempotency-Key entry, and an
// empty one is not sent.
func questionHeader(levers []Lever) http.Header {
	h := http.Header{"Idempotency-Key": {}}
	for _, l := range levers {
		l(h)
	}
	return h
}

// newRequest builds a request with the JSON of body (when there is one), the token (when it is not empty) and the headers.
func newRequest(ctx context.Context, method, target, token string, body any, header http.Header) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	return req, nil
}

// HTTPRequest is the http.Request that sends the AuthZEN request to the server at base (the URL of a replica) with the token
// (empty: none), for a test that has a client of its own: a balancer, a load generator, a request that must not be reused. Like
// Replica.Send it carries the levers and may be sent again.
func HTTPRequest(ctx context.Context, base string, req authzenreq.Request, token string, levers ...Lever) (*http.Request, error) {
	return newRequest(ctx, req.Method, base+req.Path, token, req.Body, questionHeader(levers))
}
