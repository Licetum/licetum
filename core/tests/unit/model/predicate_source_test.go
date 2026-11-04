package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/model"
)

func TestPredicateSourceRejectsTheOldNameInStoredAndRequestJSON(t *testing.T) {
	var condition model.PredicateCondition
	if err := json.Unmarshal([]byte(`{"attr":"department","op":"eq","value":"sales","source":"claim"}`), &condition); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("old source needs the replacement hint: %v", err)
	}
	for _, source := range []string{"subject", "context", "now"} {
		raw := `{"attr":"hour","op":"eq","value":"12","source":"` + source + `"}`
		if err := json.Unmarshal([]byte(raw), &condition); err != nil || condition.Source != source {
			t.Fatalf("source %s: %+v %v", source, condition, err)
		}
	}
}

func TestPredicateDefaultsAreCanonicalWithoutMutatingTheCallersSlice(t *testing.T) {
	original := []model.PredicateCondition{{Attr: "department", Op: model.OpEq, Value: model.PredicateValue{Str: "sales"}}}
	input := model.GroupMemberInput{Predicate: original}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	if input.Predicate[0].Source != model.SourceSubject || original[0].Source != "" {
		t.Fatalf("default must be explicit in the input copy: %+v original %+v", input.Predicate, original)
	}
}
