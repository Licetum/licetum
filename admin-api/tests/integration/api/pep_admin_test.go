//go:build integration

package api_test

// Integration tests of the routes that manage who may call the AuthZEN API: the issuers, the clients of a service and their keys
// (ADR 0001, 7.10).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

func (e *env) pepIssuerBody(name string) map[string]any {
	return map[string]any{"issuer": e.mock.URL + "/" + name, "jwks_url": e.mock.URL + "/jwks", "audiences": []string{"licetum-pdp"}}
}

// expect sends the request to the admin API and fails the test unless the status is want; it returns the decoded JSON object (nil
// when the answer is not an object).
func (e *env) expect(want int, method, path string, body any, headers ...string) map[string]any {
	e.t.Helper()
	status, raw := e.do(method, e.admin.URL+path, body, headers...)
	if status != want {
		e.t.Fatalf("%s %s = %d (%s), want %d", method, path, status, raw, want)
	}
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	return decodeMap(e.t, raw)
}

func TestPEPManagementRoutesIssuersClientsAndKeys(t *testing.T) {
	e := setup(t)
	sid := e.newService("pep-svc")
	clients := "/service/" + sid + "/pep-clients"

	// Issuers: shared by every service, named by their iss.
	issuer := e.pepIssuerBody("realm-a")
	out := e.expect(http.StatusOK, http.MethodPut, "/platform/pep-issuers", issuer)
	if out["issuer"] != issuer["issuer"] {
		t.Fatalf("the issuer comes back as %v", out)
	}
	for name, body := range map[string]map[string]any{
		"an HMAC algorithm":            {"issuer": "x", "jwks_url": e.mock.URL + "/jwks", "algs": []string{"HS256"}},
		"no algorithm":                 {"issuer": "x", "jwks_url": e.mock.URL + "/jwks", "algs": []string{}},
		"a jwks_url that is not a URL": {"issuer": "x", "jwks_url": "ftp://keys"},
		"a blank audience":             {"issuer": "x", "jwks_url": e.mock.URL + "/jwks", "audiences": []string{" "}},
		"a blank issuer":               {"issuer": " ", "jwks_url": e.mock.URL + "/jwks"},
	} {
		if status, raw := e.do(http.MethodPut, e.admin.URL+"/platform/pep-issuers", body); status != http.StatusUnprocessableEntity {
			t.Errorf("an issuer with %s = %d (%s), want 422", name, status, raw)
		}
	}
	status, raw := e.do(http.MethodGet, e.admin.URL+"/platform/pep-issuers", nil)
	if status != http.StatusOK || !strings.Contains(string(raw), issuer["issuer"].(string)) {
		t.Fatalf("the issuers = %d %s", status, raw)
	}

	// An api_key client: evaluate by default, enabled.
	checkout := e.expect(http.StatusOK, http.MethodPost, clients, map[string]any{"name": "checkout", "kind": "api_key"})
	if ops := checkout["operations"].([]any); len(ops) != 1 || ops[0] != "evaluate" || checkout["enabled"] != true || checkout["service_id"] != sid {
		t.Fatalf("a new api_key client = %v", checkout)
	}
	id := checkout["id"].(string)
	// A jwt client of the registered issuer, allowed to search too.
	jwtClient := e.expect(http.StatusOK, http.MethodPost, clients, map[string]any{"name": "billing", "kind": "jwt",
		"issuer": issuer["issuer"], "client_id": "billing-backend", "operations": []string{"evaluate", "search_resource", "evaluate"}})
	if ops := jwtClient["operations"].([]any); len(ops) != 2 {
		t.Fatalf("the operations are not de-duplicated: %v", ops)
	}

	// What does not fit is refused.
	for name, body := range map[string]map[string]any{
		"the same name":                  {"name": "checkout", "kind": "api_key"},
		"the same issuer and client id":  {"name": "other", "kind": "jwt", "issuer": issuer["issuer"], "client_id": "billing-backend"},
		"an issuer nobody registered":    {"name": "x1", "kind": "jwt", "issuer": "https://nobody", "client_id": "c"},
		"a jwt client without an issuer": {"name": "x2", "kind": "jwt", "client_id": "c"},
		"a jwt client without a client":  {"name": "x3", "kind": "jwt", "issuer": issuer["issuer"]},
		"an api_key client with issuer":  {"name": "x4", "kind": "api_key", "issuer": issuer["issuer"], "client_id": "c"},
		"an unknown kind":                {"name": "x5", "kind": "cookie"},
		"an unknown operation":           {"name": "x6", "kind": "api_key", "operations": []string{"delete_everything"}},
		"no name":                        {"kind": "api_key"},
	} {
		if status, raw := e.do(http.MethodPost, e.admin.URL+clients, body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d (%s), want 422", name, status, raw)
		}
	}
	// A service that does not exist has no entry in the policy, so even the administrator is refused: the answer does not tell it
	// from a service the caller may not touch.
	e.expect(http.StatusForbidden, http.MethodPost, "/service/"+uuid.NewString()+"/pep-clients", map[string]any{"name": "a", "kind": "api_key"})

	// The list is by name; a client is read by its id, within its own service only.
	status, raw = e.do(http.MethodGet, e.admin.URL+clients, nil)
	if status != http.StatusOK || strings.Index(string(raw), "billing") > strings.Index(string(raw), "checkout") || !strings.Contains(string(raw), "billing") {
		t.Fatalf("the clients = %d %s", status, raw)
	}
	other := e.newService("pep-other")
	e.expect(http.StatusNotFound, http.MethodGet, "/service/"+other+"/pep-clients/"+id, nil)
	e.expect(http.StatusNotFound, http.MethodPatch, "/service/"+other+"/pep-clients/"+id, map[string]any{"enabled": false})
	e.expect(http.StatusNotFound, http.MethodDelete, "/service/"+other+"/pep-clients/"+id, nil)

	// A change carries the fields that are present; the others stay.
	changed := e.expect(http.StatusOK, http.MethodPatch, clients+"/"+id, map[string]any{"enabled": false, "operations": []string{"evaluate", "explain"}})
	if changed["enabled"] != false || changed["name"] != "checkout" || len(changed["operations"].([]any)) != 2 {
		t.Fatalf("after the change: %v", changed)
	}
	renamed := e.expect(http.StatusOK, http.MethodPatch, clients+"/"+id, map[string]any{"name": " checkout-2 "})
	if renamed["name"] != "checkout-2" || renamed["enabled"] != false {
		t.Fatalf("a rename changed more: %v", renamed)
	}
	e.expect(http.StatusUnprocessableEntity, http.MethodPatch, clients+"/"+id, map[string]any{"issuer": issuer["issuer"], "client_id": "c"})
	e.expect(http.StatusUnprocessableEntity, http.MethodPatch, clients+"/"+id, map[string]any{"name": "billing"})
	e.expect(http.StatusUnprocessableEntity, http.MethodPatch, clients+"/"+id, map[string]any{"operations": []string{"nope"}})

	// Keys: the secret is in the answer that issues the key, with no-store, and nowhere else.
	status, header, raw := e.doWithHeader(http.MethodPost, e.admin.URL+clients+"/"+id+"/keys", nil, "Authorization", "Bearer "+e.adminToken)
	if status != http.StatusOK || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("issue a key = %d %v %s", status, header, raw)
	}
	issued := decodeMap(t, raw)
	key, keyID := issued["key"].(string), issued["key_id"].(string)
	if !strings.HasPrefix(key, "lct_"+keyID+"_") || issued["active"] != true {
		t.Fatalf("the issued key = %v", issued)
	}
	status, raw = e.do(http.MethodGet, e.admin.URL+clients+"/"+id+"/keys", nil)
	if status != http.StatusOK || strings.Contains(string(raw), key[len("lct_")+16+1:]) || !strings.Contains(string(raw), keyID) {
		t.Fatalf("the keys = %d %s", status, raw)
	}
	e.expect(http.StatusUnprocessableEntity, http.MethodPost, clients+"/"+jwtClient["id"].(string)+"/keys", nil)
	e.expect(http.StatusNotFound, http.MethodPost, "/service/"+other+"/pep-clients/"+id+"/keys", nil)

	// A key is revoked once, and again without harm; one that is not the client's does not exist.
	e.expect(http.StatusOK, http.MethodDelete, clients+"/"+id+"/keys/"+keyID, nil)
	e.expect(http.StatusOK, http.MethodDelete, clients+"/"+id+"/keys/"+keyID, nil)
	e.expect(http.StatusNotFound, http.MethodDelete, clients+"/"+id+"/keys/"+strings.Repeat("0", 16), nil)
	e.expect(http.StatusNotFound, http.MethodDelete, "/service/"+other+"/pep-clients/"+id+"/keys/"+keyID, nil)
	if status, raw := e.do(http.MethodGet, e.admin.URL+clients+"/"+id+"/keys", nil); status != http.StatusOK || !strings.Contains(string(raw), `"active":false`) {
		t.Fatalf("the revoked key is not listed as inactive: %d %s", status, raw)
	}

	// An issuer in use cannot go; one nobody uses can, and does not exist afterwards.
	e.expect(http.StatusConflict, http.MethodDelete, "/platform/pep-issuers?issuer="+issuer["issuer"].(string), nil)
	lone := e.pepIssuerBody("realm-b")
	e.expect(http.StatusOK, http.MethodPut, "/platform/pep-issuers", lone)
	e.expect(http.StatusOK, http.MethodDelete, "/platform/pep-issuers?issuer="+lone["issuer"].(string), nil)
	e.expect(http.StatusNotFound, http.MethodDelete, "/platform/pep-issuers?issuer="+lone["issuer"].(string), nil)

	// A deleted client is gone, and its name is free again.
	e.expect(http.StatusOK, http.MethodDelete, clients+"/"+id, nil)
	e.expect(http.StatusNotFound, http.MethodGet, clients+"/"+id, nil)
	e.expect(http.StatusOK, http.MethodPost, clients, map[string]any{"name": "checkout-2", "kind": "api_key"})
}

// The routes are governed by the platform policy of the service: whoever may read the service reads its clients, whoever may write
// it manages them, and the issuers are for the holders of the policy as a whole.
func TestPEPManagementRoutesFollowThePolicyOfTheService(t *testing.T) {
	e := setup(t)
	mine, theirs := e.newService("pep-mine"), e.newService("pep-theirs")
	policy := model.PlatformTypeResourceID(model.PlatformService).String()
	e.must(http.MethodPost, "/assignment_rule", map[string]any{"type_resource_id": policy, "resource_id": mine, "subject": "owner", "access": []string{"write"}})
	e.must(http.MethodPost, "/assignment_rule", map[string]any{"type_resource_id": policy, "resource_id": mine, "subject": "reader", "access": []string{"read"}})
	owner := "Bearer " + e.token(jwt.MapClaims{"preferred_username": "owner"})
	reader := "Bearer " + e.token(jwt.MapClaims{"preferred_username": "reader"})
	stranger := "Bearer " + e.token(jwt.MapClaims{"preferred_username": "stranger"})

	body := map[string]any{"name": "client", "kind": "api_key"}
	created := e.expect(http.StatusOK, http.MethodPost, "/service/"+mine+"/pep-clients", body, "Authorization", owner)
	id := created["id"].(string)
	e.expect(http.StatusForbidden, http.MethodPost, "/service/"+theirs+"/pep-clients", body, "Authorization", owner)
	e.expect(http.StatusForbidden, http.MethodPost, "/service/"+mine+"/pep-clients", map[string]any{"name": "c2", "kind": "api_key"}, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodPost, "/service/"+mine+"/pep-clients", map[string]any{"name": "c3", "kind": "api_key"}, "Authorization", stranger)

	// The reader sees the clients and their keys, and changes nothing.
	e.expect(http.StatusOK, http.MethodGet, "/service/"+mine+"/pep-clients", nil, "Authorization", reader)
	e.expect(http.StatusOK, http.MethodGet, "/service/"+mine+"/pep-clients/"+id+"/keys", nil, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodPost, "/service/"+mine+"/pep-clients/"+id+"/keys", nil, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodPatch, "/service/"+mine+"/pep-clients/"+id, map[string]any{"enabled": false}, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodDelete, "/service/"+mine+"/pep-clients/"+id, nil, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodGet, "/service/"+mine+"/pep-clients", nil, "Authorization", stranger)
	e.expect(http.StatusForbidden, http.MethodGet, "/service/"+theirs+"/pep-clients", nil, "Authorization", reader)

	// The owner of one service reaches neither the other's clients nor the issuers.
	e.expect(http.StatusForbidden, http.MethodGet, "/service/"+theirs+"/pep-clients", nil, "Authorization", owner)
	e.expect(http.StatusForbidden, http.MethodGet, "/platform/pep-issuers", nil, "Authorization", owner)
	e.expect(http.StatusForbidden, http.MethodPut, "/platform/pep-issuers", e.pepIssuerBody("realm-c"), "Authorization", owner)
	e.expect(http.StatusForbidden, http.MethodDelete, "/platform/pep-issuers?issuer=x", nil, "Authorization", owner)
	// No credential at all is a 401.
	e.expect(http.StatusUnauthorized, http.MethodGet, "/service/"+mine+"/pep-clients", nil, "Authorization", "")
}
