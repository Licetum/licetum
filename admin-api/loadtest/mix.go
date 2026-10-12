package loadtest

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// op is one of the requests of the check API the traffic sends.
type op int

const (
	opPermission op = iota
	opCheck
	opAuthzen
	opAuthzenBatch
	opOnBehalf
	numOps
)

var opNames = [numOps]string{"permission", "check", "authzen", "authzen_batch", "on_behalf"}

// Mix is the share of each request in the traffic: a request is drawn with probability weight/total (k6 draws them, see
// k6/check.js, which reads the same spelling).
type Mix [numOps]int

// DefaultMix is the traffic of a service that asks for every decision it makes: the checks, the permission lists, the
// AuthZEN evaluations (single and batched) and the calls on behalf of a person.
const DefaultMix = "permission=2,check=3,authzen=3,authzen_batch=1,on_behalf=1"

// ParseMix reads "name=weight,..." over the names of the requests. A request left out has weight 0.
func ParseMix(s string) (Mix, error) {
	var m Mix
	total := 0
	for part := range strings.SplitSeq(s, ",") {
		name, weight, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return m, fmt.Errorf("mix %q: want name=weight", part)
		}
		i := indexOf(opNames[:], name)
		if i < 0 {
			return m, fmt.Errorf("mix: unknown request %q (one of %v)", name, opNames)
		}
		w, err := strconv.Atoi(weight)
		if err != nil || w < 0 {
			return m, fmt.Errorf("mix %s: weight %q is not a non-negative integer", name, weight)
		}
		m[i] = w
		total += w
	}
	if total == 0 {
		return m, fmt.Errorf("mix %q: no request has a weight", s)
	}
	return m, nil
}

// String is the mix as ParseMix reads it, with the requests that have a weight.
func (m Mix) String() string {
	parts := make([]string, 0, numOps)
	for i, w := range m {
		if w > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", opNames[i], w))
		}
	}
	return strings.Join(parts, ",")
}

func indexOf(names []string, name string) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return -1
}

// countDecisions counts the answers of a response: every "allowed" (check, on-behalf) and "decision" (AuthZEN) field,
// and the ones that are granted. The bodies are the ones the API writes, so the fields are matched as written.
func countDecisions(body []byte) (decisions, allowed int) {
	count := func(field string) int { return bytes.Count(body, []byte(field)) }
	decisions = count(`"allowed":`) + count(`"decision":`)
	allowed = count(`"allowed":true`) + count(`"decision":true`)
	return decisions, allowed
}
