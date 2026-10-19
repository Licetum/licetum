package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/i18n"
)

// docStrings collects the translatable strings of an OpenAPI document: summaries, descriptions and tag names.
func docStrings(t *testing.T, doc []byte) map[string]bool {
	t.Helper()
	var root any
	if err := json.Unmarshal(doc, &root); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch k {
				case "summary", "description":
					if s, ok := child.(string); ok {
						out[s] = true
						continue
					}
				case "tags":
					if list, ok := child.([]any); ok {
						for _, item := range list {
							switch tag := item.(type) {
							case string:
								out[tag] = true
							case map[string]any:
								if name, ok := tag["name"].(string); ok {
									out[name] = true
								}
								walk(tag)
							}
						}
						continue
					}
				}
				walk(child)
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(root)
	return out
}

// TestContract_API02_DocsTranslationsComplete: every English string of the two OpenAPI documents has a translation in each of the other
// languages, and no translation is left over from a string the documents no longer have.
func TestContract_API02_DocsTranslationsComplete(t *testing.T) {
	app := service.New(engine.New(nil, nil, &config.Config{}))
	english := map[string]bool{}
	for _, build := range []func(*service.App) (http.Handler, huma.API){NewAdminAPI, checkAPIOf} {
		_, api := build(app)
		doc, err := server.LocalizedSpec(api, i18n.Default, false)
		if err != nil {
			t.Fatal(err)
		}
		for s := range docStrings(t, doc) {
			english[s] = true
		}
	}

	for _, lang := range i18n.Supported {
		if lang == i18n.Default {
			continue
		}
		table := server.DocTranslations[lang]
		var missing, unused []string
		for s := range english {
			if _, ok := table[s]; !ok {
				missing = append(missing, s)
			}
		}
		for s := range table {
			if !english[s] {
				unused = append(unused, s)
			}
		}
		slices.Sort(missing)
		slices.Sort(unused)
		if len(missing) > 0 {
			t.Errorf("%s: %d strings without a translation, e.g. %q", lang, len(missing), missing[0])
		}
		if len(unused) > 0 {
			t.Errorf("%s: %d translations no document uses, e.g. %q", lang, len(unused), unused[0])
		}
	}
}

// checkAPIOf builds the check API for the documentation test: it lives in the core and takes the engine.
func checkAPIOf(app *service.App) (http.Handler, huma.API) {
	return server.NewCheckAPI(app.App)
}
