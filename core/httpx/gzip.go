package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipPool reuses the compressors: a gzip writer carries a large window, too costly to allocate per response.
var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// WithGzip compresses the JSON responses for clients that accept gzip — a permission of a large service is a
// big answer. The decision is taken when the status is written, by the Content-Type the handler set; any other
// response passes through untouched.
func WithGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// gzipWriter compresses the body when the response is compressible; gz is nil otherwise.
type gzipWriter struct {
	http.ResponseWriter
	gz    *gzip.Writer
	wrote bool
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	h := w.Header()
	if compressible(code, h.Get("Content-Type")) && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// close flushes the compressed stream and gives the compressor back to the pool.
func (w *gzipWriter) close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	// Reset drops the reference to this response: a pooled compressor must not keep its connection alive.
	w.gz.Reset(io.Discard)
	gzipPool.Put(w.gz)
	w.gz = nil
}

// compressible reports whether a response with this status and Content-Type has a body worth compressing: JSON
// with content, not a redirect nor a bodiless status.
func compressible(code int, contentType string) bool {
	if code < 200 || code == http.StatusNoContent || code == http.StatusNotModified {
		return false
	}
	mediaType := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// acceptsGzip reports whether an Accept-Encoding header accepts gzip: a gzip coding whose q is above zero (q=0
// refuses it, RFC 9110).
func acceptsGzip(header string) bool {
	for _, item := range strings.Split(header, ",") {
		params := strings.Split(item, ";")
		if !strings.EqualFold(strings.TrimSpace(params[0]), "gzip") {
			continue
		}
		q := 1.0
		for _, param := range params[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				q = f
			}
		}
		return q > 0
	}
	return false
}
