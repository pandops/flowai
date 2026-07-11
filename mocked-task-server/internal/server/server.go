// Package server implements the v0001 mocked task server. It serves three
// surfaces under /v1: Router (task listing), State Registry (executor
// registration + events), Env Registry (env reads). All surfaces share the
// platform error envelope and request-id correlation.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/flowai/platform/mocked-task-server/internal/httpapi"
	"github.com/flowai/platform/mocked-task-server/internal/logging"
	"github.com/flowai/platform/mocked-task-server/internal/platform"
	"github.com/flowai/platform/mocked-task-server/internal/store"
)

// Server is the mocked task server.
type Server struct {
	store  store.ExecutorStore
	logger *slog.Logger

	mu    sync.RWMutex
	tasks map[string]*platform.RouterTask
	envs  map[string]map[string]string
}

// New constructs a new Server backed by store and logger.
func New(s store.ExecutorStore, logger *slog.Logger) *Server {
	return &Server{
		store:  s,
		logger: logger,
		tasks:  map[string]*platform.RouterTask{},
		envs:   map[string]map[string]string{},
	}
}

// Routes returns a chi.Mux with all three surfaces mounted under /v1.
func (s *Server) Routes() http.Handler {
	r := httpapi.NewRouter("mocked-task-server", s.logger)
	r.Route("/v1", func(v1 chi.Router) {
		v1.Get("/tasks", s.listTasks)
		v1.Put("/executors/{executor_id}", s.upsertExecutor)
		v1.Post("/executors/{executor_id}/events", s.appendExecutorEvent)
		v1.Post("/tasks/{task_id}/events", s.appendTaskEvent)
		v1.Get("/env", s.getEnv)

		httpapi.RegisterProbes(v1, "mocked-task-server", "", httpapi.ReadinessFunc(func() bool { return true }))

		// Test-only endpoints. Only enabled when the MOCKED_SERVER_TEST_MODE
		// env var is set on the server process. These exist so e2e tests
		// (autotest/) can seed Router tasks without bypassing the wire
		// protocol.
		if os.Getenv("MOCKED_SERVER_TEST_MODE") == "true" {
			v1.Post("/_test/tasks", s.testSeedTask)
			v1.Delete("/_test/tasks", s.testClearTasks)
			v1.Delete("/_test/tasks/{task_id}", s.testDeleteTask)
		}
	})
	return r
}

func (s *Server) testSeedTask(w http.ResponseWriter, r *http.Request) {
	var t platform.RouterTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		httpapi.BadRequest(w, r, "invalid task body: "+err.Error(), nil)
		return
	}
	if t.TaskID == "" {
		httpapi.BadRequest(w, r, "task_id is required", nil)
		return
	}
	s.SetRouterTask(&t)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"task_id": t.TaskID})
}

func (s *Server) testClearTasks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.tasks = map[string]*platform.RouterTask{}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "task_id")
	if taskID == "" {
		httpapi.BadRequest(w, r, "missing task_id", nil)
		return
	}
	s.DeleteRouterTask(taskID)
	w.WriteHeader(http.StatusNoContent)
}

// SetRouterTask allows tests/operators to seed Router tasks.
func (s *Server) SetRouterTask(t *platform.RouterTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.TaskID] = t
}

// DeleteRouterTask removes a Router task.
func (s *Server) DeleteRouterTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, taskID)
}

// SetEnv allows tests/operators to seed Env Registry scopes.
func (s *Server) SetEnv(scopeToken string, kv map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.envs[scopeToken] = kv
}

// ListExecutors returns all active Executor records (copy).
func (s *Server) ListExecutors(ctx context.Context) ([]*platform.ExecutorRecord, error) {
	return s.store.ListExecutors(ctx)
}

// GetExecutor returns a single Executor record by id.
func (s *Server) GetExecutor(ctx context.Context, id string) (*platform.ExecutorRecord, error) {
	return s.store.GetExecutor(ctx, id)
}

// ListExecutorEvents returns events for the given executor.
func (s *Server) ListExecutorEvents(ctx context.Context, id string) ([]*platform.ExecutorEvent, error) {
	return s.store.ListExecutorEvents(ctx, id)
}

// ListTaskEvents returns events for the given task.
func (s *Server) ListTaskEvents(ctx context.Context, id string) ([]*platform.TaskEvent, error) {
	return s.store.ListTaskEvents(ctx, id)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]platform.RouterTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		if filter != "" && t.RoutingTarget != filter {
			continue
		}
		out = append(out, *t)
	}
	httpapi.JSON(w, r, http.StatusOK, map[string]any{"tasks": out})
}

func (s *Server) upsertExecutor(w http.ResponseWriter, r *http.Request) {
	execID := chi.URLParam(r, "executor_id")
	if execID == "" {
		httpapi.BadRequest(w, r, "missing executor_id", nil)
		return
	}
	var req platform.ExecutorRecord
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.BadRequest(w, r, "invalid json body: "+err.Error(), nil)
		return
	}
	if req.ExecutorID == "" {
		req.ExecutorID = execID
	}
	if req.ExecutorID != execID {
		httpapi.BadRequest(w, r, "executor_id mismatch between path and body", nil)
		return
	}
	existing, _ := s.store.GetExecutor(r.Context(), execID)
	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	if err := s.store.UpsertExecutor(r.Context(), &req); err != nil {
		httpapi.Internal(w, r, err.Error())
		return
	}
	resp := platform.ExecutorRegistrationResponse{
		ExecutorID:   req.ExecutorID,
		RegisteredAt: req.RegisteredAt,
	}
	if resp.RegisteredAt.IsZero() {
		resp.RegisteredAt = time.Now().UTC()
	}
	httpapi.JSON(w, r, status, resp)
}

func (s *Server) appendExecutorEvent(w http.ResponseWriter, r *http.Request) {
	execID := chi.URLParam(r, "executor_id")
	if execID == "" {
		httpapi.BadRequest(w, r, "missing executor_id", nil)
		return
	}
	var req platform.ExecutorEvent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.BadRequest(w, r, "invalid json body: "+err.Error(), nil)
		return
	}
	if req.ExecutorID == "" {
		req.ExecutorID = execID
	}
	if req.ExecutorID != execID {
		httpapi.BadRequest(w, r, "executor_id mismatch between path and body", nil)
		return
	}
	if req.EventID == "" {
		req.EventID = uuid.NewString()
	}
	if idem := r.Header.Get("Idempotency-Key"); idem != "" {
		s.store.MarkIdempotency(idem)
	}
	if err := s.store.AppendExecutorEvent(r.Context(), &req); err != nil {
		httpapi.Internal(w, r, err.Error())
		return
	}
	httpapi.JSON(w, r, http.StatusAccepted, platform.EventAppendResponse{
		EventID:    req.EventID,
		AcceptedAt: req.OccurredAt,
	})
}

func (s *Server) appendTaskEvent(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "task_id")
	if taskID == "" {
		httpapi.BadRequest(w, r, "missing task_id", nil)
		return
	}
	var req platform.TaskEvent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.BadRequest(w, r, "invalid json body: "+err.Error(), nil)
		return
	}
	if req.TaskID == "" {
		req.TaskID = taskID
	}
	if req.TaskID != taskID {
		httpapi.BadRequest(w, r, "task_id mismatch between path and body", nil)
		return
	}
	if req.EventID == "" {
		req.EventID = uuid.NewString()
	}
	if idem := r.Header.Get("Idempotency-Key"); idem != "" {
		s.store.MarkIdempotency(idem)
	}
	if err := s.store.AppendTaskEvent(r.Context(), &req); err != nil {
		httpapi.Internal(w, r, err.Error())
		return
	}
	httpapi.JSON(w, r, http.StatusAccepted, platform.EventAppendResponse{
		EventID:    req.EventID,
		AcceptedAt: req.OccurredAt,
	})
}

func (s *Server) getEnv(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := q.Get("scope_token")
	if scope == "" {
		httpapi.BadRequest(w, r, "missing scope_token", nil)
		return
	}
	if q.Get("executor_id") == "" || q.Get("routing_target") == "" {
		httpapi.BadRequest(w, r, "missing executor_id or routing_target", nil)
		return
	}
	s.mu.RLock()
	kv, ok := s.envs[scope]
	s.mu.RUnlock()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpapi.JSON(w, r, http.StatusOK, map[string]any{"values": kv})
}

// Silence the linter when this file is unused during type-only checks.
var _ = logging.FromContext