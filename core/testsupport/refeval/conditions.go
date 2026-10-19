package refeval

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// truth is the value of a condition in three-valued logic: a condition that reads an attribute the request does not
// carry is unknown. The order no < unknown < yes makes AND the minimum and OR the maximum.
type truth int8

const (
	no truth = iota
	unknown
	yes
)

// truthOf turns a boolean into a truth.
func truthOf(b bool) truth {
	if b {
		return yes
	}
	return no
}

// attrs are what the conditions of one case read: the claims, the request context, and the time of the decision.
type attrs struct {
	claims  map[string]any
	context map[string]any
	now     time.Time
}

// value returns the text of the attribute a condition reads, or false when the request does not carry it. A claim or a
// context attribute that is present but null counts as missing.
func (a attrs) value(c model.PredicateCondition) (string, bool) {
	var raw any
	var ok bool
	switch c.Source {
	case model.SourceNow:
		switch c.Attr {
		case "hour":
			return strconv.Itoa(a.now.UTC().Hour()), true
		case "weekday":
			day := int(a.now.UTC().Weekday())
			if day == 0 {
				day = 7 // Monday is 1, Sunday is 7
			}
			return strconv.Itoa(day), true
		}
		return "", false
	case model.SourceContext:
		raw, ok = a.context[c.Attr]
	default:
		raw, ok = a.claims[c.Attr]
	}
	if !ok || raw == nil {
		return "", false
	}
	return pyStr(raw), true
}

// holds evaluates conditions joined by AND; no conditions hold.
func (a attrs) holds(conds []model.PredicateCondition) truth {
	out := yes
	for _, c := range conds {
		v, ok := a.value(c)
		if !ok {
			out = min(out, unknown)
			continue
		}
		out = min(out, truthOf(compare(c.Op, c.Value, v)))
	}
	return out
}

// compare applies an operator to the text of an attribute. A scalar value of in and not_in is a list of one item. eq, ne
// and regex take one value: with a list they never hold (validation lets it through, and the Python version compared
// the text with the list, which never matched). Numeric operators and in_cidr take one value too (in_cidr a list of
// networks as well) and are false on an attribute that is not a number or an address; the text is parsed as it is.
func compare(op string, v model.PredicateValue, s string) bool {
	values := v.List
	if !v.IsList {
		values = []string{v.Str}
	}
	switch op {
	case model.OpEq:
		return !v.IsList && s == v.Str
	case model.OpNe:
		return !v.IsList && s != v.Str
	case model.OpIn:
		return slices.Contains(values, s)
	case model.OpNotIn:
		return !slices.Contains(values, s)
	case model.OpRegex:
		if v.IsList {
			return false
		}
		re, err := regexp.Compile(v.Str)
		return err == nil && re.MatchString(s)
	case model.OpGt, model.OpGe, model.OpLt, model.OpLe:
		x, err1 := strconv.ParseFloat(s, 64)
		y, err2 := strconv.ParseFloat(v.Str, 64)
		if err1 != nil || err2 != nil || v.IsList {
			return false
		}
		switch op {
		case model.OpGt:
			return x > y
		case model.OpGe:
			return x >= y
		case model.OpLt:
			return x < y
		}
		return x <= y
	case model.OpInCIDR:
		ip := net.ParseIP(s)
		if ip == nil {
			return false
		}
		for _, n := range values {
			if _, network, err := net.ParseCIDR(n); err == nil && network.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// metaValue is a resource attribute as the synchronizer keeps it: a string, or a list of strings.
type metaValue struct {
	str    string
	list   []string
	isList bool
}

// metaOf returns the attributes of a resource, normalized as a synchronizer does: invalid names, nulls and objects are
// dropped, numbers and booleans are rendered like claim values, list items that are not scalars are dropped. The
// bounds on the number and size of attributes are not applied: a fixture states its attributes as the source reports
// them after the synchronizer took them.
func (g *graph) metaOf(r resKey) map[string]metaValue {
	if m, ok := g.meta[r]; ok {
		return m
	}
	out := map[string]metaValue{}
	if res := g.resources[r]; res != nil {
		for k, v := range res.Meta {
			if !model.ValidMetaKey(k) {
				continue
			}
			switch x := v.(type) {
			case nil, map[string]any:
			case []any:
				items := []string{}
				for _, item := range x {
					switch item.(type) {
					case nil, map[string]any, []any:
					default:
						items = append(items, pyStr(item))
					}
				}
				out[k] = metaValue{list: items, isList: true}
			default:
				out[k] = metaValue{str: pyStr(x)}
			}
		}
	}
	g.meta[r] = out
	return out
}

// contains reports whether a resource attribute equals a value, or, as a list, holds it.
func (m metaValue) contains(s string) bool {
	if m.isList {
		return slices.Contains(m.list, s)
	}
	return m.str == s
}

// whereMatches evaluates the conditions of a resource_where on the attributes of a resource. An attribute the resource
// lacks makes its condition false. On a list attribute ne and not_in hold when no item equals (is in) the value, every
// other operator when some item satisfies it.
func whereMatches(meta map[string]metaValue, conds []model.PredicateCondition) bool {
	for _, c := range conds {
		m, ok := meta[c.Attr]
		if !ok {
			return false
		}
		items := []string{m.str}
		if m.isList {
			items = m.list
		}
		var holds bool
		switch c.Op {
		case model.OpNe, model.OpNotIn:
			holds = true
			for _, item := range items {
				holds = holds && compare(c.Op, c.Value, item)
			}
		default:
			for _, item := range items {
				holds = holds || compare(c.Op, c.Value, item)
			}
		}
		if !holds {
			return false
		}
	}
	return true
}

// inForce reports whether a validity window holds a moment: from its start (included) to its end (excluded).
func inForce(from, to *time.Time, now time.Time) bool {
	return (from == nil || !now.Before(*from)) && (to == nil || now.Before(*to))
}

// matchesClaim reports whether a resource matches the value of a claim by name, id or meta.<attribute>.
func (g *graph) matchesClaim(r *policyfix.Resource, matchBy, value string) bool {
	switch matchBy {
	case "", model.MatchByName:
		return r.NameOf() == value
	case model.MatchByID:
		return r.ID == value
	}
	key, ok := model.MetaKeyOf(matchBy)
	if !ok {
		return false
	}
	m, ok := g.metaOf(resKey{r.Type, r.ID})[key]
	return ok && m.contains(value)
}

// pyStr renders a value as Python's str() would, which is how claim values, request attributes and resource attributes
// are compared: True and False, integers without a fraction (JSON numbers arrive as float64), lists as ['a', 'b'].
// truthy mirrors Python's `if not value` for a value decoded from JSON.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case float64:
		return x != 0
	case float32:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func pyStr(v any) string {
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
	case float64:
		return pyFloat(x)
	case float32:
		return pyFloat(float64(x))
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = pyRepr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
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

// pyRepr renders a value as Python's repr() would inside a list: strings quoted, the rest as pyStr.
func pyRepr(v any) string {
	s, ok := v.(string)
	if !ok {
		return pyStr(v)
	}
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = `"`
	}
	r := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`, quote, `\`+quote)
	return quote + r.Replace(s) + quote
}

// pyFloat renders a number: an integral value as an integer (a JSON integer is an int in Python), anything else as
// Python's repr of a float, which switches to the exponent form below 1e-4 and from 1e16.
func pyFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case f == math.Trunc(f) && math.Abs(f) < 1e16:
		return strconv.FormatInt(int64(f), 10)
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	exp, _ := strconv.Atoi(e[strings.LastIndexByte(e, 'e')+1:])
	if exp >= -4 && exp < 16 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return e
}
