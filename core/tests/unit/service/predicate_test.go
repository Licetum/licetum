package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

func condition(attr, op string, value any) model.PredicateCondition {
	result := model.PredicateCondition{Attr: attr, Op: op}
	switch value := value.(type) {
	case string:
		result.Value = model.PredicateValue{Str: value}
	case []string:
		result.Value = model.PredicateValue{List: value, IsList: true}
	}
	return result
}

func TestEvaluatePredicateMatchesClaimsUsingStableStringRules(t *testing.T) {
	claims := engine.Claims{
		"department": "sales", "level": 5, "ratio": 1.5, "active": true,
		"groups": []any{"a", "b"}, "email": "alice@corp", "nothing": nil,
	}
	tests := []struct {
		name string
		pred []model.PredicateCondition
		want bool
	}{
		{"empty matches everyone", nil, true},
		{"eq string", []model.PredicateCondition{condition("department", "eq", "sales")}, true},
		{"eq mismatch", []model.PredicateCondition{condition("department", "eq", "hr")}, false},
		{"eq integer uses claim string", []model.PredicateCondition{condition("level", "eq", "5")}, true},
		{"eq float uses claim string", []model.PredicateCondition{condition("ratio", "eq", "1.5")}, true},
		{"eq boolean uses Python spelling", []model.PredicateCondition{condition("active", "eq", "True")}, true},
		{"in list", []model.PredicateCondition{condition("level", "in", []string{"4", "5"})}, true},
		{"regex searches", []model.PredicateCondition{condition("email", "regex", "@corp$")}, true},
		{"missing claim never matches", []model.PredicateCondition{condition("absent", "eq", "x")}, false},
		{"conditions are ANDed", []model.PredicateCondition{condition("department", "eq", "sales"), condition("level", "eq", "6")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := engine.EvaluatePredicate(tt.pred, claims); got != tt.want {
				t.Fatalf("EvaluatePredicate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPyStrMatchesClaimFormattingContract(t *testing.T) {
	tests := map[string]string{
		`"x"`: "x", `5`: "5", `-3`: "-3", `1.5`: "1.5", `true`: "True", `false`: "False", `null`: "None",
		`["a", "it's"]`: `['a', "it's"]`, `[1, true, null]`: "[1, True, None]", `{"b":1,"a":"x"}`: "{'a': 'x', 'b': 1}",
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			var value any
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			if got := engine.PyStr(value); got != want {
				t.Fatalf("PyStr(%s) = %q, want %q", raw, got, want)
			}
		})
	}
}
