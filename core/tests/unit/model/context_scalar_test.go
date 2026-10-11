package model_test

import (
	"testing"

	"github.com/licetum/licetum/core/model"
)

// A request attribute is a scalar or a list of scalars: an object is refused, nested lists too.
func TestContextObjectsAreRefused(t *testing.T) {
	if err := (model.RequestContext{"ip": map[string]any{"x": "1"}}).Validate(); err == nil {
		t.Error("an object is accepted as a request attribute")
	}
	if err := (model.RequestContext{"tags": []any{map[string]any{"x": "1"}}}).Validate(); err == nil {
		t.Error("a list holding an object is accepted")
	}
	if err := (model.RequestContext{"tags": []any{[]any{"x"}}}).Validate(); err == nil {
		t.Error("a list within a list is accepted")
	}
	if err := (model.RequestContext{"n": 7, "ok": true, "tags": []any{"x", 1.5, false}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
