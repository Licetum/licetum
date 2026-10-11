package db_test

import (
	"context"
	"math"
	"testing"

	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
)

// A pool size that does not fit the pool's int32 is refused before any connection is opened: the configured
// URI is only parsed here, no database is needed.
func TestConnectRefusesAPoolThatDoesNotFitInt32(t *testing.T) {
	cfg := config.Database{URI: "postgresql://u:p@127.0.0.1:1/db", PoolSize: math.MaxInt32, MaxOverflow: 1}
	if _, err := db.Connect(context.Background(), cfg); err == nil {
		t.Fatal("Connect accepted a pool larger than int32")
	}
}
