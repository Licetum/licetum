package logx_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/logx"
)

func TestMissingDeploymentSaltOmitsSubjectsAndWarnsOnce(t *testing.T) {
	var output bytes.Buffer
	opts := logx.DefaultOptions(slog.LevelInfo)
	handler, err := logx.New(&output, opts)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := handler.Apply(opts); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(handler)
	logger.Info("request", "user", "alice-secret")
	logger.With("subject", "bob-secret").Info("decision", "on_behalf_of", "carol-secret")
	text := output.String()
	if strings.Contains(text, "alice-secret") || strings.Contains(text, "bob-secret") || strings.Contains(text, "carol-secret") {
		t.Fatal("subject leaked without deployment salt")
	}
	if strings.Count(text, "LOG__SUBJECT_SALT is empty") != 1 {
		t.Fatal("missing-salt warning was not emitted exactly once")
	}
	output.Reset()
	opts.Salt = "deployment-secret"
	if err := handler.Apply(opts); err != nil {
		t.Fatal(err)
	}
	logger.Info("request", "user", "alice-secret")
	if !strings.Contains(output.String(), `"user":`) || strings.Contains(output.String(), "alice-secret") || strings.Contains(output.String(), opts.Salt) {
		t.Fatal("configured salt was not used safely")
	}
}
