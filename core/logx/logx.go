// Package logx is the process logger. Its handler reads the current configuration on every record, so the
// format, the fields, the level, the event switches, the sampling and the treatment of the subject can change at
// run time (see service.App.PutLogSettings) without a restart.
package logx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// Field is one attribute of a record the logs keep. As is the name it is written under (empty: its own name).
type Field struct {
	Name string
	As   string
}

// Options is the logging configuration.
type Options struct {
	// Format is json (one JSON object per line), logfmt (key=value pairs) or text (plain lines).
	Format string
	// Level is the lowest level written.
	Level slog.Level
	// Fields, when not empty, are the only attributes written, each under its As name.
	Fields []Field
	// Events switches the events of a category off when false: access (the request line of the API), decision
	// (one answer of the check API) and sync (a resource synchronization). A category that is not listed is on.
	Events map[string]bool
	// Subject is what the subject of a record becomes: hash (keyed with Salt), plain or omit.
	Subject string
	// Salt keys the hash of the subject.
	Salt string
	// SampleAllowed is the share of allowed decisions that are logged, between 0 and 1. Denials are always logged.
	SampleAllowed float64
}

// categories names the events that can be switched off, by the message of their record.
var categories = map[string]string{
	"request":                     "access",
	"decision":                    "decision",
	"resource graph synchronized": "sync",
}

// DecisionCategory is the category of the decision events.
const DecisionCategory = "decision"

var nameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

// DefaultOptions is the configuration a process starts with, before the stored settings are read: JSON at the given
// level, every event on, the subject hashed under a salt of this process, and every allowed decision logged.
func DefaultOptions(level slog.Level) Options {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	return Options{
		Format:        "json",
		Level:         level,
		Subject:       "hash",
		Salt:          hex.EncodeToString(salt),
		SampleAllowed: 1,
	}
}

// Validate reports the first setting that cannot be applied.
func (o Options) Validate() error {
	switch o.Format {
	case "json", "logfmt", "text":
	default:
		return fmt.Errorf("format %q: want json, logfmt or text", o.Format)
	}
	switch o.Subject {
	case "hash", "plain", "omit":
	default:
		return fmt.Errorf("subject %q: want hash, plain or omit", o.Subject)
	}
	if o.SampleAllowed < 0 || o.SampleAllowed > 1 {
		return fmt.Errorf("sample_allowed %v: want a value between 0 and 1", o.SampleAllowed)
	}
	seen := map[string]bool{}
	for _, f := range o.Fields {
		if !nameRE.MatchString(f.Name) {
			return fmt.Errorf("field %q: want a letter or underscore, then letters, digits, dots, dashes or underscores", f.Name)
		}
		as := f.As
		if as == "" {
			as = f.Name
		}
		if !nameRE.MatchString(as) {
			return fmt.Errorf("field %q: the name %q is not valid", f.Name, as)
		}
		if seen[as] {
			return fmt.Errorf("field name %q is used twice", as)
		}
		seen[as] = true
	}
	return nil
}

func (o Options) eventOn(category string) bool {
	on, set := o.Events[category]
	return !set || on
}

// Switch is the handler of the process logger. It writes to one writer; Apply changes its configuration.
type Switch struct {
	out io.Writer
	cur atomic.Pointer[current]
}

type current struct {
	opts    Options
	handler slog.Handler
}

// New returns a Switch writing to out with the given configuration.
func New(out io.Writer, opts Options) (*Switch, error) {
	s := &Switch{out: out}
	if err := s.Apply(opts); err != nil {
		return nil, err
	}
	return s, nil
}

// Apply replaces the configuration. A configuration that cannot be applied is an error and leaves the current one in place.
func (s *Switch) Apply(opts Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	s.cur.Store(&current{opts: opts, handler: build(s.out, opts)})
	return nil
}

// Options returns the configuration in force.
func (s *Switch) Options() Options {
	return s.cur.Load().opts
}

// Enabled implements slog.Handler.
func (s *Switch) Enabled(_ context.Context, level slog.Level) bool {
	return level >= s.cur.Load().opts.Level
}

// Handle implements slog.Handler: it drops the records of a switched-off event and formats the rest.
func (s *Switch) Handle(ctx context.Context, r slog.Record) error {
	c := s.cur.Load()
	if category, ok := categories[r.Message]; ok && !c.opts.eventOn(category) {
		return nil
	}
	return c.handler.Handle(ctx, r)
}

// WithAttrs implements slog.Handler. The attributes are added to every record of the returned handler.
func (s *Switch) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &bound{sw: s, attrs: attrs}
}

// WithGroup implements slog.Handler. The logs carry no groups, so the name is ignored.
func (s *Switch) WithGroup(string) slog.Handler { return s }

// Wants reports whether events of a category are logged at all at the current level, so that a caller can skip
// building the event when it is not.
func (s *Switch) Wants(category string) bool {
	c := s.cur.Load()
	return c.opts.eventOn(category) && slog.LevelInfo >= c.opts.Level
}

// KeepAllowed reports whether one allowed decision is logged: a share of them, set by SampleAllowed.
func (s *Switch) KeepAllowed() bool {
	rate := s.cur.Load().opts.SampleAllowed
	switch {
	case rate >= 1:
		return true
	case rate <= 0:
		return false
	}
	return unitRandom() < rate
}

type bound struct {
	sw    *Switch
	attrs []slog.Attr
}

func (b *bound) Enabled(ctx context.Context, level slog.Level) bool { return b.sw.Enabled(ctx, level) }

func (b *bound) Handle(ctx context.Context, r slog.Record) error {
	r = r.Clone()
	r.AddAttrs(b.attrs...)
	return b.sw.Handle(ctx, r)
}

func (b *bound) WithAttrs(attrs []slog.Attr) slog.Handler {
	all := make([]slog.Attr, 0, len(b.attrs)+len(attrs))
	all = append(append(all, b.attrs...), attrs...)
	return &bound{sw: b.sw, attrs: all}
}

func (b *bound) WithGroup(string) slog.Handler { return b }

// build returns the handler of a configuration. The options are validated by the caller.
func build(out io.Writer, opts Options) slog.Handler {
	replace := replacer(opts)
	hopts := &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: replace}
	switch opts.Format {
	case "json":
		return slog.NewJSONHandler(out, hopts)
	case "logfmt":
		return slog.NewTextHandler(out, hopts)
	}
	return &plain{out: out, replace: replace}
}

// baseKeys are the attributes every record has. Their names can be changed through Fields, but they are never dropped.
var baseKeys = map[string]bool{slog.TimeKey: true, slog.LevelKey: true, slog.MessageKey: true}

// unitRandom is a uniform number in [0, 1): the draw that decides whether one allowed decision is sampled.
func unitRandom() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
}

// replacer applies the configuration to every attribute: the subject treatment, the whitelist of fields and the
// renames. It is the one place that decides what is written, for every format.
func replacer(opts Options) func([]string, slog.Attr) slog.Attr {
	names := map[string]string{}
	for _, f := range opts.Fields {
		as := f.As
		if as == "" {
			as = f.Name
		}
		names[f.Name] = as
	}
	return func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) > 0 {
			return a // the records carry no groups
		}
		if baseKeys[a.Key] {
			if as, ok := names[a.Key]; ok {
				a.Key = as
			}
			return a
		}
		if a.Key == "subject" || a.Key == "user" {
			switch opts.Subject {
			case "omit":
				return slog.Attr{}
			case "hash":
				// an anonymous request has no subject: nothing to hash, the empty value stays empty
				if s := a.Value.String(); s != "" {
					a.Value = slog.StringValue(hashSubject(opts.Salt, s))
				}
			}
		}
		if len(opts.Fields) == 0 {
			return a
		}
		as, ok := names[a.Key]
		if !ok {
			return slog.Attr{}
		}
		a.Key = as
		return a
	}
}

// hashSubject is the subject as the logs keep it: a keyed hash, the same for the same subject within one salt.
func hashSubject(salt, subject string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + subject))
	return hex.EncodeToString(sum[:16])
}

// plain writes one line per record: time, level and message, then key=value pairs. Names come from the same
// replacer as the other formats, so a renamed or dropped field is renamed or dropped here too.
type plain struct {
	out     io.Writer
	replace func([]string, slog.Attr) slog.Attr
	mu      sync.Mutex
}

func (p *plain) Enabled(context.Context, slog.Level) bool { return true }

func (p *plain) WithAttrs([]slog.Attr) slog.Handler { return p }

func (p *plain) WithGroup(string) slog.Handler { return p }

func (p *plain) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if p.keeps(slog.TimeKey) {
		b.WriteString(r.Time.Format(time.RFC3339Nano))
	}
	if p.keeps(slog.LevelKey) {
		b.WriteString(" " + r.Level.String())
	}
	if p.keeps(slog.MessageKey) {
		b.WriteString(" " + r.Message)
	}
	r.Attrs(func(a slog.Attr) bool {
		a = p.replace(nil, a)
		if a.Key != "" {
			b.WriteString(" " + a.Key + "=" + quote(a.Value.String()))
		}
		return true
	})
	b.WriteString("\n")

	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := io.WriteString(p.out, b.String())
	return err
}

// keeps reports whether a base attribute is written at all.
func (p *plain) keeps(key string) bool {
	return p.replace(nil, slog.String(key, "")).Key != ""
}

// quote writes a value so that a line stays one field per token: a value with a space, a quote, '=' or a control
// character (a line break among them) is quoted and escaped.
func quote(v string) string {
	if v == "" || strings.IndexFunc(v, needsQuote) >= 0 {
		return strconv.Quote(v)
	}
	return v
}

func needsQuote(r rune) bool {
	return r == ' ' || r == '"' || r == '=' || unicode.IsControl(r)
}
