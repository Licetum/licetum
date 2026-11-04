package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
)

func TestOutboxDeclarationsCoverAdminOperations(t *testing.T) {
	_, a := NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
	count := 0
	for _, path := range a.OpenAPI().Paths {
		for _, op := range []*huma.Operation{path.Post, path.Put, path.Patch, path.Delete} {
			if op == nil {
				continue
			}
			count++
			declaration, ok := OperationOutbox(op)
			if !ok || declaration.Services == "" {
				t.Fatalf("missing declaration: %s", op.OperationID)
			}
			if declaration.Streams == nil && declaration.Services != "none" {
				t.Fatalf("read operation affects services: %s", op.OperationID)
			}
		}
	}
	if count < 40 {
		t.Fatalf("unexpectedly small write-route table: %d", count)
	}
}

func TestMissingOutboxDeclarationFailsRegistration(t *testing.T) {
	_, a := NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
	a.OpenAPI().Paths["/undeclared"] = &huma.PathItem{Post: &huma.Operation{OperationID: "undeclared", Method: http.MethodPost}}
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(recovered.(string), "undeclared") {
			t.Fatalf("missing route declaration did not fail: %v", recovered)
		}
	}()
	validateOutboxDeclarations(a)
}
