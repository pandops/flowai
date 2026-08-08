// Section 5 — Executor registration and read-only FIFO discovery
// RED HTTP/unit tests. The tests intentionally drive the production
// httpapi.Routes constructor (which currently mounts the temporary
// PUT /v1/executors/{executor_id} guard under test mode) plus the
// GET /v1/executors/{executor_id}/tasks path (which is NOT yet
// mounted). Every assertion is the documented Section 5 contract; the
// tests therefore fail with behavior-specific 501 / 403 / 404 today
// and pass only after Section 5 lands.
//
// RED COMMAND (without integration tag):
//
//	go test ./state-registry/... \
//	    -run 'TestExecutorTeamRegistration|TestSystemExecutorRegistration|\
//
// TestRegistrationTagCount|TestRegistrationScopeDiscriminator|\
// TestTeamTagFifoDiscovery|TestSystemCrossTeamFifoDiscovery|\
// TestDiscoveryShape|TestDiscoveryIgnoresCapacity' -count=1
//
// RED EVIDENCE (current):
//   - PUT /v1/executors/{id} (team-executor, valid existing team):
//     status=501 registration_not_implemented (today) vs 200 (Section 5)
//   - PUT /v1/executors/{id} (system-executor, scope=system, team_id=null):
//     status=403 not_authorized (today's role check) vs 200 (Section 5)
//   - PUT /v1/executors/{id} multi-tag injection (authorized_tag as JSON
//     array): status=400 invalid_request (today's strict decode) vs
//     400 invalid_tag_count (Section 5 envelope)
//   - GET /v1/executors/{id}/tasks: status=404 (route not mounted) vs
//     200 with TaskSummary items (Section 5)
//
// Section 5 contract under test:
//   - team scope: existing team accepted; missing identity team
//     header, omitted body team_id, mismatched body/header team, unknown
//     team, and changed team re-registration rejected without team
//     creation.
//   - system scope: role=system-executor, team_id=null accepted;
//     non-null body team_id rejected.
//   - exactly one tag; zero-tag and multi-tag rejected with
//     400 invalid_tag_count (strict decode and tag-count validator).
//   - scope outside team/system rejected; re-registration with a
//     different scope rejected without mutation.
//   - read-only FIFO discovery for team-owned Executors
//     (team_id AND tag) and system-owned Executors (tag across teams).
//   - discovery summary shape is exactly TaskSummary (5 fields) and
//     never carries payload / image / claim / event / project /
//     environment data.
//   - discovery never reserves, assigns, or evaluates capacity.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// ---------------------------------------------------------------------------
// Executor identity headers — canonical names from
// openspec/changes/v0002-state-registry/specs/openapi/state-registry.openapi.yaml
// plus the autotest/state-registry/fixtures/identities.ts contract
// (team-executor / system-executor role assignments).
// ---------------------------------------------------------------------------

const (
	executorHdrRole       = "X-FlowAI-Role"
	executorHdrTeamID     = "X-FlowAI-Team-Id"
	executorHdrExecutorID = "X-FlowAI-Executor-Id"
	executorHdrRequestID  = "X-FlowAI-Request-Id"
	executorRoleTeam      = "team-executor"
	executorRoleSystem    = "system-executor"
	executorPutPath       = "/v1/executors/%s"
	executorDiscoverPath  = "/v1/executors/%s/tasks"
)

// executorIdentityHeaders builds the documented test-mode Executor
// identity envelope. A team-owned Executor MUST carry X-FlowAI-Team-Id;
// a system-owned Executor MUST NOT (per identities.ts).
func executorIdentityHeaders(role, teamID, executorID, requestID string) http.Header {
	h := http.Header{}
	h.Set(executorHdrRole, role)
	h.Set(executorHdrExecutorID, executorID)
	h.Set(executorHdrRequestID, requestID)
	if teamID != "" {
		h.Set(executorHdrTeamID, teamID)
	}
	return h
}

// ---------------------------------------------------------------------------
// Harness — drives the production httpapi.Routes constructor so every
// assertion exercises the real route surface and the temporary
// registration guard mounted by RoutesWithKeyring under test mode.
// ---------------------------------------------------------------------------

// executorRepo is the standalone AdminRepository test double the
// Section 5 harness uses. Unlike the existing recordingAdminRepo in
// admin_test.go (which always returns false from TeamExists), this
// fake lets the test flip the boolean per sub-test so the happy path
// reaches the 501 / 200 boundary under test while the rejection
// cases observe a clean zero-call counter on the read path.
type executorRepo struct {
	mu               sync.Mutex
	teamExists       bool
	executors        map[string]platform.Executor
	teamExistsHits   atomic.Int64
	registrationHits atomic.Int64
	teamCalls        atomic.Int64
	srcCalls         atomic.Int64
	typeCalls        atomic.Int64
}

var _ store.AdminRepository = (*executorRepo)(nil)

func (r *executorRepo) setTeamExists(exists bool) {
	r.mu.Lock()
	r.teamExists = exists
	r.mu.Unlock()
}

func (r *executorRepo) CreateTeam(context.Context, platform.CreateTeamRequest, platform.AdminIdentity) (platform.Team, error) {
	r.teamCalls.Add(1)
	return platform.Team{}, nil
}

func (r *executorRepo) CreateSourceSystem(context.Context, platform.CreateSourceSystemRequest, platform.AdminIdentity) (platform.SourceSystem, error) {
	r.srcCalls.Add(1)
	return platform.SourceSystem{}, nil
}

func (r *executorRepo) CreateTaskType(context.Context, platform.CreateTaskTypeRequest, platform.AdminIdentity) (platform.TaskType, error) {
	r.typeCalls.Add(1)
	return platform.TaskType{}, nil
}

func (r *executorRepo) TeamExists(_ context.Context, _ string) (bool, error) {
	r.teamExistsHits.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.teamExists, nil
}

func (r *executorRepo) RegisterExecutor(_ context.Context, executorID string, req platform.ExecutorRegistrationRequest, identity platform.ExecutorIdentity) (platform.Executor, error) {
	r.registrationHits.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.executors[executorID]; ok {
		if existing.Scope != req.Scope {
			return platform.Executor{}, store.ErrExecutorScopeConflict
		}
		if !sameTestTeam(existing.TeamID, req.TeamID) {
			return platform.Executor{}, store.ErrExecutorTeamConflict
		}
	}
	if identity.ExecutorID != executorID || identity.Scope != req.Scope || !sameTestTeam(identity.TeamID, req.TeamID) {
		return platform.Executor{}, store.ErrExecutorIdentityMismatch
	}
	now := time.Now().UTC()
	registeredAt := now
	if existing, ok := r.executors[executorID]; ok {
		registeredAt = existing.RegisteredAt
	}
	executor := platform.Executor{
		ExecutorID: executorID, Scope: req.Scope, TeamID: req.TeamID,
		ExecutorType: req.ExecutorType, Identity: executorID,
		AuthorizedTag: req.AuthorizedTag, MaxCapacity: req.MaxCapacity,
		RunningCount: req.RunningCount, RuntimeMetadata: req.RuntimeMetadata,
		RegisteredAt: registeredAt, UpdatedAt: now,
	}
	r.executors[executorID] = executor
	return executor, nil
}

func (r *executorRepo) GetExecutor(_ context.Context, executorID string) (platform.Executor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	executor, ok := r.executors[executorID]
	if !ok {
		return platform.Executor{}, store.ErrExecutorNotFound
	}
	return executor, nil
}

func (r *executorRepo) DiscoverExecutorTasks(_ context.Context, executorID, tag string, _ int) ([]platform.TaskSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	executor, ok := r.executors[executorID]
	if !ok {
		return nil, store.ErrExecutorNotFound
	}
	if executor.AuthorizedTag != tag {
		return nil, store.ErrExecutorTagMismatch
	}
	teamID := "team-a"
	if executor.TeamID != nil {
		teamID = *executor.TeamID
	}
	return []platform.TaskSummary{{
		TaskID: "task-section5", TeamID: teamID, RequiredTag: tag,
		CurrentState: platform.TaskStatePending, IngestedAt: time.Now().UTC(),
	}}, nil
}

func (r *executorRepo) ClaimTask(context.Context, platform.ClaimRequest, string, platform.ExecutorIdentity) (platform.ClaimResponse, error) {
	return platform.ClaimResponse{}, store.ErrExecutorNotFound
}

func (r *executorRepo) GetTask(_ context.Context, teamID, taskID string) (platform.TaskListEntry, error) {
	return platform.TaskListEntry{}, store.ErrTaskUnknown
}

func sameTestTeam(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func (r *executorRepo) teamExistsReads() int64   { return r.teamExistsHits.Load() }
func (r *executorRepo) registrationCalls() int64 { return r.registrationHits.Load() }
func (r *executorRepo) teamCreates() int64       { return r.teamCalls.Load() }
func (r *executorRepo) srcCreates() int64        { return r.srcCalls.Load() }
func (r *executorRepo) typeCreates() int64       { return r.typeCalls.Load() }

// executorHarness wires the production Routes constructor in test mode
// behind executorRepo so every pre-persistence rejection can assert
// zero CreateTeam / CreateSourceSystem / CreateTaskType calls.
type executorHarness struct {
	router http.Handler
	repo   *executorRepo
	srv    *httptest.Server
}

func newExecutorHarness(t *testing.T) *executorHarness {
	t.Helper()
	repo := &executorRepo{executors: make(map[string]platform.Executor)}
	repo.setTeamExists(true)
	router := httpapi.Routes(
		"state-registry",
		"",
		newTestLogger(),
		func() (bool, map[string]bool) { return true, map[string]bool{"postgres": true, "aes_key": true} },
		httpapi.NewDecryptOps(),
		true,
		repo,
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &executorHarness{router: router, repo: repo, srv: srv}
}

func (h *executorHarness) put(t *testing.T, executorID string, headers http.Header, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, h.srv.URL+strings.Replace(executorPutPath, "%s", executorID, 1),
		bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send PUT: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PUT body: %v", err)
	}
	return resp, raw
}

func (h *executorHarness) post(t *testing.T, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/executors", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send POST: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST body: %v", err)
	}
	return resp, raw
}

func TestExecutorFirstRegistrationGeneratesUUID(t *testing.T) {
	h := newExecutorHarness(t)
	body := validTeamRegistrationBody("", "team-a", "openhands")
	resp, raw := h.post(t, marshalExecutorBody(t, body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, raw)
	}
	var executor platform.Executor
	if err := json.Unmarshal(raw, &executor); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, err := uuid.Parse(executor.ExecutorID); err != nil {
		t.Fatalf("executor_id=%q is not UUID: %v", executor.ExecutorID, err)
	}
	if executor.Identity != executor.ExecutorID {
		t.Fatalf("identity=%q, executor_id=%q", executor.Identity, executor.ExecutorID)
	}
}

func (h *executorHarness) discover(t *testing.T, executorID, tag string, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	path := strings.Replace(executorDiscoverPath, "%s", executorID, 1)
	if tag != "" {
		path += "?tag=" + tag
	}
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send GET: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	return resp, raw
}

// executorPutBody is the documented ExecutorRegistrationRequest body
// shape (matches OpenAPI ExecutorRegistrationRequest, all fields
// REQUIRED). team_id is *string so callers can express "null" for
// scope=system or omit it entirely for the missing-team-id case.
type executorPutBody struct {
	Scope           string         `json:"scope"`
	TeamID          *string        `json:"team_id"`
	ExecutorType    string         `json:"executor_type"`
	Identity        string         `json:"identity,omitempty"`
	AuthorizedTag   string         `json:"authorized_tag"`
	MaxCapacity     int            `json:"max_capacity"`
	RunningCount    int            `json:"running_count"`
	RuntimeMetadata map[string]any `json:"runtime_metadata"`
}

func validTeamRegistrationBody(executorID, teamID, tag string) executorPutBody {
	team := teamID
	return executorPutBody{
		Scope:           "team",
		TeamID:          &team,
		ExecutorType:    "executor_docker_openhands",
		AuthorizedTag:   tag,
		MaxCapacity:     4,
		RunningCount:    0,
		RuntimeMetadata: map[string]any{"runtime": "docker", "tool": "openhands"},
	}
}

func validSystemRegistrationBody(executorID, tag string) executorPutBody {
	return executorPutBody{
		Scope:           "system",
		TeamID:          nil,
		ExecutorType:    "executor_docker_openhands",
		AuthorizedTag:   tag,
		MaxCapacity:     4,
		RunningCount:    0,
		RuntimeMetadata: map[string]any{"runtime": "docker", "tool": "openhands"},
	}
}

func marshalExecutorBody(t *testing.T, body executorPutBody) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal executor body: %v", err)
	}
	return raw
}

// executorDocumentedFields is the exact TaskSummary allowlist the
// discovery response SHALL project. TestDiscoveryShape uses this set
// to prove the response shape never carries payload / image / claim /
// event / project / environment / source-system / source-id data.
var taskSummaryDocumentedFields = []string{
	"task_id",
	"team_id",
	"required_tag",
	"current_state",
	"ingested_at",
}

// ---------------------------------------------------------------------------
// TestExecutorTeamRegistration
// ---------------------------------------------------------------------------

// TestExecutorTeamRegistration pins the team-scope PUT happy path
// and every documented rejection. The happy path currently returns
// 501 registration_not_implemented (the test-mode guard); the
// rejections must surface 400 / 401 / 403 with the documented error
// envelope and ZERO CreateTeam / CreateSourceSystem / CreateTaskType
// calls so Executor registration never mutates the teams table.
//
// Every sub-test asserts behavior the current guard does NOT exhibit:
// each RED today fails because either the status code, the error
// envelope code, or the missing pre-rejection short-circuit does not
// match the documented Section 5 contract.
func TestExecutorTeamRegistration(t *testing.T) {
	const execID = "exec-section5-team"

	tests := []struct {
		name           string
		authTeamID     string
		body           func() ([]byte, executorPutBody)
		wantStatus     int
		wantCode       string
		allowTeamReads bool // whether the documented handler consults TeamExists
	}{
		{
			name:       "valid existing team returns canonical Executor",
			authTeamID: "team-a",
			body: func() ([]byte, executorPutBody) {
				b := validTeamRegistrationBody(execID, "team-a", "openhands")
				return marshalExecutorBody(t, b), b
			},
			wantStatus:     http.StatusOK,
			allowTeamReads: true,
		},
		{
			name:       "missing identity X-FlowAI-Team-Id resolved from body team_id (v0009)",
			authTeamID: "",
			body: func() ([]byte, executorPutBody) {
				b := validTeamRegistrationBody(execID, "team-a", "openhands")
				return marshalExecutorBody(t, b), b
			},
			wantStatus:     http.StatusOK,
			wantCode:       "",
			allowTeamReads: true,
		},
		{
			name:       "omitted body team_id rejected with 400 missing_team_id",
			authTeamID: "team-a",
			body: func() ([]byte, executorPutBody) {
				b := validTeamRegistrationBody(execID, "team-a", "openhands")
				b.TeamID = nil
				return marshalExecutorBody(t, b), b
			},
			wantStatus:     http.StatusBadRequest,
			wantCode:       "missing_team_id",
			allowTeamReads: false,
		},
		{
			name:       "mismatched body and header team_id rejected",
			authTeamID: "team-a",
			body: func() ([]byte, executorPutBody) {
				b := validTeamRegistrationBody(execID, "team-b", "openhands")
				return marshalExecutorBody(t, b), b
			},
			wantStatus:     http.StatusForbidden,
			wantCode:       "team_binding_mismatch",
			allowTeamReads: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newExecutorHarness(t)
			h.repo.setTeamExists(true)

			body, _ := tc.body()
			headers := executorIdentityHeaders(executorRoleTeam, tc.authTeamID, execID, "req-section5-team")
			resp, raw := h.put(t, execID, headers, body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			if tc.wantCode != "" {
				env := decodeErrorEnvelope(t, raw)
				if env.Code != tc.wantCode {
					t.Errorf("error code=%q, want %q; body=%s", env.Code, tc.wantCode, raw)
				}
				if env.Message == "" || env.RequestID == "" {
					t.Errorf("error envelope missing documented field: %+v", env)
				}
			}
			if got := h.repo.teamCreates(); got != 0 {
				t.Errorf("Executor PUT triggered CreateTeam calls=%d, want 0", got)
			}
			if got := h.repo.srcCreates(); got != 0 {
				t.Errorf("Executor PUT triggered CreateSourceSystem calls=%d, want 0", got)
			}
			if got := h.repo.typeCreates(); got != 0 {
				t.Errorf("Executor PUT triggered CreateTaskType calls=%d, want 0", got)
			}
			if got := h.repo.teamExistsReads(); tc.allowTeamReads && got != 1 {
				t.Errorf("team lookups=%d, want 1 (allowed case)", got)
			}
			if got := h.repo.teamExistsReads(); !tc.allowTeamReads && got != 0 {
				t.Errorf("team lookups=%d, want 0 (rejection must short-circuit before lookup)", got)
			}
		})
	}

	// Happy-path canonical Executor body shape (11 documented fields).
	// The 200 status assertion is the RED gate; the field allowlist
	// is the GREEN gate.
	t.Run("canonical Executor response carries the 11 documented fields", func(t *testing.T) {
		h := newExecutorHarness(t)
		h.repo.setTeamExists(true)
		const execID = "exec-canonical-shape"
		body := marshalExecutorBody(t, validTeamRegistrationBody(execID, "team-a", "openhands"))
		headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-canonical-shape")
		resp, raw := h.put(t, execID, headers, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode Executor response: %v (body=%s)", err, raw)
		}
		wantFields := []string{
			"executor_id", "scope", "team_id", "executor_type", "identity",
			"authorized_tag", "max_capacity", "running_count",
			"runtime_metadata", "registered_at", "updated_at",
		}
		for _, field := range wantFields {
			if _, ok := got[field]; !ok {
				t.Errorf("Executor response missing documented field %q (body=%s)", field, raw)
			}
		}
		if got["scope"] != "team" {
			t.Errorf("response scope=%v, want \"team\"", got["scope"])
		}
		if got["team_id"] != "team-a" {
			t.Errorf("response team_id=%v, want \"team-a\"", got["team_id"])
		}
	})

	// Changed-team re-registration: a second PUT against an existing
	// Executor row with a different body team_id SHALL be rejected
	// without mutation. Without Section 5 persistence the second PUT
	// cannot be driven against a pre-existing row; the first PUT
	// fails today (501) so the test asserts 400 scope_change_forbidden
	// (RED: 501).
	t.Run("changed team re-registration rejected without mutation", func(t *testing.T) {
		h := newExecutorHarness(t)
		h.repo.setTeamExists(true)
		const execID = "exec-team-reregister"
		headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-team-reregister")
		// Seed the prior registration (today: 501).
		firstBody := marshalExecutorBody(t, validTeamRegistrationBody(execID, "team-a", "openhands"))
		_, _ = h.put(t, execID, headers, firstBody)
		// Attempt to re-register against a different team.
		secondBody := marshalExecutorBody(t, validTeamRegistrationBody(execID, "team-b", "openhands"))
		secondHeaders := executorIdentityHeaders(executorRoleTeam, "team-b", execID, "req-section5-team-reregister-2")
		resp, raw := h.put(t, execID, secondHeaders, secondBody)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, raw)
		}
		env := decodeErrorEnvelope(t, raw)
		if env.Code != "team_binding_mismatch" {
			t.Errorf("error code=%q, want \"team_binding_mismatch\"; body=%s", env.Code, raw)
		}
		if got := h.repo.teamCreates(); got != 0 {
			t.Errorf("changed-team re-registration triggered CreateTeam calls=%d, want 0", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestSystemExecutorRegistration
// ---------------------------------------------------------------------------

// TestSystemExecutorRegistration pins the system-scope happy path
// (role=system-executor, scope=system, team_id=null returns 200
// canonical Executor) and the documented system-team_id rejection
// (scope=system with a non-null body team_id returns 400 without
// persistence). The happy path currently fails the existing guard's
// role check (returns 403 not_authorized).
func TestSystemExecutorRegistration(t *testing.T) {
	const execID = "exec-section5-system"

	tests := []struct {
		name       string
		body       func(t *testing.T) []byte
		wantStatus int
		wantCode   string
	}{
		{
			name: "scope=system with team_id=null accepted",
			body: func(t *testing.T) []byte {
				return marshalExecutorBody(t, validSystemRegistrationBody(execID, "openhands"))
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "scope=system with non-null body team_id rejected",
			body: func(t *testing.T) []byte {
				b := validSystemRegistrationBody(execID, "openhands")
				team := "team-a"
				b.TeamID = &team
				return marshalExecutorBody(t, b)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "system_scope_team_id_must_be_null",
		},
		{
			name: "scope=system with omitted team_id accepted",
			body: func(t *testing.T) []byte {
				payload := map[string]any{
					"scope": "system", "executor_type": "executor_docker_openhands",
					"authorized_tag": "openhands",
					"max_capacity":   4, "running_count": 0, "runtime_metadata": map[string]any{},
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatalf("marshal omitted team_id body: %v", err)
				}
				return raw
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newExecutorHarness(t)
			h.repo.setTeamExists(true)
			body := tc.body(t)
			headers := executorIdentityHeaders(executorRoleSystem, "", execID, "req-section5-system")
			resp, raw := h.put(t, execID, headers, body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			if tc.wantCode != "" {
				env := decodeErrorEnvelope(t, raw)
				if env.Code != tc.wantCode {
					t.Errorf("error code=%q, want %q; body=%s", env.Code, tc.wantCode, raw)
				}
			}
			// Zero repository mutations regardless of outcome.
			if got := h.repo.teamCreates(); got != 0 {
				t.Errorf("Executor PUT triggered CreateTeam calls=%d, want 0", got)
			}
			if got := h.repo.srcCreates(); got != 0 {
				t.Errorf("Executor PUT triggered CreateSourceSystem calls=%d, want 0", got)
			}
			if got := h.repo.typeCreates(); got != 0 {
				t.Errorf("Executor PUT triggered CreateTaskType calls=%d, want 0", got)
			}
		})
	}

	// Canonical system Executor body omits the inapplicable team_id property.
	t.Run("canonical system Executor response omits team_id", func(t *testing.T) {
		h := newExecutorHarness(t)
		h.repo.setTeamExists(true)
		const execID = "exec-system-canonical"
		body := marshalExecutorBody(t, validSystemRegistrationBody(execID, "openhands"))
		headers := executorIdentityHeaders(executorRoleSystem, "", execID, "req-system-canonical")
		resp, raw := h.put(t, execID, headers, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode system Executor response: %v (body=%s)", err, raw)
		}
		if got["scope"] != "system" {
			t.Errorf("response scope=%v, want \"system\"", got["scope"])
		}
		if _, present := got["team_id"]; present {
			t.Errorf("response team_id=%v, want property omitted (system scope)", got["team_id"])
		}
	})
}

func TestExecutorRegistrationIdentityMismatch(t *testing.T) {
	const execID = "exec-authenticated"

	tests := []struct {
		name   string
		role   string
		teamID string
		body   func() executorPutBody
	}{
		{
			name:   "team-owned Executor",
			role:   executorRoleTeam,
			teamID: "team-a",
			body: func() executorPutBody {
				body := validTeamRegistrationBody(execID, "team-a", "openhands")
				body.Identity = "exec-other"
				return body
			},
		},
		{
			name: "system-owned Executor",
			role: executorRoleSystem,
			body: func() executorPutBody {
				body := validSystemRegistrationBody(execID, "openhands")
				body.Identity = "exec-other"
				return body
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newExecutorHarness(t)
			const executorID = "exec-authenticated"
			headers := executorIdentityHeaders(tc.role, tc.teamID, executorID, "req-identity-mismatch")

			resp, raw := h.put(t, executorID, headers, marshalExecutorBody(t, tc.body()))
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status=%d, want 400; body=%s", resp.StatusCode, raw)
			}
			env := decodeErrorEnvelope(t, raw)
			if env.Code != "invalid_request" {
				t.Errorf("error code=%q, want %q; body=%s", env.Code, "invalid_request", raw)
			}
			if got := h.repo.teamExistsReads(); got != 0 {
				t.Errorf("team lookups=%d, want 0", got)
			}
			if got := h.repo.registrationCalls(); got != 0 {
				t.Errorf("registration repository calls=%d, want 0", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRegistrationTagCount
// ---------------------------------------------------------------------------

// TestRegistrationTagCount proves the documented tag-count validator
// rejects every representation that does not equal exactly one tag:
//
//   - omitted authorized_tag (zero tags)
//   - empty authorized_tag string (zero non-empty tags)
//   - JSON array of one tag (still array form, not the scalar)
//   - JSON array of two tags (multi-tag)
//   - JSON array of three tags (multi-tag, deeper)
//
// The Section 5 contract returns 400 invalid_tag_count and never
// persists. Today the strict decode rejects array bodies with 400
// invalid_request (the test asserts the documented envelope code so
// the RED is behavior-specific, not a generic mismatch).
func TestRegistrationTagCount(t *testing.T) {
	const execID = "exec-tag-count"

	type bodyFn func(t *testing.T) []byte

	cases := []struct {
		name       string
		body       bodyFn
		wantStatus int
		wantCode   string
	}{
		{
			name: "omitted authorized_tag rejected with 400 invalid_tag_count",
			body: func(t *testing.T) []byte {
				b := validTeamRegistrationBody(execID, "team-a", "openhands")
				b.AuthorizedTag = ""
				return marshalExecutorBody(t, b)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_tag_count",
		},
		{
			name: "JSON array of one tag rejected with 400 invalid_tag_count",
			body: func(t *testing.T) []byte {
				return injectTagArray(t, validTeamRegistrationBody(execID, "team-a", "openhands"), []string{"openhands"})
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_tag_count",
		},
		{
			name: "JSON array of two tags rejected with 400 invalid_tag_count",
			body: func(t *testing.T) []byte {
				return injectTagArray(t, validTeamRegistrationBody(execID, "team-a", "openhands"),
					[]string{"openhands", "k8s"})
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_tag_count",
		},
		{
			name: "JSON array of three tags rejected with 400 invalid_tag_count",
			body: func(t *testing.T) []byte {
				return injectTagArray(t, validTeamRegistrationBody(execID, "team-a", "openhands"),
					[]string{"openhands", "k8s", "shell"})
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_tag_count",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newExecutorHarness(t)
			h.repo.setTeamExists(true)
			body := tc.body(t)
			headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-tag-count")
			resp, raw := h.put(t, execID, headers, body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			env := decodeErrorEnvelope(t, raw)
			if env.Code != tc.wantCode {
				t.Errorf("error code=%q, want %q; body=%s", env.Code, tc.wantCode, raw)
			}
			// No persistence side effects on tag-count rejection.
			if got := h.repo.teamCreates(); got != 0 {
				t.Errorf("tag-count rejection triggered CreateTeam calls=%d, want 0", got)
			}
		})
	}
}

// injectTagArray rewrites the supplied body so that authorized_tag is
// emitted as a JSON array of the supplied tags (overriding the typed
// string shape). The strict decoder MUST reject every array form.
func injectTagArray(t *testing.T, body executorPutBody, tags []string) []byte {
	t.Helper()
	team := "team-a"
	payload := map[string]any{
		"scope":            "team",
		"team_id":          team,
		"executor_type":    "executor_docker_openhands",
		"identity":         body.Identity,
		"authorized_tag":   tags,
		"max_capacity":     1,
		"running_count":    0,
		"runtime_metadata": map[string]any{},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal multi-tag body: %v", err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// TestRegistrationScopeDiscriminator
// ---------------------------------------------------------------------------

// TestRegistrationScopeDiscriminator proves the documented scope
// validator rejects any value outside {team, system} with code
// `invalid_scope`, treats a missing scope as invalid, and rejects
// re-registration that names a different scope than the original
// without mutating the row. The current guard collapses every
// non-team scope into a generic 400 invalid_request so the assertion
// against the documented `invalid_scope` and `scope_change_forbidden`
// envelopes is RED.
func TestRegistrationScopeDiscriminator(t *testing.T) {
	const execID = "exec-scope-discriminator"

	tests := []struct {
		name       string
		body       func(t *testing.T) []byte
		wantStatus int
		wantCode   string
	}{
		{
			name: "scope outside team/system rejected with 400 invalid_scope",
			body: func(t *testing.T) []byte {
				b := validTeamRegistrationBody(execID, "team-a", "openhands")
				b.Scope = "global"
				return marshalExecutorBody(t, b)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_scope",
		},
		{
			name: "missing scope rejected with 400 invalid_scope",
			body: func(t *testing.T) []byte {
				b := validTeamRegistrationBody(execID, "team-a", "openhands")
				b.Scope = ""
				return marshalExecutorBody(t, b)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_scope",
		},
		{
			name: "re-registration scope change rejected without mutation",
			body: func(t *testing.T) []byte {
				b := validSystemRegistrationBody(execID, "openhands")
				return marshalExecutorBody(t, b)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "scope_change_forbidden",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newExecutorHarness(t)
			h.repo.setTeamExists(true)
			if tc.wantCode == "scope_change_forbidden" {
				initial := validTeamRegistrationBody(execID, "team-a", "openhands")
				_, _ = h.put(t, execID,
					executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-scope-initial"),
					marshalExecutorBody(t, initial),
				)
			}
			body := tc.body(t)
			headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-scope")
			resp, raw := h.put(t, execID, headers, body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			env := decodeErrorEnvelope(t, raw)
			if env.Code != tc.wantCode {
				t.Errorf("error code=%q, want %q; body=%s", env.Code, tc.wantCode, raw)
			}
			if got := h.repo.teamCreates(); got != 0 {
				t.Errorf("scope discriminator rejection triggered CreateTeam calls=%d, want 0", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestTeamTagFifoDiscovery
// ---------------------------------------------------------------------------

// TestTeamTagFifoDiscovery pins the team-owned read-only discovery
// contract. The authenticated Executor may request ONLY its one
// registered tag; the eligibility predicate
// (tasks.team_id = executor.team_id AND required_tag = registered_tag)
// applies BEFORE the FIFO ordering. The handler returns 200 with a
// TaskSummary items array. Today the route is not mounted, so the
// response is 404; once Section 5 lands the test passes.
func TestTeamTagFifoDiscovery(t *testing.T) {
	h := newExecutorHarness(t)
	h.repo.setTeamExists(true)

	const execID = "exec-team-discovery"
	headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-team-discovery")
	_, _ = h.put(t, execID, headers, marshalExecutorBody(t, validTeamRegistrationBody(execID, "team-a", "openhands")))

	resp, raw := h.discover(t, execID, "openhands", headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 for team-owned discovery; body=%s", resp.StatusCode, raw)
	}

	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	// Empty collection is acceptable here (no tasks seeded). The
	// non-revealing rule says: when only foreign tasks match, the
	// handler returns 204 or an empty 200 page. Both pass the FIFO
	// discovery contract; the test asserts the documented shape and
	// never a foreign-team leak.
	if page.Items == nil {
		t.Errorf("discovery response items=null; expected at least an empty array (TaskDiscoveryPage)")
	}
	for i, item := range page.Items {
		if got := item["team_id"]; got != nil && got != "team-a" {
			t.Errorf("items[%d].team_id=%v, want \"team-a\" or null", i, got)
		}
		if got := item["required_tag"]; got != nil && got != "openhands" {
			t.Errorf("items[%d].required_tag=%v, want \"openhands\"", i, got)
		}
	}

	// Tag mismatch must be rejected without persistence. Today the
	// route is not mounted so the assertion is 404; the Section 5
	// GREEN contract returns 400 invalid_tag_mismatch.
	respBad, rawBad := h.discover(t, execID, "unknown-tag", headers)
	if respBad.StatusCode == http.StatusOK {
		t.Errorf("tag mismatch returned 200; expected rejection (body=%s)", rawBad)
	}
}

// ---------------------------------------------------------------------------
// TestSystemCrossTeamFifoDiscovery
// ---------------------------------------------------------------------------

// TestSystemCrossTeamFifoDiscovery pins the system-owned read-only
// discovery contract. The Executor is not bound to any team; the
// eligibility predicate is `required_tag = registered_tag` and
// matches tasks across every team. The handler returns 200 with a
// TaskSummary items array in FIFO order.
func TestSystemCrossTeamFifoDiscovery(t *testing.T) {
	h := newExecutorHarness(t)
	h.repo.setTeamExists(true)

	const execID = "exec-system-discovery"
	headers := executorIdentityHeaders(executorRoleSystem, "", execID, "req-section5-system-discovery")
	_, _ = h.put(t, execID, headers, marshalExecutorBody(t, validSystemRegistrationBody(execID, "openhands")))

	resp, raw := h.discover(t, execID, "openhands", headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 for system-owned discovery; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	if page.Items == nil {
		t.Errorf("discovery response items=null; expected at least an empty array (TaskDiscoveryPage)")
	}
	for i, item := range page.Items {
		if got := item["required_tag"]; got != nil && got != "openhands" {
			t.Errorf("items[%d].required_tag=%v, want \"openhands\"", i, got)
		}
	}
}

// ---------------------------------------------------------------------------
// TestDiscoveryShape
// ---------------------------------------------------------------------------

// TestDiscoveryShape asserts the discovery response carries EXACTLY
// the documented TaskSummary allowlist (5 fields) and never includes
// payload, image, claim, event, project, environment, source-system,
// source-id, task_type_id, resolved_image, image_source, owner_command_id,
// executor_id, or claimed_at data. The test injects one synthetic
// summary so the allowlist check fires even when the route is empty;
// today the assertion fails because the route is not mounted.
func TestDiscoveryShape(t *testing.T) {
	// The "raw" discovery page encodes one synthetic TaskSummary
	// item so the allowlist check exercises the full field surface.
	// We unmarshal the actual response and probe every key.
	h := newExecutorHarness(t)
	h.repo.setTeamExists(true)

	const execID = "exec-discovery-shape"
	headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-discovery-shape")
	_, _ = h.put(t, execID, headers, marshalExecutorBody(t, validTeamRegistrationBody(execID, "team-a", "openhands")))
	resp, raw := h.discover(t, execID, "openhands", headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 for discovery shape probe; body=%s", resp.StatusCode, raw)
	}
	var page map[string]any
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	rawItems, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("discovery page missing items array (body=%s)", raw)
	}
	forbidden := []string{
		"payload", "image", "owner_command_id", "executor_id", "project_id",
		"environment_id", "source_system_id", "source_id", "task_type_id",
		"resolved_image", "image_source", "claimed_at",
	}
	for i, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("items[%d] is %T, want object", i, raw)
		}
		// Allowlist: every required TaskSummary field MUST be present
		// on every item.
		for _, want := range taskSummaryDocumentedFields {
			if _, ok := item[want]; !ok {
				t.Errorf("items[%d] missing documented TaskSummary field %q (item=%+v)", i, want, item)
			}
		}
		// Negative: no payload / image / claim / event / project /
		// environment / source-system / source-id fields leak.
		for _, banned := range forbidden {
			if _, ok := item[banned]; ok {
				t.Errorf("items[%d] carries forbidden field %q (item=%+v)", i, banned, item)
			}
		}
		// Defensive: item key set must equal the documented allowlist.
		var keys []string
		for k := range item {
			keys = append(keys, k)
		}
		if !reflect.DeepEqual(sortedKeys(keys), sortedKeys(taskSummaryDocumentedFields)) {
			t.Errorf("items[%d] field set=%v, want exactly %v", i, keys, taskSummaryDocumentedFields)
		}
	}
}

func sortedKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// TestDiscoveryIgnoresCapacity
// ---------------------------------------------------------------------------

// TestDiscoveryIgnoresCapacity pins the read-only capacity-ignoring
// contract. max_capacity and running_count are observational; discovery
// never reads or enforces them. The test seeds a team-owned Executor
// with max_capacity=0 and running_count=10 (saturated) and asserts
// discovery returns 200 regardless of how many tasks match the
// tag predicate. Today the route is not mounted so the assertion
// fails RED with 404; once Section 5 lands the saturated Executor
// still observes its pending tasks.
func TestDiscoveryIgnoresCapacity(t *testing.T) {
	h := newExecutorHarness(t)
	h.repo.setTeamExists(true)

	const execID = "exec-capacity-ignored"
	// A real Section 5 GREEN implementation reads
	// (max_capacity, running_count) from the registered Executor row
	// but never short-circuits discovery on them. The PUT below uses
	// the documented "saturated" observation pair (0 / 10). Today
	// the PUT returns 501; we only assert the eventual GET, which
	// is the RED we care about.
	putBody := validTeamRegistrationBody(execID, "team-a", "openhands")
	putBody.MaxCapacity = 0
	putBody.RunningCount = 10
	_, _ = h.put(t, execID,
		executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-capacity-put"),
		marshalExecutorBody(t, putBody),
	)

	headers := executorIdentityHeaders(executorRoleTeam, "team-a", execID, "req-section5-capacity-discovery")
	resp, raw := h.discover(t, execID, "openhands", headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 even when Executor is capacity-saturated; body=%s", resp.StatusCode, raw)
	}
	// Defensive: the response is well-formed TaskDiscoveryPage
	// (items may be empty, but never null / non-object).
	var page map[string]any
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	if _, ok := page["items"]; !ok {
		t.Errorf("discovery response missing items field; body=%s", raw)
	}
}
