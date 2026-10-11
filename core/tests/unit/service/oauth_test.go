package service_test

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

func TestOAuthStateRoundTripAndSecretIsolation(t *testing.T) {
	app := &engine.App{Cfg: &config.Config{Auth: config.Auth{StateSecret: "secret", StateTTL: time.Minute}}}
	serviceID := uuid.New()
	redirect := "http://localhost:5173/cb"

	state, err := app.CreateState(serviceID, &redirect, "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := app.VerifyState(state)
	if err != nil || decoded.ServiceID != serviceID || decoded.RedirectTo == nil || *decoded.RedirectTo != redirect || decoded.Nonce != "nonce-1" {
		t.Fatalf("state = %+v, error = %v", decoded, err)
	}

	withoutRedirect, err := app.CreateState(serviceID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = app.VerifyState(withoutRedirect)
	if err != nil || decoded.RedirectTo != nil || decoded.Nonce == "" {
		t.Fatalf("state without redirect = %+v, error = %v", decoded, err)
	}

	otherSecret := &engine.App{Cfg: &config.Config{Auth: config.Auth{StateSecret: "other", StateTTL: time.Minute}}}
	if _, err := otherSecret.VerifyState(state); err == nil {
		t.Fatal("a state signed with another secret must be rejected")
	}
	expired := &engine.App{Cfg: &config.Config{Auth: config.Auth{StateSecret: "secret", StateTTL: -time.Minute}}}
	old, err := expired.CreateState(serviceID, nil, "nonce")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.VerifyState(old); err == nil {
		t.Fatal("an expired state must be rejected")
	}
}

func TestGeneratePKCEPairUsesS256WithoutPadding(t *testing.T) {
	verifier, challenge := engine.GeneratePKCEPair()
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("verifier length %d is outside RFC 7636 range", len(verifier))
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want || strings.Contains(challenge, "=") {
		t.Fatalf("challenge = %q, want %q", challenge, want)
	}
}

func TestTokenFragmentUsesURLQueryEncoding(t *testing.T) {
	if got := engine.TokenFragment(map[string]any{"access_token": "a b", "expires_in": 300.0}); got != "access_token=a+b&expires_in=300" {
		t.Fatalf("TokenFragment = %q", got)
	}
}
