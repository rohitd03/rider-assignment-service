package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type contextKey string

// requestIDKey is the context key under which the request ID is stored.
// Unexported type prevents key collisions with other packages.
const requestIDKey contextKey = "request_id"

// RequestIDFromContext returns the request ID injected by the logging middleware.
// Returns an empty string if the context carries no request ID.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// responseWriter wraps http.ResponseWriter to capture the status code written
// by the downstream handler. WriteHeader is idempotent — only the first call
// is recorded, matching net/http semantics.
type responseWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (rw *responseWriter) WriteHeader(status int) {
	if !rw.written {
		rw.status = status
		rw.written = true
		rw.ResponseWriter.WriteHeader(status)
	}
}

// Write ensures a status is captured even when the handler calls Write without
// an explicit WriteHeader (net/http implicitly uses 200 in that case).
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Logger is an HTTP middleware that emits one structured log line per request.
type Logger struct {
	log *slog.Logger
}

func NewLogger(log *slog.Logger) *Logger {
	return &Logger{log: log}
}

// Wrap returns a handler that:
//  1. Generates a UUID request ID and stores it in the request context.
//  2. Delegates to next.
//  3. Logs method, path, status, duration, and request_id at the appropriate
//     level: INFO for 1xx/2xx/3xx, WARN for 4xx, ERROR for 5xx.
func (l *Logger) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		r = r.WithContext(ctx)

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		attrs := []any{
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration", duration.String(),
		}

		switch {
		case rw.status >= 500:
			l.log.ErrorContext(ctx, "request completed", attrs...)
		case rw.status >= 400:
			l.log.WarnContext(ctx, "request completed", attrs...)
		default:
			l.log.InfoContext(ctx, "request completed", attrs...)
		}
	})
}
