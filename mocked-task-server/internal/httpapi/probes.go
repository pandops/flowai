package httpapi

import (
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
)

// ReadinessChecker returns true if the service is ready to serve traffic.
type ReadinessChecker func() (ready bool, dependencies map[string]bool)

// ReadinessFunc adapts a simple func to ReadinessChecker.
func ReadinessFunc(fn func() bool) ReadinessChecker {
	return func() (bool, map[string]bool) {
		return fn(), nil
	}
}

// RegisterProbes mounts GET /v1/livez and GET /v1/readyz onto the router.
//
//   - /livez always returns 200 OK if the process can answer the request.
//   - /readyz returns 200 OK when checker reports ready, otherwise 503.
//
// `executorID` is echoed in liveness/readiness responses for correlation.
func RegisterProbes(r chi.Router, serviceName, executorID string, ready ReadinessChecker) {
	r.Get("/livez", func(w http.ResponseWriter, req *http.Request) {
		JSON(w, req, http.StatusOK, map[string]any{
			"status":      "ok",
			"service":     serviceName,
			"executor_id": executorID,
		})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ok, deps := ready()
		if ok {
			JSON(w, req, http.StatusOK, map[string]any{
				"status":      "ready",
				"service":     serviceName,
				"executor_id": executorID,
				"dependencies": deps,
			})
			return
		}
		JSON(w, req, http.StatusServiceUnavailable, map[string]any{
			"status":      "not_ready",
			"service":     serviceName,
			"executor_id": executorID,
			"dependencies": deps,
		})
	})
}

// AtomicBool is a tiny wrapper around atomic.Bool for use as a ReadinessChecker
// dependency flag.
type AtomicBool struct {
	v atomic.Bool
}

// Set updates the value.
func (a *AtomicBool) Set(v bool) { a.v.Store(v) }

// Get returns the value.
func (a *AtomicBool) Get() bool { return a.v.Load() }