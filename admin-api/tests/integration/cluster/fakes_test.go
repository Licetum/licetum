//go:build integration

package cluster_test

import (
	"net/http"
	"testing"
	"time"
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
	c := startReplicas(t, 2)
	p := c.SeedPolicy("org", 0)
	waitIdle := func() { time.Sleep(200 * time.Millisecond) }

	c.IdP.ResetCounters()
	alice := c.UserToken("alice")
	if res := c.Replicas[0].Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("alice: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 1 {
		t.Fatalf("a token that is new to the system asked the JWKS %d times, want 1", got)
	}
	waitIdle() // the verified token is written to Redis in the background
	// The same token on the other replica is found in the shared cache.
	if res := c.Replicas[1].Permission(c, p.Slug, alice); res.Status != http.StatusOK {
		t.Fatalf("alice on the second replica: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 1 {
		t.Fatalf("the verified token was not shared between replicas: %d JWKS requests", got)
	}

	// Rotation: tokens signed with the new kid verify, because the JWKS now lists it.
	oldKid := c.IdP.KeyID()
	newKid := c.IdP.RotateKey(true)
	if newKid == oldKid {
		t.Fatal("the key did not change")
	}
	rotated := c.UserToken("carol")
	if res := c.Replicas[0].Permission(c, p.Slug, rotated); res.Status != http.StatusOK {
		t.Fatalf("a token of the new key: %d %s", res.Status, res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 2 {
		t.Fatalf("JWKS requests after the rotation: %d, want 2", got)
	}

	// A provider that fails: the token cannot be verified.
	c.IdP.SetJWKSStatus(http.StatusInternalServerError)
	dave := c.UserToken("dave")
	if res := c.Replicas[1].Permission(c, p.Slug, dave); res.Status == http.StatusOK {
		t.Fatalf("a token was accepted while the JWKS failed: %s", res.Body)
	}
	if got := c.IdP.JWKSRequests(); got != 3 {
		t.Fatalf("JWKS requests: %d, want 3 (a failing provider is still asked)", got)
	}
}
