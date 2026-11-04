package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/yaml"

	"github.com/licetum/licetum/core/i18n"
)

// The OpenAPI document is authored in English (operation summaries/descriptions in this
// package, field docs in struct tags of internal/model). locales/<lang>.json maps each English
// string to its translation; TestContract_API02_DocsTranslationsComplete fails when a string has no translation
// or a translation is no longer used.

//go:embed locales/*.json
var localeFS embed.FS

var DocTranslations = func() map[i18n.Lang]map[string]string {
	out := map[i18n.Lang]map[string]string{}
	for _, l := range i18n.Supported {
		if l == i18n.Default {
			continue
		}
		raw, err := localeFS.ReadFile("locales/" + string(l) + ".json")
		if err != nil {
			panic(fmt.Sprintf("docs translations for %s: %v", l, err))
		}
		table := map[string]string{}
		if err := json.Unmarshal(raw, &table); err != nil {
			panic(fmt.Sprintf("docs translations for %s: %v", l, err))
		}
		out[l] = table
	}
	return out
}()

// translatableKeys are the OpenAPI string fields that carry human-readable text.
var translatableKeys = map[string]bool{"summary": true, "description": true}

// walkDocStrings visits every translatable string of a generic JSON document (descriptions,
// summaries, tag names) and replaces it with fn's result.
func walkDocStrings(v any, fn func(string) string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			switch {
			case translatableKeys[k]:
				if s, ok := child.(string); ok {
					x[k] = fn(s)
					continue
				}
			case k == "tags":
				// operation.tags is a list of names; the top-level tags is a list of objects.
				if list, ok := child.([]any); ok {
					for i, item := range list {
						switch tag := item.(type) {
						case string:
							list[i] = fn(tag)
						case map[string]any:
							if name, ok := tag["name"].(string); ok {
								tag["name"] = fn(name)
							}
							walkDocStrings(tag, fn)
						}
					}
					continue
				}
			}
			x[k] = walkDocStrings(child, fn)
		}
	case []any:
		for i, item := range x {
			x[i] = walkDocStrings(item, fn)
		}
	}
	return v
}

// localizedSpec renders the OpenAPI document in the given language as JSON.
func localizedSpec(spec *huma.OpenAPI, l i18n.Lang) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil || l == i18n.Default {
		return raw, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	table := DocTranslations[l]
	walkDocStrings(doc, func(s string) string {
		if t, ok := table[s]; ok && t != "" {
			return t
		}
		return s
	})
	return json.Marshal(doc)
}

// LocalizedSpec is used by the `licetum openapi` command.
func LocalizedSpec(api huma.API, l i18n.Lang, asYAML bool) ([]byte, error) {
	out, err := localizedSpec(api.OpenAPI(), l)
	if err != nil || !asYAML {
		return out, err
	}
	var buf bytes.Buffer
	err = yaml.Convert(&buf, bytes.NewReader(out))
	return buf.Bytes(), err
}

type specCache struct {
	mu   sync.Mutex
	json map[i18n.Lang][]byte
	yaml map[i18n.Lang][]byte
	api  huma.API
}

func (c *specCache) get(l i18n.Lang, asYAML bool) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cache := c.json
	if asYAML {
		cache = c.yaml
	}
	if b, ok := cache[l]; ok {
		return b, nil
	}
	b, err := LocalizedSpec(c.api, l, asYAML)
	if err != nil {
		return nil, err
	}
	cache[l] = b
	return b, nil
}

var languageNames = map[i18n.Lang]string{i18n.EN: "English", i18n.RU: "Русский", i18n.ZH: "中文"}

const swaggerUIVersion = "5.31.1"

var docsPage = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@{{.Version}}/swagger-ui.css">
  <style>
    body { margin: 0; }
    .lang-switch { display: flex; justify-content: flex-end; gap: 12px; padding: 10px 20px;
      font: 14px sans-serif; border-bottom: 1px solid #eee; }
    .lang-switch a { color: #3b4151; text-decoration: none; }
    .lang-switch a[aria-current] { font-weight: 700; text-decoration: underline; }
  </style>
</head>
<body>
  <nav class="lang-switch">
    {{range .Languages}}<a href="?lang={{.Code}}" hreflang="{{.Code}}"{{if .Current}} aria-current="page"{{end}}>{{.Name}}</a>{{end}}
  </nav>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@{{.Version}}/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.ui = SwaggerUIBundle({ url: {{.SpecURL}}, dom_id: "#swagger-ui", deepLinking: true, persistAuthorization: true });
  </script>
</body>
</html>`))

type docsLanguage struct {
	Code    string
	Name    string
	Current bool
}

// registerDocs serves /openapi.json, /openapi.yaml and /docs in the language picked by
// requestLang (?lang= first, then Accept-Language).
func registerDocs(mux *http.ServeMux, api huma.API) {
	cache := &specCache{json: map[i18n.Lang][]byte{}, yaml: map[i18n.Lang][]byte{}, api: api}

	serveSpec := func(asYAML bool, contentType string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, err := cache.get(i18n.FromContext(r.Context()), asYAML)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(body)
		}
	}
	mux.HandleFunc("GET /openapi.json", serveSpec(false, "application/openapi+json"))
	mux.HandleFunc("GET /openapi.yaml", serveSpec(true, "application/openapi+yaml"))

	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		lang := i18n.FromContext(r.Context())
		langs := make([]docsLanguage, 0, len(i18n.Supported))
		for _, l := range i18n.Supported {
			langs = append(langs, docsLanguage{Code: string(l), Name: languageNames[l], Current: l == lang})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = docsPage.Execute(w, map[string]any{
			"Lang":      lang,
			"Title":     api.OpenAPI().Info.Title,
			"Version":   swaggerUIVersion,
			"SpecURL":   "/openapi.json?lang=" + string(lang),
			"Languages": langs,
		})
	})
}
