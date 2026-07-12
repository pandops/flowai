// Package store provides the ephemeral in-memory state used by the mocked task
// server's Router, State Registry, and Env Registry surfaces.
package store

import (
	"container/list"
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
	// ClaimIdempotency atomically checks whether key was seen and, if
	// not, records it. Returns (true, nil) on first claim, (false,
	// nil) on already-seen. The returned conflict path guarantees no
	// Seen-then-Mark race.
	ClaimIdempotency(key string) (claimed bool)
	IdempotencySeen(key string) bool
}

// IdempotencyCap is the bounded retention for Idempotency-Keys.
// Adapted to ephemeral v0001: a small constant prevents unbounded
// growth from misbehaving clients without dropping useful dedupe
// state during a normal Executor lifecycle.
const IdempotencyCap = 4096

// MemoryStore is the default in-memory store. Concurrent-safe.
type MemoryStore struct {
	mu sync.RWMutex

	executors  map[string]*platform.ExecutorRecord
	execEvents map[string][]*platform.ExecutorEvent // by executor_id
	taskEvents map[string][]*platform.TaskEvent     // by task_id

	// Idempotency keys: FIFO list of keys (oldest at front) plus a
	// map for O(1) lookup. Bounded by IdempotencyCap.
	idemMu   sync.Mutex
	idemList *list.List
	idemMap  map[string]*list.Element
}

// NewMemoryStore constructs a fresh in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		executors:  map[string]*platform.ExecutorRecord{},
		execEvents: map[string][]*platform.ExecutorEvent{},
		taskEvents: map[string][]*platform.TaskEvent{},
		idemList:   list.New(),
		idemMap:    map[string]*list.Element{},
	}
}

// IdempotencySeen returns true if the key was recorded before.
func (s *MemoryStore) IdempotencySeen(key string) bool {
	if key == "" {
		return false
	}
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	_, ok := s.idemMap[key]
	return ok
}

// ClaimIdempotency atomically records the key on first call and
// returns true; subsequent calls return false. The map insert +
// list push happen under a single lock acquisition so two
// concurrent requests cannot both observe "not seen".
func (s *MemoryStore) ClaimIdempotency(key string) (claimed bool) {
	if key == "" {
		// Empty key is a no-op marker; not claimable, not seen.
		return false
	}
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	if _, ok := s.idemMap[key]; ok {
		return false
	}
	elem := s.idemList.PushBack(key)
	s.idemMap[key] = elem
	for s.idemList.Len() > IdempotencyCap {
		front := s.idemList.Front()
		if front == nil {
			break
		}
		s.idemList.Remove(front)
		delete(s.idemMap, front.Value.(string))
	}
	return true
}

// MarkIdempotency records a key for later dedupe checks. Prefer
// ClaimIdempotency for race-safe semantics; this helper is kept for
// the ExecutorStore interface.
func (s *MemoryStore) MarkIdempotency(key string) {
	s.ClaimIdempotency(key)
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
