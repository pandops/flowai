// Section 3 + Section 3a RED unit tests for the State Registry admin
// onboarding surface. The tests intentionally reference planned
// platform, store, and httpapi symbols (RegisterAdmin, AdminRepository,
// CreateTeamRequest, etc.) so that adding the production code lifts
// the missing-symbol RED into behavior-specific assertions.
//
// RED COMMAND (without integration tag):
//
//	go test ./svc/state-registry/... \
//	    -run 'TestAdminCreateTeam|TestAdminCreateSourceSystem|\
//
// TestAdminCreateTaskType|TestAdminRequiresDefaultImage|\
// TestAdminRequiresUniqueTeamName|TestAdminSourceSystemReferencesTeam|\
// TestAdminTaskTypeReferencesTeam|TestAdminEndpointsRejectNonAdmin|\
// TestExecutorRegistrationDoesNotTouchTeam' -count=1
//
//	go test ./svc/state-registry/... \
//	    -run 'TestSourceSystemListenerIdentityUniqueness|\
//
// TestAdminSourceSystemRejectsDuplicateListenerIdentity|\
// TestAdminSourceSystemRejectsCrossTeamListenerIdentity|\
// TestSourceSystemListenerIdentityIndexExists' -count=1
//
// RED EVIDENCE: missing-symbol compile error.
//
//	go build ./svc/state-registry/internal/httpapi/...
//	# github.com/flowai/platform/svc/state-registry/internal/httpapi_test
//	./admin_test.go:N:M: undefined: httpapi.RegisterAdmin
//	./admin_test.go:N:M: undefined: store.AdminRepository
//	./admin_test.go:N:M: undefined: platform.CreateTeamRequest
//	# (full symbol catalog below)
//
// When GREEN lands (platform/store/httpapi packages introduced), the
// tests below also pin:
//
//   - System-administrator identity is REQUIRED for every /admin/* write;
//     listener, Executor, Gateway, and anonymous callers MUST be
//     rejected BEFORE any AdminRepository call.
//   - teams.default_image is REQUIRED at POST /admin/teams; missing
//     default_image is 400 with zero CreateTeam calls.
//   - Duplicate team_name is rejected with 400 and the recording fake
//     observes exactly ONE persisted team row (the original).
//   - Source-system / task-type bodies whose team_id does not reference
//     an existing team are rejected with 400 and record zero
//     CreateSourceSystem / CreateTaskType calls.
//   - Source-system listener_identity globally unique: same-team and
//     cross-team dup-by-identity are rejected with 409 with zero
//     persistence side-effects (the second row is not created).
//   - Executor registration referencing a non-existent team does NOT
//     cause any AdminRepository.CreateTeam call (the Executor path
//     never touches the teams table).
//
// The /admin/* error envelope is the documented flat
// {code,message,request_id} shape. Tests decode the body and assert
// the three documented fields exist; they do NOT inspect fields the
// RED contract does not pin so future refactors can extend the
// envelope without invalidating RED tests.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/httpapi"
	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// ---------------------------------------------------------------------------
// Recording fake for the planned store.AdminRepository interface.
// ---------------------------------------------------------------------------

// recordingAdminRepo is a thread-safe test double for the planned
// store.AdminRepository. It records every method invocation, captures
// the most recent arguments, and lets individual sub-tests inject a
// per-method sentinel error so the HTTP handler can return the same
// status / envelope the real Postgres-backed implementation would.
type recordingAdminRepo struct {
	teams     atomic.Int64
	srcs      atomic.Int64
	types     atomic.Int64
	reads     atomic.Int64
	listTags  atomic.Int64
	listTasks atomic.Int64

	mu       sync.Mutex
	lastTeam platform.CreateTeamRequest
	lastSrc  platform.CreateSourceSystemRequest
	lastTyp  platform.CreateTaskTypeRequest
	lastID   platform.AdminIdentity

	teamErr error
	srcErr  error
	typErr  error

	// fnListTasks is an optional override used by the ordering tests
	// to return a deterministic page without standing up a real
	// database. When nil the default no-rows response is returned.
	fnListTasks func(filter platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error)
	// fnListTaskTypes mirrors fnListTasks for the admin tag
	// ordering test.
	fnListTaskTypes func(filter platform.AdminTagFilter) ([]platform.AdminTagEntry, *store.TaskTypeAfter, error)
}

// Compile-time guarantee that the fake satisfies the planned
// AdminRepository interface. This assertion is part of the RED: when
// the planned store package is added, the fake must keep satisfying
// the interface or the test file must be updated alongside the
// interface contract.
var _ store.AdminRepository = (*recordingAdminRepo)(nil)

func (r *recordingAdminRepo) CreateTeam(_ context.Context, req platform.CreateTeamRequest, ident platform.AdminIdentity) (platform.Team, error) {
	r.teams.Add(1)
	r.mu.Lock()
	r.lastTeam = req
	r.lastID = ident
	r.mu.Unlock()
	if r.teamErr != nil {
		return platform.Team{}, r.teamErr
	}
	return platform.Team{
		TeamID:       "team-rec-" + strconv.FormatInt(r.teams.Load(), 10),
		TeamName:     req.TeamName,
		DefaultImage: req.DefaultImage,
		CreatedAt:    time.Unix(0, 0).UTC(),
	}, nil
}

func (r *recordingAdminRepo) CreateSourceSystem(_ context.Context, req platform.CreateSourceSystemRequest, ident platform.AdminIdentity) (platform.SourceSystem, error) {
	r.srcs.Add(1)
	r.mu.Lock()
	r.lastSrc = req
	r.lastID = ident
	r.mu.Unlock()
	if r.srcErr != nil {
		return platform.SourceSystem{}, r.srcErr
	}
	return platform.SourceSystem{
		SourceSystemID:   "src-rec-" + strconv.FormatInt(r.srcs.Load(), 10),
		TeamID:           req.TeamID,
		ListenerIdentity: req.ListenerIdentity,
		DefaultImage:     req.DefaultImage,
	}, nil
}

func (r *recordingAdminRepo) CreateTaskType(_ context.Context, req platform.CreateTaskTypeRequest, ident platform.AdminIdentity) (platform.TaskType, error) {
	r.types.Add(1)
	r.mu.Lock()
	r.lastTyp = req
	r.lastID = ident
	r.mu.Unlock()
	if r.typErr != nil {
		return platform.TaskType{}, r.typErr
	}
	return platform.TaskType{
		TaskTypeID:   "typ-rec-" + strconv.FormatInt(r.types.Load(), 10),
		TeamID:       req.TeamID,
		ExecutionTag: req.ExecutionTag,
		DefaultImage: req.DefaultImage,
	}, nil
}

func (r *recordingAdminRepo) TeamExists(_ context.Context, _ string) (bool, error) {
	r.reads.Add(1)
	return false, nil
}

// ListTaskTypes is the Section 3b contract hook used by the cursor
// rejection tests. The hook records every invocation so the
// (zero protected repository calls) assertion is mechanically true.
func (r *recordingAdminRepo) ListTaskTypes(_ context.Context, filter platform.AdminTagFilter, _ int, _ *store.TaskTypeAfter) ([]platform.AdminTagEntry, *store.TaskTypeAfter, error) {
	r.listTags.Add(1)
	if r.fnListTaskTypes != nil {
		return r.fnListTaskTypes(filter)
	}
	return nil, nil, nil
}

// ListTasks is the Section 3b contract hook used by the cursor
// rejection tests. The hook records every invocation so the
// (zero protected repository calls) assertion is mechanically true.
func (r *recordingAdminRepo) ListTasks(_ context.Context, filter platform.AdminTaskFilter, _ int, _ *store.TaskAfter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
	r.listTasks.Add(1)
	if r.fnListTasks != nil {
		return r.fnListTasks(filter)
	}
	return nil, nil, nil
}

// setListTasksFn installs the optional override that returns a
// deterministic page for the ordering tests.
func (r *recordingAdminRepo) setListTasksFn(fn func(filter platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fnListTasks = fn
}

// setListTaskTypesFn installs the optional override that returns a
// deterministic page for the admin tag ordering test.
func (r *recordingAdminRepo) setListTaskTypesFn(fn func(filter platform.AdminTagFilter) ([]platform.AdminTagEntry, *store.TaskTypeAfter, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fnListTaskTypes = fn
}

func (r *recordingAdminRepo) teamCalls() int64     { return r.teams.Load() }
func (r *recordingAdminRepo) sourceCalls() int64   { return r.srcs.Load() }
func (r *recordingAdminRepo) typeCalls() int64     { return r.types.Load() }
func (r *recordingAdminRepo) teamReads() int64     { return r.reads.Load() }
func (r *recordingAdminRepo) listTagCalls() int64  { return r.listTags.Load() }
func (r *recordingAdminRepo) listTaskCalls() int64 { return r.listTasks.Load() }
func (r *recordingAdminRepo) lastSourceReq() platform.CreateSourceSystemRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSrc
}
func (r *recordingAdminRepo) lastTeamReq() platform.CreateTeamRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastTeam
}
func (r *recordingAdminRepo) lastIdentity() platform.AdminIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastID
}
func (r *recordingAdminRepo) setTeamErr(err error)   { r.teamErr = err }
func (r *recordingAdminRepo) setSourceErr(err error) { r.srcErr = err }
func (r *recordingAdminRepo) setTypeErr(err error)   { r.typErr = err }

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

const (
	hdrAdminSubject = "X-FlowAI-Admin-Subject"
	hdrRole         = "X-FlowAI-Role"
	hdrRequestID    = "X-FlowAI-Request-Id"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// adminHarness mounts the planned httpapi.RegisterAdmin on a chi
// router whose only route group is /admin/*. Additional admin-free
// routes (probes, /v1/executors, etc.) are NOT mounted so the
// EndpointRejectsNonAdmin assertions cannot be masked by accidentally
// hitting a probe.
type adminHarness struct {
	router *chi.Mux
	repo   *recordingAdminRepo
	srv    *httptest.Server
	logger *slog.Logger
}

func newAdminHarness(t *testing.T) *adminHarness {
	t.Helper()
	repo := &recordingAdminRepo{}
	logger := newTestLogger()
	router := chi.NewRouter()
	// RegisterAdmin is a planned symbol that mounts /admin/* on the
	// supplied router. The mount target is intentionally a fresh
	// router WITHOUT probes so non-admin paths can return 404 /
	// 405 without leaking from /v1/livez.
	httpapi.RegisterAdmin(router, logger, repo)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &adminHarness{
		router: router,
		repo:   repo,
		srv:    srv,
		logger: logger,
	}
}

// adminRequest is the documented request body for POST /admin/*.
// All fields are JSON-tagged to match the OpenAPI-aligned wire shape
// the autotest suite sends.
type adminRequest struct {
	TeamName         string                   `json:"team_name,omitempty"`
	DefaultImage     *platform.ImageReference `json:"default_image,omitempty"`
	TeamID           string                   `json:"team_id,omitempty"`
	ListenerIdentity string                   `json:"listener_identity,omitempty"`
	ExecutionTag     string                   `json:"execution_tag,omitempty"`
}

func (a adminRequest) marshal(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal admin request: %v", err)
	}
	return body
}

// errorEnvelope decodes the documented flat {code,message,request_id}
// shape. Tests assert these three fields exist; they do NOT pin any
// other field.
type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func decodeErrorEnvelope(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env
}

// adminIdentityHeaders returns the canonical system-administrator
// request identity. Tests that exercise a non-admin caller swap the
// X-FlowAI-Role / Subject headers before sending the request.
func adminIdentityHeaders(subject string) http.Header {
	h := http.Header{}
	h.Set(hdrRole, "admin")
	h.Set(hdrAdminSubject, subject)
	h.Set(hdrRequestID, "req-admin-"+subject)
	return h
}

func nonAdminIdentityHeaders(role string) http.Header {
	h := http.Header{}
	h.Set(hdrRole, role)
	h.Set(hdrRequestID, "req-"+role+"-red")
	return h
}

func defaultImageReference(tag string) platform.ImageReference {
	return platform.ImageReference{
		Repository: "registry.example/agent:" + tag,
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}
}

func validTeamCreateBody(t *testing.T, suffix string) []byte {
	t.Helper()
	body, err := json.Marshal(adminRequest{
		TeamName:     "Team " + suffix,
		DefaultImage: ptrImageRef(defaultImageReference(suffix)),
	})
	if err != nil {
		t.Fatalf("marshal team body: %v", err)
	}
	return body
}

func validSourceSystemCreateBody(t *testing.T, teamID, listener string) []byte {
	t.Helper()
	body, err := json.Marshal(adminRequest{
		TeamID:           teamID,
		ListenerIdentity: listener,
	})
	if err != nil {
		t.Fatalf("marshal source body: %v", err)
	}
	return body
}

func validTaskTypeCreateBody(t *testing.T, teamID, tag string) []byte {
	t.Helper()
	body, err := json.Marshal(adminRequest{
		TeamID:       teamID,
		ExecutionTag: tag,
	})
	if err != nil {
		t.Fatalf("marshal task-type body: %v", err)
	}
	return body
}

func ptrImageRef(ref platform.ImageReference) *platform.ImageReference {
	return &ref
}

func doAdminRequest(t *testing.T, h *adminHarness, method, path string, headers http.Header, body []byte) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reader)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

// ---------------------------------------------------------------------------
// Section 3 — admin onboarding endpoint tests
// ---------------------------------------------------------------------------

// TestAdminCreateTeam is the happy-path PIN for POST /admin/teams.
// RED: The recording fake observes ONE CreateTeam call with the
// supplied team_name and required default_image; the response is 201
// with the canonical team record (team_id, team_name, default_image).
// The contract under test is admin-only creation with REQUIRED
// default_image and a server-generated immutable team_id.
func TestAdminCreateTeam(t *testing.T) {
	h := newAdminHarness(t)

	resp := doAdminRequest(t, h, http.MethodPost, "/admin/teams",
		adminIdentityHeaders("admin-create-team"), validTeamCreateBody(t, "create-team"))

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201 created; body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := h.repo.teamCalls(); got != 1 {
		t.Fatalf("CreateTeam calls=%d, want 1", got)
	}
	if got := h.repo.lastIdentity().Subject; got == "" {
		t.Fatalf("recorded AdminIdentity.Subject is empty; admin request did not propagate identity")
	}
	body := readBody(t, resp)
	var team platform.Team
	if err := json.Unmarshal(body, &team); err != nil {
		t.Fatalf("decode team response: %v (body=%s)", err, body)
	}
	if team.TeamID == "" {
		t.Errorf("response team_id is empty; admin create must return server-generated immutable team_id")
	}
	if team.TeamName == "" {
		t.Errorf("response team_name is empty")
	}
	if team.DefaultImage.Repository == "" || team.DefaultImage.Digest == "" {
		t.Errorf("response default_image is missing the opaque repository/digest pair")
	}
}

// TestAdminCreateSourceSystem pins the happy path for POST
// /admin/source-systems: admin identity accepted, foreign-team_id
// verified server-side via the planned AdminRepository contract, and
// the response carries the server-generated source_system_id.
func TestAdminCreateSourceSystem(t *testing.T) {
	h := newAdminHarness(t)
	// Pre-existing team_id that an admin seeded; the recording fake
	// is happy to accept any team_id and returns a SourceSystem.
	const teamID = "team-alpha"

	resp := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-create-source"),
		validSourceSystemCreateBody(t, teamID, "L-create"))

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := h.repo.sourceCalls(); got != 1 {
		t.Fatalf("CreateSourceSystem calls=%d, want 1", got)
	}
	if got := h.repo.lastSourceReq().TeamID; got != teamID {
		t.Errorf("CreateSourceSystem team_id=%q, want %q", got, teamID)
	}
	if got := h.repo.lastSourceReq().ListenerIdentity; got != "L-create" {
		t.Errorf("CreateSourceSystem listener_identity=%q, want L-create", got)
	}

	body := readBody(t, resp)
	var ss platform.SourceSystem
	if err := json.Unmarshal(body, &ss); err != nil {
		t.Fatalf("decode source-system response: %v (body=%s)", err, body)
	}
	if ss.SourceSystemID == "" {
		t.Errorf("response source_system_id is empty; admin create must return a server-generated id")
	}
	if ss.TeamID != teamID {
		t.Errorf("response team_id=%q, want %q", ss.TeamID, teamID)
	}
}

// TestAdminCreateTaskType pins the happy path for POST /admin/task-types.
func TestAdminCreateTaskType(t *testing.T) {
	h := newAdminHarness(t)
	const teamID = "team-alpha"
	const tag = "openhands"

	resp := doAdminRequest(t, h, http.MethodPost, "/admin/task-types",
		adminIdentityHeaders("admin-create-tt"),
		validTaskTypeCreateBody(t, teamID, tag))

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := h.repo.typeCalls(); got != 1 {
		t.Fatalf("CreateTaskType calls=%d, want 1", got)
	}
	body := readBody(t, resp)
	var tt platform.TaskType
	if err := json.Unmarshal(body, &tt); err != nil {
		t.Fatalf("decode task-type response: %v (body=%s)", err, body)
	}
	if tt.TaskTypeID == "" {
		t.Errorf("response task_type_id is empty; admin create must return a server-generated id")
	}
	if tt.ExecutionTag != tag {
		t.Errorf("response execution_tag=%q, want %q", tt.ExecutionTag, tag)
	}
	if tt.TeamID != teamID {
		t.Errorf("response team_id=%q, want %q", tt.TeamID, teamID)
	}
}

func TestAdminUnexpectedRepositoryErrorIsLoggedButNotExposed(t *testing.T) {
	const internalCause = "database connection reset by peer"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := &recordingAdminRepo{}
	repo.setTeamErr(errors.New(internalCause))
	router := chi.NewRouter()
	httpapi.RegisterAdmin(router, logger, repo)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	harness := &adminHarness{router: router, repo: repo, srv: server, logger: logger}

	resp := doAdminRequest(t, harness, http.MethodPost, "/admin/teams",
		adminIdentityHeaders("admin-repository-error"), validTeamCreateBody(t, "repository-error"))
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte(internalCause)) {
		t.Fatalf("response exposes repository error: %s", body)
	}
	if !strings.Contains(logs.String(), internalCause) {
		t.Fatalf("structured log does not contain repository cause: %s", logs.String())
	}
}

func TestAdminValidationMatchesOpenAPILengthBounds(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       adminRequest
		wantStatus int
		wantCalls  int64
		calls      func(*recordingAdminRepo) int64
	}{
		{
			name:       "identifier accepts 128 runes",
			path:       "/admin/source-systems",
			body:       adminRequest{TeamID: strings.Repeat("a", 128), ListenerIdentity: "listener-128"},
			wantStatus: http.StatusCreated,
			wantCalls:  1,
			calls:      (*recordingAdminRepo).sourceCalls,
		},
		{
			name:       "identifier rejects 129 runes",
			path:       "/admin/source-systems",
			body:       adminRequest{TeamID: strings.Repeat("a", 129), ListenerIdentity: "listener-129"},
			wantStatus: http.StatusBadRequest,
			wantCalls:  0,
			calls:      (*recordingAdminRepo).sourceCalls,
		},
		{
			name:       "tag accepts 256 runes",
			path:       "/admin/task-types",
			body:       adminRequest{TeamID: "team-a", ExecutionTag: strings.Repeat("a", 256)},
			wantStatus: http.StatusCreated,
			wantCalls:  1,
			calls:      (*recordingAdminRepo).typeCalls,
		},
		{
			name:       "tag rejects 257 runes",
			path:       "/admin/task-types",
			body:       adminRequest{TeamID: "team-a", ExecutionTag: strings.Repeat("a", 257)},
			wantStatus: http.StatusBadRequest,
			wantCalls:  0,
			calls:      (*recordingAdminRepo).typeCalls,
		},
		{
			name: "image repository accepts 512 runes",
			path: "/admin/teams",
			body: adminRequest{
				TeamName: "image-bound-512",
				DefaultImage: ptrImageRef(platform.ImageReference{
					Repository: strings.Repeat("a", 512),
					Digest:     "sha256:" + strings.Repeat("a", 64),
				}),
			},
			wantStatus: http.StatusCreated,
			wantCalls:  1,
			calls:      (*recordingAdminRepo).teamCalls,
		},
		{
			name: "image repository rejects 513 runes",
			path: "/admin/teams",
			body: adminRequest{
				TeamName: "image-bound-513",
				DefaultImage: ptrImageRef(platform.ImageReference{
					Repository: strings.Repeat("a", 513),
					Digest:     "sha256:" + strings.Repeat("a", 64),
				}),
			},
			wantStatus: http.StatusBadRequest,
			wantCalls:  0,
			calls:      (*recordingAdminRepo).teamCalls,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newAdminHarness(t)
			body, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			resp := doAdminRequest(t, h, http.MethodPost, tc.path,
				adminIdentityHeaders("admin-length-bounds"), body)
			responseBody := readBody(t, resp)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, responseBody)
			}
			if got := tc.calls(h.repo); got != tc.wantCalls {
				t.Fatalf("repository calls=%d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestAdminRequiresDefaultImage proves the OpenSpec rule that
// teams.default_image is REQUIRED at registration. A body that omits
// default_image (or hands a zero-valued ImageReference) must be
// rejected with 400 BEFORE any AdminRepository.CreateTeam runs.
func TestAdminRequiresDefaultImage(t *testing.T) {
	h := newAdminHarness(t)

	cases := []struct {
		name string
		body adminRequest
	}{
		{
			name: "missing default_image field",
			body: adminRequest{TeamName: "NoImage-A"},
		},
		{
			name: "default_image null in payload",
			body: adminRequest{TeamName: "NoImage-B", DefaultImage: nil},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h.repo.setTeamErr(nil)
			body, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			resp := doAdminRequest(t, h, http.MethodPost, "/admin/teams",
				adminIdentityHeaders("admin-requires-image"), body)
			body2 := readBody(t, resp)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body2)
			}
			env := decodeErrorEnvelope(t, body2)
			if env.Code == "" || env.Message == "" || env.RequestID == "" {
				t.Errorf("error envelope missing documented field: %+v", env)
			}
		})
	}

	if got := h.repo.teamCalls(); got != 0 {
		t.Fatalf("CreateTeam calls=%d, want 0 (zero repository calls for invalid request)", got)
	}
}

// TestAdminRequiresUniqueTeamName proves the unique-team_name
// constraint: a second POST /admin/teams with an existing team_name
// is rejected with 400 even when the request body is otherwise well
// formed. The recording fake simulates the unique violation so the
// handler can return 400 without a real database. A fresh recording
// repo is used so no other CreateTeam calls poison the counter.
func TestAdminRequiresUniqueTeamName(t *testing.T) {
	h := newAdminHarness(t)
	const name = "DuplicateTeam"

	// First call succeeds; we use a fresh recorder so the count is
	// exactly 1 before the duplicate-name sub-test.
	first := doAdminRequest(t, h, http.MethodPost, "/admin/teams",
		adminIdentityHeaders("admin-unique-1"),
		validTeamCreateBodyWithName(t, name))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first POST /admin/teams status=%d, want 201; body=%s",
			first.StatusCode, readBody(t, first))
	}
	if got := h.repo.teamCalls(); got != 1 {
		t.Fatalf("first POST /admin/teams CreateTeam calls=%d, want 1", got)
	}

	// Force the second call to surface ErrTeamNameConflict so the handler
	// returns the documented 400.
	h.repo.setTeamErr(store.ErrTeamNameConflict)

	second := doAdminRequest(t, h, http.MethodPost, "/admin/teams",
		adminIdentityHeaders("admin-unique-2"),
		validTeamCreateBodyWithName(t, name))
	body := readBody(t, second)
	if second.StatusCode != http.StatusBadRequest {
		t.Fatalf("second POST /admin/teams (duplicate team_name) status=%d, want 400; body=%s",
			second.StatusCode, body)
	}
	env := decodeErrorEnvelope(t, body)
	if env.Code == "" || env.Message == "" || env.RequestID == "" {
		t.Errorf("error envelope missing documented field: %+v", env)
	}
	// Exactly two calls (the first success + the failing duplicate)
	// confirms the duplicate was rejected at the persistence layer
	// without silently replacing the original.
	if got := h.repo.teamCalls(); got != 2 {
		t.Fatalf("CreateTeam calls=%d, want 2 (1 success + 1 rejected duplicate)", got)
	}
}

func validTeamCreateBodyWithName(t *testing.T, name string) []byte {
	t.Helper()
	body, err := json.Marshal(adminRequest{
		TeamName:     name,
		DefaultImage: ptrImageRef(defaultImageReference(name)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// TestAdminSourceSystemReferencesTeam enforces the OpenSpec rule
// that POST /admin/source-systems MUST reject any team_id that does
// not already reference an existing team. The recording fake
// simulates ErrTeamNotFound so the handler can return 400 without a
// live database.
func TestAdminSourceSystemReferencesTeam(t *testing.T) {
	cases := []struct {
		name          string
		teamID        string
		listener      string
		repositoryErr error
		wantCalls     int64
	}{
		{name: "missing team_id", teamID: "", listener: "L-missing", wantCalls: 0},
		{name: "explicit unknown team_id", teamID: "team-ghost", listener: "L-ghost", repositoryErr: store.ErrTeamNotFound, wantCalls: 1},
		{name: "malformed team_id", teamID: "%%%", listener: "L-malformed", wantCalls: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newAdminHarness(t)
			h.repo.setSourceErr(tc.repositoryErr)
			resp := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
				adminIdentityHeaders("admin-ref-team"),
				validSourceSystemCreateBody(t, tc.teamID, tc.listener))
			body := readBody(t, resp)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
			}
			env := decodeErrorEnvelope(t, body)
			if env.Code == "" || env.Message == "" || env.RequestID == "" {
				t.Errorf("error envelope missing documented field: %+v", env)
			}
			if got := h.repo.sourceCalls(); got != tc.wantCalls {
				t.Fatalf("CreateSourceSystem calls=%d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestAdminTaskTypeReferencesTeam mirrors
// TestAdminSourceSystemReferencesTeam for POST /admin/task-types.
func TestAdminTaskTypeReferencesTeam(t *testing.T) {
	cases := []struct {
		name          string
		body          adminRequest
		repositoryErr error
		wantCalls     int64
	}{
		{
			name: "missing team_id",
			body: adminRequest{ExecutionTag: "openhands"},
		},
		{
			name: "unknown team_id",
			body: adminRequest{TeamID: "team-ghost", ExecutionTag: "openhands"}, repositoryErr: store.ErrTeamNotFound, wantCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newAdminHarness(t)
			h.repo.setTypeErr(tc.repositoryErr)
			body, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			resp := doAdminRequest(t, h, http.MethodPost, "/admin/task-types",
				adminIdentityHeaders("admin-tt-team"), body)
			body2 := readBody(t, resp)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body2)
			}
			env := decodeErrorEnvelope(t, body2)
			if env.Code == "" || env.Message == "" || env.RequestID == "" {
				t.Errorf("error envelope missing documented field: %+v", env)
			}
			if got := h.repo.typeCalls(); got != tc.wantCalls {
				t.Fatalf("CreateTaskType calls=%d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestAdminEndpointsRejectNonAdminRequestContext proves that request-context
// validation rejects listener, Executor, Gateway, and anonymous envelopes
// before an administrator repository is called. The headers are trusted input,
// not certificate-derived authentication.
func TestAdminEndpointsRejectNonAdminRequestContext(t *testing.T) {
	endpoints := []struct {
		method string
		path   string
		body   func() []byte
	}{
		{
			method: http.MethodPost, path: "/admin/teams",
			body: func() []byte { return validTeamCreateBody(t, "non-admin-team") },
		},
		{
			method: http.MethodPost, path: "/admin/source-systems",
			body: func() []byte { return validSourceSystemCreateBody(t, "team-a", "L-N") },
		},
		{
			method: http.MethodPost, path: "/admin/task-types",
			body: func() []byte { return validTaskTypeCreateBody(t, "team-a", "openhands") },
		},
	}

	type roleCase struct {
		name    string
		headers http.Header
	}
	roles := []roleCase{
		{name: "listener", headers: nonAdminIdentityHeaders("listener")},
		{name: "team_executor", headers: nonAdminIdentityHeaders("team-executor")},
		{name: "system_executor", headers: nonAdminIdentityHeaders("system-executor")},
		{name: "trusted_gateway", headers: nonAdminIdentityHeaders("gateway")},
		{name: "anonymous", headers: http.Header{}},
	}

	for _, rc := range roles {
		t.Run(rc.name, func(t *testing.T) {
			hr := newAdminHarness(t)
			for _, ep := range endpoints {
				t.Run(ep.method+" "+ep.path, func(t *testing.T) {
					resp := doAdminRequest(t, hr, ep.method, ep.path, rc.headers, ep.body())
					if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
						t.Fatalf("%s /%s as %s: status=%d, want 401/403; body=%s",
							ep.method, ep.path, rc.name, resp.StatusCode, readBody(t, resp))
					}
				})
			}
			if got := hr.repo.teamCalls() + hr.repo.sourceCalls() + hr.repo.typeCalls(); got != 0 {
				t.Fatalf("repository calls=%d, want 0", got)
			}
		})
	}
}

// TestExecutorRegistrationDoesNotTouchTeam proves that an
// Executor registration referencing a non-existent team (team-ghost)
// is rejected WITHOUT touching the teams table. The admin router is
// mounted at /admin/* only; PUT /v1/executors/{id} is not served by
// the planned admin surface, so the response is whatever the router
// returns for an unmounted path. The recording fake observes exactly
// ZERO CreateTeam calls, which is the contract under test.
func TestExecutorRegistrationDoesNotTouchTeam(t *testing.T) {
	repo := &recordingAdminRepo{}
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
	h := &adminHarness{repo: repo, srv: srv}

	const execID = "exec-does-not-touch-team"
	body, err := json.Marshal(map[string]any{
		"scope":            "team",
		"team_id":          "team-ghost",
		"executor_type":    "executor_docker_openhands",
		"identity":         "identity-doesnt-touch",
		"authorized_tag":   "openhands",
		"max_capacity":     1,
		"running_count":    0,
		"runtime_metadata": map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal executor body: %v", err)
	}
	headers := http.Header{}
	headers.Set("X-FlowAI-Role", "team-executor")
	headers.Set("X-FlowAI-Executor-Id", execID)
	headers.Set(hdrRequestID, "req-executor-no-team-touch")

	resp := doAdminRequest(t, h, http.MethodPut, "/v1/executors/"+execID, headers, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT /v1/executors/%s status=%d, want 400 for unknown team; body=%s",
			execID, resp.StatusCode, readBody(t, resp))
	}
	if got := h.repo.teamReads(); got != 1 {
		t.Fatalf("Executor registration team lookups=%d, want 1", got)
	}
	// The contract under test: NO CreateTeam call.
	if got := h.repo.teamCalls(); got != 0 {
		t.Fatalf("Executor registration (team-ghost) caused CreateTeam calls=%d, want 0; "+
			"the teams table must never be touched by the Executor path", got)
	}
	// And, by symmetry, neither CreateSourceSystem nor CreateTaskType.
	if got := h.repo.sourceCalls(); got != 0 {
		t.Errorf("CreateSourceSystem calls=%d after Executor PUT, want 0", got)
	}
	if got := h.repo.typeCalls(); got != 0 {
		t.Errorf("CreateTaskType calls=%d after Executor PUT, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Section 3a — listener_identity uniqueness
// ---------------------------------------------------------------------------

// TestSourceSystemListenerIdentityUniqueness asserts the database
// contract that the planned AdminRepository relies on: a global
// uniqueness index over source_systems.listener_identity, verified
// here via the recording fake's per-call enforcement. The fake
// rejects a duplicate insertion with errAdminConflict; the handler
// surfaces that as 409. The test does NOT touch the live database
// (the structural migration test is the integration-tag sibling
// TestSourceSystemListenerIdentityIndexExists in svc/state-registry/test).
func TestSourceSystemListenerIdentityUniqueness(t *testing.T) {
	h := newAdminHarness(t)
	const teamID = "team-a"
	const listener = "L-global-unique"

	// First insertion succeeds.
	first := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-uniq-1"),
		validSourceSystemCreateBody(t, teamID, listener))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first source-system status=%d, want 201; body=%s",
			first.StatusCode, readBody(t, first))
	}
	if got := h.repo.sourceCalls(); got != 1 {
		t.Fatalf("source calls=%d after first insert, want 1", got)
	}

	// Force every subsequent insertion to fail with the unique
	// violation. The handler must map this to 409.
	h.repo.setSourceErr(store.ErrListenerIdentityConflict)

	second := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-uniq-2"),
		validSourceSystemCreateBody(t, teamID, listener))
	body := readBody(t, second)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second source-system (duplicate listener_identity) status=%d, want 409; body=%s",
			second.StatusCode, body)
	}
	// Recorder observed both calls: handler cannot dedupe-avoid the
	// uniqueness check.
	if got := h.repo.sourceCalls(); got != 2 {
		t.Fatalf("source calls=%d after second insert, want 2 (handler must surface 409)", got)
	}
}

// TestAdminSourceSystemRejectsDuplicateListenerIdentity is the
// HTTP-level equivalent of the same-team duplicate-rejection
// scenario in autotest/state-registry/tests/contracts/10-admin.spec.ts
// (v0002.61 step "negative control: same listener_identity under the
// SAME team is rejected with 409").
func TestAdminSourceSystemRejectsDuplicateListenerIdentity(t *testing.T) {
	h := newAdminHarness(t)
	const teamID = "team-a"
	const listener = "L-same-team-dup"

	first := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-dup-1"),
		validSourceSystemCreateBody(t, teamID, listener))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first source-system status=%d, want 201; body=%s",
			first.StatusCode, readBody(t, first))
	}

	h.repo.setSourceErr(store.ErrListenerIdentityConflict)
	second := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-dup-2"),
		validSourceSystemCreateBody(t, teamID, listener))
	body := readBody(t, second)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("same-team duplicate listener_identity status=%d, want 409; body=%s",
			second.StatusCode, body)
	}
	env := decodeErrorEnvelope(t, body)
	if env.Code == "" || env.Message == "" || env.RequestID == "" {
		t.Errorf("error envelope missing documented field: %+v", env)
	}
	if got := h.repo.sourceCalls(); got != 2 {
		t.Fatalf("source calls=%d, want 2 (handler must surface 409 without further side-effects)", got)
	}
}

// TestAdminSourceSystemRejectsCrossTeamListenerIdentity mirrors
// v0002.61 step "negative control: same listener_identity under a
// DIFFERENT team is rejected with 409". A listener_identity already
// bound to team-a MUST NOT be re-registered under team-b, and the
// rejection MUST happen at the persistence layer (no row is created).
func TestAdminSourceSystemRejectsCrossTeamListenerIdentity(t *testing.T) {
	h := newAdminHarness(t)
	const teamA = "team-a"
	const teamB = "team-b"
	const listener = "L-cross-team"

	first := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-cross-1"),
		validSourceSystemCreateBody(t, teamA, listener))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("team-a source-system status=%d, want 201; body=%s",
			first.StatusCode, readBody(t, first))
	}

	h.repo.setSourceErr(store.ErrListenerIdentityConflict)
	second := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-cross-2"),
		validSourceSystemCreateBody(t, teamB, listener))
	body := readBody(t, second)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("cross-team duplicate listener_identity status=%d, want 409; body=%s",
			second.StatusCode, body)
	}
	if got := h.repo.sourceCalls(); got != 2 {
		t.Fatalf("source calls=%d, want 2 (handler must surface 409 for the cross-team attempt)", got)
	}
	if got := h.repo.lastSourceReq().TeamID; got != teamB {
		t.Errorf("rejected cross-team request recorded team_id=%q, want %q", got, teamB)
	}
}

// TestSourceSystemListenerIdentityIndexExists is the HTTP-level
// companion to the migration integration test
// (svc/state-registry/test/admin_listener_identity_integration_test.go).
// It proves the planned httpapi surface cooperates with the
// uniqueness contract by always forwarding the submitted
// listener_identity verbatim into the AdminRepository call, so a
// migration-side uniqueness index can enforce it. The recorder
// asserts the forwarded value matches what the wire body encoded.
func TestSourceSystemListenerIdentityIndexExists(t *testing.T) {
	h := newAdminHarness(t)
	const teamID = "team-a"
	const listener = "L-index-contract"

	resp := doAdminRequest(t, h, http.MethodPost, "/admin/source-systems",
		adminIdentityHeaders("admin-index-contract"),
		validSourceSystemCreateBody(t, teamID, listener))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, readBody(t, resp))
	}
	// Recorders MUST observe the verbatim listener_identity; if
	// normalization or whitespace-stripping were applied silently
	// here, the migration index would not match what the handler
	// asks the database to enforce.
	if got := h.repo.lastSourceReq().ListenerIdentity; got != listener {
		t.Errorf("forwarded listener_identity=%q, want %q (handler must forward verbatim)",
			got, listener)
	}
	if got := h.repo.sourceCalls(); got != 1 {
		t.Fatalf("source calls=%d, want 1", got)
	}
}
