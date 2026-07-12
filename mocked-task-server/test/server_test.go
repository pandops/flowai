// Unit tests for the mocked-task-server. Verifies the wire shapes for all
// three surfaces (Router, State Registry, Env Registry) plus idempotency
// and probe handling.
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/mocked-task-server/internal/platform"
	"github.com/flowai/platform/mocked-task-server/internal/server"
	"github.com/flowai/platform/mocked-task-server/internal/store"
)

func newTestServer(t *testing.T) (*server.Server, *store.MemoryStore, *httptest.Server) {
	t.Helper()
	mem := store.NewMemoryStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(mem, logger)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return srv, mem, ts
}

func TestRouterListTasks_NoFilter(t *testing.T) {
	srv, _, ts := newTestServer(t)
	srv.SetRouterTask(&platform.RouterTask{
		TaskID: uuid.NewString(), RoutingTarget: "openhands", AgentRuntime: "openhands",
		Status: platform.TaskStatusQueued, Prompt: "hello",
	})
	srv.SetRouterTask(&platform.RouterTask{
		TaskID: uuid.NewString(), RoutingTarget: "claude", AgentRuntime: "claude",
		Status: platform.TaskStatusQueued, Prompt: "hi",
	})
	resp, err := http.Get(ts.URL + "/v1/tasks")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out platform.TaskListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(out.Tasks))
	}
}

func TestRouterListTasks_WithFilter(t *testing.T) {
	srv, _, ts := newTestServer(t)
	srv.SetRouterTask(&platform.RouterTask{
		TaskID: uuid.NewString(), RoutingTarget: "openhands", AgentRuntime: "openhands",
		Status: platform.TaskStatusQueued, Prompt: "hello",
	})
	srv.SetRouterTask(&platform.RouterTask{
		TaskID: uuid.NewString(), RoutingTarget: "claude", AgentRuntime: "claude",
		Status: platform.TaskStatusQueued, Prompt: "hi",
	})
	resp, err := http.Get(ts.URL + "/v1/tasks?filter=openhands")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	var out platform.TaskListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
	if out.Tasks[0].RoutingTarget != "openhands" {
		t.Fatalf("routing_target mismatch: %s", out.Tasks[0].RoutingTarget)
	}
}

func TestStateRegistryUpsertExecutor(t *testing.T) {
	_, mem, ts := newTestServer(t)
	execID := "exec-" + uuid.NewString()
	body := map[string]any{
		"executor_id":         execID,
		"executor_type":       platform.ExecutorTypeDockerOpenHands,
		"routing_target":      "openhands",
		"capacity":            2,
		"running_child_count": 0,
		"metadata":            map[string]any{},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/executors/"+execID, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}

	req2, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/executors/"+execID, bytes.NewReader(buf))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on re-registration, got %d", resp2.StatusCode)
	}

	got, err := mem.GetExecutor(context.Background(), execID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RoutingTarget != "openhands" {
		t.Fatalf("routing_target mismatch: %s", got.RoutingTarget)
	}
}

func TestStateRegistryAppendExecutorEvent(t *testing.T) {
	_, mem, ts := newTestServer(t)
	execID := "exec-" + uuid.NewString()
	body, _ := json.Marshal(map[string]any{
		"event_id":    uuid.NewString(),
		"executor_id": execID,
		"type":        string(platform.ExecutorEventHealthy),
		"occurred_at": "2026-01-01T00:00:00Z",
		"payload":     map[string]any{},
	})
	resp, err := http.Post(ts.URL+"/v1/executors/"+execID+"/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	evs, err := mem.ListExecutorEvents(context.Background(), execID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if evs[0].Type != platform.ExecutorEventHealthy {
		t.Fatalf("type mismatch: %s", evs[0].Type)
	}
}

func TestStateRegistryAppendTaskEvent(t *testing.T) {
	_, mem, ts := newTestServer(t)
	taskID := uuid.NewString()
	execID := "exec-" + uuid.NewString()
	body, _ := json.Marshal(map[string]any{
		"event_id":    uuid.NewString(),
		"task_id":     taskID,
		"executor_id": execID,
		"source":      "executor",
		"type":        "task.started",
		"occurred_at": "2026-01-01T00:00:00Z",
		"payload":     map[string]any{},
	})
	resp, err := http.Post(ts.URL+"/v1/tasks/"+taskID+"/events", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	evs, err := mem.ListTaskEvents(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
}

func TestEnvRegistryReturnsValues(t *testing.T) {
	srv, _, ts := newTestServer(t)
	srv.SetEnv("scope-1", map[string]string{"FOO": "bar", "BAZ": "qux"})
	u := ts.URL + "/v1/env?executor_id=exec-1&routing_target=openhands&scope_token=scope-1"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Values map[string]string `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Values["FOO"] != "bar" || out.Values["BAZ"] != "qux" {
		t.Fatalf("env mismatch: %+v", out.Values)
	}
}

func TestEnvRegistryNoContent(t *testing.T) {
	_, _, ts := newTestServer(t)
	u := ts.URL + "/v1/env?executor_id=exec-1&routing_target=openhands&scope_token=missing"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestEnvRegistryBadRequestMissingScope(t *testing.T) {
	_, _, ts := newTestServer(t)
	u := ts.URL + "/v1/env?executor_id=exec-1&routing_target=openhands"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "missing scope_token") {
		t.Fatalf("unexpected error body: %s", string(body))
	}
}

func TestIdempotencyKeyMarked(t *testing.T) {
	_, mem, ts := newTestServer(t)
	taskID := uuid.NewString()
	body, _ := json.Marshal(map[string]any{
		"event_id":    uuid.NewString(),
		"task_id":     taskID,
		"executor_id": "exec-1",
		"source":      "executor",
		"type":        "task.started",
		"occurred_at": "2026-01-01T00:00:00Z",
		"payload":     map[string]any{},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/tasks/"+taskID+"/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if !mem.IdempotencySeen("key-123") {
		t.Fatalf("expected idempotency key to be marked")
	}

	// Idempotent replay must NOT append a second event AND must return
	// 202 Accepted with a fresh server-stamped accepted_at.
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/tasks/"+taskID+"/events", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "key-123")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("replay status: %d", resp2.StatusCode)
	}
	var replayOut platform.EventAppendResponse
	if err := json.NewDecoder(resp2.Body).Decode(&replayOut); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replayOut.AcceptedAt.IsZero() {
		t.Fatalf("replay accepted_at zero")
	}
	if replayOut.AcceptedAt.Equal(mustTime("2026-01-01T00:00:00Z")) {
		t.Fatalf("replay accepted_at reused client occurred_at (server must stamp)")
	}
	evs, _ := mem.ListTaskEvents(context.Background(), taskID)
	if len(evs) != 1 {
		t.Fatalf("idempotent replay wrote duplicate events: got %d want 1", len(evs))
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestIdempotencyClaimAtomic exercises the Seen-then-Mark race window.
// Two concurrent requests with the same Idempotency-Key must result in
// exactly one stored event (the second is a replay) and the store's
// ClaimIdempotency must return false to the loser.
func TestIdempotencyClaimAtomic(t *testing.T) {
	_, mem, ts := newTestServer(t)
	taskID := uuid.NewString()
	body, _ := json.Marshal(map[string]any{
		"event_id":    uuid.NewString(),
		"task_id":     taskID,
		"executor_id": "exec-1",
		"source":      "executor",
		"type":        "task.started",
		"occurred_at": "2026-01-01T00:00:00Z",
		"payload":     map[string]any{},
	})

	const N = 16
	done := make(chan struct{}, N)
	results := make(chan int, N)
	for i := 0; i < N; i++ {
		go func() {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/tasks/"+taskID+"/events", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "concurrent-key")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("post: %v", err)
				done <- struct{}{}
				return
			}
			results <- resp.StatusCode
			resp.Body.Close()
			done <- struct{}{}
		}()
	}
	for i := 0; i < N; i++ {
		<-done
	}
	close(results)

	if !mem.IdempotencySeen("concurrent-key") {
		t.Fatalf("expected key to be marked after race")
	}
	evs, _ := mem.ListTaskEvents(context.Background(), taskID)
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 stored event after race, got %d", len(evs))
	}
}

// TestIdempotencyCapBounded verifies the FIFO cap evicts old entries so
// the store cannot grow unboundedly.
func TestIdempotencyCapBounded(t *testing.T) {
	mem := store.NewMemoryStore()
	for i := 0; i < store.IdempotencyCap+128; i++ {
		mem.ClaimIdempotency(fmt.Sprintf("k-%d", i))
	}
	if !mem.IdempotencySeen(fmt.Sprintf("k-%d", store.IdempotencyCap+127)) {
		t.Fatalf("newest key must be present")
	}
	if mem.IdempotencySeen("k-0") {
		t.Fatalf("oldest key should have been evicted by FIFO cap")
	}
}

// TestAppendEventBodyCapRejected confirms a >1MiB body on a mutation
// endpoint is rejected by the platform's MaxBytesReader.
func TestAppendEventBodyCapRejected(t *testing.T) {
	_, _, ts := newTestServer(t)
	taskID := uuid.NewString()
	// Build a body whose total length exceeds the cap by wrapping
	// payload content in repetitive JSON-encoded material.
	longPayload := strings.Repeat("a", int(server.MaxMutationBodyBytes)+1024)
	frame := map[string]any{
		"event_id":    uuid.NewString(),
		"task_id":     taskID,
		"executor_id": "exec-1",
		"source":      "executor",
		"type":        "task.started",
		"occurred_at": "2026-01-01T00:00:00Z",
		"payload":     map[string]any{"junk": longPayload},
	}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if int64(len(body)) <= server.MaxMutationBodyBytes {
		t.Fatalf("payload not large enough: %d <= %d", len(body), server.MaxMutationBodyBytes)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/tasks/"+taskID+"/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 for oversized body, got %d: %s", resp.StatusCode, string(raw))
	}
}

// TestUpsertExecutorBodyCapRejected confirms the same cap applies to
// PUT /v1/executors/{id}.
func TestUpsertExecutorBodyCapRejected(t *testing.T) {
	_, _, ts := newTestServer(t)
	huge := bytes.Repeat([]byte("a"), int(server.MaxMutationBodyBytes)+1024)
	rec := map[string]any{
		"executor_id":         "exec-cap",
		"executor_type":       platform.ExecutorTypeDockerOpenHands,
		"routing_target":      "openhands",
		"capacity":            2,
		"running_child_count": 0,
		"metadata":            map[string]any{"payload": string(huge)},
	}
	body, _ := json.Marshal(rec)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/executors/exec-cap", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", resp.StatusCode)
	}
}

func TestLivezReturnsOK(t *testing.T) {
	_, _, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/livez")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
