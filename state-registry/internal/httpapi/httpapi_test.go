// Package httpapi tests cover the readiness probes, the conditional
// mounting of the test-only decrypt-ops endpoint, and the counter
// behavior used by the autotest harness.
package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flowai/platform/state-registry/internal/health"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

type rejectingAdminRepository struct {
	calls int
}

type routeListRepository struct {
	rejectingAdminRepository
}

func (*routeListRepository) ListTaskTypes(context.Context, platform.AdminTagFilter, int, *store.TaskTypeAfter) ([]platform.AdminTagEntry, *store.TaskTypeAfter, error) {
	return []platform.AdminTagEntry{}, nil, nil
}

func (*routeListRepository) ListTasks(context.Context, platform.AdminTaskFilter, int, *store.TaskAfter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
	return []platform.TaskListEntry{}, nil, nil
}

type routeCursorKeyring struct{}

func (routeCursorKeyring) ActiveKeyID() string { return "test-key" }
func (routeCursorKeyring) Keys() map[string][]byte {
	return map[string][]byte{"test-key": make([]byte, 32)}
}

var _ store.AdminRepository = (*rejectingAdminRepository)(nil)

func (r *rejectingAdminRepository) CreateTeam(context.Context, platform.CreateTeamRequest, platform.AdminIdentity) (platform.Team, error) {
	r.calls++
	return platform.Team{}, nil
}

func (r *rejectingAdminRepository) CreateSourceSystem(context.Context, platform.CreateSourceSystemRequest, platform.AdminIdentity) (platform.SourceSystem, error) {
	r.calls++
	return platform.SourceSystem{}, nil
}

func (r *rejectingAdminRepository) CreateTaskType(context.Context, platform.CreateTaskTypeRequest, platform.AdminIdentity) (platform.TaskType, error) {
	r.calls++
	return platform.TaskType{}, nil
}

func (r *rejectingAdminRepository) TeamExists(context.Context, string) (bool, error) {
	r.calls++
	return true, nil
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func alwaysReady() (bool, map[string]bool) {
	return true, map[string]bool{"postgres": true, "aes_key": true}
}

func alwaysNotReady() (bool, map[string]bool) {
	return false, map[string]bool{"postgres": false, "aes_key": false}
}

func TestLivezReturnsOK(t *testing.T) {
	r := Routes("state-registry", "", newTestLogger(), alwaysReady, NewDecryptOps(), false)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/livez")
	if err != nil {
		t.Fatalf("get livez: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body=%v", body)
	}
}

func TestReadyzReturns200WhenReady(t *testing.T) {
	r := Routes("state-registry", "", newTestLogger(), alwaysReady, NewDecryptOps(), false)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("get readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestReadyzReturns503WhenNotReady(t *testing.T) {
	r := Routes("state-registry", "", newTestLogger(), alwaysNotReady, NewDecryptOps(), false)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("get readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDecryptOpsCounterStartsZero(t *testing.T) {
	ops := NewDecryptOps()
	if ops.Snapshot() != 0 {
		t.Fatalf("counter must start at zero")
	}
}

func TestDecryptOpsRecordIncrements(t *testing.T) {
	ops := NewDecryptOps()
	ops.Record()
	ops.Record()
	ops.Record()
	if ops.Snapshot() != 3 {
		t.Fatalf("expected 3 after three Record() calls, got %d", ops.Snapshot())
	}
}

func TestDecryptOpsReset(t *testing.T) {
	ops := NewDecryptOps()
	ops.Record()
	ops.Reset()
	if ops.Snapshot() != 0 {
		t.Fatalf("counter must be zero after Reset")
	}
}

func TestDecryptOpsRouteIsAbsentByDefault(t *testing.T) {
	ops := NewDecryptOps()
	ops.Record()
	r := Routes("state-registry", "", newTestLogger(), alwaysReady, ops, false)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/_test/decrypt-ops")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 when test mode is off", resp.StatusCode)
	}
}

func TestDecryptOpsRouteIsPresentInTestMode(t *testing.T) {
	ops := NewDecryptOps()
	ops.Record()
	ops.Record()
	r := Routes("state-registry", "", newTestLogger(), alwaysReady, ops, true)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/_test/decrypt-ops")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d, want 200 when test mode is on", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["decrypt_ops"].(float64) != 2 {
		t.Fatalf("expected 2 decrypt ops, got %v", body["decrypt_ops"])
	}
}

func TestProductionRoutesRejectForgedIdentityHeadersBeforeRepositoryAccess(t *testing.T) {
	repo := &rejectingAdminRepository{}
	router := Routes("state-registry", "", newTestLogger(), alwaysReady, NewDecryptOps(), false, repo)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		role   string
	}{
		{
			name:   "forged admin",
			method: http.MethodPost,
			path:   "/admin/teams",
			body:   `{"team_name":"forged","default_image":{"repository":"registry.example/agent","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`,
			role:   "admin",
		},
		{
			name:   "forged executor",
			method: http.MethodPut,
			path:   "/v1/executors/exec-forged",
			body:   `{"scope":"team","team_id":"team-forged"}`,
			role:   "team-executor",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-FlowAI-Role", tc.role)
			req.Header.Set("X-FlowAI-Admin-Subject", "forged-subject")
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status=%d, want 404 while production identity adapters are unavailable", recorder.Code)
			}
		})
	}

	if repo.calls != 0 {
		t.Fatalf("repository calls=%d, want 0 for forged production headers", repo.calls)
	}
}

func TestRoutesWithKeyringMountsSection3bPaths(t *testing.T) {
	repo := &routeListRepository{}
	handler := RoutesWithKeyring(
		"state-registry",
		"",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func() (bool, map[string]bool) { return true, map[string]bool{} },
		NewDecryptOps(),
		routeCursorKeyring{},
		true,
		repo,
	)

	cases := []struct {
		name    string
		path    string
		headers http.Header
	}{
		{name: "admin tags", path: "/admin/tags", headers: http.Header{"X-FlowAI-Role": {"admin"}, "X-FlowAI-Admin-Subject": {"admin-a"}}},
		{name: "gateway tasks", path: "/v1/tasks", headers: http.Header{"X-FlowAI-Role": {"gateway"}, "X-FlowAI-Team-Id": {"team-a"}, "X-FlowAI-Operator-Id": {"operator-a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			for name, values := range tc.headers {
				for _, value := range values {
					req.Header.Add(name, value)
				}
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s returned %d, want 200; body=%s", tc.path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestComposeProbeIsUsedForReadyz(t *testing.T) {
	probe := health.NewPostgresProbe(nil)
	if probe.Ready() {
		t.Fatalf("probe must report not-ready before any ping")
	}
	checker := health.Compose(probe, make([]byte, 32))
	ok, deps := checker()
	if ok {
		t.Fatalf("checker must be not-ready when postgres probe hasn't pinged")
	}
	if deps["postgres"] {
		t.Fatalf("postgres must be reported as false")
	}
}
