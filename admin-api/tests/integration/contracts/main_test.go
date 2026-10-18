//go:build integration

package contracts_test

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
