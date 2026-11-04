package harness

import (
	"fmt"
	"net/http"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// ConsistencyHeader is the request header Levers turns into the freshness lever of the request (authzenreq.Fresh sets it).
const ConsistencyHeader = authzenreq.ConsistencyHeader

// Levers wraps a check API handler so that a test can set the levers of the engine that the network cannot: the header
// X-Test-Consistency (higher, or minimize_latency) becomes engine.WithConsistency in the context of the request, and
// wins over what the request asks for. It is test code and lives under tests/, so no binary has it: a real
// process takes the freshness of a request from its caller alone. A value that is none of the two levels is a 400, so
// that a misspelt lever never turns into a silent default.
func Levers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(ConsistencyHeader); v != "" {
			c := engine.Consistency(v)
			if c != engine.HigherConsistency && c != engine.MinimizeLatency {
				http.Error(w, fmt.Sprintf("%s: %q is neither %q nor %q", ConsistencyHeader, v, engine.HigherConsistency, engine.MinimizeLatency), http.StatusBadRequest)
				return
			}
			r = r.WithContext(engine.WithConsistency(r.Context(), c))
		}
		next.ServeHTTP(w, r)
	})
}
