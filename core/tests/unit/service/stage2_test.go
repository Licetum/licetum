package service_test

import (
	"encoding/json"
	"testing"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

// A batch is checked before anything is written: the error names the operation that is wrong, so a client can fix it.
func TestPredicatesOnClaimsCompareNumbersAndNetworks(t *testing.T) {
	level := func(v string) engine.Claims { return engine.Claims{"level": json.Number(v)} }
	gt := []model.PredicateCondition{{Attr: "level", Op: model.OpGt, Value: model.PredicateValue{Str: "5"}}}
	if !engine.EvaluatePredicate(gt, level("7")) || engine.EvaluatePredicate(gt, level("5")) {
		t.Fatal("gt does not compare the claim as a number")
	}
	if engine.EvaluatePredicate(gt, engine.Claims{}) {
		t.Fatal("an absent claim satisfies gt")
	}

	network := []model.PredicateCondition{{Attr: "ip", Op: model.OpInCIDR,
		Value: model.PredicateValue{List: []string{"10.0.0.0/8", "192.168.1.0/24"}, IsList: true}}}
	if !engine.EvaluatePredicate(network, engine.Claims{"ip": "192.168.1.9"}) {
		t.Fatal("an address inside a network is not in it")
	}
	if engine.EvaluatePredicate(network, engine.Claims{"ip": "172.16.0.1"}) {
		t.Fatal("an address outside every network is in one")
	}
	if engine.EvaluatePredicate(network, engine.Claims{"ip": "not-an-address"}) {
		t.Fatal("a malformed address is in a network")
	}

	notIn := []model.PredicateCondition{{Attr: "team", Op: model.OpNotIn, Value: model.PredicateValue{List: []string{"x"}, IsList: true}}}
	if engine.EvaluatePredicate(notIn, engine.Claims{}) {
		t.Fatal("an absent claim satisfies not_in: a condition over a missing attribute never holds")
	}
}
