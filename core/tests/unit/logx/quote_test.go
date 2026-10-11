package logx_test

import (
	"log/slog"
	"strings"
	"testing"
)

// A value with a control character is quoted and escaped, so that one record stays one line of the text output.
func TestTextQuotesAValueWithAControlCharacter(t *testing.T) {
	sw, buf := newSwitch(t, options("text"))
	slog.New(sw).Info("request", "path", "a\rb\x1bc")

	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, `path="a\rb`) {
		t.Fatalf("a control character breaks the record: %q", out)
	}
}
