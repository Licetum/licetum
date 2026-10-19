package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/cli"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/server"
)

func docsHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, _ := NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
	return handler
}

func TestOpenAPIExposesStableOperations(t *testing.T) {
	admin, _ := NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
	record := httptest.NewRecorder()
	admin.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if record.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200", record.Code)
	}
	checkOpenAPIDocument(t, record.Body.Bytes())

	// The PDP serves no document on its public port, nor the component schemas of it, nor docs: `licetum openapi pdp` prints the
	// document (TestContract_API01), and its operations are on the ops listener.
	pdp, api := server.NewPDPAPI(&engine.App{Cfg: &config.Config{}})
	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/docs", "/schemas/Error.json"} {
		record := httptest.NewRecorder()
		pdp.ServeHTTP(record, httptest.NewRequest(http.MethodGet, path, nil))
		if record.Code != http.StatusNotFound {
			t.Fatalf("pdp: %s status = %d, want 404", path, record.Code)
		}
	}
	doc, err := server.LocalizedSpec(api, i18n.Default, false)
	if err != nil {
		t.Fatal(err)
	}
	checkOpenAPIDocument(t, doc)
}

// checkOpenAPIDocument checks what makes a document usable: its OpenAPI version and a non-empty set of paths.
func checkOpenAPIDocument(t *testing.T, raw []byte) {
	t.Helper()
	var spec struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.1.0" || len(spec.Paths) == 0 {
		t.Fatalf("invalid OpenAPI document: version=%q paths=%d", spec.OpenAPI, len(spec.Paths))
	}
}

// TestContract_API01_CommittedSchemasMatchTheCode: the committed schemas are the documents the openapi commands print, so the
// client generated from the admin schema and the drift check of task go:schema:check describe the code. The test and the
// commands run the same function (cli.OpenAPI). The schemas are read relative to this package, as the other tests of the
// module read their files. After a change of an operation or a model, run task go:schema and commit the result.
func TestContract_API01_CommittedSchemasMatchTheCode(t *testing.T) {
	admin := map[string]func() huma.API{"admin": func() huma.API {
		_, a := NewAdminAPI(service.New(&engine.App{Cfg: &config.Config{}}))
		return a
	}}
	pdp := map[string]func() huma.API{"pdp": func() huma.API {
		_, a := server.NewPDPAPI(&engine.App{Cfg: &config.Config{}})
		return a
	}}
	for _, c := range []struct {
		file string
		args []string
		apps map[string]func() huma.API
	}{
		{"../schema/admin.yaml", []string{"admin", "-yaml"}, admin},
		{"../../core/schema/pdp.yaml", []string{"pdp", "-yaml"}, pdp},
	} {
		committed, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		generated, err := cli.OpenAPI(c.args, c.apps)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(generated, committed) {
			t.Errorf("%s is not what `openapi %s -yaml` prints: run task go:schema and commit the result", c.file, c.args[0])
		}
	}
}

func TestLocalizedDocsHonorQueryAndAcceptLanguage(t *testing.T) {
	handler := docsHandler(t)
	tests := []struct {
		name, target, accept, wantSummary, wantLanguage string
	}{
		{"default", "/openapi.json", "", "Create a role", "en"},
		{"query ru", "/openapi.json?lang=ru", "", "Создать роль", "ru"},
		{"query zh", "/openapi.json?lang=zh", "", "创建角色", "zh"},
		{"header zh", "/openapi.json", "zh-CN,zh;q=0.9", "创建角色", "zh"},
		{"query wins", "/openapi.json?lang=en", "ru", "Create a role", "en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.accept != "" {
				req.Header.Set("Accept-Language", tt.accept)
			}
			handler.ServeHTTP(record, req)
			if record.Code != http.StatusOK || record.Header().Get("Content-Language") != tt.wantLanguage {
				t.Fatalf("status/language = %d/%q", record.Code, record.Header().Get("Content-Language"))
			}
			var spec struct {
				Paths map[string]map[string]struct {
					Summary string `json:"summary"`
				} `json:"paths"`
			}
			if err := json.Unmarshal(record.Body.Bytes(), &spec); err != nil {
				t.Fatal(err)
			}
			if got := spec.Paths["/role"]["post"].Summary; got != tt.wantSummary {
				t.Fatalf("summary = %q, want %q", got, tt.wantSummary)
			}
		})
	}

	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/docs?lang=zh", nil))
	if record.Code != http.StatusOK || !strings.Contains(record.Body.String(), `lang="zh"`) || !strings.Contains(record.Body.String(), "/openapi.json?lang=zh") {
		t.Fatalf("localized docs page = %d %.300s", record.Code, record.Body.String())
	}
}

func TestValidationErrorsAreLocalizedAndMachineReadable(t *testing.T) {
	// A route that asks for nothing but a service id, so that nothing stands before its validation: every operation of the admin
	// API but the platform id is behind the policy of the platform.
	handler, _ := server.NewServer(&engine.App{Cfg: &config.Config{}}, "probe", nil, func(api huma.API) {
		server.Register(api, huma.Operation{OperationID: "probe", Method: http.MethodGet, Path: "/service/{service_id}/probe"},
			func(ctx context.Context, in *ServicePath) (*server.Out[string], error) { return server.OK("ok") })
	})
	for _, tt := range []struct {
		language i18n.Lang
		accept   string
		prefix   string
	}{
		{i18n.EN, "", "Validation failed"},
		{i18n.RU, "ru-RU,ru;q=0.9", "Ошибка валидации"},
		{i18n.ZH, "zh-CN", "验证失败"},
	} {
		record := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/service/not-a-uuid/probe", nil)
		if tt.accept != "" {
			req.Header.Set("Accept-Language", tt.accept)
		}
		handler.ServeHTTP(record, req)
		var body struct {
			Detail string `json:"detail"`
			Extra  struct {
				Code string `json:"code"`
			} `json:"extra"`
		}
		if err := json.Unmarshal(record.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if record.Code != http.StatusUnprocessableEntity || !strings.HasPrefix(body.Detail, tt.prefix) || body.Extra.Code != "validation_failed" {
			t.Fatalf("language %s: %d %s", tt.language, record.Code, record.Body.String())
		}
	}
}
