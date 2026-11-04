package db

import (
	"encoding/json"
	"testing"

	"github.com/licetum/licetum/core/outbox"
)

func TestCanonicalImageComparisonPreservesExactLiteralValues(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
		unchanged           bool
	}{
		{"large JSON numbers", `{"id":"row","properties":{"n":9007199254740992}}`, `{"id":"row","properties":{"n":9007199254740993}}`, false},
		{"timestamp-shaped names", `{"id":"row","name":"2030-01-01T00:00:00Z"}`, `{"id":"row","name":"2030-01-01T00:00:00+00:00"}`, false},
		{"database timestamp representation", `{"id":"row","valid_from":"2030-01-01T00:00:00Z"}`, `{"id":"row","valid_from":"2030-01-01T00:00:00+00:00"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			touched := Touched{BeforeKnown: true, BeforeFields: json.RawMessage(test.before)}
			if got := unchangedImage(touched, outbox.Image{Row: json.RawMessage(test.after)}); got != test.unchanged {
				t.Fatalf("unchanged=%v, want %v", got, test.unchanged)
			}
		})
	}
}
