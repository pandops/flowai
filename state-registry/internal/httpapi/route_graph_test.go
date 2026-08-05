// route_graph_test.go — production route graph regression.
//
// The State Registry normal production binary must mount every
// documented business route; only /v1/_test/decrypt-ops remains
// gated by the testMode flag (which is rejected by the un-tagged
// production build's config loader anyway). After v0009 the State
// Registry does not terminate backend service-to-service mTLS or
// derive identity from peer certificates; the X-FlowAI-* headers
// are trusted request data, and the deployment network policy
// owns the caller boundary.
//
// This file pins three production invariants:
//
//  1. The production router (testMode=false, keyring present, repo
//     implements every documented capability) mounts the full set
//     of documented business surfaces.
//  2. The /v1/_test/decrypt-ops counter route is NOT mounted when
//     testMode is false.
//  3. Each representative business route returns a non-auth status
//     (data-validation outcome from the handler) without invoking
//     any repository method, proving the route is reachable but
//     no service-auth check is performed.
//
// The testMode=true sub-case retains the legacy "/v1/_test/decrypt-ops
// is present" assertion from httpapi_test.go unchanged so the build
// tag gate stays locked.
package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// routeGraphRepo is a no-op superset implementation of every
// `store.*Repository` capability that `httpapi.RoutesWithKeyring`
// dispatches against. Every method increments its category counter
// so the route-graph regression can prove the production
// per-surface middleware rejects forged/unauthenticated requests
// BEFORE the request reaches the repository.
//
// The methods intentionally return benign zero values and nil
// errors; none of them ever run during the production rejection
// probes (zero calls) or the documented happy-path probes (where
// the harness supplies a fully verified peer cert).
type routeGraphRepo struct {
	calls atomic.Int64
}

var (
	_ store.AdminRepository           = (*routeGraphRepo)(nil)
	_ store.ExecutorRepository        = (*routeGraphRepo)(nil)
	_ store.ListenerRepository        = (*routeGraphRepo)(nil)
	_ store.ListRepository            = (*routeGraphRepo)(nil)
	_ store.TaskEventRepository       = (*routeGraphRepo)(nil)
	_ store.ExecutorEventRepository   = (*routeGraphRepo)(nil)
	_ store.TaskPointReadRepository   = (*routeGraphRepo)(nil)
	_ store.ControlRepository         = (*routeGraphRepo)(nil)
	_ store.AuditRepository           = (*routeGraphRepo)(nil)
	_ store.EnvironmentRepository     = (*routeGraphRepo)(nil)
	_ store.SecretRepository          = (*routeGraphRepo)(nil)
	_ store.OpenEnvironmentRepository = (*routeGraphRepo)(nil)
	_ store.StreamRepository          = (*routeGraphRepo)(nil)
)

func (r *routeGraphRepo) totalCalls() int64 { return r.calls.Load() }

func (r *routeGraphRepo) bump() { r.calls.Add(1) }

func (r *routeGraphRepo) CreateTeam(context.Context, platform.CreateTeamRequest, platform.AdminIdentity) (platform.Team, error) {
	r.bump()
	return platform.Team{}, nil
}

func (r *routeGraphRepo) CreateSourceSystem(context.Context, platform.CreateSourceSystemRequest, platform.AdminIdentity) (platform.SourceSystem, error) {
	r.bump()
	return platform.SourceSystem{}, nil
}

func (r *routeGraphRepo) CreateTaskType(context.Context, platform.CreateTaskTypeRequest, platform.AdminIdentity) (platform.TaskType, error) {
	r.bump()
	return platform.TaskType{}, nil
}

func (r *routeGraphRepo) TeamExists(context.Context, string) (bool, error) {
	r.bump()
	return true, nil
}

func (r *routeGraphRepo) RegisterExecutor(context.Context, string, platform.ExecutorRegistrationRequest, platform.ExecutorIdentity) (platform.Executor, error) {
	r.bump()
	return platform.Executor{}, nil
}

func (r *routeGraphRepo) GetExecutor(context.Context, string) (platform.Executor, error) {
	r.bump()
	return platform.Executor{}, nil
}

func (r *routeGraphRepo) DiscoverExecutorTasks(context.Context, string, string, int) ([]platform.TaskSummary, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) ClaimTask(context.Context, platform.ClaimRequest, string, platform.ExecutorIdentity) (platform.ClaimResponse, error) {
	r.bump()
	return platform.ClaimResponse{}, nil
}

func (r *routeGraphRepo) IngestTask(context.Context, platform.TaskIngestionRequest, platform.ListenerIdentity) (platform.TaskListEntry, bool, error) {
	r.bump()
	return platform.TaskListEntry{}, true, nil
}

func (r *routeGraphRepo) ListTaskTypes(context.Context, platform.AdminTagFilter, int, *store.TaskTypeAfter) ([]platform.AdminTagEntry, *store.TaskTypeAfter, error) {
	r.bump()
	return nil, nil, nil
}

func (r *routeGraphRepo) ListTasks(context.Context, platform.AdminTaskFilter, int, *store.TaskAfter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
	r.bump()
	return nil, nil, nil
}

func (r *routeGraphRepo) ListTaskEvents(context.Context, string, string) ([]platform.TaskEvent, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) AppendTaskEvent(context.Context, platform.TaskEventAppendRequest, platform.ExecutorIdentity) (platform.TaskEvent, error) {
	r.bump()
	return platform.TaskEvent{}, nil
}

func (r *routeGraphRepo) AppendExecutorEvent(context.Context, platform.ExecutorEventAppendRequest, platform.ExecutorIdentity) (platform.ExecutorEvent, error) {
	r.bump()
	return platform.ExecutorEvent{}, nil
}

func (r *routeGraphRepo) ListExecutorEvents(context.Context, string, string) ([]platform.ExecutorEvent, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) GetTask(context.Context, string, string) (platform.TaskListEntry, error) {
	r.bump()
	return platform.TaskListEntry{}, nil
}

func (r *routeGraphRepo) CreateTaskControl(context.Context, platform.GatewayIdentity, string, platform.CreateTaskControlRequest) (platform.TaskControl, error) {
	r.bump()
	return platform.TaskControl{}, nil
}

func (r *routeGraphRepo) GetTaskControl(context.Context, string, string, string) (platform.TaskControl, error) {
	r.bump()
	return platform.TaskControl{}, nil
}

func (r *routeGraphRepo) ListAssignedTaskControls(context.Context, string, platform.ExecutorIdentity) ([]platform.TaskControl, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) ListAuditEntries(context.Context, string, string, string, int, *store.AuditAfter) ([]platform.AuditEntry, *store.AuditAfter, error) {
	r.bump()
	return nil, nil, nil
}

func (r *routeGraphRepo) GetAuditEntry(context.Context, string, string) (platform.AuditEntry, error) {
	r.bump()
	return platform.AuditEntry{}, nil
}

func (r *routeGraphRepo) CreateEnvironment(context.Context, platform.GatewayIdentity, platform.EnvironmentWriteRequest) (platform.Environment, error) {
	r.bump()
	return platform.Environment{}, nil
}

func (r *routeGraphRepo) ListEnvironments(context.Context, string, *string, *string, int) ([]platform.Environment, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) ListEnvironmentsPaged(context.Context, string, *string, *string, int, *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error) {
	r.bump()
	return nil, nil, nil
}

func (r *routeGraphRepo) GetEnvironment(context.Context, string, string) (platform.Environment, error) {
	r.bump()
	return platform.Environment{}, nil
}

func (r *routeGraphRepo) ReplaceEnvironment(context.Context, platform.GatewayIdentity, string, platform.EnvironmentWriteRequest) (platform.Environment, error) {
	r.bump()
	return platform.Environment{}, nil
}

func (r *routeGraphRepo) DeleteEnvironment(context.Context, platform.GatewayIdentity, string) error {
	r.bump()
	return nil
}

func (r *routeGraphRepo) CreateSecret(context.Context, platform.GatewayIdentity, string, platform.SecretCreateRequest) (platform.SecretWriteResponse, error) {
	r.bump()
	return platform.SecretWriteResponse{}, nil
}

func (r *routeGraphRepo) ReplaceSecret(context.Context, platform.GatewayIdentity, string, string, platform.SecretReplaceRequest) (platform.SecretWriteResponse, error) {
	r.bump()
	return platform.SecretWriteResponse{}, nil
}

func (r *routeGraphRepo) GetSecret(context.Context, string, string, string) (platform.LogicalSecret, error) {
	r.bump()
	return platform.LogicalSecret{}, nil
}

func (r *routeGraphRepo) ListSecrets(context.Context, string, string, int) ([]platform.LogicalSecret, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) ListSecretsPaged(context.Context, string, string, int, *store.SecretAfter) ([]platform.LogicalSecret, *store.SecretAfter, error) {
	r.bump()
	return nil, nil, nil
}

func (r *routeGraphRepo) ListSecretVersions(context.Context, string, string, string, int) ([]platform.SecretVersion, error) {
	r.bump()
	return nil, nil
}

func (r *routeGraphRepo) ListSecretVersionsPaged(context.Context, string, string, string, int, *store.SecretVersionAfter) ([]platform.SecretVersion, *store.SecretVersionAfter, error) {
	r.bump()
	return nil, nil, nil
}

func (r *routeGraphRepo) GetSecretVersion(context.Context, string, string, string, int) (platform.SecretVersion, error) {
	r.bump()
	return platform.SecretVersion{}, nil
}

func (r *routeGraphRepo) RevokeSecret(context.Context, platform.GatewayIdentity, string, string) (platform.LogicalSecret, error) {
	r.bump()
	return platform.LogicalSecret{}, nil
}

func (r *routeGraphRepo) OpenEnvironment(context.Context, store.OpenEnvironmentRequest) (platform.OpenEnvironmentResponse, error) {
	r.bump()
	return platform.OpenEnvironmentResponse{}, nil
}

func (r *routeGraphRepo) ListStreamTaskEvents(context.Context, string, string, time.Time, string) ([]platform.TaskEvent, error) {
	r.bump()
	return nil, nil
}

// routeGraphKeyring is the test-mode minimal keyring. It carries a
// single active key so the cursor-based reads can mint cursors
// without ever touching the disk.
type routeGraphKeyring struct {
	activeID string
	keys     map[string][]byte
}

func (k routeGraphKeyring) ActiveKeyID() string { return k.activeID }

func (k routeGraphKeyring) Keys() map[string][]byte { return k.keys }

// walkRoutes returns the sorted set of (method, route) pairs
// registered on the supplied chi router. chi.Walk visits every
// mounted handler including the subroute groups.
func walkRoutes(t *testing.T, h http.Handler) map[string]struct{} {
	t.Helper()
	seen := map[string]struct{}{}
	if err := chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if route == "" || method == "" {
			return nil
		}
		seen[method+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	return seen
}

// newRouteGraphLogger discards every log record so the route-graph
// assertions are not coupled to slog formatting.
func newRouteGraphLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestRouteGraphProductionMountsBusinessRoutes walks the production
// router (testMode=false, keyring present, repo implements every
// documented capability) and asserts the documented business
// surfaces are mounted. The /v1/_test/decrypt-ops route must NOT
// appear because production normal mode excludes it.
func TestRouteGraphProductionMountsBusinessRoutes(t *testing.T) {
	repo := &routeGraphRepo{}
	kr := routeGraphKeyring{
		activeID: "kg-1",
		keys:     map[string][]byte{"kg-1": make([]byte, 32)},
	}
	router := httpapi.RoutesWithKeyring(
		"state-registry",
		"",
		newRouteGraphLogger(),
		func() (bool, map[string]bool) { return true, map[string]bool{"postgres": true, "aes_key": true} },
		httpapi.NewDecryptOps(),
		kr,
		false, // testMode=false → production
		repo,
	)

	seen := walkRoutes(t, router)

	want := []string{
		// Health probes — always mounted.
		"GET /v1/livez",
		"GET /v1/readyz",
		// Transport probe — always mounted (production auth is via
		// `withPeerIdentity` in cmd/state-registry/main.go).
		"GET /v1/events/stream",
		// Admin onboarding.
		"POST /admin/teams",
		"POST /admin/source-systems",
		"POST /admin/task-types",
		// Admin read projections.
		"GET /admin/tags",
		"GET /admin/tasks",
		// Listener ingestion (POST /v1/tasks) — same path as the
		// trusted-Gateway collection (GET /v1/tasks).
		"POST /v1/tasks",
		// Executor registration + read + discover + claim.
		"PUT /v1/executors/{executor_id}",
		"GET /v1/executors/{executor_id}",
		"GET /v1/executors/{executor_id}/tasks",
		"POST /v1/executors/{executor_id}/claim",
		// Executor self events (write + trusted-Gateway read).
		"POST /v1/executors/{executor_id}/events",
		"GET /v1/executors/{executor_id}/events",
		// Task point read + lifecycle events.
		"GET /v1/tasks/{task_id}",
		"GET /v1/tasks/{task_id}/events",
		"POST /v1/tasks/{task_id}/events",
		// Controls (Gateway create/read + Executor list).
		"POST /v1/tasks/{task_id}/controls",
		"GET /v1/tasks/{task_id}/controls",
		"GET /v1/tasks/{task_id}/controls/{control_id}",
		// Environments (CRUD + open).
		"POST /v1/environments",
		"GET /v1/environments",
		"GET /v1/environments/{environment_id}",
		"PUT /v1/environments/{environment_id}",
		"DELETE /v1/environments/{environment_id}",
		// Secrets.
		"POST /v1/environments/{environment_id}/secrets",
		"GET /v1/environments/{environment_id}/secrets",
		"GET /v1/environments/{environment_id}/secrets/{secret_id}",
		"PUT /v1/environments/{environment_id}/secrets/{secret_id}",
		"DELETE /v1/environments/{environment_id}/secrets/{secret_id}",
		"POST /v1/environments/{environment_id}/secrets/{secret_id}/versions",
		"GET /v1/environments/{environment_id}/secrets/{secret_id}/versions",
		"GET /v1/environments/{environment_id}/secrets/{secret_id}/versions/{version}",
		// Open environment via signed scope token.
		"GET /v1/environments/{environment_id}/open",
		// Audit.
		"GET /v1/audit",
		"GET /v1/audit/{audit_id}",
		// Trusted-Gateway task collection.
		"GET /v1/tasks",
	}
	for _, key := range want {
		if _, ok := seen[key]; !ok {
			t.Errorf("production router missing documented route %q; seen=%v", key, sortedSetKeys(seen))
		}
	}

	// /v1/_test/decrypt-ops MUST NOT be mounted when testMode=false.
	if _, ok := seen["GET /v1/_test/decrypt-ops"]; ok {
		t.Errorf("production router must NOT mount /v1/_test/decrypt-ops; seen=%v", sortedSetKeys(seen))
	}

	if repo.totalCalls() != 0 {
		t.Fatalf("chi.Walk must not invoke any repository method; calls=%d", repo.totalCalls())
	}
}

// TestRouteGraphProductionRequestsAreMountedButNotAuthRejected
// proves the v0009 contract: every representative business route is
// mounted in production and the State Registry does NOT reject
// requests based on transport identity. An empty header bag (no
// X-FlowAI-*) reaches the data-validation layer; network policy
// owns the caller boundary.
func TestRouteGraphProductionRequestsAreMountedButNotAuthRejected(t *testing.T) {
	repo := &routeGraphRepo{}
	kr := routeGraphKeyring{
		activeID: "kg-1",
		keys:     map[string][]byte{"kg-1": make([]byte, 32)},
	}
	router := httpapi.RoutesWithKeyring(
		"state-registry",
		"",
		newRouteGraphLogger(),
		func() (bool, map[string]bool) { return true, map[string]bool{"postgres": true, "aes_key": true} },
		httpapi.NewDecryptOps(),
		kr,
		false,
		repo,
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	type expectedMounted struct {
		method string
		path   string
		// wantStatuses: every per-surface data-validation layer
		// reports missing-body differently. 401 / 403 are NOT
		// permitted after v0009.
		// wantStatuses: data-validation outcomes. 401 / 403 are
		// NOT permitted after v0009.
		wantStatuses []int
	}
	cases := []expectedMounted{
		{method: http.MethodPost, path: "/admin/teams", wantStatuses: []int{http.StatusBadRequest, http.StatusOK, http.StatusCreated}},
		{method: http.MethodPost, path: "/admin/source-systems", wantStatuses: []int{http.StatusBadRequest}},
		{method: http.MethodPost, path: "/admin/task-types", wantStatuses: []int{http.StatusBadRequest}},
		{method: http.MethodGet, path: "/admin/tags", wantStatuses: []int{http.StatusBadRequest, http.StatusOK}},
		{method: http.MethodGet, path: "/admin/tasks", wantStatuses: []int{http.StatusBadRequest, http.StatusOK}},
		{method: http.MethodPost, path: "/v1/tasks", wantStatuses: []int{http.StatusBadRequest}},
		{method: http.MethodPut, path: "/v1/executors/exec-auth-reject", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusOK}},
		{method: http.MethodGet, path: "/v1/executors/exec-auth-reject", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusOK}},
		{method: http.MethodGet, path: "/v1/executors/exec-auth-reject/tasks?tag=openhands", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusOK}},
		{method: http.MethodPost, path: "/v1/executors/exec-auth-reject/claim", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodPost, path: "/v1/executors/exec-auth-reject/events", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/executors/exec-auth-reject/events", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/tasks/task-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/tasks/task-auth-reject/events", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodPost, path: "/v1/tasks/task-auth-reject/events", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusForbidden}},
		{method: http.MethodPost, path: "/v1/tasks/task-auth-reject/controls", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/tasks/task-auth-reject/controls", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusOK}},
		{method: http.MethodGet, path: "/v1/tasks/task-auth-reject/controls/ctrl-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodPost, path: "/v1/environments", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments/env-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodPut, path: "/v1/environments/env-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodDelete, path: "/v1/environments/env-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodPost, path: "/v1/environments/env-auth-reject/secrets", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments/env-auth-reject/secrets", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments/env-auth-reject/secrets/secret-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodPut, path: "/v1/environments/env-auth-reject/secrets/secret-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodDelete, path: "/v1/environments/env-auth-reject/secrets/secret-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodPost, path: "/v1/environments/env-auth-reject/secrets/secret-auth-reject/versions", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments/env-auth-reject/secrets/secret-auth-reject/versions", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments/env-auth-reject/secrets/secret-auth-reject/versions/1", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/environments/env-auth-reject/open?task_id=task-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/audit", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/audit/audit-auth-reject", wantStatuses: []int{http.StatusNotFound}},
		{method: http.MethodGet, path: "/v1/tasks", wantStatuses: []int{http.StatusBadRequest, http.StatusNotFound}},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			// The v0009 contract: 401 is no longer returned for the
			// service-auth boundary. 403 is only rejected when the
			// body code is unauthenticated / not_authorized.
			if resp.StatusCode == http.StatusUnauthorized {
				t.Fatalf("status=401, want non-auth response (v0009); body=%s", string(body))
			}
			if resp.StatusCode == http.StatusForbidden {
				var envelope struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(body, &envelope)
				if envelope.Code == "unauthenticated" || envelope.Code == "not_authorized" {
					t.Fatalf("status=403 with auth code=%q, want non-auth response (v0009); body=%s", envelope.Code, string(body))
				}
			}
		})
	}
}

// TestRouteGraphProductionDecryptOpsRemainsTestModeOnly re-asserts
// the production invariant that the test-only decrypt-ops counter
// route is NOT mounted in normal mode but IS mounted in test mode.
// Combined with the cmd/state-registry config loader that rejects
// `STATE_REGISTRY_TEST_MODE=true` in the un-tagged production
// binary, this guarantees the counter never reaches a production
// deployment.
func TestRouteGraphProductionDecryptOpsRemainsTestModeOnly(t *testing.T) {
	repo := &routeGraphRepo{}
	kr := routeGraphKeyring{
		activeID: "kg-1",
		keys:     map[string][]byte{"kg-1": make([]byte, 32)},
	}

	// Production router (testMode=false): /v1/_test/decrypt-ops is absent.
	prod := httpapi.RoutesWithKeyring(
		"state-registry",
		"",
		newRouteGraphLogger(),
		func() (bool, map[string]bool) { return true, map[string]bool{"postgres": true, "aes_key": true} },
		httpapi.NewDecryptOps(),
		kr,
		false,
		repo,
	)
	prodSrv := httptest.NewServer(prod)
	t.Cleanup(prodSrv.Close)
	prodResp, err := http.Get(prodSrv.URL + "/v1/_test/decrypt-ops")
	if err != nil {
		t.Fatalf("production decrypt-ops GET: %v", err)
	}
	_ = prodResp.Body.Close()
	if prodResp.StatusCode != http.StatusNotFound {
		t.Fatalf("production /v1/_test/decrypt-ops status=%d, want 404 (testMode=false)", prodResp.StatusCode)
	}

	// Test-mode router (testMode=true): /v1/_test/decrypt-ops is present.
	testRouter := httpapi.RoutesWithKeyring(
		"state-registry",
		"",
		newRouteGraphLogger(),
		func() (bool, map[string]bool) { return true, map[string]bool{"postgres": true, "aes_key": true} },
		httpapi.NewDecryptOps(),
		kr,
		true,
		repo,
	)
	testSrv := httptest.NewServer(testRouter)
	t.Cleanup(testSrv.Close)
	testResp, err := http.Get(testSrv.URL + "/v1/_test/decrypt-ops")
	if err != nil {
		t.Fatalf("test-mode decrypt-ops GET: %v", err)
	}
	defer testResp.Body.Close()
	if testResp.StatusCode != http.StatusOK {
		t.Fatalf("test-mode /v1/_test/decrypt-ops status=%d, want 200 (testMode=true)", testResp.StatusCode)
	}
}

// sortedSetKeys returns the sorted contents of a map[string]struct{}.
func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
