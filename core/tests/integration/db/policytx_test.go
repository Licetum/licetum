//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/db"
)

func TestPolicyTxSerializesValidationAndRollsBackOnError(t *testing.T) {
	pool, _ := newPEPPool(t)
	ctx := context.Background()
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := db.PolicyTx(ctx, pool, nil, func(context.Context, pgx.Tx) ([]db.Change, error) { close(locked); <-release; return nil, nil })
		done <- err
	}()
	<-locked
	deadline, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	_, err := db.PolicyTx(deadline, pool, nil, func(context.Context, pgx.Tx) ([]db.Change, error) { return nil, errors.New("must not enter") })
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("second validation bypassed lock: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
