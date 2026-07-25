// Package httpapi mounts the State Registry HTTP scaffold. The scaffold
// exposes the platform health probes (/v1/livez, /v1/readyz) and, only in
// explicit test mode, the temporary header-authenticated Section 3 admin
// routes, the Section 3b admin/Gateway reads, and the Section 4 listener
// ingestion route, plus test observability. Production business routes
// remain closed until their mutually authenticated service-identity
// adapters are implemented.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/flowai/platform/state-registry/internal/health"
	"github.com/flowai/platform/state-registry/internal/store"
)

// JSON writes v as JSON with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// NewRouter returns a chi router pre-configured with platform middleware.
func NewRouter(serviceName string, logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(slogRequestLogger(serviceName, logger))
	r.Use(middleware.Timeout(30 * time.Second))
	return r
}

// Routes wires the scaffold probes and conditionally mounts test-only
// surfaces. Header-derived identities are deliberately confined to test mode:
// production startup therefore fails closed until mutually authenticated
// service identity is wired by the transport implementation slice. The caller
// passes the process logger so requests share the same slog handler.
func Routes(serviceName, executorID string, logger *slog.Logger, checker health.ReadinessChecker, ops *DecryptOps, testMode bool, adminRepositories ...store.AdminRepository) http.Handler {
	return RoutesWithKeyring(serviceName, executorID, logger, checker, ops, nil, testMode, adminRepositories...)
}

// RoutesWithKeyring is the explicit constructor that accepts the
// cursor keyring. When the keyring is nil the admin list handlers and
// the trusted-Gateway list adapter are NOT mounted; production startup
// therefore fails closed (no business routes) when the cursor key
// configuration is unavailable. The supplied first repository is
// dynamically asserted against ListRepository and ListenerRepository;
// neither capability is required, so a caller that only needs the
// admin onboarding surface keeps working unchanged.
func RoutesWithKeyring(serviceName, executorID string, logger *slog.Logger, checker health.ReadinessChecker, ops *DecryptOps, keyring cursorKeyring, testMode bool, adminRepositories ...store.AdminRepository) http.Handler {
	r := NewRouter(serviceName, logger)
	r.Route("/v1", func(v1 chi.Router) {
		RegisterProbes(v1, serviceName, executorID, checker)
		if testMode {
			v1.Get("/_test/decrypt-ops", ops.Handler())
			if len(adminRepositories) > 0 && adminRepositories[0] != nil {
				if executorRepo, ok := adminRepositories[0].(store.ExecutorRepository); ok {
					RegisterExecutor(v1, logger, adminRepositories[0], executorRepo)
				} else {
					RegisterExecutorRegistrationGuard(v1, logger, adminRepositories[0])
				}
			}
		}
	})
	if testMode && len(adminRepositories) > 0 && adminRepositories[0] != nil {
		RegisterAdmin(r, logger, adminRepositories[0])
		if keyring != nil {
			if list, ok := adminRepositories[0].(store.ListRepository); ok {
				RegisterAdminList(r, logger, list, keyring)
			}
		}
	}
	if testMode && keyring != nil && len(adminRepositories) > 0 && adminRepositories[0] != nil {
		if list, ok := adminRepositories[0].(store.ListRepository); ok {
			RegisterGatewayList(r, logger, list, keyring)
		}
	}
	// The Section 4 listener ingestion route shares the existing
	// /v1/tasks path with the trusted-Gateway GET. RegisterListener
	// internally guards against nil dependencies so callers that
	// pass a non-listener repository (or nil) keep the prior 404
	// behavior — the listener adapter is mounted only when the
	// supplied first repository implements ListenerRepository.
	if testMode && len(adminRepositories) > 0 && adminRepositories[0] != nil {
		if listener, ok := adminRepositories[0].(store.ListenerRepository); ok {
			RegisterListener(r, logger, listener)
		}
	}
	return r
}

// RegisterProbes mounts GET /v1/livez and GET /v1/readyz.
func RegisterProbes(r chi.Router, serviceName, executorID string, ready health.ReadinessChecker) {
	r.Get("/livez", func(w http.ResponseWriter, req *http.Request) {
		JSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"service":     serviceName,
			"executor_id": executorID,
		})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ok, deps := ready()
		if ok {
			JSON(w, http.StatusOK, map[string]any{
				"status":       "ready",
				"service":      serviceName,
				"executor_id":  executorID,
				"dependencies": deps,
			})
			return
		}
		JSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "not_ready",
			"service":      serviceName,
			"executor_id":  executorID,
			"dependencies": deps,
		})
	})
}

// DecryptOps is the test-only counter that lets the autotest harness
// observe the number of decrypt operations the State Registry has
// performed. The counter is incremented only inside the protected
// scope-token open path (not implemented at this scaffold step).
type DecryptOps struct {
	mu    sync.Mutex
	count int64
}

// NewDecryptOps returns a fresh counter.
func NewDecryptOps() *DecryptOps { return &DecryptOps{} }

// Record is called by the future protected decrypt path. The scaffold
// does not invoke it; the autotest harness uses Snapshot to confirm
// no decrypt operation ran during a smoke request.
func (o *DecryptOps) Record() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.count++
}

// Snapshot returns the current counter value.
func (o *DecryptOps) Snapshot() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.count
}

// Reset clears the counter. Used by the autotest harness between
// smoke phases when a fresh observation window is required.
func (o *DecryptOps) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.count = 0
}

// Handler returns an http.HandlerFunc that responds with the current
// decrypt-operation count. The route is namespaced under /v1/_test/* so
// it is only mounted when the production startup explicitly enables
// test mode.
func (o *DecryptOps) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]any{
			"service":     "state-registry",
			"decrypt_ops": o.Snapshot(),
		})
	}
}
