package loadtest

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// op is one of the requests of the AuthZEN API the traffic sends.
type op int

const (
	opSearchResource op = iota
	opEvaluation
	opEvaluations10
	opEvaluations100
	opEvaluationProperties
	numOps
)

// opNames are the names of the requests, as the mix spells them and as the k6 script names its metrics. The script lists the
// same names in the same order (TestK6ScriptNamesTheRequestsOfTheMix keeps them so).
//   - search_resource: the documents a person may read, one page of 20 (POST …/access/v1/search/resource);
//   - evaluation: one question about a person (POST …/access/v1/evaluation);
//   - evaluations10 and evaluations100: ten and a hundred questions about a person in one request (…/evaluations);
//   - evaluation_properties: one question about another person, asked by a client application the service trusts, which
//     brings the claims of the person in subject.properties (…/evaluation with the token of the client).
var opNames = [numOps]string{"search_resource", "evaluation", "evaluations10", "evaluations100", "evaluation_properties"}

// Mix is the share of each request in the traffic: a request is drawn with probability weight/total (k6 draws them, see
// k6/check.js, which reads the same spelling).
type Mix [numOps]int

// DefaultMix is the traffic of a service that asks for every decision it makes: the lists of what a person may read, the
// evaluations of one question and of ten, and the questions about another person that a trusted client asks. The shares
// are those of the traffic budgets.yaml was measured with: the lists 2, the single questions 5, the questions in tens 2,
// the questions about another person 1.
const DefaultMix = "search_resource=2,evaluation=5,evaluations10=2,evaluation_properties=1"

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

// countDecisions counts the answers of a response: every "decision" field of the AuthZEN API, and the ones that are
// granted. The bodies are the ones the API writes, so the fields are matched as written. A search has no decisions.
func countDecisions(body []byte) (decisions, allowed int) {
	count := func(field string) int { return bytes.Count(body, []byte(field)) }
	return count(`"decision":`), count(`"decision":true`)
}
