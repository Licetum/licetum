// Package api is the HTTP layer built on Huma v2: routes, (de)serialization, schema validation,
// OpenAPI 3.1 (/openapi.json) and interactive docs (/docs), both in every supported language.
// Business logic lives in internal/service; this package only parses requests and shapes responses.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/rs/cors"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/i18n"
)

// Version is the API version in the OpenAPI document.
const Version = "1.0.0"

// description is the OpenAPI info.description (English source; translated in docs_i18n.go).
const description = `**Licetum** is a centralized authorization service for all of your company's services.
Access rights are stored in one place: instead of a separate role model in every application,
rules are defined and checked centrally.

### Why

- **Single point of control.** User permissions for every system are granted, revoked and checked in one place.
- **Cross-service access.** Any entity of any service (a project, report, gallery, branch, etc.) can be
shared, and the same users and groups can be granted access to it across all systems.
- **Simpler maintenance.** Services don't implement authorization themselves; they ask Licetum:
"may user X perform action Y on object Z?"
- **Transparency and audit.** You can see who has which permissions and where they come from.

### Errors

Every error is ` + "`{\"detail\": \"text\", \"extra\": {\"type\": \"generic\" | \"missing\" | \"blocked_by\", \"code\": \"...\"}}`" + `.
` + "`detail`" + ` is localized according to the ` + "`Accept-Language`" + ` header (en, ru, zh); ` + "`extra.code`" + ` is a stable
machine-readable code.
`

func init() {
	// Every error produced by Huma itself (schema validation, malformed JSON, unexpected
	// failures) uses the same shape as service-layer errors — the frontend only parses that one.
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		lang := i18n.Default
		if ctx != nil {
			lang = i18n.FromContext(ctx.Context())
		}
		return newHumaError(status, msg, errs...).Localize(lang)
	}
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return newHumaError(status, msg, errs...)
	}
}

func newHumaError(status int, msg string, errs ...error) *apperr.Error {
	if status >= http.StatusInternalServerError {
		for _, err := range errs {
			if err != nil {
				slog.Error("internal error", "error", err)
			}
		}
		return apperr.Internal()
	}

	var details []string
	for _, err := range errs {
		if err == nil {
			continue
		}
		if d, ok := err.(huma.ErrorDetailer); ok {
			ed := d.ErrorDetail()
			if ed.Location != "" {
				details = append(details, ed.Location+": "+ed.Message)
			} else {
				details = append(details, ed.Message)
			}
			continue
		}
		details = append(details, err.Error())
	}
	if msg != "" && msg != "validation failed" {
		details = append([]string{msg}, details...)
	}
	return apperr.New(status, i18n.ValidationFailed).WithSuffix(strings.Join(details, "; "))
}

// newAPI creates a Huma API on top of the standard ServeMux. Huma's own /openapi and /docs
// routes are disabled: docs.go serves them with ?lang= support.
func newAPI(mux *http.ServeMux, title string, tags []*huma.Tag) huma.API {
	cfg := huma.DefaultConfig(title, Version)
	// No $schema field in responses and no Link headers: the response shapes are part of the API
	// contract (kept from the legacy Python version).
	cfg.CreateHooks = nil
	cfg.Info.Description = description
	cfg.Info.License = &huma.License{Name: "MIT", Identifier: "MIT"}
	cfg.Tags = tags
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	return humago.New(mux, cfg)
}

// Errors documented on every operation (like the APIRouter defaults in the Python version).
var defaultErrors = []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError}

// Register wraps huma.Register with shared defaults (status 200 as in FastAPI, the standard
// errors), localizes returned errors to the request language and logs the ones worth seeing
// (5xx, 424, errors with a cause).
func Register[I, O any](api huma.API, op huma.Operation, h func(context.Context, *I) (*O, error)) {
	if op.DefaultStatus == 0 {
		op.DefaultStatus = http.StatusOK
	}
	op.Errors = mergeErrors(defaultErrors, op.Errors)
	huma.Register(api, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := h(ctx, in)
		if err != nil {
			return nil, LocalizeError(ctx, op.OperationID, err)
		}
		return out, nil
	})
}

// LocalizeError turns a handler error into the response error in the request language: an
// *apperr.Error as is, anything else as a 500 without details. Logs the errors worth seeing
// (5xx, 424, errors with a cause).
func LocalizeError(ctx context.Context, operationID string, err error) *apperr.Error {
	apiErr, ok := apperr.As(err)
	if !ok {
		slog.ErrorContext(ctx, "internal error", "operation", operationID, "error", err)
		return apperr.Internal().Localize(i18n.FromContext(ctx))
	}
	if apiErr.Status >= http.StatusInternalServerError || apiErr.Status == http.StatusFailedDependency || apiErr.Unwrap() != nil {
		slog.WarnContext(ctx, "request failed", "operation", operationID, "status", apiErr.Status, "error", err)
	}
	return apiErr.Localize(i18n.FromContext(ctx))
}

func mergeErrors(base, extra []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, code := range append(append([]int{}, base...), extra...) {
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}

// Out is a response with a JSON body.
type Out[T any] struct {
	Body T
}

func OK[T any](v T) (*Out[T], error) { return &Out[T]{Body: v}, nil }

// ---------- middleware ----------

// requestLang picks the response language: explicit ?lang= (used by /docs and /openapi.json),
// then Accept-Language, then the default (English).
func requestLang(r *http.Request) i18n.Lang {
	if code := r.URL.Query().Get("lang"); code != "" {
		if l, ok := i18n.FromCode(code); ok {
			return l
		}
	}
	return i18n.Parse(r.Header.Get("Accept-Language"))
}

func withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := requestLang(r)
		w.Header().Set("Content-Language", string(lang))
		w.Header().Add("Vary", "Accept-Language")
		next.ServeHTTP(w, r.WithContext(i18n.WithLang(r.Context(), lang)))
	})
}

// stripTrailingSlash: the FastAPI routers were declared with a trailing "/" (/service/) and
// some nested ones without it. Normalize the path so both forms work without redirects.
func stripTrailingSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) > 1 && strings.HasSuffix(r.URL.Path, "/") {
			r.URL.Path = strings.TrimRight(r.URL.Path, "/")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			r.URL.RawPath = ""
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	// trace names the revision of the write the request made: it goes out in the revisionHeader.
	trace *engine.Trace
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	if s.trace != nil {
		if rev := s.trace.Revision(); rev > 0 {
			s.Header().Set(revisionHeader, strconv.FormatInt(rev, 10))
		}
	}
	s.ResponseWriter.WriteHeader(code)
}

// requestInfo says what a request is about. The Huma middleware knows the operation and, after a verified
// token, the caller; the outermost middleware writes the access (audit) log line and records the metrics.
type requestInfo struct {
	id, operation, user string
	// onBehalfOf is the person a trusted client asked about (the delegated endpoints); user is then the client.
	onBehalfOf string
	// trace is what the request leaves for the service layer (the actor, the revision of its write).
	trace *engine.Trace
}

type requestInfoKey struct{}

func requestInfoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(requestInfoKey{}).(*requestInfo)
	return info
}

// SetRequestUser records the caller in the audit log line. It is called only once the token is verified, so
// an unverified token never shows up in the log.
func SetRequestUser(ctx context.Context, subject string) {
	if info := requestInfoFrom(ctx); info != nil {
		info.user = subject
		if info.trace != nil {
			info.trace.SetActor(subject)
		}
	}
}

// SetOnBehalfOf records the person a trusted client asked about, in the audit log line.
func SetOnBehalfOf(ctx context.Context, subject string) {
	if info := requestInfoFrom(ctx); info != nil {
		info.onBehalfOf = subject
	}
}

const requestIDHeader = "X-Request-ID"

// revisionHeader carries the revision of a write: pass it back as at_least_as_fresh to get answers that reflect the write.
const revisionHeader = "X-Authz-Revision"

// requestID keeps the caller's X-Request-ID when it is a sane token (up to 128 characters of letters, digits,
// '.', '_' and '-') and generates one otherwise, so that a request can be followed through the logs.
func requestID(r *http.Request) string {
	if id := r.Header.Get(requestIDHeader); id != "" && len(id) <= 128 && validRequestID(id) {
		return id
	}
	return uuid.NewString()
}

func validRequestID(id string) bool {
	for _, c := range id {
		ok := c == '.' || c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !ok {
			return false
		}
	}
	return true
}

func logAndRecover(app *engine.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		info := &requestInfo{id: requestID(r)}
		ctx, trace := engine.WithTrace(r.Context())
		trace.SetRequestID(info.id)
		info.trace = trace
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, trace: trace}
		rec.Header().Set(requestIDHeader, info.id)
		r = r.WithContext(context.WithValue(ctx, requestInfoKey{}, info))
		defer func() {
			if p := recover(); p != nil {
				if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(p)
				}
				slog.ErrorContext(r.Context(), "panic", "error", p, "stack", string(debug.Stack()))
				writeJSONError(rec, apperr.Internal().Localize(requestLang(r)))
			}
			app.ObserveHTTP(info.operation, strconv.Itoa(rec.status), time.Since(start).Seconds())
			attrs := []any{
				"request_id", info.id, "method", r.Method, "path", r.URL.Path, "operation", info.operation,
				"user", info.user, "status", rec.status, "duration_ms", time.Since(start).Milliseconds(),
			}
			if info.onBehalfOf != "" {
				attrs = append(attrs, "on_behalf_of", info.onBehalfOf)
			}
			slog.InfoContext(r.Context(), "request", attrs...)
		}()
		next.ServeHTTP(rec, r)
	})
}

func writeJSONError(w http.ResponseWriter, err *apperr.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Status)
	_ = json.NewEncoder(w).Encode(err)
}

var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// withCORS is enabled only when CORS__ALLOW_ORIGINS is set (as in the Python version).
func withCORS(cfg config.Cors, next http.Handler) http.Handler {
	if len(cfg.AllowOrigins) == 0 {
		return next
	}
	methods := cfg.AllowMethods
	for _, m := range methods {
		if m == "*" {
			methods = allMethods
			break
		}
	}
	return cors.New(cors.Options{
		AllowedOrigins:   cfg.AllowOrigins,
		AllowedMethods:   methods,
		AllowedHeaders:   cfg.AllowHeaders,
		AllowCredentials: cfg.AllowCredentials,
	}).Handler(next)
}

type healthOutput struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"Always ok while the process is responding"`
	}
}

// NewServer assembles the common stack: Huma API + /healthz + localized docs + JSON 404 + middleware.
func NewServer(app *engine.App, title string, tags []*huma.Tag, registerRoutes func(api huma.API)) (http.Handler, huma.API) {
	mux := http.NewServeMux()
	api := newAPI(mux, title, tags)
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if info := requestInfoFrom(ctx.Context()); info != nil {
			info.operation = ctx.Operation().OperationID
		}
		next(ctx)
	})
	registerRoutes(api)
	Register(api, huma.Operation{
		OperationID: "healthz",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness check",
		Tags:        []string{"System"},
	}, func(context.Context, *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
	registerDocs(mux, api)
	// Prometheus metrics; not part of the API description.
	mux.Handle("GET /metrics", app.MetricsHandler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, apperr.NotFound(i18n.RouteNotFound).Localize(i18n.FromContext(r.Context())))
	})
	return logAndRecover(app, withGzip(withCORS(app.Cfg.Cors, stripTrailingSlash(withLang(mux))))), api
}
