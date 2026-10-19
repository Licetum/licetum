package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

const (
	evaluationPath = "/service/" + fakeServiceRef + "/access/v1/evaluation"
	pepKey         = "lct_0123456789abcdef_" + "0123456789012345678901234567890123456789abc"
)

// evaluationBody asks whether the person may do the action on todo/1; explain asks for the explanation.
func evaluationBody(person, action string, explain bool) string {
	ctx := ""
	if explain {
		ctx = `,"context":{"licetum":{"explain":true}}`
	}
	return fmt.Sprintf(`{"subject":{"type":"user","id":%q,"properties":{"department":"sales"}},"resource":{"type":"todo","id":"1"},"action":{"name":%q}%s}`,
		person, action, ctx)
}

// A PEP client asks about the person of the body, with the claims of the properties: nothing of the person comes from the credential.
func TestPEPClientAsksAboutThePersonOfTheBody(t *testing.T) {
	fake := newFakePDP([4]string{"bob", "read", "todo", "1"}).withPEPClient(pepKey, "gateway", engine.PEPEvaluate)
	h := authzenTestAPI(fake, &config.Config{})

	rec := authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", false), "Bearer "+pepKey)
	if rec.Code != http.StatusOK || decodeAnswer(t, rec).Decision == nil || !*decodeAnswer(t, rec).Decision {
		t.Fatalf("bob may read todo/1: %d %s", rec.Code, rec.Body)
	}
	rec = authzenCallAs(h, evaluationPath, evaluationBody("carol", "read", false), "Bearer "+pepKey)
	if rec.Code != http.StatusOK || *decodeAnswer(t, rec).Decision {
		t.Fatalf("carol may not: %d %s", rec.Code, rec.Body)
	}
}

// Each route needs the operation of its own; a client that lacks it is refused with the one 403 of AZ09.
func TestPEPClientNeedsTheOperationOfTheRoute(t *testing.T) {
	fake := newFakePDP().withPEPClient(pepKey, "searcher", engine.PEPSearchResource)
	h := authzenTestAPI(fake, &config.Config{})

	rec := authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", false), "Bearer "+pepKey)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Licetum-Error-Code") != "authzen_forbidden" {
		t.Errorf("an evaluation without the evaluate operation: %d %s %s, want 403 authzen_forbidden", rec.Code, rec.Header().Get("Licetum-Error-Code"), rec.Body)
	}
	rec = authzenCallAs(h, searchResourcePath, resourceSearchBody(10, "", "read"), "Bearer "+pepKey)
	if rec.Code != http.StatusOK {
		t.Errorf("a resource search with the search_resource operation: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"search/action", "search/subject"} {
		rec = authzenCallAs(h, "/service/"+fakeServiceRef+"/access/v1/"+path,
			`{"subject":{"type":"user","id":"bob"},"resource":{"type":"todo","id":"1"},"action":{"name":"read"}}`, "Bearer "+pepKey)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without its operation: %d %s, want 403", path, rec.Code, rec.Body)
		}
	}
}

// TestContract_AZ12_ExplainNeedsTheExplainOperationOfTheClient: an explanation tells why a decision was made, which is more than
// the decision, so a client asks for it only with the explain operation (off by default).
func TestContract_AZ12_ExplainNeedsTheExplainOperationOfTheClient(t *testing.T) {
	const withExplain = "lct_fedcba9876543210_" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
	fake := newFakePDP([4]string{"bob", "read", "todo", "1"}).
		withPEPClient(pepKey, "plain", engine.PEPEvaluate).
		withPEPClient(withExplain, "debugger", engine.PEPEvaluate, engine.PEPExplain)
	h := authzenTestAPI(fake, &config.Config{})

	rec := authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", false), "Bearer "+pepKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("a decision without an explanation needs the evaluate operation only: %d %s", rec.Code, rec.Body)
	}
	rec = authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", true), "Bearer "+pepKey)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Licetum-Error-Code") != "authzen_forbidden" {
		t.Errorf("an explanation without the explain operation: %d %s, want 403 authzen_forbidden", rec.Code, rec.Body)
	}
	rec = authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", true), "Bearer "+withExplain)
	if rec.Code != http.StatusOK || decodeAnswer(t, rec).Context["reason_admin"] == nil {
		t.Errorf("an explanation with the explain operation: %d %s, want 200 with reason_admin", rec.Code, rec.Body)
	}
}

// TestContract_AZ14_AnExhaustedRateBucketIsA429WithRetryAfter: a client over its limit of requests per second gets a 429 with
// Retry-After, and the other clients are served; the limit is the one of this pod.
func TestContract_AZ14_AnExhaustedRateBucketIsA429WithRetryAfter(t *testing.T) {
	const other = "lct_aaaaaaaaaaaaaaaa_" + "0123456789012345678901234567890123456789abc"
	fake := newFakePDP([4]string{"bob", "read", "todo", "1"}).
		withPEPClient(pepKey, "busy", engine.PEPEvaluate).
		withPEPClient(other, "quiet", engine.PEPEvaluate)
	h := authzenTestAPI(fake, &config.Config{PEP: config.PEP{RateLimit: 1, Burst: 2}})

	for i := range 2 {
		if rec := authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", false), "Bearer "+pepKey); rec.Code != http.StatusOK {
			t.Fatalf("request %d is within the burst: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	rec := authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", false), "Bearer "+pepKey)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Licetum-Error-Code") != "authzen_rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("the third request is over the limit: %d %s %s, want 429 authzen_rate_limited with Retry-After", rec.Code, rec.Header().Get("Licetum-Error-Code"), rec.Body)
	}
	if rec := authzenCallAs(h, evaluationPath, evaluationBody("bob", "read", false), "Bearer "+other); rec.Code != http.StatusOK {
		t.Errorf("another client is not held back by the first: %d %s", rec.Code, rec.Body)
	}
}

// The bucket refills with time: the wait the limiter reports is the time until a token is there.
func TestClientLimitsRefillWithTime(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newClientLimits(config.PEP{RateLimit: 10, Burst: 2}, func() time.Time { return now })
	client := uuid.New()
	for range 2 {
		if ok, _ := l.allow(client); !ok {
			t.Fatal("the burst of 2 is allowed")
		}
	}
	ok, wait := l.allow(client)
	if ok || wait != 100*time.Millisecond {
		t.Fatalf("an empty bucket of 10 per second refuses and says 100 ms: ok=%v wait=%v", ok, wait)
	}
	now = now.Add(100 * time.Millisecond)
	if ok, _ := l.allow(client); !ok {
		t.Error("a token is there after 100 ms")
	}
	if ok, _ := l.allow(client); ok {
		t.Error("only one token came back")
	}
	if newClientLimits(config.PEP{}, time.Now) != nil {
		t.Error("a rate of 0 turns the limit off")
	}
}

// TestContract_AZ08_ABearerTokenIsRequiredAndTheSchemeIsCaseInsensitive: a request without credentials, or with another scheme,
// is a 401 that carries the Bearer challenge; the scheme Bearer is written in any case.
func TestContract_AZ08_ABearerTokenIsRequiredAndTheSchemeIsCaseInsensitive(t *testing.T) {
	fake := newFakePDP([4]string{fakeCaller, "read", "todo", "1"}).withPEPClient(pepKey, "gateway", engine.PEPEvaluate)
	h := authzenTestAPI(fake, &config.Config{})
	body := evaluationBody(fakeCaller, "read", false)

	for name, authorization := range map[string]string{"no header": "", "another scheme": "Basic " + pepKey, "a bare token": pepKey} {
		rec := authzenCallAs(h, evaluationPath, body, authorization)
		if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: %d WWW-Authenticate %q, want 401 with the Bearer challenge", name, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
	for _, scheme := range []string{"bearer", "BEARER", "Bearer"} {
		if rec := authzenCallAs(h, evaluationPath, body, scheme+" "+pepKey); rec.Code != http.StatusOK {
			t.Errorf("scheme %q: %d %s, want 200", scheme, rec.Code, rec.Body)
		}
	}
	// A key the directory does not know is a 401 with the invalid_token challenge, not a 403.
	rec := authzenCallAs(h, evaluationPath, body, "Bearer lct_ffffffffffffffff_"+strings.Repeat("x", 43))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Errorf("an unknown key: %d WWW-Authenticate %q, want 401 invalid_token", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}
