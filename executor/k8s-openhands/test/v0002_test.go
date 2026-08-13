// Service-local integration tests for the v0005 K8s Executor. These
// tests stand up a v0005 State Registry stub that mimics the
// documented wire shape and assert the K8s-specific Pod lifecycle:
// claim before runtime, resolved_image adopted verbatim, local
// capacity gating, the 409/404 taxonomy, the seven-field task
// event envelopes, the v0005 Pod label set, and the bbolt
// persistence contract.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor/k8s-openhands/internal/cache"
	"github.com/flowai/platform/executor/k8s-openhands/internal/executor"
	fakekube "github.com/flowai/platform/executor/k8s-openhands/internal/mocks/k8s"
	"github.com/flowai/platform/executor/k8s-openhands/internal/stateregistryclient"
)

type v0005TaskRecord struct {
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

type v0005EventRecord struct {
	EventID    string
	TeamID     string
	TaskID     string
	ExecutorID string
	EventType  string
	OccurredAt string
	Payload    json.RawMessage
}

type v0005Stub struct {
	mu         sync.Mutex
	executors  map[string]map[string]any
	tasks      map[string]*v0005TaskRecord
	taskOrder  []string
	taskEvents map[string][]v0005EventRecord
	claimHits  atomic.Int32
}

func newV0005Stub() *v0005Stub {
	return &v0005Stub{
		executors:  map[string]map[string]any{},
		tasks:      map[string]*v0005TaskRecord{},
		taskEvents: map[string][]v0005EventRecord{},
	}
}

func (s *v0005Stub) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/executors/", s.handleExecutor)
	mux.HandleFunc("/v1/tasks/", s.handleTask)
	mux.HandleFunc("/v1/environments/", s.handleEnvironment)
	return mux
}

func (s *v0005Stub) handleExecutor(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/executors/")
	id := path
	suffix := ""
	if idx := strings.Index(path, "/"); idx >= 0 {
		id = path[:idx]
		suffix = path[idx+1:]
	}
	switch {
	case suffix == "tasks" && r.Method == http.MethodGet:
		s.handleDiscover(w, r, id)
	case suffix == "claim" && r.Method == http.MethodPost:
		s.claimHits.Add(1)
		s.handleClaim(w, r, id)
	case suffix == "events" && r.Method == http.MethodPost:
		s.handleSelfEvent(w, r, id)
	case suffix == "" && r.Method == http.MethodPut:
		s.handleRegister(w, r, id)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *v0005Stub) handleRegister(w http.ResponseWriter, r *http.Request, id string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeV0005Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	s.executors[id] = body
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
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
	})
}

func (s *v0005Stub) handleDiscover(w http.ResponseWriter, r *http.Request, id string) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		writeV0005Error(w, r, http.StatusBadRequest, "invalid_request", "tag is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []map[string]any{}
	for _, taskID := range s.taskOrder {
		t := s.tasks[taskID]
		if t.CurrentState != "pending" || t.RequiredTag != tag {
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

func (s *v0005Stub) handleClaim(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		TaskID    string `json:"task_id"`
		CommandID string `json:"command_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeV0005Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	t, ok := s.tasks[req.TaskID]
	if !ok {
		s.mu.Unlock()
		writeV0005Error(w, r, http.StatusNotFound, "task_not_found", "task is unknown")
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
			writeV0005Error(w, r, http.StatusConflict, "older_task_must_be_claimed_first", "")
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
		writeV0005Error(w, r, http.StatusConflict, "task_already_claimed", "")
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
	s.taskEvents[t.TaskID] = append(s.taskEvents[t.TaskID], v0005EventRecord{
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

func (s *v0005Stub) buildClaimResponseLocked(t *v0005TaskRecord) map[string]any {
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

func (s *v0005Stub) handleTask(w http.ResponseWriter, r *http.Request) {
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

func (s *v0005Stub) handleTaskEvent(w http.ResponseWriter, r *http.Request, taskID string) {
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
		writeV0005Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	s.taskEvents[taskID] = append(s.taskEvents[taskID], v0005EventRecord{
		EventID: ev.EventID, TeamID: ev.TeamID, TaskID: ev.TaskID, ExecutorID: ev.ExecutorID,
		EventType: ev.EventType, OccurredAt: ev.OccurredAt, Payload: ev.Payload,
	})
	s.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"event_id":    ev.EventID,
		"team_id":     ev.TeamID,
		"accepted_at": time.Now().UTC(),
	})
}

func (s *v0005Stub) handleSelfEvent(w http.ResponseWriter, r *http.Request, id string) {
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"event_id":    "evt-self",
		"accepted_at": time.Now().UTC(),
	})
}

func (s *v0005Stub) handleEnvironment(w http.ResponseWriter, r *http.Request) {
	envID := strings.TrimPrefix(r.URL.Path, "/v1/environments/")
	if idx := strings.Index(envID, "/"); idx >= 0 {
		envID = envID[:idx]
	}
	taskID := r.URL.Query().Get("task_id")
	if envID == "" || taskID == "" {
		writeV0005Error(w, r, http.StatusNotFound, "environment_unknown_or_unavailable", "")
		return
	}
	s.mu.Lock()
	t, ok := s.tasks[taskID]
	s.mu.Unlock()
	if !ok || t.EnvironmentID != envID {
		writeV0005Error(w, r, http.StatusNotFound, "environment_unknown_or_unavailable", "")
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

func writeV0005Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": code, "message": message, "request_id": r.Header.Get("X-Request-Id"),
	})
}

func (s *v0005Stub) addPendingTask(taskID, image, imageSource string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := &v0005TaskRecord{
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

func (s *v0005Stub) taskEventTypes(taskID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, ev := range s.taskEvents[taskID] {
		out = append(out, ev.EventType)
	}
	return out
}

func registryClientFor(cfg *executor.Config) (*stateregistryclient.Client, error) {
	return stateregistryclient.New(cfg.StateRegistryURL, stateregistryclient.Identity{
		ExecutorID: cfg.ExecutorID,
		Scope:      cfg.Scope,
		TeamID:     cfg.TeamID,
	}, nil)
}

func setupK8sV0005Env(t *testing.T, mutate func(*executor.Config)) (*v0005Stub, *executor.Executor, *fakekube.FakeClient, *executor.Config) {
	t.Helper()
	stub := newV0005Stub()
	stubSrv := httptest.NewServer(stub.routes())
	t.Cleanup(stubSrv.Close)

	cfg := &executor.Config{
		ExecutorID:           "exec-" + uuid.NewString(),
		ExecutorAPIBind:      "127.0.0.1:0",
		MaxPods:              2,
		OpenHandsImage:       "ghcr.io/openhands/agent-server:latest-python",
		OpenHandsPort:        8000,
		ImagePullPolicy:      "if-not-present",
		LogLevel:             "error",
		StateRegistryURL:     stubSrv.URL,
		Scope:                "team",
		TeamID:               "team-a",
		AuthorizedTag:        "openhands",
		PollInterval:         30 * time.Millisecond,
		Namespace:            "flowai",
		ServiceAccount:       "executor-k8s-openhands",
		StorageClassName:     "flowai-local-path",
		CacheDir:             t.TempDir(),
		OpenHandsWorkspace:   "/workspace/project",
		OpenHandsLLMModel:    "test-model",
		OpenHandsLLMAPIKey:   "test-key",
		OpenHandsLLMUsageID:  "flowai-executor",
		FinishedCleanupDelay: 0,
		FailedCleanupDelay:   0,
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	store, err := cache.Open(cfg.CacheDir)
	if err != nil {
		t.Fatalf("cache open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lock, err := store.LockFile()
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	registry, err := registryClientFor(cfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	kube := fakekube.NewFakeClient()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exec := executor.New(cfg, registry, kube, store, lock, func(format string, args ...any) {
		logger.Info(fmt.Sprintf(format, args...))
	})
	return stub, exec, kube, cfg
}

func startK8sExec(t *testing.T, e *executor.Executor) (context.CancelFunc, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = e.Run(ctx)
	}()
	return cancel, stopped
}

func containsString(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}
