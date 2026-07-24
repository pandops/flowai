// Section 4 — listener ingestion RED HTTP/unit tests for POST
// /v1/tasks. The tests mount the production httpapi.RegisterListener
// adapter on a fresh chi router and drive it through a recording
// fake that implements the new store.ListenerRepository contract. Each
// test asserts the documented OpenAPI contract end-to-end and uses
// the recording fake's call counter to verify the documented
// pre-rejection paths execute ZERO repository calls.
//
// RED COMMAND (without integration tag):
//
//	go test ./state-registry/... \
//	    -run 'TestListenerTeamBinding|TestListenerSourceSystemBinding|\
//
// TestIngestPendingNoEvent|TestTeamScopedSourceIdDeduplicate|\
// TestIngestSetsIngestedAt|TestCrossTeamSourceIdIndependenceViaSeparateSourceSystems|\
// TestListenerRequiresTaskTypeId|TestListenerRejectsForeignTaskTypeId|\
// TestRequiredTagDerivedFromTaskType|TestListenerCannotSupplyRequiredTag|\
// TestListenerRejectsListenerAuthoredRequiredTag' -count=1
//
// GREEN COMMAND: same as RED. The recording repository keeps handler
// assertions deterministic; tagged integration tests exercise the real
// PostgreSQL implementation.
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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// ---------------------------------------------------------------------------
// Listener identity headers — canonical names from
// openspec/changes/v0002-state-registry/specs/openapi/state-registry.openapi.yaml.
// ---------------------------------------------------------------------------

const (
	listenerHdrRole         = "X-FlowAI-Role"
	listenerHdrTeamID       = "X-FlowAI-Team-Id"
	listenerHdrIdentity     = "X-FlowAI-Listener-Identity"
	listenerHdrSourceSystem = "X-FlowAI-Source-System-Id"
	listenerHdrRequestID    = "X-FlowAI-Request-Id"
	listenerHdrRequiredTag  = "X-FlowAI-Required-Tag"
	listenerIngestionPath   = "/v1/tasks"
	listenerIngestionRole   = "listener"
)

// listenerAuthHeaders returns the documented five-header listener
// identity set bound to one (team, source_system, identity) triple.
func listenerAuthHeaders(teamID, sourceSystemID, listenerIdentity, requestID string) http.Header {
	h := http.Header{}
	h.Set(listenerHdrRole, listenerIngestionRole)
	h.Set(listenerHdrTeamID, teamID)
	h.Set(listenerHdrSourceSystem, sourceSystemID)
	h.Set(listenerHdrIdentity, listenerIdentity)
	h.Set(listenerHdrRequestID, requestID)
	return h
}

// validIngestionBody returns the canonical happy-path ingestion body.
func validIngestionBody(t *testing.T) platform.TaskIngestionRequest {
	t.Helper()
	return platform.TaskIngestionRequest{
		TeamID:         "team-a",
		SourceSystemID: "source-system-a",
		SourceID:       "external-a-1",
		TaskTypeID:     "task-type-a",
		Payload:        json.RawMessage(`{"hello":"world"}`),
		Image: &platform.ImageReference{
			Repository: "registry.example/agent:stable",
			Digest:     "sha256:" + strings.Repeat("a", 64),
		},
	}
}

func marshalIngestion(t *testing.T, req platform.TaskIngestionRequest) []byte {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal ingestion request: %v", err)
	}
	return body
}

// ---------------------------------------------------------------------------
// Recording fake implementing the store.ListenerRepository contract.
// ---------------------------------------------------------------------------

// recordingListenerRepo is the deterministic test double the listener
// handler calls into. The fake exposes hooks so each test can pin the
// canonical task identity the handler must echo back. Call counters let
// the test assert that documented pre-rejection paths execute ZERO
// repository calls.
type recordingListenerRepo struct {
	calls atomic.Int64

	mu           sync.Mutex
	lastReq      platform.TaskIngestionRequest
	lastIdent    platform.ListenerIdentity
	ingestErr    error
	executionTag string
	// ingestions remembers every (team, source_system, source_id)
	// triple the fake has accepted so subsequent calls return the
	// canonical dedupe row with created=false.
	ingestions map[string]platform.TaskListEntry
	onIngest   func(req platform.TaskIngestionRequest, ident platform.ListenerIdentity) (platform.TaskListEntry, bool, error)
}

var _ store.ListenerRepository = (*recordingListenerRepo)(nil)

func newRecordingListenerRepo() *recordingListenerRepo {
	return &recordingListenerRepo{ingestions: map[string]platform.TaskListEntry{}}
}

func (r *recordingListenerRepo) IngestTask(_ context.Context, req platform.TaskIngestionRequest, ident platform.ListenerIdentity) (platform.TaskListEntry, bool, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.lastReq = req
	r.lastIdent = ident
	hook := r.onIngest
	execTag := r.executionTag
	errOverride := r.ingestErr
	r.mu.Unlock()
	if hook != nil {
		return hook(req, ident)
	}
	if errOverride != nil {
		return platform.TaskListEntry{}, false, errOverride
	}
	if execTag == "" {
		execTag = "execution-tag-a"
	}
	key := req.TeamID + "|" + req.SourceSystemID + "|" + req.SourceID
	r.mu.Lock()
	if existing, ok := r.ingestions[key]; ok {
		r.mu.Unlock()
		return existing, false, nil
	}
	entry := canonicalPending("", req, execTag)
	r.ingestions[key] = entry
	r.mu.Unlock()
	return entry, true, nil
}

func canonicalPending(presetID string, req platform.TaskIngestionRequest, execTag string) platform.TaskListEntry {
	id := presetID
	if id == "" {
		id = "task-fake-" + req.TeamID + "-" + req.SourceSystemID + "-" + req.SourceID
	}
	var img *platform.ImageReference
	if req.Image != nil {
		copyImg := *req.Image
		img = &copyImg
	}
	return platform.TaskListEntry{
		TaskID:         id,
		TeamID:         req.TeamID,
		SourceSystemID: req.SourceSystemID,
		SourceID:       req.SourceID,
		TaskTypeID:     req.TaskTypeID,
		RequiredTag:    execTag,
		Payload:        req.Payload,
		CurrentState:   platform.TaskStatePending,
		ProjectID:      req.ProjectID,
		EnvironmentID:  req.EnvironmentID,
		Image:          img,
		IngestedAt:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}
}

func TestListenerIdentityRemainsOpaque(t *testing.T) {
	h := newListenerHarness(t)
	const opaqueIdentity = "spiffe://flowai.internal/listeners/team-a/source-a"
	body := validIngestionBody(t)
	resp, raw := h.doIngest(t,
		listenerAuthHeaders("team-a", "source-system-a", opaqueIdentity, "req-opaque-listener"),
		marshalIngestion(t, body),
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, raw)
	}
	if got := h.repo.lastIngestIdentity().Identity; got != opaqueIdentity {
		t.Errorf("listener identity=%q, want opaque value %q", got, opaqueIdentity)
	}
}

func TestListenerRequiresIdentityHeader(t *testing.T) {
	h := newListenerHarness(t)
	headers := listenerAuthHeaders("team-a", "source-system-a", "", "req-missing-listener")
	resp, raw := h.doIngest(t, headers, marshalIngestion(t, validIngestionBody(t)))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401; body=%s", resp.StatusCode, raw)
	}
	if h.repo.ingestCalls() != 0 {
		t.Errorf("IngestTask calls=%d, want 0 on missing listener identity", h.repo.ingestCalls())
	}
}

func TestListenerRejectsNonObjectPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload json.RawMessage
	}{
		{name: "null", payload: json.RawMessage(`null`)},
		{name: "array", payload: json.RawMessage(`[]`)},
		{name: "string", payload: json.RawMessage(`"value"`)},
		{name: "number", payload: json.RawMessage(`1`)},
		{name: "boolean", payload: json.RawMessage(`true`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newListenerHarness(t)
			body := validIngestionBody(t)
			body.Payload = tc.payload
			resp, raw := h.doIngest(t,
				listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-invalid-payload"),
				marshalIngestion(t, body),
			)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, raw)
			}
			if h.repo.ingestCalls() != 0 {
				t.Errorf("IngestTask calls=%d, want 0 on invalid payload", h.repo.ingestCalls())
			}
		})
	}
}

func (r *recordingListenerRepo) ingestCalls() int64 { return r.calls.Load() }

func (r *recordingListenerRepo) lastIngestRequest() platform.TaskIngestionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastReq
}

func (r *recordingListenerRepo) lastIngestIdentity() platform.ListenerIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastIdent
}

func (r *recordingListenerRepo) setExecutionTag(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executionTag = tag
}

func (r *recordingListenerRepo) setIngestError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ingestErr = err
}

// ---------------------------------------------------------------------------
// Test harness — mounts the production RegisterListener on a fresh chi
// router so every assertion exercises the handler in isolation, free
// from /v1/livez and /v1/_test/decrypt-ops surface noise.
// ---------------------------------------------------------------------------

type listenerHarness struct {
	router http.Handler
	repo   *recordingListenerRepo
	srv    *httptest.Server
}

func newListenerHarness(t *testing.T) *listenerHarness {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	repo := newRecordingListenerRepo()
	router := chi.NewRouter()
	httpapi.RegisterListener(router, logger, repo)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &listenerHarness{router: router, repo: repo, srv: srv}
}

type routedListenerRepo struct {
	*recordingAdminRepo
	*recordingListenerRepo
}

func TestRoutesMountsListenerOnlyInTestMode(t *testing.T) {
	repo := &routedListenerRepo{
		recordingAdminRepo:    &recordingAdminRepo{},
		recordingListenerRepo: newRecordingListenerRepo(),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ready := func() (bool, map[string]bool) { return true, map[string]bool{} }
	body := marshalIngestion(t, validIngestionBody(t))
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-route")

	for _, tc := range []struct {
		name       string
		testMode   bool
		wantStatus int
	}{
		{name: "test mode mounts listener adapter", testMode: true, wantStatus: http.StatusCreated},
		{name: "production mode remains fail closed", testMode: false, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := httpapi.Routes("state-registry", "", logger, ready, httpapi.NewDecryptOps(), tc.testMode, repo)
			srv := httptest.NewServer(handler)
			defer srv.Close()

			req, err := http.NewRequest(http.MethodPost, srv.URL+listenerIngestionPath, bytes.NewReader(body))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header = headers.Clone()
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("send request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
		})
	}
}

func (h *listenerHarness) doIngest(t *testing.T, headers http.Header, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+listenerIngestionPath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build POST /v1/tasks: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send POST /v1/tasks: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, raw
}

// ---------------------------------------------------------------------------
// TestListenerTeamBinding — body team_id must equal the listener's
// authenticated team. A submission to a foreign team is rejected with
// 403 before any repository call happens.
// ---------------------------------------------------------------------------

func TestListenerTeamBinding(t *testing.T) {
	tests := []struct {
		name        string
		authTeamID  string
		bodyTeamID  string
		wantStatus  int
		wantRepoHit bool
	}{
		{
			name:        "matching team_id accepted by listener binding",
			authTeamID:  "team-a",
			bodyTeamID:  "team-a",
			wantStatus:  http.StatusCreated,
			wantRepoHit: true,
		},
		{
			name:        "foreign team_id rejected by listener binding",
			authTeamID:  "team-a",
			bodyTeamID:  "team-b",
			wantStatus:  http.StatusForbidden,
			wantRepoHit: false,
		},
		{
			name:        "missing X-FlowAI-Team-Id rejected before body parse",
			authTeamID:  "",
			bodyTeamID:  "team-a",
			wantStatus:  http.StatusUnauthorized,
			wantRepoHit: false,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newListenerHarness(t)
			body := validIngestionBody(t)
			body.TeamID = tc.bodyTeamID
			headers := listenerAuthHeaders(
				tc.authTeamID,
				"source-system-a",
				"listener-identity-a",
				"req-listener-team-binding",
			)
			resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			if got := h.repo.ingestCalls(); (got > 0) != tc.wantRepoHit {
				t.Errorf("IngestTask calls=%d, want hit=%v", got, tc.wantRepoHit)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestListenerSourceSystemBinding — body source_system_id must equal
// the listener's authenticated source system. A submission referencing
// another source system is rejected with 403.
// ---------------------------------------------------------------------------

func TestListenerSourceSystemBinding(t *testing.T) {
	tests := []struct {
		name        string
		authSource  string
		bodySource  string
		wantStatus  int
		wantRepoHit bool
	}{
		{
			name:        "matching source_system_id accepted",
			authSource:  "source-system-a",
			bodySource:  "source-system-a",
			wantStatus:  http.StatusCreated,
			wantRepoHit: true,
		},
		{
			name:        "foreign source_system_id rejected",
			authSource:  "source-system-a",
			bodySource:  "source-system-b",
			wantStatus:  http.StatusForbidden,
			wantRepoHit: false,
		},
		{
			name:        "missing X-FlowAI-Source-System-Id rejected",
			authSource:  "",
			bodySource:  "source-system-a",
			wantStatus:  http.StatusUnauthorized,
			wantRepoHit: false,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newListenerHarness(t)
			body := validIngestionBody(t)
			body.SourceSystemID = tc.bodySource
			headers := listenerAuthHeaders(
				"team-a",
				tc.authSource,
				"listener-identity-a",
				"req-listener-source-binding",
			)
			resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			if got := h.repo.ingestCalls(); (got > 0) != tc.wantRepoHit {
				t.Errorf("IngestTask calls=%d, want hit=%v", got, tc.wantRepoHit)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestIngestPendingNoEvent — successful ingestion commits a pending row
// and never appends a lifecycle event. The handler projects a pending
// task with the canonical task_id and ingested_at.
// ---------------------------------------------------------------------------

func TestIngestPendingNoEvent(t *testing.T) {
	h := newListenerHarness(t)
	body := validIngestionBody(t)
	resp, raw := h.doIngest(t,
		listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-ingest-pending-no-event"),
		marshalIngestion(t, body),
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201 created; body=%s", resp.StatusCode, raw)
	}
	var got platform.TaskListEntry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode ingestion response: %v", err)
	}
	if got.CurrentState != platform.TaskStatePending {
		t.Errorf("current_state=%q, want pending (no event at ingestion)", got.CurrentState)
	}
	if got.TaskID == "" {
		t.Errorf("response task_id is empty; ingestion must return the canonical task")
	}
	if got.IngestedAt == "" {
		t.Errorf("response ingested_at is empty; ingestion must return the immutable timestamp")
	}
	if got.OwnerCommandID != nil {
		t.Errorf("response owner_command_id=%v, want nil on pending", *got.OwnerCommandID)
	}
	if got.ExecutorID != nil {
		t.Errorf("response executor_id=%v, want nil on pending", *got.ExecutorID)
	}
	if h.repo.ingestCalls() != 1 {
		t.Errorf("IngestTask calls=%d, want 1", h.repo.ingestCalls())
	}
}

// ---------------------------------------------------------------------------
// TestTeamScopedSourceIdDeduplicate — a retry of the same
// (team_id, source_system_id, source_id) tuple within the same team
// returns the existing task with 200 OK; no second row is created.
// ---------------------------------------------------------------------------

func TestTeamScopedSourceIdDeduplicate(t *testing.T) {
	h := newListenerHarness(t)
	body := validIngestionBody(t)
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-dedupe")

	resp1, raw1 := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first ingest status=%d, want 201; body=%s", resp1.StatusCode, raw1)
	}
	var first platform.TaskListEntry
	if err := json.Unmarshal(raw1, &first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	resp2, raw2 := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("retry status=%d, want 200; body=%s", resp2.StatusCode, raw2)
	}
	var second platform.TaskListEntry
	if err := json.Unmarshal(raw2, &second); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if second.TaskID != first.TaskID {
		t.Errorf("retry returned task_id=%q, want %q (dedupe must return canonical id)", second.TaskID, first.TaskID)
	}
	if second.IngestedAt != first.IngestedAt {
		t.Errorf("retry replaced ingested_at=%q with %q; ingested_at is immutable", second.IngestedAt, first.IngestedAt)
	}
}

// ---------------------------------------------------------------------------
// TestIngestSetsIngestedAt — successful ingestion sets ingested_at
// exactly once and never replaces it on a retry.
// ---------------------------------------------------------------------------

func TestIngestSetsIngestedAt(t *testing.T) {
	h := newListenerHarness(t)
	body := validIngestionBody(t)
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-ingested-at")

	resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, raw)
	}
	var got platform.TaskListEntry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.IngestedAt == "" {
		t.Fatalf("ingested_at is empty; the listener must set ingested_at on the first successful ingest")
	}

	respRetry, rawRetry := h.doIngest(t, headers, marshalIngestion(t, body))
	if respRetry.StatusCode != http.StatusOK {
		t.Fatalf("retry status=%d, want 200; body=%s", respRetry.StatusCode, rawRetry)
	}
	var retry platform.TaskListEntry
	if err := json.Unmarshal(rawRetry, &retry); err != nil {
		t.Fatalf("decode retry: %v", err)
	}
	if retry.IngestedAt != got.IngestedAt {
		t.Errorf("retry changed ingested_at from %q to %q; ingested_at is immutable", got.IngestedAt, retry.IngestedAt)
	}
}

// ---------------------------------------------------------------------------
// TestCrossTeamSourceIdIndependenceViaSeparateSourceSystems — the same
// external source_id may exist in two teams because each team registers
// its own source system. Independence comes from distinct team-owned
// source systems.
// ---------------------------------------------------------------------------

func TestCrossTeamSourceIdIndependenceViaSeparateSourceSystems(t *testing.T) {
	h := newListenerHarness(t)
	const sharedSourceID = "external-shared-1"

	bodyA := validIngestionBody(t)
	bodyA.TeamID = "team-a"
	bodyA.SourceSystemID = "source-system-a"
	bodyA.SourceID = sharedSourceID
	respA, rawA := h.doIngest(t,
		listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-cross-team-a"),
		marshalIngestion(t, bodyA),
	)
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("team-a status=%d, want 201; body=%s", respA.StatusCode, rawA)
	}
	var teamA platform.TaskListEntry
	if err := json.Unmarshal(rawA, &teamA); err != nil {
		t.Fatalf("decode team-a response: %v", err)
	}

	bodyB := validIngestionBody(t)
	bodyB.TeamID = "team-b"
	bodyB.SourceSystemID = "source-system-b"
	bodyB.SourceID = sharedSourceID
	respB, rawB := h.doIngest(t,
		listenerAuthHeaders("team-b", "source-system-b", "listener-identity-b", "req-cross-team-b"),
		marshalIngestion(t, bodyB),
	)
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("team-b status=%d, want 201; body=%s", respB.StatusCode, rawB)
	}
	var teamB platform.TaskListEntry
	if err := json.Unmarshal(rawB, &teamB); err != nil {
		t.Fatalf("decode team-b response: %v", err)
	}
	if teamA.TaskID == teamB.TaskID {
		t.Errorf("team-a and team-b returned the same task_id=%q; independence must come from distinct source systems", teamA.TaskID)
	}
	if teamA.TeamID != "team-a" || teamB.TeamID != "team-b" {
		t.Errorf("response team_id must match the authenticated listener: team-a=%q team-b=%q", teamA.TeamID, teamB.TeamID)
	}
}

// ---------------------------------------------------------------------------
// TestListenerRequiresTaskTypeId — task_type_id is REQUIRED. A body that
// omits task_type_id is rejected with 400 before any repository call.
// ---------------------------------------------------------------------------

func TestListenerRequiresTaskTypeId(t *testing.T) {
	h := newListenerHarness(t)
	body := validIngestionBody(t)
	body.TaskTypeID = ""
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-missing-task-type")

	resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if code, _ := env["code"].(string); code == "" {
		t.Errorf("error envelope missing `code` field: %s", raw)
	}
	if msg, _ := env["message"].(string); !strings.Contains(strings.ToLower(msg), "task_type_id") {
		t.Errorf("error message %q does not mention the missing task_type_id", msg)
	}
	if h.repo.ingestCalls() != 0 {
		t.Errorf("IngestTask calls=%d, want 0 on missing task_type_id", h.repo.ingestCalls())
	}
}

// ---------------------------------------------------------------------------
// TestListenerRejectsForeignTaskTypeId — task_type_id must reference a
// task type registered for the authenticated listener's team. A
// foreign-team or unknown task_type_id is rejected with 403.
// ---------------------------------------------------------------------------

func TestListenerRejectsForeignTaskTypeId(t *testing.T) {
	tests := []struct {
		name       string
		authTeamID string
		bodyTypeID string
		fn         func(*recordingListenerRepo)
		wantStatus int
	}{
		{
			name:       "same-team task_type_id accepted",
			authTeamID: "team-a",
			bodyTypeID: "task-type-a",
			fn:         func(r *recordingListenerRepo) { r.setExecutionTag("execution-tag-a") },
			wantStatus: http.StatusCreated,
		},
		{
			name:       "foreign-team task_type_id rejected",
			authTeamID: "team-a",
			bodyTypeID: "task-type-b",
			fn: func(r *recordingListenerRepo) {
				r.setIngestError(store.ErrListenerTaskTypeUnknown)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "unknown task_type_id rejected",
			authTeamID: "team-a",
			bodyTypeID: "task-type-missing",
			fn: func(r *recordingListenerRepo) {
				r.setIngestError(store.ErrListenerTaskTypeUnknown)
			},
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newListenerHarness(t)
			if tc.fn != nil {
				tc.fn(h.repo)
			}
			body := validIngestionBody(t)
			body.TeamID = tc.authTeamID
			body.TaskTypeID = tc.bodyTypeID
			headers := listenerAuthHeaders(tc.authTeamID, "source-system-a", "listener-identity-a", "req-foreign-tt")
			resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", resp.StatusCode, tc.wantStatus, raw)
			}
			if tc.wantStatus == http.StatusForbidden && h.repo.ingestCalls() != 1 {
				t.Errorf("IngestTask calls=%d, want 1 for foreign-task-type rejection", h.repo.ingestCalls())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRequiredTagDerivedFromTaskType — the handler derives required_tag
// from the task type and never echoes any listener-supplied value.
// ---------------------------------------------------------------------------

func TestRequiredTagDerivedFromTaskType(t *testing.T) {
	h := newListenerHarness(t)
	h.repo.setExecutionTag("execution-tag-a")
	body := validIngestionBody(t)
	// The body intentionally omits required_tag (the documented
	// TaskIngestionRequest schema does not declare one); the handler
	// must echo back the repository-derived execution tag and never
	// carry any listener-supplied required_tag value.
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-derived-tag")

	resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", resp.StatusCode, raw)
	}
	var got platform.TaskListEntry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.RequiredTag != "execution-tag-a" {
		t.Errorf("response required_tag=%q, want derived value execution-tag-a", got.RequiredTag)
	}
	// The repository must receive the canonical task_type_id; the
	// handler never injects any listener-authored required_tag.
	if got := h.repo.lastIngestRequest().TaskTypeID; got != "task-type-a" {
		t.Errorf("recorded task_type_id=%q, want task-type-a", got)
	}
}

// ---------------------------------------------------------------------------
// TestListenerCannotSupplyRequiredTag — the handler rejects any body
// that carries a required_tag key with 400
// listener_supplied_required_tag_forbidden before any repository call.
// ---------------------------------------------------------------------------

func TestListenerCannotSupplyRequiredTag(t *testing.T) {
	h := newListenerHarness(t)
	body := validIngestionBody(t)
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-cannot-supply")

	// Inject required_tag at the top level; the strict decode would
	// also reject it, but the documented contract demands a
	// dedicated 400 envelope with code
	// listener_supplied_required_tag_forbidden.
	raw := marshalIngestion(t, body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	m["required_tag"] = "listener-supplied-forbidden"
	modified, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, body2 := h.doIngest(t, headers, modified)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 listener_supplied_required_tag_forbidden; body=%s", resp.StatusCode, body2)
	}
	var env map[string]any
	if err := json.Unmarshal(body2, &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if code, _ := env["code"].(string); code != "listener_supplied_required_tag_forbidden" {
		t.Errorf("error code=%q, want listener_supplied_required_tag_forbidden; body=%s", code, body2)
	}
	if h.repo.ingestCalls() != 0 {
		t.Errorf("IngestTask calls=%d, want 0 on required_tag rejection", h.repo.ingestCalls())
	}
}

// ---------------------------------------------------------------------------
// TestListenerRejectsListenerAuthoredRequiredTag — every variant is
// rejected with 400 listener_supplied_required_tag_forbidden.
// ---------------------------------------------------------------------------

func TestListenerRejectsListenerAuthoredRequiredTag(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, body []byte, headers http.Header) []byte
	}{
		{
			name: "top-level required_tag field",
			mutate: func(t *testing.T, body []byte, _ http.Header) []byte {
				t.Helper()
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("decode: %v", err)
				}
				m["required_tag"] = "listener-supplied-top"
				out, err := json.Marshal(m)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				return out
			},
		},
		{
			name: "nested body wrapper carrying required_tag",
			mutate: func(t *testing.T, body []byte, _ http.Header) []byte {
				t.Helper()
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("decode: %v", err)
				}
				m["payload"] = map[string]any{
					"required_tag": "listener-supplied-nested",
					"task_type_id": m["task_type_id"],
				}
				out, err := json.Marshal(m)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				return out
			},
		},
		{
			name: "X-FlowAI-Required-Tag request header",
			mutate: func(t *testing.T, body []byte, headers http.Header) []byte {
				t.Helper()
				headers.Set(listenerHdrRequiredTag, "listener-supplied-header")
				return body
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newListenerHarness(t)
			headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-authored-required-tag")
			body := marshalIngestion(t, validIngestionBody(t))
			body = tc.mutate(t, body, headers)
			resp, raw := h.doIngest(t, headers, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, raw)
			}
			var env map[string]any
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if code, _ := env["code"].(string); code != "listener_supplied_required_tag_forbidden" {
				t.Errorf("error code=%q, want listener_supplied_required_tag_forbidden; body=%s", code, raw)
			}
			if h.repo.ingestCalls() != 0 {
				t.Errorf("IngestTask calls=%d, want 0 on required_tag rejection", h.repo.ingestCalls())
			}
		})
	}
}

// TestListenerIngestRetainsCanonicalImageOnRetry covers the
// retry-image-immutability contract: when the listener retries an
// existing tuple with a different image, the handler returns the
// canonical existing row without replacing the image.
func TestListenerIngestRetainsCanonicalImageOnRetry(t *testing.T) {
	h := newListenerHarness(t)
	canonical := platform.ImageReference{
		Repository: "registry.example/agent:stable",
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}
	override := platform.ImageReference{
		Repository: "registry.example/agent:override",
		Digest:     "sha256:" + strings.Repeat("b", 64),
	}
	body := validIngestionBody(t)
	body.Image = &canonical
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-image-immutability")

	resp1, raw1 := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("initial ingest status=%d, want 201; body=%s", resp1.StatusCode, raw1)
	}

	body.Image = &override
	resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	var got platform.TaskListEntry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode retry: %v", err)
	}
	if got.Image == nil {
		t.Fatalf("retry response image is nil; canonical image must be retained")
	}
	if got.Image.Repository != "registry.example/agent:stable" {
		t.Errorf("retry replaced image repository=%q with listener-supplied value; canonical image is immutable", got.Image.Repository)
	}
}

// TestListenerIngestMapsRepositoryMismatchToNonRevealing403 covers the
// non-revealing classification contract: when the repository returns
// ErrListenerSourceMismatch the handler MUST respond with 403
// not_authorized and never leak the listener identity value.
// ---------------------------------------------------------------------------

func TestListenerIngestMapsRepositoryMismatchToNonRevealing403(t *testing.T) {
	h := newListenerHarness(t)
	h.repo.setIngestError(store.ErrListenerSourceMismatch)
	body := validIngestionBody(t)
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-mismatch")

	resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403; body=%s", resp.StatusCode, raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if code, _ := env["code"].(string); code != "not_authorized" {
		t.Errorf("error code=%q, want not_authorized; body=%s", code, raw)
	}
	// The response must NEVER echo the listener identity value.
	if strings.Contains(string(raw), "listener-identity-a") {
		t.Errorf("response leaks listener identity value: %s", raw)
	}
}

// TestListenerIngestMapsUnexpectedRepositoryErrorToGeneric500 covers
// the failure-closed contract for unexpected repository failures.
// ---------------------------------------------------------------------------

func TestListenerIngestMapsUnexpectedRepositoryErrorToGeneric500(t *testing.T) {
	h := newListenerHarness(t)
	h.repo.setIngestError(errors.New("postgres connection refused"))
	body := validIngestionBody(t)
	headers := listenerAuthHeaders("team-a", "source-system-a", "listener-identity-a", "req-unexpected")

	resp, raw := h.doIngest(t, headers, marshalIngestion(t, body))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "postgres connection refused") {
		t.Errorf("response leaks repository error message: %s", raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if code, _ := env["code"].(string); code != "internal_error" {
		t.Errorf("error code=%q, want internal_error", code)
	}
}
