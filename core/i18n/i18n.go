// Package i18n holds the supported UI/API languages, Accept-Language negotiation and the
// message catalog used for user-facing API errors.
//
// English is the default and the source language. Every catalog entry must have a
// translation for every supported language — TestCatalogComplete enforces it.
package i18n

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// Lang is a supported language code (ISO 639-1).
type Lang string

const (
	EN Lang = "en"
	RU Lang = "ru"
	ZH Lang = "zh"
)

// Default is used when the client does not ask for a supported language.
const Default = EN

// Supported lists languages in the order they are offered to clients; the first one is the default.
var Supported = []Lang{EN, RU, ZH}

var matcher = language.NewMatcher([]language.Tag{language.English, language.Russian, language.Chinese})

// Parse picks the best supported language for an Accept-Language header value
// (q-weights and regional variants like "zh-CN" / "ru-RU" are honoured).
func Parse(acceptLanguage string) Lang {
	if strings.TrimSpace(acceptLanguage) == "" {
		return Default
	}
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return Default
	}
	_, idx, conf := matcher.Match(tags...)
	if conf == language.No {
		return Default
	}
	return Supported[idx]
}

// FromCode validates an explicit language code (e.g. from ?lang=); ok is false if unsupported.
func FromCode(code string) (Lang, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	if i := strings.IndexAny(code, "-_"); i > 0 {
		code = code[:i]
	}
	for _, l := range Supported {
		if string(l) == code {
			return l, true
		}
	}
	return Default, false
}

type ctxKey struct{}

// WithLang stores the request language in the context.
func WithLang(ctx context.Context, l Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request language, or Default if none was set.
func FromContext(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return l
	}
	return Default
}

// Key identifies a catalog message. It doubles as the stable machine-readable error code
// returned to clients in extra.code, so never rename an existing key.
type Key string

// Text is a message in every supported language; values are fmt format strings.
type Text map[Lang]string

// T renders the message for the given language, falling back to English.
func T(l Lang, key Key, args ...any) string {
	text, ok := catalog[key]
	if !ok {
		return string(key)
	}
	format, ok := text[l]
	if !ok {
		format = text[Default]
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// Keys returns every catalog key (for tests).
func Keys() []Key {
	out := make([]Key, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	return out
}

// Lookup returns the raw translations of a key (for tests).
func Lookup(key Key) (Text, bool) {
	t, ok := catalog[key]
	return t, ok
}
