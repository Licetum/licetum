//go:build integration

package cluster_test

// Tier M: several check API replicas on one PostgreSQL and one Redis. The tests are named after the acceptance
// criteria of the production-load plan (F0-1 ...), see the issue "F0: test infrastructure and baseline".

import (
	"context"
	"fmt"
	"os"
	"testing"

	support "github.com/licetum/licetum/core/testsupport"
)

var infra *support.Infrastructure

func TestMain(m *testing.M) {
	i, err := support.Start(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "integration infrastructure: %v\n", err)
		os.Exit(1)
	}
	infra = i
	code := m.Run()
	if err := i.Terminate(context.Background()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "terminate integration infrastructure: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
