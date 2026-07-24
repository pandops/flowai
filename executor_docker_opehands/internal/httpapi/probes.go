// Package httpapi provides the platform HTTP server scaffolding shared by all
// FlowAI backend services.
package httpapi

import (
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
)

// ReadinessChecker returns true if the service is ready to serve traffic.
// `deps` carries dependency health flags for the readiness response. The
// flags feed the executor.openapi.yaml ReadinessResponse schema; the two
// required booleans are state_registry_registered and openhands_reachable.
type ReadinessChecker func() (ready bool, deps ReadinessDeps)

// ReadinessDeps bundles the dependency flags surfaced by the readiness
// response. Additional flags may be added without breaking the contract.
type ReadinessDeps struct {
	StateRegistryRegistered bool
	OpenHandsReachable      bool
}

// ReadinessFunc adapts a simple func that only knows ready/not-ready.
func ReadinessFunc(fn func() bool) ReadinessChecker {
	return func() (bool, ReadinessDeps) {
		return fn(), ReadinessDeps{}
	}
}

// ReadinessFuncWithDeps adapts a func that owns its dependency flags.
func ReadinessFuncWithDeps(fn func() (bool, bool, bool)) ReadinessChecker {
	return func() (bool, ReadinessDeps) {
		ok, registered, reachable := fn()
		return ok, ReadinessDeps{
			StateRegistryRegistered: registered,
			OpenHandsReachable:      reachable,
		}
	}
}

// RegisterProbes mounts GET /v1/livez and GET /v1/readyz onto the router.
//
//   - /livez returns the LivenessResponse shape (status="ok", executor_id).
//   - /readyz returns the ReadinessResponse shape with the required
//     booleans state_registry_registered and openhands_reachable, plus
//     the 503 Service Unavailable envelope when the checker reports
//     not-ready.
func RegisterProbes(r chi.Router, serviceName, executorID string, ready ReadinessChecker) {
	r.Get("/livez", func(w http.ResponseWriter, req *http.Request) {
		JSON(w, req, http.StatusOK, platform.LivenessResponse{
			Status:     "ok",
			ExecutorID: executorID,
		})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ok, deps := ready()
		body := platform.ReadinessResponse{
			ExecutorID: executorID,
		}
		if ok {
			body.Status = "ready"
		} else {
			body.Status = "ready"
		}
		body.StateRegistryRegistered = deps.StateRegistryRegistered
		body.OpenHandsReachable = deps.OpenHandsReachable
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		JSON(w, req, status, body)
	})
}

// AtomicBool is a thin wrapper around atomic.Bool for use as a
// ReadinessChecker dependency flag.
type AtomicBool struct {
	v atomic.Bool
}

// Set updates the value.
func (a *AtomicBool) Set(v bool) { a.v.Store(v) }

// Get returns the value.
func (a *AtomicBool) Get() bool { return a.v.Load() }
