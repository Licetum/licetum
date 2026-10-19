// Package api is the HTTP layer built on Huma v2: routes, (de)serialization, schema validation,
// OpenAPI 3.1 (/openapi.json) and interactive docs (/docs), both in every supported language.
// Business logic lives in internal/service; this package only parses requests and shapes responses.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/rs/cors"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/config"
	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/httpx"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/logx"
)

// Version is the API version in the OpenAPI document.
const Version = "0.1.0"

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
		authzen := false
		if ctx != nil {
			lang = i18n.FromContext(ctx.Context())
			authzen = ctx.Operation() != nil && slices.Contains(ctx.Operation().Tags, authzenTag)
			// The AuthZEN specification knows neither a 422 nor a 413: a request that is not valid, or too large, is a 400.
			if authzen && (status == http.StatusUnprocessableEntity || status == http.StatusRequestEntityTooLarge) {
				status = http.StatusBadRequest
			}
		}
		apiErr := newHumaError(status, msg, errs...).Localize(lang)
		if authzen {
			// Huma writes these errors without the headers of an error (only the errors of a handler carry them, see
			// authzenError.GetHeaders), so the code goes on the response here.
			if ctx != nil {
				ctx.SetHeader(licetumErrorCodeHeader, apiErr.Extra.Code)
			}
			return authzenErrorOf(apiErr)
		}
		return apiErr
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
// routes are disabled: docs.go serves them with ?lang= support. Every operation names itself in the access log line.
// Huma also serves the component schemas of the document under /schemas by default; schemas says whether that stays on.
func newAPI(mux *http.ServeMux, title string, tags []*huma.Tag, schemas bool) huma.API {
	cfg := huma.DefaultConfig(title, Version)
	// No $schema field in responses and no Link headers: the response shapes are part of the API
	// contract (kept from the legacy Python version).
	cfg.CreateHooks = nil
	cfg.Info.Description = description
	cfg.Info.License = &huma.License{Name: "MIT", Identifier: "MIT"}
	cfg.Tags = tags
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	if !schemas {
		cfg.SchemasPath = ""
	}
	api := humago.New(mux, cfg)
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if info := requestInfoFrom(ctx.Context()); info != nil {
			info.operation = ctx.Operation().OperationID
		}
		next(ctx)
	})
	return api
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
	authzen := slices.Contains(op.Tags, authzenTag)
	huma.Register(api, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := h(ctx, in)
		if err != nil {
			apiErr := LocalizeError(ctx, op.OperationID, err)
			if authzen {
				return nil, authzenErrorOf(apiErr)
			}
			return nil, apiErr
		}
		return out, nil
	})
	if authzen {
		documentAuthzenErrors(api, op)
	}
}

// LocalizeError turns a handler error into the response error in the request language: an
// *apperr.Error as is, anything else as a 500 without details. Logs the errors worth seeing
// (5xx, 424, errors with a cause).
func LocalizeError(ctx context.Context, operationID string, err error) *apperr.Error {
	// When a dependency is down every request fails the same way: one line a second per kind of failure (logx.Repeated).
	apiErr, ok := apperr.As(err)
	if !ok {
		// Not the fault of the request, and not an error of the program either: the time it was given ran out, or the
		// database cannot be reached. The caller may try again, which a 500 does not tell it.
		switch {
		case errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled):
			// The client went away: nobody reads the answer, and under load many clients give up, so it is not logged.
			return apperr.ServiceUnavailable(i18n.RequestTimeout).Localize(i18n.FromContext(ctx))
		case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
			apiErr, ok = apperr.ServiceUnavailable(i18n.RequestTimeout).WithHeader("Retry-After", "1").Wrap(err), true
		case db.IsUnavailable(err):
			apiErr, ok = apperr.ServiceUnavailable(i18n.PolicyUnavailable).WithHeader("Retry-After", "1").Wrap(err), true
		}
	}
	if !ok {
		logx.Repeated.Log(ctx, slog.LevelError, fmt.Sprintf("internal error|%s|%T", operationID, err),
			"internal error", "operation", operationID, "error", err)
		return apperr.Internal().Localize(i18n.FromContext(ctx))
	}
	if apiErr.Status >= http.StatusInternalServerError || apiErr.Status == http.StatusFailedDependency || apiErr.Unwrap() != nil {
		logx.Repeated.Log(ctx, slog.LevelWarn, "request failed|"+operationID+"|"+strconv.Itoa(apiErr.Status)+"|"+apiErr.Extra.Code,
			"request failed", "operation", operationID, "status", apiErr.Status, "error", err)
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
	// trace names the revisions of the request: the one of its write goes out in the revisionHeader, and the one of the
	// policy its answers were read at in the licetumRevisionHeader.
	trace *engine.Trace
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	if s.trace != nil {
		if rev := s.trace.Revision(); rev > 0 {
			s.Header().Set(revisionHeader, strconv.FormatInt(rev, 10))
			s.Header().Set(licetumRevisionHeader, licetumRevision(rev))
		} else if code == http.StatusOK {
			// An answer carries the revision it was read at; a request that read none (a probe, the metadata) carries none.
			if rev := s.trace.ReadRevision(); rev > 0 {
				s.Header().Set(licetumRevisionHeader, licetumRevision(rev))
			}
		}
		if s.trace.SyncPending() {
			s.Header().Set(syncHeader, "pending")
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

// requestIDMax is the longest X-Request-ID that is returned as it was received.
const requestIDMax = 128

// revisionHeader carries the revision of a write: pass it back as at_least_as_fresh to get answers that reflect the write.
const revisionHeader = "X-Authz-Revision"

// licetumRevisionHeader carries the revision of the policy: the one an answer was read at, or the one a write made. Its
// value is "1.<revision>": the epoch 1 (there are no epochs before P3) and the revision of the rules.
const licetumRevisionHeader = "Licetum-Revision"

// licetumErrorCodeHeader carries the catalog key of an error of an AuthZEN operation (see authzenError).
const licetumErrorCodeHeader = "Licetum-Error-Code"

// syncHeader says "pending" when the answer lacks the resources of a type whose synchronization had not finished in time
// (SYNC__REQUEST_WAIT): such a type contributes no resources.
const syncHeader = "X-Authz-Sync"

// licetumRevision is the value of Licetum-Revision for a revision of the rules.
func licetumRevision(rev int64) string {
	return "1." + strconv.FormatInt(rev, 10)
}

// requestID is the identifier of a request: the caller's X-Request-ID, returned exactly as it was received when it is safe
// for a header (see safeRequestID), and a generated UUID otherwise or when the caller sent none.
func requestID(r *http.Request) string {
	if id := r.Header.Get(requestIDHeader); safeRequestID(id) {
		return id
	}
	return uuid.NewString()
}

// safeRequestID says whether an X-Request-ID can be returned as it was received: 1 to requestIDMax printable ASCII
// characters, with no space. A value with a space or a control character is replaced by a generated one, so that the
// header and the log line cannot be split by what a client sent.
func safeRequestID(id string) bool {
	if id == "" || len(id) > requestIDMax {
		return false
	}
	for i := range len(id) {
		if id[i] < 0x21 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

// logRequestID is the copy of a received X-Request-ID that the access log carries when it is not the request's identifier:
// at most requestIDMax bytes of it, with every byte that is not printable ASCII escaped.
func logRequestID(received string) string {
	if len(received) > requestIDMax {
		received = received[:requestIDMax]
	}
	quoted := strconv.QuoteToASCII(received)
	return quoted[1 : len(quoted)-1]
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
			// The path, the user and the person named on behalf come from the request or from the token: they are
			// made safe for a log line before they are written.
			attrs := []any{
				"request_id", logSafe(info.id), "method", logSafe(r.Method), "path", logSafe(r.URL.Path), "operation", info.operation,
				"user", logSafe(info.user), "status", rec.status, "duration_ms", time.Since(start).Milliseconds(),
			}
			if info.onBehalfOf != "" {
				attrs = append(attrs, "on_behalf_of", logSafe(info.onBehalfOf))
			}
			// An X-Request-ID that was replaced is logged as it was received, escaped, so that the line still shows what the
			// client sent.
			if received := r.Header.Get(requestIDHeader); received != "" && received != info.id {
				attrs = append(attrs, "received_request_id", logRequestID(received))
			}
			slog.InfoContext(r.Context(), "request", attrs...)
		}()
		next.ServeHTTP(rec, r)
	})
}

// logSafe escapes the line breaks of a value that came from a client or a token, so one request cannot write a
// second log entry. The JSON handler escapes everything else.
func logSafe(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", `\n`), "\r", `\r`)
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

type readyOutput struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"ok when the process may take requests"`
	}
}

// admissionExempt: the probes and the scrape of the metrics are always answered, however loaded the process is.
func admissionExempt(path string) bool {
	switch strings.TrimRight(path, "/") {
	case "/healthz", "/readyz", "/metrics":
		return true
	}
	return false
}

// withAdmission refuses a request with 503 and Retry-After when the process already serves as many as it was told to
// (HTTP__MAX_INFLIGHT), and puts the deadline of a request (HTTP__REQUEST_TIMEOUT) on the context of the others. It stands
// before everything else: a refused request costs the process as little as possible and is neither logged nor timed.
func withAdmission(app *engine.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if app.Draining() {
			// The process is leaving: ask the client to open its next connection elsewhere, so that no request is sent on a
			// connection that is about to be closed.
			w.Header().Set("Connection", "close")
		}
		if admissionExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		release, ok := app.Admit()
		if !ok {
			w.Header().Set("Retry-After", "1")
			writeJSONError(w, apperr.ServiceUnavailable(i18n.Overloaded).Localize(requestLang(r)))
			return
		}
		defer release()
		if timeout := app.RequestTimeout(); timeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// newRouteTable is the route table of an application: the Huma API on a mux, with the operations registerRoutes adds, and the
// JSON 404 for the paths none of them serves. The probes, the docs and the metrics are not part of it: NewServer adds them
// to the admin API, and the PDP's public port has none of them (its operations are on the ops listener, see NewOpsHandler).
// schemas keeps the component schemas under /schemas (the admin API serves them; the PDP does not).
func newRouteTable(title string, tags []*huma.Tag, schemas bool, registerRoutes func(api huma.API)) (*http.ServeMux, huma.API) {
	mux := http.NewServeMux()
	api := newAPI(mux, title, tags, schemas)
	registerRoutes(api)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, apperr.NotFound(i18n.RouteNotFound).Localize(i18n.FromContext(r.Context())))
	})
	return mux, api
}

// registerProbes registers the liveness probe (GET /healthz) and the readiness probe (GET /readyz). The admin API serves them on
// its port; the PDP serves them on its ops listener (NewOpsHandler).
func registerProbes(api huma.API, app *engine.App) {
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
	Register(api, huma.Operation{
		OperationID: "readyz",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Readiness check",
		Description: `Answers 200 when the process may take requests and 503 when it may not: PostgreSQL does not answer within a second,
the schema of the database is older than the release (run ` + "`licetum migrate`" + `), the copy of the policy is not loaded yet, or
the process is shutting down and waits for the balancer to stop sending it requests. A schema newer than the release is
fine. Redis is not checked: without it the process still answers, only slower (see the metric licetum_degraded).
The result is reused for a second. Use it as the readiness probe and ` + "`/healthz`" + ` as the liveness probe.`,
		Tags:   []string{"System"},
		Errors: []int{http.StatusServiceUnavailable},
	}, func(ctx context.Context, _ *struct{}) (*readyOutput, error) {
		if err := app.Ready(ctx); err != nil {
			reason := "unknown"
			var notReady *engine.NotReadyError
			if errors.As(err, &notReady) {
				reason = notReady.Reason
			}
			return nil, apperr.ServiceUnavailable(i18n.NotReady).WithSuffix(reason).Wrap(err)
		}
		out := &readyOutput{}
		out.Body.Status = "ok"
		return out, nil
	})
}

// withStack puts the middleware of the public port around a route table: admission, the access log and the recovery from
// panics, gzip, CORS, the trailing slash and the language of the answers.
func withStack(app *engine.App, routes http.Handler) http.Handler {
	return withAdmission(app, logAndRecover(app, httpx.WithGzip(withCORS(app.Cfg.Cors, stripTrailingSlash(withLang(routes))))))
}

// NewServer assembles the stack of the admin API: the route table with the probes, the localized docs and the metrics, and
// the middleware of the public port.
func NewServer(app *engine.App, title string, tags []*huma.Tag, registerRoutes func(api huma.API)) (http.Handler, huma.API) {
	mux, api := newRouteTable(title, tags, true, registerRoutes)
	registerProbes(api, app)
	registerDocs(mux, api)
	// Prometheus metrics; not part of the API description.
	mux.Handle("GET /metrics", app.MetricsHandler())
	return withStack(app, mux), api
}
