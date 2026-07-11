// Package store provides the in-memory + optional PostgreSQL persistence used
// by the mocked task server. The State Registry surface persists through this
// store; the Router and Env Registry surfaces are in-memory only.
package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/flowai/platform/mocked-task-server/internal/platform"
)

// ErrNotFound is returned when a requested record is missing.
var ErrNotFound = errors.New("not found")

// ExecutorStore is the storage contract for the State Registry surface.
type ExecutorStore interface {
	UpsertExecutor(ctx context.Context, rec *platform.ExecutorRecord) error
	GetExecutor(ctx context.Context, executorID string) (*platform.ExecutorRecord, error)
	ListExecutors(ctx context.Context) ([]*platform.ExecutorRecord, error)
	AppendExecutorEvent(ctx context.Context, ev *platform.ExecutorEvent) error
	ListExecutorEvents(ctx context.Context, executorID string) ([]*platform.ExecutorEvent, error)
	AppendTaskEvent(ctx context.Context, ev *platform.TaskEvent) error
	ListTaskEvents(ctx context.Context, taskID string) ([]*platform.TaskEvent, error)
	MarkIdempotency(key string)
	IdempotencySeen(key string) bool
}

// MemoryStore is the default in-memory store. Concurrent-safe.
type MemoryStore struct {
	mu sync.RWMutex

	executors   map[string]*platform.ExecutorRecord
	execEvents  map[string][]*platform.ExecutorEvent // by executor_id
	taskEvents  map[string][]*platform.TaskEvent     // by task_id

	// Idempotency keys. Maps key -> recordedAt.
	idempotency map[string]time.Time
}

// NewMemoryStore constructs a fresh in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		executors:   map[string]*platform.ExecutorRecord{},
		execEvents:  map[string][]*platform.ExecutorEvent{},
		taskEvents:  map[string][]*platform.TaskEvent{},
		idempotency: map[string]time.Time{},
	}
}

// IdempotencySeen returns true if the key was recorded before (and within retention).
func (s *MemoryStore) IdempotencySeen(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.idempotency[key]; ok {
		return true
	}
	return false
}

// MarkIdempotency records a key for later dedupe checks.
func (s *MemoryStore) MarkIdempotency(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idempotency[key] = time.Now().UTC()
}

// UpsertExecutor creates or replaces an active Executor record.
func (s *MemoryStore) UpsertExecutor(ctx context.Context, rec *platform.ExecutorRecord) error {
	if rec == nil || rec.ExecutorID == "" {
		return errors.New("executor record missing executor_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec.RegisteredAt.IsZero() {
		rec.RegisteredAt = time.Now().UTC()
	}
	s.executors[rec.ExecutorID] = rec
	return nil
}

// GetExecutor loads an active Executor record.
func (s *MemoryStore) GetExecutor(ctx context.Context, executorID string) (*platform.ExecutorRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.executors[executorID]
	if !ok {
		return nil, ErrNotFound
	}
	return rec, nil
}

// ListExecutors returns all active Executor records (copy).
func (s *MemoryStore) ListExecutors(ctx context.Context) ([]*platform.ExecutorRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*platform.ExecutorRecord, 0, len(s.executors))
	for _, rec := range s.executors {
		out = append(out, rec)
	}
	return out, nil
}

// AppendExecutorEvent appends one Executor lifecycle event.
func (s *MemoryStore) AppendExecutorEvent(ctx context.Context, ev *platform.ExecutorEvent) error {
	if ev == nil || ev.ExecutorID == "" {
		return errors.New("executor event missing executor_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	s.execEvents[ev.ExecutorID] = append(s.execEvents[ev.ExecutorID], ev)
	return nil
}

// ListExecutorEvents returns events for an executor, newest-last.
func (s *MemoryStore) ListExecutorEvents(ctx context.Context, executorID string) ([]*platform.ExecutorEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	evs := s.execEvents[executorID]
	out := make([]*platform.ExecutorEvent, len(evs))
	copy(out, evs)
	return out, nil
}

// AppendTaskEvent appends one task lifecycle event.
func (s *MemoryStore) AppendTaskEvent(ctx context.Context, ev *platform.TaskEvent) error {
	if ev == nil || ev.TaskID == "" {
		return errors.New("task event missing task_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	s.taskEvents[ev.TaskID] = append(s.taskEvents[ev.TaskID], ev)
	return nil
}

// ListTaskEvents returns events for a task, newest-last.
func (s *MemoryStore) ListTaskEvents(ctx context.Context, taskID string) ([]*platform.TaskEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	evs := s.taskEvents[taskID]
	out := make([]*platform.TaskEvent, len(evs))
	copy(out, evs)
	return out, nil
}