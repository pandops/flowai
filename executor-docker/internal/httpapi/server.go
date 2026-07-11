// Package httpapi provides the platform HTTP server scaffolding shared by all
// FlowAI backend services: chi-based router, slog request middleware,
// platform health probes (/v1/livez, /v1/readyz) and a uniform error envelope.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/flowai/platform/executor-docker/internal/logging"
)

// RequestIDHeader is the canonical correlation header used across services.
const RequestIDHeader = "X-Request-Id"

// ErrorEnvelope is the platform-wide error response shape.
type ErrorEnvelope struct {
	RequestID string         `json:"request_id"`
	Error     ErrorBody      `json:"error"`
}

// ErrorBody is the nested error payload.
type ErrorBody struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// NewRouter returns a chi router pre-configured with platform middleware
// (RequestID, real IP, structured access logs, panic recovery).
func NewRouter(serviceName string, logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(slogRequestLogger(serviceName, logger))
	r.Use(middleware.Timeout(30 * time.Second))
	return r
}

// JSON writes v as JSON to w with the given status code.
func JSON(w http.ResponseWriter, r *http.Request, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an error envelope in JSON with the given status code.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]interface{}) {
	env := ErrorEnvelope{
		RequestID: RequestID(r),
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	JSON(w, r, status, env)
}

// RequestID returns the request correlation id, generating one if missing.
func RequestID(r *http.Request) string {
	id := middleware.GetReqID(r.Context())
	if id == "" {
		id = r.Header.Get(RequestIDHeader)
	}
	return id
}

// WriteErrorFromContext logs and writes an error; if err is nil, code/message
// are used directly.
func WriteErrorFromContext(ctx context.Context, w http.ResponseWriter, r *http.Request, status int, err error, code, message string) {
	logger := logging.FromContext(ctx)
	reqID := RequestID(r)
	if err != nil {
		logger.Error("http error", "request_id", reqID, "status", status, "code", code, "message", message, "error", err.Error())
	} else {
		logger.Warn("http error", "request_id", reqID, "status", status, "code", code, "message", message)
	}
	WriteError(w, r, status, code, message, nil)
}

// slogRequestLogger emits a structured access log line per request.
func slogRequestLogger(serviceName string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			dur := time.Since(start)
			logger.Info("http",
				"service", serviceName,
				"request_id", RequestID(r),
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

// Common HTTP error helpers.
var (
	ErrBadRequest    = errors.New("bad request")
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrInternal      = errors.New("internal error")
	ErrUnavailable   = errors.New("service unavailable")
)

// BadRequest writes a 400 error envelope.
func BadRequest(w http.ResponseWriter, r *http.Request, message string, details map[string]interface{}) {
	WriteError(w, r, http.StatusBadRequest, "bad_request", message, details)
}

// NotFound writes a 404 error envelope.
func NotFound(w http.ResponseWriter, r *http.Request, message string) {
	WriteError(w, r, http.StatusNotFound, "not_found", message, nil)
}

// Conflict writes a 409 error envelope.
func Conflict(w http.ResponseWriter, r *http.Request, message string) {
	WriteError(w, r, http.StatusConflict, "conflict", message, nil)
}

// Internal writes a 500 error envelope.
func Internal(w http.ResponseWriter, r *http.Request, message string) {
	WriteError(w, r, http.StatusInternalServerError, "internal_error", message, nil)
}

// ServiceUnavailable writes a 503 error envelope.
func ServiceUnavailable(w http.ResponseWriter, r *http.Request, message string) {
	WriteError(w, r, http.StatusServiceUnavailable, "service_unavailable", message, nil)
}