package i18n_test

import (
	"context"
	"regexp"
	"slices"
	"testing"

	"github.com/licetum/licetum/core/i18n"
)

var verbRe = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)

// Every key must be translated into every supported language, with the same format verbs
// in the same order — otherwise fmt would render %!v(MISSING) or swap arguments.
func TestCatalogComplete(t *testing.T) {
	for _, key := range i18n.Keys() {
		text, _ := i18n.Lookup(key)
		want := verbRe.FindAllString(text[i18n.Default], -1)
		for _, l := range i18n.Supported {
			s, ok := text[l]
			if !ok || s == "" {
				t.Errorf("%s: no %s translation", key, l)
				continue
			}
			if got := verbRe.FindAllString(s, -1); !slices.Equal(got, want) {
				t.Errorf("%s/%s: format verbs %v, want %v (as in %s)", key, l, got, want, i18n.Default)
			}
		}
	}
}

func TestParse(t *testing.T) {
	tests := map[string]i18n.Lang{
		"":                                 i18n.EN,
		"en-US,en;q=0.9":                   i18n.EN,
		"ru-RU,ru;q=0.9,en-US;q=0.8":       i18n.RU,
		"zh-CN,zh;q=0.9":                   i18n.ZH,
		"zh-TW":                            i18n.ZH,
		"de-DE,de;q=0.9":                   i18n.EN,
		"de-DE,ru;q=0.8,en;q=0.5":          i18n.RU,
		"fr;q=0.9, zh-Hans;q=0.95, en;q=0": i18n.ZH,
		"garbage;;;":                       i18n.EN,
	}
	for header, want := range tests {
		if got := i18n.Parse(header); got != want {
			t.Errorf("Parse(%q) = %s, want %s", header, got, want)
		}
	}
}

func TestFromCode(t *testing.T) {
	for code, want := range map[string]i18n.Lang{"ru": i18n.RU, "ZH": i18n.ZH, "zh-CN": i18n.ZH, "en_US": i18n.EN} {
		if got, ok := i18n.FromCode(code); !ok || got != want {
			t.Errorf("FromCode(%q) = %s, %v", code, got, ok)
		}
	}
	if _, ok := i18n.FromCode("de"); ok {
		t.Error("de must not be supported")
	}
}

func TestT(t *testing.T) {
	if got := i18n.T(i18n.RU, i18n.ServiceNotFound, "x"); got != "Сервис x не найден" {
		t.Errorf("T(ru) = %q", got)
	}
	if got := i18n.T(i18n.ZH, i18n.GroupSelfNest); got != "用户组不能嵌套到自身" {
		t.Errorf("T(zh) = %q", got)
	}
	if got := i18n.T(i18n.Lang("de"), i18n.GroupSelfNest); got != "A group cannot be nested into itself" {
		t.Errorf("fallback to English: %q", got)
	}
	if got := i18n.FromContext(i18n.WithLang(context.Background(), i18n.ZH)); got != i18n.ZH {
		t.Errorf("FromContext = %s", got)
	}
	if got := i18n.FromContext(context.Background()); got != i18n.Default {
		t.Errorf("FromContext default = %s", got)
	}
}
