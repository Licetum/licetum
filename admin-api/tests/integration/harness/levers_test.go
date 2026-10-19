package harness_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/engine"
)

func TestLeversSetTheFreshnessOfTheRequest(t *testing.T) {
	type seen struct {
		level engine.Consistency
		set   bool
	}
	var got seen
	h := harness.Levers(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.level, got.set = engine.ConsistencyIn(r.Context())
	}))

	for _, c := range []struct {
		header string
		want   seen
		status int
	}{
		{"", seen{}, http.StatusOK},
		{"higher", seen{engine.HigherConsistency, true}, http.StatusOK},
		{"minimize_latency", seen{engine.MinimizeLatency, true}, http.StatusOK},
		{"Higher", seen{}, http.StatusBadRequest},
	} {
		got = seen{}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		if c.header != "" {
			req.Header.Set(harness.ConsistencyHeader, c.header)
		}
		h.ServeHTTP(rec, req)
		if rec.Code != c.status || got != c.want {
			t.Errorf("header %q: status %d, lever %+v; want status %d, lever %+v", c.header, rec.Code, got, c.status, c.want)
		}
	}
}
