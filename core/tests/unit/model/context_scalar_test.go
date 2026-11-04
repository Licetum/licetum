package model_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/model"
)

// A request attribute that is an object or a list within a list is refused: a scalar or a list of scalars is all it takes.
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

// On an AuthZEN request a value that is an object, or a list within a list, is ignored, not refused: the request is decided
// as if it did not carry the attribute, and the other attributes are kept.
func TestContract_AZ06_NestedContextValuesAreIgnored(t *testing.T) {
	attrs, _, err := model.ParseAuthzenContext(map[string]any{
		"ip":   map[string]any{"x": "1"},
		"tags": []any{[]any{"x"}, map[string]any{"y": "2"}},
		"ok":   "yes",
	})
	if err != nil {
		t.Fatalf("a nested value must not be refused: %v", err)
	}
	if len(attrs) != 1 || attrs["ok"] != "yes" {
		t.Fatalf("only the scalar attribute is kept: %+v", attrs)
	}
}

// The values an AuthZEN request carries as scalars are the attributes the conditions read.
func TestContract_AZ06_ScalarContextValuesAreKept(t *testing.T) {
	raw := map[string]any{"n": 7, "ok": true, "s": "text", "tags": []any{"x", 1.5, false}}
	attrs, _, err := model.ParseAuthzenContext(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs) != len(raw) {
		t.Fatalf("every scalar is kept: %+v", attrs)
	}
	for name, want := range raw {
		if got, ok := attrs[name]; !ok || !jsonEqual(t, got, want) {
			t.Errorf("attribute %s: %v, want %v", name, got, want)
		}
	}
}

// Unknown fields are ignored: in the body of a request, which a decoder that does not refuse them reads, and in the
// context, where an unknown name is not a settings key and an unknown attribute is kept as one.
func TestContract_AZ06_UnknownFieldsAreIgnored(t *testing.T) {
	var evaluation model.AuthzenEvaluationRequest
	raw := `{"subject":{"type":"user","id":"alice","nickname":"al"},"resource":{"type":"branch","id":"b1"},` +
		`"action":{"name":"read"},"unexpected":{"x":1},"context":{"ip":"10.0.0.1"}}`
	if err := json.Unmarshal([]byte(raw), &evaluation); err != nil {
		t.Fatalf("an unknown field is not an error: %v", err)
	}
	if err := evaluation.Validate(); err != nil {
		t.Fatalf("the evaluation is complete: %v", err)
	}
	var batch model.AuthzenEvaluationsRequest
	if err := json.Unmarshal([]byte(`{"subject":{"type":"user","id":"alice"},"unexpected":true,`+
		`"evaluations":[{"resource":{"type":"branch","id":"b1"},"action":{"name":"read"},"x":0}]}`), &batch); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("the batch is complete: %v", err)
	}
	attrs, _, err := model.ParseAuthzenContext(map[string]any{"unexpected": "kept", "ip": "10.0.0.1"})
	if err != nil || len(attrs) != 2 {
		t.Fatalf("an attribute the rules do not know is kept: %+v, %v", attrs, err)
	}
}

// API03: the bounds of the context are refused on an AuthZEN request, at the limit accepted and one over refused: 64
// attributes (the ignored ones left out), a string of 1024 bytes, a list of 64 items with strings of 1024 bytes.
func TestContract_API03_ContextBounds(t *testing.T) {
	attrs := func(n int) map[string]any {
		out := map[string]any{}
		for i := range n {
			out["a"+strconv.Itoa(i)] = "x"
		}
		return out
	}
	if _, _, err := model.ParseAuthzenContext(attrs(64)); err != nil {
		t.Fatalf("64 attributes must be accepted: %v", err)
	}
	if _, _, err := model.ParseAuthzenContext(attrs(65)); err == nil {
		t.Fatal("65 attributes must be refused")
	}
	// The ignored ones are not counted: 64 valid attributes and some objects are accepted.
	withIgnored := attrs(64)
	withIgnored["nested"] = map[string]any{"x": "1"}
	if _, _, err := model.ParseAuthzenContext(withIgnored); err != nil {
		t.Fatalf("an ignored value is not counted: %v", err)
	}

	str := func(n int) map[string]any { return map[string]any{"path": strings.Repeat("p", n)} }
	if _, _, err := model.ParseAuthzenContext(str(1024)); err != nil {
		t.Fatalf("a string of 1024 bytes must be accepted: %v", err)
	}
	rejectedWith(t, errOf(model.ParseAuthzenContext(str(1025))), i18n.ContextValueTooLarge)

	list := func(n, itemLen int) map[string]any {
		items := make([]any, n)
		for i := range items {
			items[i] = strings.Repeat("v", itemLen)
		}
		return map[string]any{"tags": items}
	}
	if _, _, err := model.ParseAuthzenContext(list(64, 1024)); err != nil {
		t.Fatalf("64 items of 1024 bytes must be accepted: %v", err)
	}
	rejectedWith(t, errOf(model.ParseAuthzenContext(list(65, 1))), i18n.ContextValueTooLarge)
	rejectedWith(t, errOf(model.ParseAuthzenContext(list(1, 1025))), i18n.ContextValueTooLarge)
}

// errOf is the error of a parse, for the tests that only look at it.
func errOf(_ model.RequestContext, _ model.AuthzenLicetum, err error) error { return err }

// jsonEqual compares two values as JSON, which is how a request carries them.
func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}
