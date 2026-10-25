//go:build integration

package contracts

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
	"github.com/licetum/licetum/core/testsupport/policyfix"
)

// A case is asked as one evaluations request about its subject: the questions are the evaluations (a question without a
// resource is about the type as a whole), the attributes of the case and the request for an explanation are the context, and
// the freshest answer is asked for. The context of the case itself is not changed by it.
func TestEvaluationsOfACaseCarryItsQuestionsAndItsContext(t *testing.T) {
	f, err := policyfix.Parse("evaluations", []byte(`
contracts: []
services: [shop]
types:
  - {name: store, service: shop, sync: true}
  - {name: reports, service: shop, sync: false}
resources:
  - {type: store, id: s1}
cases:
  - name: with a context
    subject: ann
    context: {ip: 10.0.0.1}
    questions:
      - {type: store, resource: s1, action: read, allowed: true}
      - {type: reports, action: list, allowed: true}
  - name: without a context
    subject: bob
    questions:
      - {type: store, resource: s1, action: write, allowed: false}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		c    *policyfix.Case
		want string
	}{
		{&f.Cases[0], `{
			"subject": {"type": "user", "id": "ann"},
			"evaluations": [
				{"resource": {"type": "store", "id": "s1"}, "action": {"name": "read"}},
				{"resource": {"type": "reports", "id": "*"}, "action": {"name": "list"}}
			],
			"context": {"licetum": {"explain": true}, "ip": "10.0.0.1"}
		}`},
		{&f.Cases[1], `{
			"subject": {"type": "user", "id": "bob"},
			"evaluations": [{"resource": {"type": "store", "id": "s1"}, "action": {"name": "write"}}],
			"context": {"licetum": {"explain": true}}
		}`},
	} {
		req := evaluationsOf("shop", tc.c, tc.c.Questions)
		if req.Method != http.MethodPost || req.Path != "/service/shop/access/v1/evaluations" {
			t.Errorf("case %q: %s %s", tc.c.Name, req.Method, req.Path)
		}
		if got := req.Header.Get(authzenreq.ConsistencyHeader); got != "higher" {
			t.Errorf("case %q: the freshest answer is not asked for: %s = %q", tc.c.Name, authzenreq.ConsistencyHeader, got)
		}
		raw, err := json.Marshal(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case %q:\n got %s\nwant %s", tc.c.Name, raw, tc.want)
		}
	}
	if _, ok := f.Cases[0].Context["licetum"]; ok || len(f.Cases[0].Context) != 1 {
		t.Errorf("the request changed the context of the case: %v", f.Cases[0].Context)
	}
}
