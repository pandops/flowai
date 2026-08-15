// Package httpapi mounts the State Registry HTTP scaffold. The
// scaffold exposes the platform health probes (/v1/livez,
// /v1/readyz), the v0002.20 events-stream transport probe, and
// every documented business surface (admin onboarding + read
// projections, listener ingestion, Executor registration +
// discovery + claim, task point read + lifecycle events + Executor
// self events, trusted-Gateway controls, trusted-Gateway
// environments + secrets + open environment + audit), and, only in
// explicit test mode, the temporary observability counter. After
// v0009 the State Registry does not terminate backend
// service-to-service mTLS or derive identity from peer
// certificates; the X-FlowAI-* headers are trusted request data,
// and the deployment network policy owns the caller boundary.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/flowai/platform/svc/state-registry/internal/health"
	"github.com/flowai/platform/svc/state-registry/internal/store"
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

// Routes wires probes, business routes, and the optional test-only decrypt
// counter. The caller passes the process logger so requests share the same
// slog handler.
func Routes(serviceName, executorID string, logger *slog.Logger, checker health.ReadinessChecker, ops *DecryptOps, testMode bool, adminRepositories ...store.AdminRepository) http.Handler {
	return RoutesWithKeyring(serviceName, executorID, logger, checker, ops, nil, testMode, adminRepositories...)
}

// RoutesWithKeyring is the explicit constructor that accepts the
// cursor keyring. Every documented business surface is mounted on
// every router (production + test mode) when the supplied first
// repository implements the relevant capability. Production
// security comes from cmd/state-registry/main.go's `withPeerIdentity`
// middleware, which strips caller-supplied X-FlowAI-* headers and
// maps only verified peer-cert claims; the per-surface
// authentication middleware then rejects every request that lacks
// the documented identity before any repository call is made.
//
// The cursor keyring gates ONLY the cursor-encrypted collection
// reads (admin /admin/tags, /admin/tasks; trusted-Gateway
// GET /v1/tasks; GET /v1/audit). When the keyring is nil those
// handlers refuse to mount, so a misconfigured production binary
// that lost its keyring configuration fails closed for the
// paginated reads. The /v1/_test/decrypt-ops counter route is the
// ONLY surface still gated on testMode.
func RoutesWithKeyring(serviceName, executorID string, logger *slog.Logger, checker health.ReadinessChecker, ops *DecryptOps, keyring cursorKeyring, testMode bool, adminRepositories ...store.AdminRepository) http.Handler {
	r := NewRouter(serviceName, logger)
	r.Route("/v1", func(v1 chi.Router) {
		RegisterProbes(v1, serviceName, executorID, checker)
		if testMode {
			v1.Get("/_test/decrypt-ops", ops.Handler())
		}
		if len(adminRepositories) > 0 && adminRepositories[0] != nil {
			if executorRepo, ok := adminRepositories[0].(store.ExecutorRepository); ok {
				RegisterExecutor(v1, logger, adminRepositories[0], executorRepo)
			} else {
				RegisterExecutorRegistrationGuard(v1, logger, adminRepositories[0])
			}
		}
	})
	if len(adminRepositories) > 0 && adminRepositories[0] != nil {
		RegisterAdmin(r, logger, adminRepositories[0])
		if keyring != nil {
			if list, ok := adminRepositories[0].(store.ListRepository); ok {
				RegisterAdminList(r, logger, list, keyring)
			}
		}
	}
	if keyring != nil && len(adminRepositories) > 0 && adminRepositories[0] != nil {
		if list, ok := adminRepositories[0].(store.ListRepository); ok {
			RegisterGatewayList(r, logger, list, keyring)
		}
	}
	// Section 7 read surfaces (GET /v1/tasks/{task_id}/events and
	// GET /v1/executors/{executor_id}/events) do not depend on cursor
	// encryption, so they are mounted outside the keyring-gated block
	// alongside the Section 7 write surfaces. The capability check is
	// the only requirement.
	if len(adminRepositories) > 0 && adminRepositories[0] != nil {
		if events, ok := adminRepositories[0].(store.TaskEventRepository); ok {
			RegisterTaskEventReads(r, logger, events)
			RegisterTaskEventWrites(r, logger, events)
		}
		if execEvents, ok := adminRepositories[0].(store.ExecutorEventRepository); ok {
			RegisterExecutorSelfEventReads(r, logger, execEvents)
			RegisterExecutorSelfEvents(r, logger, execEvents)
		}
	}
	// Section 6 trusted-Gateway task point read surface
	// (GET /v1/tasks/{task_id}). The handler does not depend on
	// the cursor keyring, so it sits outside the keyring gate.
	if len(adminRepositories) > 0 && adminRepositories[0] != nil {
		if taskReader, ok := adminRepositories[0].(store.TaskPointReadRepository); ok {
			RegisterGatewayTaskPointRead(r, logger, taskReader)
		}
		if controls, ok := adminRepositories[0].(store.ControlRepository); ok {
			RegisterTaskControls(r, logger, controls)
			RegisterUIControls(r, logger, controls)
		}
		if controlEvents, ok := adminRepositories[0].(store.ControlEventRepository); ok {
			RegisterTaskControlEvents(r, logger, controlEvents)
		}
		if taskLogs, ok := adminRepositories[0].(store.TaskLogRepository); ok {
			RegisterTaskLogs(r, logger, taskLogs)
		}
		if audit, ok := adminRepositories[0].(store.AuditRepository); ok && keyring != nil {
			RegisterAudit(r, logger, audit, keyring)
		}
		if environments, ok := adminRepositories[0].(store.EnvironmentRepository); ok {
			RegisterEnvironments(r, logger, environments, keyring)
		}
		if launchParameters, ok := adminRepositories[0].(store.LaunchParameterRepository); ok {
			RegisterUILaunchParameters(r, logger, launchParameters)
		}
		if uiOperator, ok := adminRepositories[0].(store.UIOperatorRepository); ok {
			RegisterUIOperator(r, logger, uiOperator)
		}
		if secrets, ok := adminRepositories[0].(store.SecretRepository); ok {
			RegisterSecrets(r, logger, secrets, keyring)
			RegisterUISecrets(r, logger, secrets)
		}
		if environmentOpen, ok := adminRepositories[0].(store.OpenEnvironmentRepository); ok {
			RegisterEnvironmentOpen(r, logger, environmentOpen, ops)
		}
	}
	// The Section 4 listener ingestion route shares the existing
	// /v1/tasks path with the trusted-Gateway GET. RegisterListener
	// internally guards against nil dependencies so callers that
	// pass a non-listener repository (or nil) keep the prior 404
	// behavior — the listener adapter is mounted only when the
	// supplied first repository implements ListenerRepository.
	if len(adminRepositories) > 0 && adminRepositories[0] != nil {
		if listener, ok := adminRepositories[0].(store.ListenerRepository); ok {
			RegisterListener(r, logger, listener)
		}
	}
	// The v0002.20 transport probe mounts GET /v1/events/stream on
	// every production router. Production security comes from the
	// verified peer-identity middleware in cmd/state-registry/main.go
	// — only a cert whose subject parses into a strict claims shape
	// with role=gateway (and a verified team_id + operator_id CN)
	// reaches the trusted-Gateway middleware mounted by
	// RegisterEventsStream. The route is a minimal transport probe;
	// no replay or fan-out is implemented here.
	var streamRepo store.StreamRepository
	if len(adminRepositories) > 0 && adminRepositories[0] != nil {
		streamRepo, _ = adminRepositories[0].(store.StreamRepository)
	}
	RegisterEventsStream(r, logger, streamRepo)
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

// DecryptOps is the test-only counter that lets the qa-e2e harness
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
// does not invoke it; the qa-e2e harness uses Snapshot to confirm
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

// Reset clears the counter. Used by the qa-e2e harness between
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
