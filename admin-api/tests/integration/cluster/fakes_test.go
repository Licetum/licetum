//go:build integration

package cluster_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/licetum/licetum/admin-api/tests/integration/harness"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// F0-3: the fake source and the fake identity provider report how often they were called.
func TestF0_3_TheFakeSourceCountsAndCanBeHeld(t *testing.T) {
	c := startReplicas(t, 2)
	p := c.SeedPolicy("org", 0)
	refresh := "/type_resource/" + p.Branch + "/resource/refresh"

	if c.Source.Calls() != 0 {
		t.Fatalf("the source was asked %d times before the test began", c.Source.Calls())
	}
	c.Must(http.MethodPost, refresh, nil)
	if got := c.Source.CallsTo("branch"); got != 1 {
		t.Fatalf("one refresh asked the branch source %d times, want 1", got)
	}
	if hist := c.Source.History(); len(hist) == 0 || hist[len(hist)-1].Kind != "branch" || hist[len(hist)-1].ID != "" {
		t.Fatalf("history: %+v", hist)
	}

	// Held: the refresh waits at the source until the test lets it go.
	c.Source.Hold()
	done := make(chan int, 1)
	go func() {
		status, _ := c.AdminDo(http.MethodPost, refresh, nil)
		done <- status
	}()
	if !c.Source.WaitForCalls(2, 5*time.Second) {
		t.Fatal("the refresh never reached the source")
	}
	if c.Source.InFlight() != 1 {
		t.Fatalf("in flight: %d, want 1", c.Source.InFlight())
	}
	select {
	case status := <-done:
		t.Fatalf("the refresh answered (%d) while the source was held", status)
	case <-time.After(200 * time.Millisecond):
	}
	c.Source.Release()
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("refresh after the release: %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh did not finish after the release")
	}

	// A source that fails is still counted; a delay is felt by the caller; a large body is sent whole.
	c.Source.SetStatus(http.StatusInternalServerError)
	before := c.Source.Calls()
	status, _ := c.AdminDo(http.MethodPost, refresh, nil)
	if status != http.StatusFailedDependency || c.Source.Calls() != before+1 {
		t.Fatalf("failing source: status %d, calls %d -> %d", status, before, c.Source.Calls())
	}
	c.Source.SetStatus(0)
	c.Source.SetDelay(300 * time.Millisecond)
	start := time.Now()
	c.Must(http.MethodPost, refresh, nil)
	if took := time.Since(start); took < 300*time.Millisecond {
		t.Fatalf("a delayed source answered in %v", took)
	}
	c.Source.SetDelay(0)

	c.Source.Reset()
	if c.Source.Calls() != 0 || c.Source.CallsTo("branch") != 0 || len(c.Source.History()) != 0 {
		t.Fatal("Reset left calls behind")
	}
}

func TestF0_3_TheFakeIdPCountsJWKSRequestsAndRotatesKeys(t *testing.T) {
	// A key set is kept for ten minutes and fetched again for an unknown key at most once per AUTH__JWKS_MIN_REFRESH; the
	// rotation below happens right after the first fetch, so the interval is short here.
	c, p, _ := pepJWTCluster(t, 2, harness.WithConfig(func(cfg *config.Config) { cfg.Auth.JWKSMinRefresh = time.Millisecond }))
	ask := func(r *harness.Replica, subject string) *harness.Result {
		return r.Send(c, authzenreq.SearchResource(p.Slug, "alice", "branch", "read"), c.PEPToken("checkout", subject))
	}

	c.IdP.ResetCounters()
	if res := ask(c.Replicas[0], "first"); res.Status != http.StatusOK {
		t.Fatalf("the first token: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 1 {
		t.Fatalf("a token that is new to the system asked the JWKS %d times, want 1", got)
	}
	// Each process keeps the key set it fetched: the other replica asks for it once.
	if res := ask(c.Replicas[1], "second"); res.Status != http.StatusOK {
		t.Fatalf("the second replica: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 2 {
		t.Fatalf("JWKS requests after the second replica's first token: %d, want 2 (one per process)", got)
	}

	// Rotation: tokens signed with the new kid verify, because the JWKS now lists it.
	oldKid := c.IdP.KeyID()
	newKid := c.IdP.RotateKey(true)
	if newKid == oldKid {
		t.Fatal("the key did not change")
	}
	if res := ask(c.Replicas[0], "rotated"); res.Status != http.StatusOK {
		t.Fatalf("a token of the new key: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 3 {
		t.Fatalf("JWKS requests after the rotation: %d, want 3", got)
	}

	// The key set is cached in each process: a token of a key it lists needs no request, whatever the provider does.
	c.IdP.SetJWKSStatus(http.StatusInternalServerError)
	if res := ask(c.Replicas[0], "erin"); res.Status != http.StatusOK {
		t.Fatalf("a token of a known key while the JWKS fails: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 3 {
		t.Fatalf("JWKS requests: %d, want 3 (the key set is cached)", got)
	}

	// A provider that fails and a key the cached set lacks: the token cannot be verified.
	c.IdP.RotateKey(true)
	time.Sleep(5 * time.Millisecond)
	if res := ask(c.Replicas[0], "dave"); res.Status == http.StatusOK {
		t.Fatalf("a token was accepted while the JWKS failed: %s", res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 4 {
		t.Fatalf("JWKS requests: %d, want 4 (a failing provider is still asked)", got)
	}
}
