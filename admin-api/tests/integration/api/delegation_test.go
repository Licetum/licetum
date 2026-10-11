//go:build integration

package api_test

// Integration tests of the calls made on behalf of a person: the trusted clients of a service, the claims a call carries,
// the audit, and the list of claims the policy reads.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func (w *world) onBehalf(path string, body any, claims jwt.MapClaims) (int, []byte) {
	w.t.Helper()
	headers := []string{}
	if claims != nil {
		headers = append(headers, "Authorization", "Bearer "+w.token(claims))
	}
	return w.do(http.MethodPost, w.check.URL+"/service/"+w.org+"/on_behalf/"+path, body, headers...)
}

func TestATrustedClientAsksOnBehalfOfAPerson(t *testing.T) {
	w := newWorld(t)
	group := w.must("POST", "/group", map[string]any{"name": "sales"})["id"].(string)
	w.must("POST", "/group/"+group+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "department", "op": "eq", "value": "sales"}}})
	team := w.must("POST", "/group", map[string]any{"name": "team"})["id"].(string)
	w.must("POST", "/group/"+team+"/member", map[string]any{
		"predicate": []any{map[string]any{"attr": "preferred_username", "op": "eq", "value": "alice"}}})
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D1", "member_of_group_id": group, "access": []string{"read"}})
	w.grant(map[string]any{"type_resource_id": w.division, "resource_id": "D2", "member_of_group_id": team, "access": []string{"read"}})

	ask := func(subject string, claims map[string]any, resource string) map[string]any {
		status, raw := w.onBehalf("check", map[string]any{
			"subject": subject, "claims": claims,
			"checks": []any{map[string]any{"type": "branch", "resource_id": resource, "action": "read"}},
		}, jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"})
		if status != http.StatusOK {
			t.Fatalf("on behalf: %d %s", status, raw)
		}
		return decodeMap(t, raw)["results"].([]any)[0].(map[string]any)
	}

	// Nobody is trusted yet.
	status, raw := w.onBehalf("check", map[string]any{"subject": "dave", "checks": []any{map[string]any{"type": "branch", "action": "read"}}},
		jwt.MapClaims{"azp": "docs-backend", "sub": "svc"})
	requireErrorCode(t, status, raw, http.StatusForbidden, "delegation_not_trusted")

	// The service names a client; the list is normalized.
	updated := w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{" docs-backend ", "docs-backend", "reports"}})
	if got := updated["trusted_clients"].([]any); len(got) != 2 || got[0] != "docs-backend" || got[1] != "reports" {
		t.Fatalf("trusted clients: %v", got)
	}
	status, raw = w.do("PATCH", w.admin.URL+"/service/"+w.org, map[string]any{"trusted_clients": []string{"two words"}})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "service_trusted_client_invalid")
	if got := w.must("PATCH", "/service/"+w.org, map[string]any{"name": "org"})["trusted_clients"].([]any); len(got) != 2 {
		t.Fatalf("a PATCH without the list keeps it: %v", got)
	}

	// The claims the caller sends decide, as a token's would.
	if r := ask("dave", map[string]any{"department": "sales"}, "B1"); r["allowed"] != true {
		t.Fatalf("dave from sales reads B1: %+v", r)
	}
	if r := ask("dave", nil, "B1"); r["allowed"] != false {
		t.Fatalf("dave without the claim does not: %+v", r)
	}
	// preferred_username defaults to the subject, so a rule that names a person by it applies.
	if r := ask("alice", nil, "B4"); r["allowed"] != true {
		t.Fatalf("alice is in team by her name and reads B4: %+v", r)
	}
	if r := ask("bob", nil, "B4"); r["allowed"] != false {
		t.Fatalf("bob is not: %+v", r)
	}

	// The client can also be named by client_id; a token that names none is refused.
	status, _ = w.onBehalf("check", map[string]any{"subject": "dave", "checks": []any{map[string]any{"type": "branch", "action": "read"}}},
		jwt.MapClaims{"client_id": "reports", "sub": "svc"})
	if status != http.StatusOK {
		t.Fatalf("client_id of a trusted client: %d", status)
	}
	status, raw = w.onBehalf("check", map[string]any{"subject": "dave", "checks": []any{map[string]any{"type": "branch", "action": "read"}}},
		jwt.MapClaims{"sub": "svc"})
	requireErrorCode(t, status, raw, http.StatusForbidden, "delegation_no_client")
	status, raw = w.onBehalf("check", map[string]any{"subject": "dave", "checks": []any{map[string]any{"type": "branch", "action": "read"}}},
		jwt.MapClaims{"azp": "stranger", "sub": "svc"})
	requireErrorCode(t, status, raw, http.StatusForbidden, "delegation_not_trusted")
	if status, _ = w.onBehalf("check", map[string]any{"subject": "dave", "checks": []any{map[string]any{"type": "branch", "action": "read"}}}, nil); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", status)
	}

	// A subject that looks like a group would be taken for the group itself.
	status, raw = w.onBehalf("check", map[string]any{"subject": "group:" + group, "checks": []any{map[string]any{"type": "branch", "action": "read"}}},
		jwt.MapClaims{"azp": "docs-backend", "sub": "svc"})
	requireErrorCode(t, status, raw, http.StatusUnprocessableEntity, "delegation_subject_invalid")

	// Another service does not trust the client.
	status, raw = w.do("POST", w.check.URL+"/service/"+w.appSvc+"/on_behalf/check",
		map[string]any{"subject": "dave", "checks": []any{map[string]any{"type": "item", "action": "read"}}},
		"Authorization", "Bearer "+w.token(jwt.MapClaims{"azp": "docs-backend", "sub": "svc"}))
	requireErrorCode(t, status, raw, http.StatusForbidden, "delegation_not_trusted")

	// The permissions of a person, asked on their behalf.
	status, raw = w.onBehalf("permission", map[string]any{"subject": "dave", "claims": map[string]any{"department": "sales"}, "type": "branch"},
		jwt.MapClaims{"azp": "docs-backend", "sub": "svc"})
	if status != http.StatusOK || !strings.Contains(string(raw), `"B1"`) {
		t.Fatalf("permission on behalf: %d %s", status, raw)
	}
}

func TestDelegatedCallsAreAudited(t *testing.T) {
	w := newWorld(t)
	w.must("PATCH", "/service/"+w.org, map[string]any{"trusted_clients": []string{"docs-backend"}})
	caller := jwt.MapClaims{"azp": "docs-backend", "sub": "svc-docs-backend"}
	check := map[string]any{"subject": "dave", "checks": []any{
		map[string]any{"type": "branch", "resource_id": "B1", "action": "read"},
		map[string]any{"type": "branch", "resource_id": "B2", "action": "read"}}}
	w.grant(map[string]any{"type_resource_id": w.branch, "resource_id": "B1", "subject": "dave", "access": []string{"read"}})
	for _, body := range []map[string]any{check, {"subject": "erin", "checks": check["checks"]}} {
		if status, raw := w.onBehalf("check", body, caller); status != http.StatusOK {
			t.Fatalf("check: %d %s", status, raw)
		}
	}
	if status, raw := w.onBehalf("permission", map[string]any{"subject": "dave"}, caller); status != http.StatusOK {
		t.Fatalf("permission: %d %s", status, raw)
	}
	// A refused call is not recorded.
	w.onBehalf("check", check, jwt.MapClaims{"azp": "stranger", "sub": "x"})

	page := w.must("GET", "/service/"+w.org+"/delegated_calls?limit=2", nil)
	items := page["items"].([]any)
	if len(items) != 2 || page["next_before"] == nil {
		t.Fatalf("first page: %+v", page)
	}
	last := items[0].(map[string]any)
	if last["operation"] != "permission" || last["subject"] != "dave" || last["caller_client"] != "docs-backend" ||
		last["caller_subject"] != "svc-docs-backend" || last["request_id"] == nil {
		t.Fatalf("latest call: %+v", last)
	}
	second := items[1].(map[string]any)
	if second["operation"] != "check" || second["subject"] != "erin" || second["questions"] != float64(2) || second["allowed"] != float64(0) {
		t.Fatalf("second call: %+v", second)
	}
	rest := w.must("GET", "/service/"+w.org+"/delegated_calls?limit=2&before="+jsonNumber(page["next_before"]), nil)
	older := rest["items"].([]any)
	if len(older) != 1 || older[0].(map[string]any)["subject"] != "dave" || older[0].(map[string]any)["allowed"] != float64(1) || rest["next_before"] != nil {
		t.Fatalf("second page: %+v", rest)
	}

	// Somebody who may not read the service does not see the audit.
	nobody := w.token(jwt.MapClaims{"preferred_username": "nobody", "sub": "nobody"})
	if status, _ := w.do("GET", w.admin.URL+"/service/"+w.org+"/delegated_calls", nil, "Authorization", "Bearer "+nobody); status != http.StatusForbidden {
		t.Fatalf("the audit is for administrators: %d", status)
	}
}

func TestPolicyClaimsListWhatTheRulesRead(t *testing.T) {
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
	status, raw := w.do("GET", w.admin.URL+"/service/"+w.org+"/policy/claims", nil)
	if status != http.StatusOK {
		t.Fatalf("claims: %d %s", status, raw)
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
