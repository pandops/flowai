package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// slogRequestLogger emits one structured access-log line per request.
// The log records service name, request_id, method, path, status,
// bytes, duration, and remote address. It never logs secret material
// or decrypted environment values.
func slogRequestLogger(serviceName string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			dur := time.Since(start)
			logger.Info("http",
				"service", serviceName,
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", dur.Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}
