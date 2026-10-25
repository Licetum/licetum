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

// reply serves every request with one status and body.
func reply(status int, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
}

// An answer that lists no resource is an answer: the caller applies it (the resources of the type go away).
func TestAnEmptyListIsAnAnswer(t *testing.T) {
	adapter := synchronizer.NewAdapterWithHTTPClient(reply(http.StatusOK, "[]"))
	rows, err := adapter.FetchAll(context.Background(), model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/list"}, "", false)
	if err != nil {
		t.Fatalf("an empty list is a valid answer, got error %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}

// Anything that is not an answer is an error, so the caller keeps the data it has.
func TestAnUnansweredSourceIsAnError(t *testing.T) {
	cases := map[string]*http.Client{
		"server error": reply(http.StatusInternalServerError, "[]"),
		"not found":    reply(http.StatusNotFound, "[]"),
		"null body":    reply(http.StatusOK, "null"),
		"invalid JSON": reply(http.StatusOK, "[{"),
	}
	for name, client := range cases {
		adapter := synchronizer.NewAdapterWithHTTPClient(client)
		_, err := adapter.FetchAll(context.Background(), model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/list"}, "", false)
		if err == nil {
			t.Fatalf("%s: want an error, got none", name)
		}
	}
}

// A SQL source without a list query has no answer to give: it is not an empty list.
func TestSQLSourceWithoutListQueryIsAnError(t *testing.T) {
	adapter := synchronizer.NewAdapterWithHTTPClient(nil)
	_, err := adapter.FetchAll(context.Background(), model.SynchronizerConfig{Kind: model.KindSQL, DSN: "postgres://x"}, "", false)
	if err == nil {
		t.Fatal("a SQL source without list_query must not answer with an empty list")
	}
}
