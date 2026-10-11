package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/licetum/licetum/admin-api/service"
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
	check, _ := server.NewCheckAPI(&engine.App{Cfg: &config.Config{}})
	for name, handler := range map[string]http.Handler{"admin": admin, "check": check} {
		t.Run(name, func(t *testing.T) {
			record := httptest.NewRecorder()
			handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
			if record.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", record.Code)
			}
			var spec struct {
				OpenAPI string                    `json:"openapi"`
				Paths   map[string]map[string]any `json:"paths"`
			}
			if err := json.Unmarshal(record.Body.Bytes(), &spec); err != nil {
				t.Fatal(err)
			}
			if spec.OpenAPI != "3.1.0" || len(spec.Paths) == 0 {
				t.Fatalf("invalid OpenAPI document: version=%q paths=%d", spec.OpenAPI, len(spec.Paths))
			}
		})
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
	handler := docsHandler(t)
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
		req := httptest.NewRequest(http.MethodGet, "/service/not-a-uuid/oauth/login", nil)
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
