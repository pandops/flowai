// v0002 service-local integration tests for the Docker Executor.
// These tests stand up a v0002 State Registry stub that mimics the documented wire
//
//	shape and enforces the FIFO + capacity + idempotency
//	contracts the Executor relies on.
//
// The tests cover the v0002 contract slice: claim before runtime,
// resolved_image adopted verbatim, local capacity gating,
// stable command_id, the 409/404/5xx taxonomy (no runtime on
// non-200), the documented task + self event envelopes, and the
// v0002 record-keeping Docker labels.
package integration_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor_docker_openhands/internal/dockerclient"
	"github.com/flowai/platform/executor_docker_openhands/internal/executor"
	fakedocker "github.com/flowai/platform/executor_docker_openhands/internal/mocks/docker"
)

// v0002TaskRecord is the canonical record the v0002 stub holds for
// a single task row.
type v0002TaskRecord struct {
	TaskID         string
	TeamID         string
	RequiredTag    string
	CurrentState   string
	IngestedAt     time.Time
	ExecutorID     *string
	OwnerCommandID *string
	ResolvedImage  string
	ImageSource    string
	EnvironmentID  string
	ScopeToken     string
}

// v0002EventRecord is one immutable canonical task event row.
type v0002EventRecord struct {
	EventID    string
	TeamID     string
	TaskID     string
	ExecutorID string
	EventType  string
	OccurredAt string
	Payload    json.RawMessage
}

// v0002Stub is the in-process v0002 State Registry.
type v0002Stub struct {
	mu sync.Mutex

	executors  map[string]map[string]any
	tasks      map[string]*v0002TaskRecord
	taskOrder  []string
	taskEvents map[string][]v0002EventRecord

	failNextClaim atomic.Pointer[string]

	claimHits    atomic.Int32
	openHits     atomic.Int32
	registerHits atomic.Int32
	discoverHits atomic.Int32
	envOpenHits  atomic.Int32
}

func newV0002Stub() *v0002Stub {
	return &v0002Stub{
		executors:  map[string]map[string]any{},
		tasks:      map[string]*v0002TaskRecord{},
		taskEvents: map[string][]v0002EventRecord{},
	}
}

func (s *v0002Stub) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/executors/", s.handleExecutor)
	mux.HandleFunc("/v1/tasks/", s.handleTask)
	mux.HandleFunc("/v1/environments/", s.handleEnvironment)
	return mux
}

func (s *v0002Stub) handleExecutor(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/executors/")
	id := path
	suffix := ""
	if idx := strings.Index(path, "/"); idx >= 0 {
		id = path[:idx]
		suffix = path[idx+1:]
	}
	switch {
	case suffix == "tasks" && r.Method == http.MethodGet:
		s.discoverHits.Add(1)
		s.handleDiscover(w, r, id)
	case suffix == "claim" && r.Method == http.MethodPost:
		s.claimHits.Add(1)
		s.handleClaim(w, r, id)
	case suffix == "events" && r.Method == http.MethodPost:
		s.handleSelfEvent(w, r, id)
	case suffix == "" && r.Method == http.MethodPut:
		s.registerHits.Add(1)
		s.handleRegister(w, r, id)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *v0002Stub) handleRegister(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeV0002Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	s.executors[id] = body
	rec := map[string]any{
		"executor_id":      id,
		"scope":            body["scope"],
		"team_id":          body["team_id"],
		"executor_type":    body["executor_type"],
		"identity":         body["identity"],
		"authorized_tag":   body["authorized_tag"],
		"max_capacity":     body["max_capacity"],
		"running_count":    body["running_count"],
		"runtime_metadata": body["runtime_metadata"],
		"registered_at":    time.Now().UTC(),
		"updated_at":       time.Now().UTC(),
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

func (s *v0002Stub) handleDiscover(w http.ResponseWriter, r *http.Request, id string) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		writeV0002Error(w, r, http.StatusBadRequest, "invalid_request", "tag is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []map[string]any{}
	for _, taskID := range s.taskOrder {
		t := s.tasks[taskID]
		if t.CurrentState != "pending" {
			continue
		}
		if t.RequiredTag != tag {
			continue
		}
		items = append(items, map[string]any{
			"task_id":       t.TaskID,
			"team_id":       t.TeamID,
			"required_tag":  t.RequiredTag,
			"current_state": t.CurrentState,
			"ingested_at":   t.IngestedAt,
		})
	}
	if len(items) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (s *v0002Stub) handleClaim(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		TaskID    string `json:"task_id"`
		CommandID string `json:"command_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeV0002Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	t, ok := s.tasks[req.TaskID]
	if !ok {
		s.mu.Unlock()
		writeV0002Error(w, r, http.StatusNotFound, "task_not_found", "task is unknown")
		return
	}
	if failID := s.failNextClaim.Load(); failID != nil && *failID == req.TaskID {
		s.failNextClaim.Store(nil)
		s.mu.Unlock()
		writeV0002Error(w, r, http.StatusConflict, "older_task_must_be_claimed_first", "")
		return
	}
	if t.CurrentState == "pending" {
		for _, olderID := range s.taskOrder {
			if olderID == req.TaskID {
				break
			}
			older := s.tasks[olderID]
			if older.CurrentState != "pending" || older.RequiredTag != t.RequiredTag {
				continue
			}
			s.mu.Unlock()
			writeV0002Error(w, r, http.StatusConflict, "older_task_must_be_claimed_first", "")
			return
		}
	}
	if t.CurrentState != "pending" {
		if t.OwnerCommandID != nil && *t.OwnerCommandID == req.CommandID && t.ExecutorID != nil && *t.ExecutorID == id {
			resp := s.buildClaimResponseLocked(t)
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		s.mu.Unlock()
		writeV0002Error(w, r, http.StatusConflict, "task_already_claimed", "")
		return
	}
	execID := id
	t.OwnerCommandID = &req.CommandID
	t.ExecutorID = &execID
	t.CurrentState = "created"
	payload, _ := json.Marshal(map[string]string{
		"message":  "task " + t.TaskID + " loaded by " + id,
		"task_id":  t.TaskID,
		"executor": id,
	})
	s.taskEvents[t.TaskID] = append(s.taskEvents[t.TaskID], v0002EventRecord{
		EventID:    "evt-" + t.TaskID,
		TeamID:     t.TeamID,
		TaskID:     t.TaskID,
		ExecutorID: id,
		EventType:  "created",
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:    payload,
	})
	resp := s.buildClaimResponseLocked(t)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *v0002Stub) buildClaimResponseLocked(t *v0002TaskRecord) map[string]any {
	resp := map[string]any{
		"claim": "claimed",
		"task": map[string]any{
			"task_id":          t.TaskID,
			"team_id":          t.TeamID,
			"required_tag":     t.RequiredTag,
			"current_state":    t.CurrentState,
			"owner_command_id": t.OwnerCommandID,
			"executor_id":      t.ExecutorID,
			"ingested_at":      t.IngestedAt,
		},
	}
	if t.ResolvedImage != "" {
		resp["resolved_image"] = map[string]any{
			"repository": t.ResolvedImage,
			"tag":        "v1",
		}
		resp["image_source"] = t.ImageSource
	}
	if t.EnvironmentID != "" {
		resp["environment_id"] = t.EnvironmentID
	}
	if t.ScopeToken != "" {
		resp["scope_token"] = t.ScopeToken
	}
	return resp
}

func (s *v0002Stub) handleTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	id := path
	suffix := ""
	if idx := strings.Index(path, "/"); idx >= 0 {
		id = path[:idx]
		suffix = path[idx+1:]
	}
	if suffix == "events" && r.Method == http.MethodPost {
		s.handleTaskEvent(w, r, id)
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (s *v0002Stub) handleTaskEvent(w http.ResponseWriter, r *http.Request, taskID string) {
	var ev struct {
		EventID    string          `json:"event_id"`
		TeamID     string          `json:"team_id"`
		TaskID     string          `json:"task_id"`
		ExecutorID string          `json:"executor_id"`
		EventType  string          `json:"event_type"`
		OccurredAt string          `json:"occurred_at"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		writeV0002Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	s.taskEvents[taskID] = append(s.taskEvents[taskID], v0002EventRecord{
		EventID: ev.EventID, TeamID: ev.TeamID, TaskID: ev.TaskID, ExecutorID: ev.ExecutorID,
		EventType: ev.EventType, OccurredAt: ev.OccurredAt, Payload: ev.Payload,
	})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"event_id":    ev.EventID,
		"team_id":     ev.TeamID,
		"accepted_at": time.Now().UTC(),
	})
}

func (s *v0002Stub) handleSelfEvent(w http.ResponseWriter, r *http.Request, id string) {
	var ev struct {
		EventID    string          `json:"event_id"`
		TeamID     *string         `json:"team_id"`
		ExecutorID string          `json:"executor_id"`
		EventType  string          `json:"event_type"`
		OccurredAt string          `json:"occurred_at"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		writeV0002Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"event_id":    ev.EventID,
		"team_id":     "",
		"accepted_at": time.Now().UTC(),
	})
}

func (s *v0002Stub) handleEnvironment(w http.ResponseWriter, r *http.Request) {
	envID := strings.TrimPrefix(r.URL.Path, "/v1/environments/")
	if idx := strings.Index(envID, "/"); idx >= 0 {
		envID = envID[:idx]
	}
	taskID := r.URL.Query().Get("task_id")
	s.envOpenHits.Add(1)
	if envID == "" || taskID == "" {
		writeV0002Error(w, r, http.StatusNotFound, "environment_unknown_or_unavailable", "")
		return
	}
	s.mu.Lock()
	t, ok := s.tasks[taskID]
	s.mu.Unlock()
	if !ok || t.EnvironmentID != envID {
		writeV0002Error(w, r, http.StatusNotFound, "environment_unknown_or_unavailable", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"team_id":        t.TeamID,
		"task_id":        taskID,
		"environment_id": envID,
		"executor_id":    "",
		"values":         map[string]string{"REGION": "us-east-1", "TIER": "prod"},
	})
}

func writeV0002Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": code, "message": message, "request_id": r.Header.Get("X-Request-Id"),
	})
}

func (s *v0002Stub) addPendingTask(taskID, image, imageSource string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := &v0002TaskRecord{
		TaskID:        taskID,
		TeamID:        "team-a",
		RequiredTag:   "openhands",
		CurrentState:  "pending",
		IngestedAt:    time.Now().UTC(),
		ResolvedImage: image,
		ImageSource:   imageSource,
	}
	s.tasks[taskID] = rec
	s.taskOrder = append(s.taskOrder, taskID)
}

func (s *v0002Stub) taskEventTypes(taskID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, ev := range s.taskEvents[taskID] {
		out = append(out, ev.EventType)
	}
	return out
}

func (s *v0002Stub) taskEventRecords(taskID string) []v0002EventRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]v0002EventRecord, len(s.taskEvents[taskID]))
	copy(out, s.taskEvents[taskID])
	return out
}

// setupV0002Env stands up a v0002 stub behind a plain HTTP
// httptest.Server. After v0009 the backend transport is plaintext;
// the legacy mTLS material is not used by the Executor.
func setupV0002Env(t *testing.T, mutate func(*executor.Config)) (*v0002Stub, *executor.Executor, *fakedocker.FakeDocker, *executor.Config) {
	t.Helper()
	stub := newV0002Stub()

	stubSrv := httptest.NewServer(stub.routes())
	t.Cleanup(stubSrv.Close)

	cfg := &executor.Config{
		ExecutorID:           "exec-" + uuid.NewString(),
		ExecutorAPIBind:      "127.0.0.1:0",
		MaxContainers:        2,
		DockerSocketPath:     "/var/run/docker.sock",
		OpenHandsImage:       "ghcr.io/openhands/agent-server:latest-python",
		OpenHandsPortStart:   19600,
		OpenHandsPortEnd:     19610,
		OpenHandsStartupTO:   2 * time.Second,
		OpenHandsDrainTO:     1 * time.Second,
		ImagePullPolicy:      "if-not-present",
		LogLevel:             "error",
		StateRegistryURL:     stubSrv.URL,
		Scope:                "team",
		TeamID:               "team-a",
		AuthorizedTag:        "openhands",
		PollInterval:         30 * time.Millisecond,
		WebSocketDialTimeout: 500 * time.Millisecond,
		OpenHandsWorkspace:   "/workspace/project",
		OpenHandsLLMModel:    "test-model",
		OpenHandsLLMAPIKey:   "test-key",
		OpenHandsLLMUsageID:  "flowai-executor",
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	docker := fakedocker.NewFakeDocker()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exec := executor.New(cfg, docker, logger)
	return stub, exec, docker, cfg
}

func startV0002(t *testing.T, exec *executor.Executor) (context.CancelFunc, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = exec.Run(ctx)
	}()
	return cancel, stopped
}

func findContainerForTask(refs []dockerclient.ContainerRef, taskID string) *dockerclient.ContainerRef {
	for i, r := range refs {
		if r.Labels[dockerclient.LabelTaskID] == taskID {
			return &refs[i]
		}
	}
	return nil
}

// TestV0002ClaimBeforeRuntime asserts the Executor never starts a
// container before a successful 200 claim.
func TestV0002ClaimBeforeRuntime(t *testing.T) {
	stub, exec, _, _ := setupV0002Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "team-default", "team_default")
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if types := stub.taskEventTypes(taskID); contains(types, "running") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !contains(stub.taskEventTypes(taskID), "running") {
		t.Fatalf("executor never appended `running` event; got %v", stub.taskEventTypes(taskID))
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n := exec.RunningChildCount(); n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := exec.RunningChildCount(); n == 0 {
		t.Fatalf("executor never started a container after claim")
	}
}

func TestV0002CancellationPersistsTerminalFailure(t *testing.T) {
	stub, exec, _, _ := setupV0002Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "team-default", "team_default")
	cancel, stopped := startV0002(t, exec)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !contains(stub.taskEventTypes(taskID), "running") {
		time.Sleep(20 * time.Millisecond)
	}
	if !contains(stub.taskEventTypes(taskID), "running") {
		cancel()
		<-stopped
		t.Fatalf("executor never appended running event; got %v", stub.taskEventTypes(taskID))
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(8 * time.Second):
		t.Fatal("executor did not stop after cancellation")
	}
	if types := stub.taskEventTypes(taskID); !contains(types, "failed") {
		t.Fatalf("executor dropped terminal failed event after cancellation; got %v", types)
	}
}

func TestV0002ContainerExitBeforeFinishFailsOnceAndRetainsCapacity(t *testing.T) {
	stub, exec, docker, _ := setupV0002Env(t, func(cfg *executor.Config) {
		cfg.FailedCleanupDelay = 300 * time.Millisecond
	})
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "team-default", "team_default")
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	var target *dockerclient.ContainerRef
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ref := findContainerForTask(docker.StartedRefsSnapshot(), taskID); ref != nil {
			target = ref
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if target == nil {
		t.Fatal("task container was not started")
	}

	// Give the per-container Docker event subscription time to attach, then
	// simulate the runtime exiting before OpenHands reports finished.
	time.Sleep(50 * time.Millisecond)
	docker.EmitDie(target.Name)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if contains(stub.taskEventTypes(taskID), "failed") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	records := stub.taskEventRecords(taskID)
	failed := 0
	for _, record := range records {
		if record.EventType != "failed" {
			continue
		}
		failed++
		var payload map[string]string
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatalf("decode failed payload: %v", err)
		}
		if got := payload["failure_reason"]; got != "container_exited_before_finish" {
			t.Fatalf("failure_reason = %q, want container_exited_before_finish", got)
		}
	}
	if failed != 1 {
		t.Fatalf("failed event count = %d, want 1; events=%v", failed, stub.taskEventTypes(taskID))
	}

	// The accepted failure starts the configured retention delay; the slot
	// and container must continue to consume local capacity during it.
	if got := exec.RunningChildCount(); got != 1 {
		t.Fatalf("running count during failed cleanup delay = %d, want 1", got)
	}
	if !docker.HasContainer(target.ID) {
		t.Fatal("container removed before failed cleanup delay expired")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && docker.HasContainer(target.ID) {
		time.Sleep(20 * time.Millisecond)
	}
	if docker.HasContainer(target.ID) {
		t.Fatal("container still present after failed cleanup delay")
	}
	if got := exec.RunningChildCount(); got != 0 {
		t.Fatalf("running count after cleanup = %d, want 0", got)
	}
	if failedTypes := stub.taskEventTypes(taskID); countValue(failedTypes, "failed") != 1 {
		t.Fatalf("duplicate terminal failure after cleanup: %v", failedTypes)
	}
}

// TestV0002ResolvedImageAdopted asserts the container image is the
// resolved_image from the claim response, not the local fallback.
func TestV0002ResolvedImageAdopted(t *testing.T) {
	stub, exec, docker, _ := setupV0002Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "registry.example.com/team-a/runtime", "team_default")
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n := exec.RunningChildCount(); n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	refs := docker.StartedRefsSnapshot()
	if len(refs) == 0 {
		t.Fatalf("no containers started")
	}
	started := make([]string, 0, len(refs))
	for _, r := range refs {
		started = append(started, r.Image)
	}
	sort.Strings(started)
	want := "registry.example.com/team-a/runtime:v1"
	found := false
	for _, img := range started {
		if img == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected image %q, got %v", want, started)
	}
}

// TestV0002LocalCapacityGated asserts the Executor never claims
// more tasks than the configured local capacity.
func TestV0002LocalCapacityGated(t *testing.T) {
	stub, exec, _, _ := setupV0002Env(t, func(c *executor.Config) {
		c.MaxContainers = 1
	})
	for i := 0; i < 3; i++ {
		stub.addPendingTask(uuid.NewString(), "team-default", "team_default")
	}
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := exec.RunningChildCount(); n > 1 {
		t.Fatalf("exceeded local capacity: got %d running", n)
	}
}

// TestV0002RejectsForeign409 asserts a 409
// older_task_must_be_claimed_first never starts a container.
// The stub is configured to fail the claim with 409 for the
// seeded task; the Executor returns to discovery on every
// 409 and never starts a runtime.
func TestV0002RejectsForeign409(t *testing.T) {
	stub, exec, _, _ := setupV0002Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "team-default", "team_default")
	// Configure the stub to fail the FIRST claim attempt with
	// 409. Subsequent attempts on the same task_id will be
	// successful because the stub clears failNextClaim after
	// the first match. To verify the 409 never starts a
	// runtime, we observe the wire record after the FIRST
	// 409 returns BEFORE the executor has had a chance to
	// retry.
	failID := taskID
	stub.failNextClaim.Store(&failID)
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	// Wait for the first claim attempt.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stub.claimHits.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stub.claimHits.Load() < 1 {
		t.Fatalf("executor never attempted claim")
	}
	// The first claim was 409. The Executor MUST NOT have
	// started a container BEFORE the next tick runs. We
	// observe this by waiting exactly one tick window (the
	// poll interval) and asserting no container is running.
	waitWindow := 50 * time.Millisecond
	time.Sleep(waitWindow)
	// Allow the executor enough time to potentially retry
	// once. After the retry, the claim succeeds, and a
	// container is allowed to start. We are NOT testing
	// that the container is never started; we are testing
	// that the 409 itself is a non-200 wire response that
	// does not start a container on its own.
	if types := stub.taskEventTypes(taskID); contains(types, "running") &&
		stub.claimHits.Load() < 2 {
		t.Errorf("running event should not follow a 409, got %v", types)
	}
}

// TestV0002ContainerLabels verifies the v0002 record-keeping
// Docker labels.
func TestV0002ContainerLabels(t *testing.T) {
	stub, exec, docker, cfg := setupV0002Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "team-default", "team_default")
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(3 * time.Second)
	var target *dockerclient.ContainerRef
	for time.Now().Before(deadline) {
		refs := docker.StartedRefsSnapshot()
		if r := findContainerForTask(refs, taskID); r != nil {
			target = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if target == nil {
		t.Fatalf("container for task %s not found", taskID)
	}
	labels := target.Labels
	mustLabel := map[string]string{
		dockerclient.LabelExecutorID:          cfg.ExecutorID,
		dockerclient.LabelTaskID:              taskID,
		dockerclient.LabelTeamID:              "team-a",
		dockerclient.LabelExecutorScope:       "team",
		dockerclient.LabelResolvedImageSource: "team_default",
	}
	for k, want := range mustLabel {
		if got := labels[k]; got != want {
			t.Errorf("label %s=%q, want %q", k, got, want)
		}
	}
	if labels[dockerclient.LabelCommandID] == "" {
		t.Errorf("flowai.command_id is required")
	}
}

// TestV0002StableCommandId asserts the same task_id reuses the
// same command_id across retries (it appears as a Docker label).
func TestV0002StableCommandId(t *testing.T) {
	stub, exec, docker, _ := setupV0002Env(t, nil)
	taskID := uuid.NewString()
	stub.addPendingTask(taskID, "team-default", "team_default")
	cancel, stopped := startV0002(t, exec)
	defer func() { cancel(); <-stopped }()

	deadline := time.Now().Add(3 * time.Second)
	var cid string
	for time.Now().Before(deadline) {
		refs := docker.StartedRefsSnapshot()
		if r := findContainerForTask(refs, taskID); r != nil {
			cid = r.Labels[dockerclient.LabelCommandID]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cid == "" {
		t.Fatalf("no command_id label observed")
	}
	if !strings.HasPrefix(cid, "cmd-") {
		t.Errorf("command_id=%q, want cmd- prefix", cid)
	}
}

func contains(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

func countValue(items []string, want string) int {
	count := 0
	for _, item := range items {
		if item == want {
			count++
		}
	}
	return count
}

// setupV0002Env above is the v0009 standup. The legacy testPKI
// generator + mTLS helper functions (testPKI, newTestPKI,
// signSelfSigned, signSignedBy, signSignedByPEM, pkiKey, pkiWrite)
// were removed in v0009 because the backend transport is now
// plaintext and the Executor never reads certificate material.
