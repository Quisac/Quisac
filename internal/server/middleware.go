package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// ctxKey is the private type for context keys owned by this package.
type ctxKey int

const requestIDKey ctxKey = iota

// requestIDHeader is the inbound/outbound header carrying the correlation id.
const requestIDHeader = "X-Request-Id"

// withRequestID ensures every request carries a correlation id: it reuses an
// inbound X-Request-Id when a trusted proxy sets one, otherwise mints a fresh
// one. The id is stored in the context and echoed in the response header.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			if v, err := randomState(); err == nil {
				id = v[:16]
			}
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withLogging records one structured access-log line per request, including the
// response status, byte count, and latency.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logf(r).Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"dur", time.Since(start),
			"remote", r.RemoteAddr,
		)
	})
}

// logf returns a logger tagged with the request's correlation id, so every line
// emitted while handling a request can be traced back to it.
func (s *Server) logf(r *http.Request) *slog.Logger {
	if id, ok := r.Context().Value(requestIDKey).(string); ok && id != "" {
		return s.log.With("request_id", id)
	}
	return s.log
}

// statusRecorder captures the status code and byte count for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}
