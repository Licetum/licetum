package engine

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/model"
)

// A token verified for one service must not be accepted from the cache for another: the services have their own
// issuer and keys.
func TestVerifiedTokenIsNotAcceptedFromTheCacheForAnotherService(t *testing.T) {
	a := &App{
		Cfg:           &config.Config{},
		HTTP:          &http.Client{},
		metrics:       newMetrics(),
		Redis:         redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: time.Millisecond}),
		tokensL1:      newLRU[Claims](8),
		invalidTokens: newLRU[error](8),
	}
	owner := &model.Service{ID: uuid.New()}
	other := &model.Service{ID: uuid.New()} // no JWKS URL: verification can only fail
	token := "header.payload.signature"

	a.keepTokenInMemory(owner.ID.String()+":"+tokenDigest(token), Claims{"sub": "u", "exp": float64(time.Now().Add(time.Hour).Unix())}, time.Now())

	if claims, err := a.VerifyToken(t.Context(), owner, token); err != nil || claims["sub"] != "u" {
		t.Fatalf("the service the token was verified for: claims = %v, err = %v", claims, err)
	}
	if claims, err := a.VerifyToken(t.Context(), other, token); err == nil {
		t.Fatalf("another service accepted the cached token: %v", claims)
	}
}
