//go:build integration

package api_test

import (
	"net/http"
	"testing"
)

// GET /service/{id}/policy/properties tells a PEP what it has to send: the properties of the subject and the attributes of the context
// that the policy of the service reads.
func TestPolicyPropertiesListWhatTheRulesRead(t *testing.T) {
	w := newWorld(t)
	group := w.must("POST", "/group", map[string]any{"name": "sales"})["id"].(string)
	w.must("POST", "/group/"+group+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	w.grant(map[string]any{"type_resource_id": w.branch, "member_of_group_id": group, "access": []string{"read"}, "requires_predicate": []any{
		map[string]any{"attr": "ip", "op": "in_cidr", "source": "context", "value": []string{"10.0.0.0/8"}}}})
	w.grant(map[string]any{"type_resource_id": w.branch, "subject": "walker", "access": []string{"read"},
		"resource_from_claim": map[string]any{"claim": "branch_name", "lookup_type_resource_id": w.branch}})
	// A rule on a type of another service does not belong to this one.
	w.must("POST", "/service/"+w.appSvc+"/type_resource", map[string]any{"name": "other"})

	status, raw := w.do("GET", w.admin.URL+"/service/"+w.org+"/policy/properties", nil)
	if status != http.StatusOK {
		t.Fatalf("properties: %d %s", status, raw)
	}
	type row struct {
		source                      string
		rules, targets, memberships float64
	}
	got := map[string]row{}
	for _, c := range decodeList(t, raw) {
		got[c["source"].(string)+":"+c["name"].(string)] = row{c["source"].(string), c["rules"].(float64), c["targets"].(float64), c["memberships"].(float64)}
	}
	if got["claim:department"].memberships != 1 {
		t.Fatalf("department is read by the membership: %+v", got)
	}
	if got["context:ip"].rules != 1 {
		t.Fatalf("ip is read from the context by a guard: %+v", got)
	}
	if got["claim:branch_name"].targets != 1 {
		t.Fatalf("branch_name is read by a claim-based target: %+v", got)
	}
}
