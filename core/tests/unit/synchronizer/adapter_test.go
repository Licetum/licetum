package synchronizer_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/synchronizer"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBindIDParamRewritesOnlyTheNamedParameter(t *testing.T) {
	tests := []struct{ name, query, want string }{
		{"named parameter", "SELECT * FROM t WHERE id = :id", "SELECT * FROM t WHERE id = $1"},
		{"postgres cast", "SELECT id::text AS id FROM t WHERE id = :id", "SELECT id::text AS id FROM t WHERE id = $1"},
		{"explicit cast", "SELECT * FROM t WHERE id = CAST(:id AS int)", "SELECT * FROM t WHERE id = CAST($1 AS int)"},
		{"longer identifier", "SELECT * FROM t WHERE id=:id AND x = :idx", "SELECT * FROM t WHERE id=$1 AND x = :idx"},
		{"escaped colon", `SELECT '10\:30' AS t WHERE id = :id`, "SELECT '10:30' AS t WHERE id = $1"},
		{"colon in identifier", "SELECT a:id FROM t", "SELECT a:id FROM t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := synchronizer.BindIDParam(tt.query); got != tt.want {
				t.Fatalf("BindIDParam(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestHTTPAdapterSendsConfiguredHeadersAndBuildsPointURL(t *testing.T) {
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path+" "+r.Header.Get("Authorization"))
		body := `{"id":"p1","name":"Plano 1"}`
		if r.URL.Path == "/list" {
			body = `[{"id":"p1","name":"Plano 1","parent_id":"b1"}]`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	adapter := synchronizer.NewAdapterWithHTTPClient(nil, client)
	config := model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/list", Headers: map[string]string{"Authorization": "Bearer t"}}

	rows, err := adapter.FetchAll(context.Background(), config, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "p1" || rows[0].ParentID == nil || *rows[0].ParentID != "b1" {
		t.Fatalf("FetchAll = %+v, want one resource with parent b1", rows)
	}
	row := adapter.FetchOne(context.Background(), config, "p1", "")
	if row == nil || row.ID != "p1" || row.Name != "Plano 1" {
		t.Fatalf("FetchOne = %+v, want p1", row)
	}
	if got, want := strings.Join(requests, "|"), "/list Bearer t|/list/p1 Bearer t"; got != want {
		t.Fatalf("requests = %q, want %q", got, want)
	}
}

func TestHTTPAdapterRejectsMalformedResourceResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"p1"}`)), Request: r}, nil
	})}
	adapter := synchronizer.NewAdapterWithHTTPClient(nil, client)
	config := model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/resource"}
	if row := adapter.FetchOne(context.Background(), config, "p1", ""); row != nil {
		t.Fatalf("malformed resource must be rejected, got %+v", row)
	}
}
