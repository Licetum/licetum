package testsupport_test

// The fakes of the test infrastructure (F0-3) behave as documented: they count what is asked of them and can be held,
// slowed down, made to fail, and (the identity provider) rotate their keys.

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	support "github.com/licetum/licetum/core/testsupport"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestSourceCountsCallsPerType(t *testing.T) {
	s := support.NewSource(t)
	s.Set("branch", map[string]any{"id": "b1", "name": "One"})

	if status, body := get(t, s.ListURL("branch")); status != 200 || body != `[{"id":"b1","name":"One"}]` {
		t.Fatalf("list: %d %s", status, body)
	}
	if status, _ := get(t, s.URL()+"/sync/branch/b1"); status != 200 {
		t.Fatalf("one: %d", status)
	}
	if status, _ := get(t, s.URL()+"/sync/branch/zzz"); status != 404 {
		t.Fatalf("unknown id: %d", status)
	}
	if status, body := get(t, s.ListURL("other")); status != 200 || body != "[]" {
		t.Fatalf("an unknown type is an empty list: %d %s", status, body)
	}
	if s.Calls() != 4 || s.CallsTo("branch") != 3 || s.CallsTo("other") != 1 {
		t.Fatalf("calls: total %d, branch %d, other %d", s.Calls(), s.CallsTo("branch"), s.CallsTo("other"))
	}
	if h := s.History(); len(h) != 4 || h[1].ID != "b1" {
		t.Fatalf("history: %+v", h)
	}
	s.Reset()
	if s.Calls() != 0 || len(s.History()) != 0 {
		t.Fatal("Reset left calls behind")
	}
}

func TestSourceStatusSizeDelayAndHold(t *testing.T) {
	s := support.NewSource(t)
	s.Set("t", map[string]any{"id": "1", "name": "x"})

	s.SetStatus(503)
	if status, _ := get(t, s.ListURL("t")); status != 503 {
		t.Fatalf("status: %d", status)
	}
	s.SetStatus(0)

	s.SetBodySize(4096)
	if _, body := get(t, s.ListURL("t")); len(body) != 4096 {
		t.Fatalf("body size %d, want 4096", len(body))
	}
	s.SetBodySize(0)

	s.SetDelay(100 * time.Millisecond)
	start := time.Now()
	get(t, s.ListURL("t"))
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("the delay was not applied")
	}
	s.SetDelay(0)

	s.Reset()
	s.Hold()
	done := make(chan struct{})
	go func() { get(t, s.ListURL("t")); close(done) }()
	if !s.WaitForCalls(1, 3*time.Second) || s.InFlight() != 1 {
		t.Fatalf("the held request did not arrive: calls %d, in flight %d", s.Calls(), s.InFlight())
	}
	select {
	case <-done:
		t.Fatal("answered while held")
	case <-time.After(100 * time.Millisecond):
	}
	s.Release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("not answered after the release")
	}
	if s.MaxInFlight() != 1 {
		t.Fatalf("max in flight %d", s.MaxInFlight())
	}
}

func TestIdPSignsTokensCountsJWKSAndRotates(t *testing.T) {
	idp := support.NewIdP(t)
	tok := idp.Token(jwt.MapClaims{"sub": "a"})
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != idp.Issuer() || parsed.Header["kid"] != "k1" || claims["sub"] != "a" {
		t.Fatalf("token: %v %v", parsed.Header, claims)
	}

	if idp.JWKSRequests() != 0 {
		t.Fatal("JWKS counted before it was asked")
	}
	_, body := get(t, idp.JWKSURL())
	if idp.JWKSRequests() != 1 || !contains(body, `"kid":"k1"`) {
		t.Fatalf("jwks: %d %s", idp.JWKSRequests(), body)
	}
	if kid := idp.RotateKey(true); kid != "k2" || idp.KeyID() != "k2" {
		t.Fatalf("rotated to %q", kid)
	}
	_, body = get(t, idp.JWKSURL())
	if !contains(body, `"kid":"k1"`) || !contains(body, `"kid":"k2"`) {
		t.Fatalf("after a rotation that keeps the old key: %s", body)
	}
	idp.RotateKey(false)
	_, body = get(t, idp.JWKSURL())
	if contains(body, `"kid":"k1"`) || contains(body, `"kid":"k2"`) || !contains(body, `"kid":"k3"`) {
		t.Fatalf("after a rotation that drops the old keys: %s", body)
	}
	idp.SetJWKSStatus(500)
	if status, _ := get(t, idp.JWKSURL()); status != 500 {
		t.Fatalf("status %d", status)
	}
	idp.ResetCounters()
	if idp.JWKSRequests() != 0 {
		t.Fatal("ResetCounters")
	}
	expired := idp.TokenValidFor(jwt.MapClaims{}, -time.Minute)
	if _, err := jwt.NewParser().Parse(expired, func(*jwt.Token) (any, error) { return nil, nil }); err == nil {
		t.Fatal("an expired token parsed")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
