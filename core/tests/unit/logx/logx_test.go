package logx_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/licetum/licetum/core/logx"
)

// options is the configuration the tests start from: the defaults, with a fixed salt so that hashes are stable.
func options(format string) logx.Options {
	o := logx.DefaultOptions(slog.LevelInfo)
	o.Format = format
	o.Salt = "test-salt"
	return o
}

func newSwitch(t *testing.T, opts logx.Options) (*logx.Switch, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	sw, err := logx.New(&buf, opts)
	if err != nil {
		t.Fatalf("new switch: %v", err)
	}
	return sw, &buf
}

// lastJSON decodes the last line of the output as a JSON record.
func lastJSON(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("not a JSON record: %v: %q", err, buf.String())
	}
	return rec
}

func TestJSONRecordCarriesTheAttributes(t *testing.T) {
	sw, buf := newSwitch(t, options("json"))
	slog.New(sw).Info("request", "request_id", "r1", "status", 200)

	rec := lastJSON(t, buf)
	if rec["msg"] != "request" || rec["request_id"] != "r1" || rec["level"] != "INFO" {
		t.Fatalf("unexpected record: %v", rec)
	}
}

func TestSubjectIsHashedUnderTheSalt(t *testing.T) {
	sw, buf := newSwitch(t, options("json"))
	logger := slog.New(sw)

	logger.Info("decision", "subject", "alice@example.com")
	first := lastJSON(t, buf)["subject"]
	if s, ok := first.(string); !ok || s == "" || strings.Contains(s, "alice") {
		t.Fatalf("the subject must be a hash, not the subject: %v", first)
	}

	// the same subject under the same salt gives the same value, so the records of one subject can still be matched
	buf.Reset()
	logger.Info("decision", "subject", "alice@example.com")
	if second := lastJSON(t, buf)["subject"]; second != first {
		t.Fatalf("the same subject must hash to the same value: %v and %v", first, second)
	}
}

func TestAnonymousSubjectStaysEmpty(t *testing.T) {
	sw, buf := newSwitch(t, options("json"))
	slog.New(sw).Info("request", "user", "", "status", 401)
	if lastJSON(t, buf)["user"] != "" {
		t.Fatalf("an anonymous request has no subject to hash: %s", buf.String())
	}
}

func TestSubjectOmitDropsAndPlainKeeps(t *testing.T) {
	o := options("json")
	o.Subject = "omit"
	sw, buf := newSwitch(t, o)
	slog.New(sw).Info("decision", "subject", "alice", "allowed", true)
	if strings.Contains(buf.String(), `"subject"`) || strings.Contains(buf.String(), "alice") {
		t.Fatalf("omit must drop the subject: %s", buf.String())
	}

	o.Subject = "plain"
	if err := sw.Apply(o); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	slog.New(sw).Info("decision", "subject", "alice", "allowed", true)
	if lastJSON(t, buf)["subject"] != "alice" {
		t.Fatalf("plain must keep the subject as it is: %s", buf.String())
	}
}

func TestFieldsOnlyWhitelistedAndRenamed(t *testing.T) {
	o := options("json")
	o.Subject = "plain"
	o.Fields = []logx.Field{{Name: "request_id"}, {Name: "subject", As: "user"}}
	sw, buf := newSwitch(t, o)
	slog.New(sw).Info("decision", "request_id", "r1", "subject", "alice", "allowed", true)

	rec := lastJSON(t, buf)
	if rec["request_id"] != "r1" || rec["user"] != "alice" {
		t.Fatalf("listed fields must be written under their names: %v", rec)
	}
	if _, ok := rec["allowed"]; ok {
		t.Fatalf("an unlisted field must not be written: %v", rec)
	}
	if _, ok := rec["subject"]; ok {
		t.Fatalf("a renamed field must not keep its old name: %v", rec)
	}
}

func TestSwitchedOffEventIsDropped(t *testing.T) {
	o := options("json")
	o.Events = map[string]bool{"decision": false}
	sw, buf := newSwitch(t, o)
	logger := slog.New(sw)
	logger.Info("decision", "allowed", true)
	if buf.Len() != 0 {
		t.Fatalf("a switched-off event must not be written: %s", buf.String())
	}
	logger.Info("request", "request_id", "r1")
	if lastJSON(t, buf)["msg"] != "request" {
		t.Fatal("a category that is on must still be written")
	}
}

func TestLevelFiltersRecords(t *testing.T) {
	o := options("json")
	o.Level = slog.LevelWarn
	sw, buf := newSwitch(t, o)
	logger := slog.New(sw)
	logger.Info("request")
	if buf.Len() != 0 {
		t.Fatalf("an info record must not pass a warn level: %s", buf.String())
	}
	logger.Warn("something")
	if lastJSON(t, buf)["level"] != "WARN" {
		t.Fatal("a warn record must pass a warn level")
	}
}

func TestLogfmtAndTextFormats(t *testing.T) {
	o := options("logfmt")
	o.Subject = "plain"
	sw, buf := newSwitch(t, o)
	slog.New(sw).Info("request", "request_id", "r1")
	out := buf.String()
	if !strings.Contains(out, "msg=request") || !strings.Contains(out, "request_id=r1") || strings.Contains(out, "{") {
		t.Fatalf("logfmt output: %s", out)
	}

	o.Format = "text"
	if err := sw.Apply(o); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	slog.New(sw).Info("request", "request_id", "r1", "note", "two words")
	out = buf.String()
	if !strings.Contains(out, " INFO request request_id=r1 note=\"two words\"") || strings.Contains(out, "{") {
		t.Fatalf("text output: %s", out)
	}
}

func TestApplyRefusesAnInvalidConfigurationAndKeepsTheOld(t *testing.T) {
	sw, buf := newSwitch(t, options("json"))
	bad := options("xml")
	if err := sw.Apply(bad); err == nil {
		t.Fatal("an unknown format must be refused")
	}
	slog.New(sw).Info("request")
	if lastJSON(t, buf)["msg"] != "request" {
		t.Fatal("the configuration in force must stay after a refused one")
	}
}

func TestValidateFieldNames(t *testing.T) {
	o := options("json")
	o.Fields = []logx.Field{{Name: "bad name"}}
	if err := o.Validate(); err == nil {
		t.Fatal("a field name with a space must be refused")
	}
	o.Fields = []logx.Field{{Name: "a", As: "x"}, {Name: "b", As: "x"}}
	if err := o.Validate(); err == nil {
		t.Fatal("two fields under one name must be refused")
	}
	o.Fields = []logx.Field{{Name: "subject", As: "user"}}
	if err := o.Validate(); err != nil {
		t.Fatalf("a valid rename must be accepted: %v", err)
	}
}

func TestSampleAllowedBounds(t *testing.T) {
	o := options("json")
	o.SampleAllowed = 0
	sw, _ := newSwitch(t, o)
	if sw.KeepAllowed() {
		t.Fatal("a sample of 0 keeps no allowed decision")
	}
	o.SampleAllowed = 1
	if err := sw.Apply(o); err != nil {
		t.Fatal(err)
	}
	if !sw.KeepAllowed() {
		t.Fatal("a sample of 1 keeps every allowed decision")
	}
}
