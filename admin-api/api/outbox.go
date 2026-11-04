package api

import (
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

const outboxMetadataKey = "licetum.outbox"

// OutboxExpectation describes a successful, state-changing execution. Nil
// Streams means no publication; an empty slice means a revision without images.
// Placeholders resolve from business owners and the effective type graph.
type OutboxExpectation struct {
	Kind     string
	Streams  []string
	Services string
}

func withOutbox(op huma.Operation, streams []string, services string) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	kind := ""
	if streams != nil {
		kind = "change"
	}
	op.Metadata[outboxMetadataKey] = OutboxExpectation{Kind: kind, Streams: streams, Services: services}
	return op
}

// OperationOutbox exposes declarations to the write-route contract tests.
func OperationOutbox(op *huma.Operation) (OutboxExpectation, bool) {
	if op == nil {
		return OutboxExpectation{}, false
	}
	declaration, ok := op.Metadata[outboxMetadataKey].(OutboxExpectation)
	return declaration, ok
}

func validateOutboxDeclarations(api huma.API) {
	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
			http.MethodHead: item.Head, http.MethodOptions: item.Options, http.MethodTrace: item.Trace,
		} {
			if op == nil {
				continue
			}
			if _, ok := OperationOutbox(op); !ok {
				panic(fmt.Sprintf("admin route %s %s (%s) has no outbox declaration", method, path, op.OperationID))
			}
		}
	}
}
