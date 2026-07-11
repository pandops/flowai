// Integration tests for the executor-docker service.
//
// These tests exercise the executor's full lifecycle (registration,
// task forwarding, event streaming, interrupt/message handling, graceful
// shutdown) against a stub HTTP server that mimics the mocked-task-server
// wire format. The stub is a pure inline httptest server — it does NOT
// import the mocked-task-server package, because that would violate Go's
// internal-package boundary between sibling services.
//
// The Docker client and OpenHands runtime are stubbed by the in-package
// fakes under executor-docker/internal/mocks.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/dockerclient"
	"github.com/flowai/platform/executor-docker/internal/executor"
	"github.com/flowai/platform/executor-docker/internal/mockedclient"
	fakedocker "github.com/flowai/platform/executor-docker/internal/mocks/docker"
	fakeoh "github.com/flowai/platform/executor-docker/internal/mocks/openhands"
)

// stubTask is the wire shape of a Router task that the inline stub server
// returns from GET /v1/tasks. It mirrors platform.RouterTask from the
// OpenAPI contract.
type stubTask struct {
	TaskID         string                   `json:"task_id"`
	RoutingTarget  string                   `json:"routing_target"`
	AgentRuntime   string                   `json:"agent_runtime"`
	Status         string                   `json:"status"`
	Prompt         string                   `json:"prompt"`
	Metadata       map[string]interface{}   `json:"metadata,omitempty"`
	PendingActions []map[string]interface{} `json:"pending_actions,omitempty"`
}

// stubExecutorRecord mirrors platform.ExecutorRegistrationRequest.
type stubExecutorRecord struct {
	ExecutorID        string                 `json:"executor_id"`
	ExecutorType      string                 `json:"executor_type"`
	RoutingTarget     string                 `json:"routing_target"`
	Capacity          int                    `json:"capacity"`
	RunningChildCount int                    `json:"running_child_count"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// stubEvent mirrors platform.ExecutorEvent and platform.TaskEvent request bodies.
type stubEvent struct {
	EventID    string                 `json:"event_id"`
	ExecutorID string                 `json:"executor_id,omitempty"`
	TaskID     string                 `json:"task_id,omitempty"`
	Source     string                 `json:"source,omitempty"`
	Type       string                 `json:"type"`
	OccurredAt time.Time              `json:"occurred_at"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

// stubServer is a minimal inline HTTP server that mimics the mocked-task-server
// wire format. It records every PUT/POST and serves queued tasks on GET.
type stubServer struct {
	mu         sync.Mutex
	tasks      map[string]*stubTask
	executors  map[string]*stubExecutorRecord
	execEvents []stubEvent
	taskEvents map[string][]stubEvent

	registerHits  atomic.Int32
	execEventHits atomic.Int32
	taskEventHits atomic.Int32
	listHits      atomic.Int32
}

func newStubServer() *stubServer {
	return &stubServer{
		tasks:      map[string]*stubTask{},
		executors:  map[string]*stubExecutorRecord{},
		taskEvents: map[string][]stubEvent{},
	}
}

func (s *stubServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks", s.handleTasks)
	mux.HandleFunc("/v1/tasks/", s.handleTaskEventsByPath)
	mux.HandleFunc("/v1/executors/", s.handleExecutors)
	mux.HandleFunc("/v1/env", s.handleEnv)
	return mux
}

func (s *stubServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	s.listHits.Add(1)
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	filter := r.URL.Query().Get("filter")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []stubTask{}
	for _, t := range s.tasks {
		if filter != "" && t.RoutingTarget != filter {
			continue
		}
		out = append(out, *t)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"tasks": out})
}

func (s *stubServer) handleExecutors(w http.ResponseWriter, r *http.Request) {
	// /v1/executors/{id}  PUT (register) or POST /events (append event)
	path := r.URL.Path[len("/v1/executors/"):]
	for i, c := range path {
		if c == '/' {
			id := path[:i]
			suffix := path[i+1:]
			switch suffix {
			case "events":
				s.execEventHits.Add(1)
				var ev stubEvent
				if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				ev.ExecutorID = id
				s.mu.Lock()
				s.execEvents = append(s.execEvents, ev)
				s.mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"event_id": ev.EventID})
				return
			}
			http.NotFound(w, r)
			return
		}
	}
	// PUT /v1/executors/{id}
	if r.Method == http.MethodPut {
		s.registerHits.Add(1)
		var rec stubExecutorRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rec.ExecutorID = path
		s.mu.Lock()
		_, existed := s.executors[path]
		s.executors[path] = &rec
		s.mu.Unlock()
		status := http.StatusCreated
		if existed {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"executor_id":   path,
			"registered_at": time.Now().UTC(),
		})
		return
	}
	http.NotFound(w, r)
}

// handleExecutors also serves POST /v1/tasks/{id}/events (we route that here
// because the executor also appends task events under /v1/executors/{id}/events).
func (s *stubServer) handleTaskEvents(taskID string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	s.taskEventHits.Add(1)
	var ev stubEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ev.TaskID = taskID
	s.mu.Lock()
	s.taskEvents[taskID] = append(s.taskEvents[taskID], ev)
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"event_id": ev.EventID})
}

func (s *stubServer) handleTaskEventsByPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path[len("/v1/tasks/"):]
	taskID := path
	if idx := strings.Index(path, "/"); idx >= 0 {
		taskID = path[:idx]
	}
	s.taskEventHits.Add(1)
	var ev stubEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ev.TaskID = taskID
	s.mu.Lock()
	s.taskEvents[taskID] = append(s.taskEvents[taskID], ev)
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"event_id": ev.EventID})
}

func (s *stubServer) handleEnv(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *stubServer) setTask(t *stubTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.TaskID] = t
}

func (s *stubServer) listTaskEvents(taskID string) []stubEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubEvent, len(s.taskEvents[taskID]))
	copy(out, s.taskEvents[taskID])
	return out
}

func (s *stubServer) executorRegistered() bool {
	return s.registerHits.Load() > 0
}

// taskEventTypes returns the types of recorded task events for a given task_id.
func (s *stubServer) taskEventTypes(taskID string) []string {
	events := s.listTaskEvents(taskID)
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// testEnv bundles the stub server, executor, fakes, and logger for one test.
type testEnv struct {
	cfg     *executor.Config
	docker  *fakedocker.FakeDocker
	oh      *fakeoh.FakeOpenHands
	exec    *executor.Executor
	logger  *slog.Logger
	stub    *stubServer
	stopped chan struct{}
}

func setupTestEnv(t *testing.T, mutate func(*executor.Config)) *testEnv {
	t.Helper()
	stub := newStubServer()
	ts := httptest.NewServer(stub.routes())
	t.Cleanup(ts.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &executor.Config{
		ExecutorID:           "exec-" + uuid.NewString(),
		ExecutorAPIBind:      "127.0.0.1:0",
		RoutingTarget:        "openhands",
		MaxContainers:        2,
		DockerSocketPath:     "/var/run/docker.sock",
		OpenHandsImage:       "ghcr.io/openhands/agent-server:latest-python",
		OpenHandsPortStart:   19000,
		OpenHandsPortEnd:     19099,
		OpenHandsInterruptEP: "/api/conversations/{conversation_id}/pause",
		OpenHandsMessageEP:   "/api/conversations/{conversation_id}/events",
		OpenHandsMessageType: "message",
		OpenHandsInterruptTO: 2 * time.Second,
		OpenHandsStartupTO:   2 * time.Second,
		OpenHandsDrainTO:     2 * time.Second,
		ImagePullPolicy:      "if-not-present",
		LogLevel:             "error",
		MockedServerURL:      ts.URL + "/v1",
		EnvScopeToken:        "default-scope",
		PollInterval:         50 * time.Millisecond,
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	docker := fakedocker.NewFakeDocker()
	mc := mockedclient.New(cfg.MockedServerURL, nil)
	exec := executor.New(cfg, docker, mc, logger)
	return &testEnv{
		cfg:     cfg,
		docker:  docker,
		oh:      &fakeoh.FakeOpenHands{},
		exec:    exec,
		logger:  logger,
		stub:    stub,
		stopped: make(chan struct{}),
	}
}

func startFakeOH(t *testing.T, env *testEnv, port int) {
	t.Helper()
	env.oh.StartOn(fmt.Sprintf("127.0.0.1:%d", port))
	env.docker.URLOverrides[port] = env.oh.URL()
}

func runExecutor(t *testing.T, env *testEnv) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(env.stopped)
		_ = env.exec.Run(ctx)
	}()
	return cancel
}

func makeQueuedTask(taskID string) *stubTask {
	return &stubTask{
		TaskID:        taskID,
		RoutingTarget: "openhands",
		AgentRuntime:  "openhands",
		Status:        "queued",
		Prompt:        "hello",
	}
}

func makeRunningTaskWithInterrupt(taskID, reason string) *stubTask {
	return &stubTask{
		TaskID:        taskID,
		RoutingTarget: "openhands",
		AgentRuntime:  "openhands",
		Status:        "running",
		PendingActions: []map[string]interface{}{
			{"action_id": uuid.NewString(), "type": "interrupt_task", "task_id": taskID, "reason": reason},
		},
	}
}

func makeRunningTaskWithMessage(taskID, content string) *stubTask {
	return &stubTask{
		TaskID:        taskID,
		RoutingTarget: "openhands",
		AgentRuntime:  "openhands",
		Status:        "running",
		PendingActions: []map[string]interface{}{
			{"action_id": uuid.NewString(), "type": "append_task_message", "task_id": taskID, "content": content, "role": "user"},
		},
	}
}

func TestStartupCleanupRemovesLeftover(t *testing.T) {
	env := setupTestEnv(t, nil)
	_, _ = env.docker.StartContainer(context.Background(), dockerclient.ContainerSpec{
		Name: "leftover", Image: env.cfg.OpenHandsImage,
		Labels: map[string]string{dockerclient.LabelExecutorID: env.cfg.ExecutorID},
	})
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.stub.executorRegistered() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !env.stub.executorRegistered() {
		t.Fatalf("executor never registered with stub server")
	}
}

func TestCapacityLimitsContainerCount(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 2
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	for i := 0; i < 4; i++ {
		env.stub.setTask(makeQueuedTask(uuid.NewString()))
	}
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := env.exec.RunningChildCount(); got > 2 {
		t.Fatalf("executor started %d containers, exceeds capacity 2", got)
	}
}

func TestInterruptRoutesToOpenHands(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 50 * time.Millisecond
		c.OpenHandsInterruptTO = 1 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "stop"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.oh.PauseCallCount() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if env.oh.PauseCallCount() == 0 {
		t.Fatalf("expected OpenHands pause to be called")
	}
}

func TestAppendTaskMessageRoutesToOpenHands(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	env.stub.setTask(makeRunningTaskWithMessage(taskID, "follow up"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		appendCalls, _ := env.oh.AppendSnapshot()
		if appendCalls > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	appendCalls, lastAppendBody := env.oh.AppendSnapshot()
	if appendCalls == 0 {
		t.Fatalf("expected OpenHands append to be called")
	}
	if lastAppendBody["content"] != "follow up" {
		t.Fatalf("append body mismatch: %+v", lastAppendBody)
	}
}

func TestHealthCheckTimeoutMarksTaskFailed(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsStartupTO = 200 * time.Millisecond
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusInternalServerError

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	// Wait for the executor to record task.failed (or hit timeout).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		types := env.stub.taskEventTypes(taskID)
		for _, ty := range types {
			if ty == "task.failed" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	types := env.stub.taskEventTypes(taskID)
	t.Fatalf("expected task.failed event, got: %v (hits=%d)", types, env.stub.taskEventHits.Load())
}

func TestGracefulShutdownDrainsContainers(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsDrainTO = 1 * time.Second
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-env.stopped:
	case <-time.After(3 * time.Second):
		t.Fatalf("executor did not stop within 3 seconds")
	}
	if env.exec.RunningChildCount() != 0 {
		t.Fatalf("expected 0 running containers after drain, got %d", env.exec.RunningChildCount())
	}
}

func TestDockerEventDieDoesNotFailOtherContainers(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 2
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	taskIDs := []string{uuid.NewString(), uuid.NewString()}
	for _, id := range taskIDs {
		env.stub.setTask(makeQueuedTask(id))
	}
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var started atomic.Int32
	var firstName string
	for _, r := range env.docker.StartedRefsSnapshot() {
		if firstName == "" {
			firstName = r.Name
		}
		started.Add(1)
	}
	if started.Load() == 0 {
		t.Skip("no containers started")
	}
	env.docker.EmitDie(firstName)
	time.Sleep(200 * time.Millisecond)
	if env.exec.RunningChildCount() > 2 {
		t.Fatalf("too many running after die: %d", env.exec.RunningChildCount())
	}
}

func TestExecutorRegistersAndJournalsStartupEvents(t *testing.T) {
	env := setupTestEnv(t, nil)
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.stub.registerHits.Load() > 0 && env.stub.execEventHits.Load() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if env.stub.registerHits.Load() == 0 {
		t.Fatalf("executor never registered")
	}
	if env.stub.execEventHits.Load() < 2 {
		t.Fatalf("expected at least 2 executor events (registered, healthy), got %d", env.stub.execEventHits.Load())
	}
}
