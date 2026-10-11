package synchronizer_test

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/synchronizer"
)

func TestHTTPResourceAttributesNormalizeIntoMeta(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"id":"p1","name":"Project 1","parent_id":"g1","owner":"alice","stars":5,"archived":false,"owners":["alice","bob"],"nested":{"a":1},"nothing":null,"bad name":"x","meta":{"visibility":"public","owner":"carol"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("[" + body + "]")), Request: r}, nil
	})}
	adapter := synchronizer.NewAdapterWithHTTPClient(nil, client)
	rows, err := adapter.FetchAll(context.Background(), model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/list"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("FetchAll returned %d resources, want 1", len(rows))
	}
	resource := rows[0]
	if resource.ParentID == nil || *resource.ParentID != "g1" {
		t.Fatalf("parent_id = %v, want g1", resource.ParentID)
	}
	want := map[string]any{"owner": "carol", "stars": "5", "archived": "False", "owners": []any{"alice", "bob"}, "visibility": "public"}
	if !reflect.DeepEqual(resource.Meta, want) {
		t.Fatalf("meta = %#v, want %#v", resource.Meta, want)
	}
}

func TestHTTPResourceAttributesDropObjectsAndOversizedValues(t *testing.T) {
	body := `[{"id":"p1","name":"Project 1","too_long":"` + strings.Repeat("x", 1025) + `","nested":{"x":1},"valid":"yes"}]`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	adapter := synchronizer.NewAdapterWithHTTPClient(nil, client)
	rows, err := adapter.FetchAll(context.Background(), model.SynchronizerConfig{Kind: model.KindHTTP, URL: "https://source.test/list"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("FetchAll returned %d resources, want 1", len(rows))
	}
	if !reflect.DeepEqual(rows[0].Meta, map[string]any{"valid": "yes"}) {
		t.Fatalf("meta = %#v, want only valid scalar attribute", rows[0].Meta)
	}
}
