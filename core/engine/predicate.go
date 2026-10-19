package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/licetum/licetum/core/model"
)

// Env is what a decision reads about one request besides the subject: the claims of its token, the attributes the
// request carries (its context) and the time of the decision. Conditions read them by their source (see
// model.PredicateCondition).
type Env struct {
	Claims  Claims
	Context Claims
	Now     time.Time
}

// NewEnv returns the environment of a request at the current time.
func NewEnv(claims Claims, context map[string]any) Env {
	return Env{Claims: claims, Context: context, Now: time.Now()}
}

// truth is the outcome of a condition. Unknown is what a condition reads from an attribute the request does not
// carry: it is neither held nor failed, and the rule decides what that means for it (see ruleTruth).
type truth uint8

const (
	no truth = iota
	Yes
	unknown
)

func truthOf(ok bool) truth {
	if ok {
		return Yes
	}
	return no
}

// and combines conditions that must all hold: one that fails decides it, an unknown one leaves it unknown.
func (t truth) and(u truth) truth {
	switch {
	case t == no || u == no:
		return no
	case t == unknown || u == unknown:
		return unknown
	}
	return Yes
}

// or combines two outcomes of one subject: held if either is, else unknown if either is.
func (t truth) or(u truth) truth {
	switch {
	case t == Yes || u == Yes:
		return Yes
	case t == unknown || u == unknown:
		return unknown
	}
	return no
}

// identitySet is what the caller is, as the rules see it: the subject itself and the groups it may belong to, each with
// its outcome. A key that is absent is no. A membership whose predicate reads an attribute the request does not carry is
// unknown, and the rule decides what that means for it (see ruleApplies).
type identitySet map[string]truth

// lookup returns the value a condition reads and whether the request carries it. The time is always known.
func (e Env) lookup(c model.PredicateCondition) (any, bool) {
	switch c.Source {
	case model.SourceContext:
		v, ok := e.Context[c.Attr]
		return v, ok && v != nil
	case model.SourceNow:
		switch c.Attr {
		case "hour":
			return e.Now.UTC().Hour(), true
		case "weekday":
			return isoWeekday(e.Now), true
		}
		return nil, false
	}
	v, ok := e.Claims[c.Attr]
	return v, ok && v != nil
}

// isoWeekday is the day of the week of t in UTC: Monday is 1, Sunday is 7.
func isoWeekday(t time.Time) int {
	wd := int(t.UTC().Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// evaluateCondition: a condition over an attribute the request does not carry is unknown.
func evaluateCondition(c model.PredicateCondition, e Env) truth {
	actual, ok := e.lookup(c)
	if !ok {
		return unknown
	}
	return truthOf(matchOne(c, actual))
}

// evaluatePredicate: every condition must hold; an empty list holds for everyone.
func evaluatePredicate(conds []model.PredicateCondition, e Env) truth {
	t := Yes
	for _, c := range conds {
		t = t.and(evaluateCondition(c, e))
		if t == no {
			return no
		}
	}
	return t
}

// EvaluatePredicate reports whether the conditions hold for the claims alone: the request carries no context, and an
// attribute it lacks does not hold.
func EvaluatePredicate(predicate []model.PredicateCondition, claims Claims) bool {
	return evaluatePredicate(predicate, Env{Claims: claims, Now: time.Now()}) == Yes
}

// resourceMatches reports whether a resource covers the conditions of a resource_where: the attributes are the ones
// its synchronizer reported (meta). A resource that does not report an attribute is not covered.
func resourceMatches(conds []model.PredicateCondition, meta map[string]any) bool {
	for _, c := range conds {
		actual, ok := meta[c.Attr]
		if !ok || actual == nil || !matchElements(c, actual) {
			return false
		}
	}
	return true
}

// matchElements compares an attribute of a resource: a list counts through its elements. The positive operators hold
// when one element does; the negative ones (ne, not_in) hold when every element does.
func matchElements(c model.PredicateCondition, actual any) bool {
	list, isList := actual.([]any)
	if !isList {
		return matchOne(c, actual)
	}
	negative := c.Op == model.OpNe || c.Op == model.OpNotIn
	for _, item := range list {
		ok := matchOne(c, item)
		if negative && !ok {
			return false
		}
		if !negative && ok {
			return true
		}
	}
	return negative
}

// matchOne compares one value with a condition. Claims are compared as their str() renders them (see PyStr), so the
// predicates written for the Python version keep matching.
func matchOne(c model.PredicateCondition, actual any) bool {
	s := PyStr(actual)
	switch c.Op {
	case model.OpEq:
		return !c.Value.IsList && s == c.Value.Str
	case model.OpNe:
		return !c.Value.IsList && s != c.Value.Str
	case model.OpIn:
		if c.Value.IsList {
			return slices.Contains(c.Value.List, s)
		}
		return s == c.Value.Str
	case model.OpNotIn:
		if c.Value.IsList {
			return !slices.Contains(c.Value.List, s)
		}
		return s != c.Value.Str
	case model.OpRegex:
		if c.Value.IsList {
			return false
		}
		re, err := compileCached(c.Value.Str)
		return err == nil && re.MatchString(s) // MatchString searches for a substring, like re.search
	case model.OpGt, model.OpGe, model.OpLt, model.OpLe:
		return compareNumbers(c.Op, s, c.Value)
	case model.OpInCIDR:
		ip := net.ParseIP(s)
		if ip == nil {
			return false
		}
		for _, network := range networksOf(c.Value) {
			if _, n, err := net.ParseCIDR(network); err == nil && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// compareNumbers applies a numeric operator; a value that is not a number holds for nothing.
func compareNumbers(op, actual string, value model.PredicateValue) bool {
	if value.IsList {
		return false
	}
	x, err := strconv.ParseFloat(actual, 64)
	if err != nil {
		return false
	}
	y, err := strconv.ParseFloat(value.Str, 64)
	if err != nil {
		return false
	}
	switch op {
	case model.OpGt:
		return x > y
	case model.OpGe:
		return x >= y
	case model.OpLt:
		return x < y
	case model.OpLe:
		return x <= y
	}
	return false
}

func networksOf(v model.PredicateValue) []string {
	if v.IsList {
		return v.List
	}
	return []string{v.Str}
}

var regexCache sync.Map // string -> *regexp.Regexp

func compileCached(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// PyStr renders a claim value the way Python's str() did in the legacy version
// (True/False for bool, ['a', 'b'] for lists, etc.), so rules written for the old
// implementation keep matching the same way.
func PyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return strconv.FormatInt(i, 10)
		}
		if f, err := x.Float64(); err == nil {
			return pyFloat(f)
		}
		return x.String()
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return pyFloat(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = pyRepr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = pyRepr(k) + ": " + pyRepr(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func pyRepr(v any) string {
	if s, ok := v.(string); ok {
		quote := "'"
		if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
			quote = `"`
		}
		s = strings.ReplaceAll(s, `\`, `\\`)
		if quote == "'" {
			s = strings.ReplaceAll(s, "'", `\'`)
		}
		return quote + s + quote
	}
	return PyStr(v)
}

func pyFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// PyTruthy mirrors Python's `if not value` for values decoded from JSON.
func PyTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
