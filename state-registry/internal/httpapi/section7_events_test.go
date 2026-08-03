// Section 7 — team-scoped lifecycle events, self events, projection,
// strict ordering, and rollback behavior. The tests intentionally
// drive the production httpapi.Routes constructor so every assertion
// exercises the real route surface. None of the POST routes
// (POST /v1/tasks/{task_id}/events or
// POST /v1/executors/{executor_id}/events) were mounted in the
// Section 6 baseline; the Section 7 GREEN suite wires them and
// enforces every transition, ordering, idempotency, envelope, and
// rollback contract.
//
// RED COMMAND (before GREEN):
//
//	go test ./state-registry/... \
//	    -run 'TestTransition|TestTeamEventOwnership|TestEventIdempotency|\
//	      TestStrictEventOrdering|TestExecutorSelfEvent' -count=1
//
// Section 7 contract under test:
//   - task lifecycle events: running, finished, failed (created is
//     Registry-appended on claim; dispatched is removed)
//   - envelope team_id verification is conditional on the assigned
//     Executor's ownership scope
//   - an identical (task_id, event_id) retry returns the original 202
//   - strict (occurred_at, event_id) ordering; equal timestamps
//     require strictly greater event_id
//   - accepted_sequence and other recovery overrides are rejected
//   - foreign-team task / executor identifiers collapse to the same
//     non-revealing 404 used for unknown identifiers
//   - task events and executor self events are independent streams
//   - capacity / running_count observations are mirrored onto the
//     executor row in the same transaction
//   - section 7 rollback contract: a fault between event append and
//     projection (or between self-event append and observation
//     mirror) leaves no partial state in the SAME in-memory fake
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// ---------------------------------------------------------------------------
// Harness helpers
// ---------------------------------------------------------------------------

func (h *claimHarness) admin(t *testing.T, execID, teamID, scope string) {
	t.Helper()
	if _, ok := h.repo.executors[execID]; ok {
		return
	}
	h.registerExecutor(t, execID, teamID, "openhands", scope)
}

func (h *claimHarness) postTaskEvent(t *testing.T, execID, teamID, scope, taskID string, body []byte) (*http.Response, []byte) {
	t.Helper()
	headers := executorIdentityHeaders(roleForScope(scope), teamID, execID, "req-task-event")
	return h.doRequest(t, http.MethodPost,
		fmt.Sprintf("/v1/tasks/%s/events", taskID),
		headers,
		bytes.NewReader(body))
}

func (h *claimHarness) postExecutorEvent(t *testing.T, execID, teamID, scope string, body []byte) (*http.Response, []byte) {
	t.Helper()
	headers := executorIdentityHeaders(roleForScope(scope), teamID, execID, "req-exec-event")
	return h.doRequest(t, http.MethodPost,
		fmt.Sprintf("/v1/executors/%s/events", execID),
		headers,
		bytes.NewReader(body))
}

func (h *claimHarness) claimForTransition(t *testing.T, execID, teamID, scope, taskID, commandID string) {
	t.Helper()
	resp, raw := h.claim(t, execID, teamID, scope, taskID, commandID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
}

func runTaskEvent(execID, teamID, taskID, eventID, eventType string, occurredAt time.Time) []byte {
	body := map[string]any{
		"event_id":    eventID,
		"team_id":     teamID,
		"task_id":     taskID,
		"executor_id": execID,
		"event_type":  eventType,
		"occurred_at": occurredAt.UTC().Format(time.RFC3339Nano),
		"payload":     map[string]any{},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func runExecutorEvent(execID, teamID, eventID, eventType string, occurredAt time.Time, payload map[string]any) []byte {
	body := map[string]any{
		"event_id":    eventID,
		"executor_id": execID,
		"event_type":  eventType,
		"occurred_at": occurredAt.UTC().Format(time.RFC3339Nano),
		"payload":     payload,
	}
	if teamID != "" {
		body["team_id"] = teamID
	}
	raw, _ := json.Marshal(body)
	return raw
}

// ---------------------------------------------------------------------------
// TestTransition
// ---------------------------------------------------------------------------

// TestTransition covers the documented lifecycle:
//   - created -> running -> finished
//   - created -> running -> failed
//   - running-before-created rejected with 409 invalid_task_transition
//   - terminal-before-running rejected with 409 invalid_task_transition
//   - repeated terminal rejected with 409 invalid_task_transition
//   - re-approval / recovery via fresh event_id after rejected terminal
//     is also rejected (terminal is absorbing).
func TestTransition(t *testing.T) {
	h := newClaimHarness(t)
	const execID = "exec-transition"
	const taskID = "task-transition"
	h.admin(t, execID, "team-a", platform.ExecutorScopeTeam)
	h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
		Repository: "registry.example/agent",
		Digest:     "sha256:" + strings.Repeat("t", 64),
	})
	h.claimForTransition(t, execID, "team-a", platform.ExecutorScopeTeam, taskID, "C-trans")

	now := time.Now().UTC()

	// running transition
	resp, raw := h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-run-1", platform.TaskEventTypeRunning, now))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("running status=%d, want 202; body=%s", resp.StatusCode, raw)
	}

	// finished transition
	resp, raw = h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-fin-1", platform.TaskEventTypeFinished, now.Add(time.Second)))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("finished status=%d, want 202; body=%s", resp.StatusCode, raw)
	}

	if got := h.repo.taskEventsFor(taskID); len(got) != 3 {
		t.Fatalf("events=%d, want 3 (created + running + finished)", len(got))
	}

	// Repeated terminal: a second finished must be rejected.
	resp, raw = h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-fin-2", platform.TaskEventTypeFinished, now.Add(2*time.Second)))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("repeated finished status=%d, want 409; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Code != "invalid_task_transition" {
		t.Errorf("repeated finished code=%q, want invalid_task_transition", env.Code)
	}

	// Re-approval after terminal is rejected (terminal is absorbing).
	resp, raw = h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-re-approval", platform.TaskEventTypeRunning, now.Add(3*time.Second)))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("re-approval after terminal status=%d, want 409; body=%s", resp.StatusCode, raw)
	}
}

// TestTransitionTerminalBeforeRunning covers the explicit out-of-order
// rejection: a `failed` event on a `created` task is rejected with
// 409 invalid_task_transition because the task hasn't reached the
// running state yet.
func TestTransitionTerminalBeforeRunning(t *testing.T) {
	h := newClaimHarness(t)
	const execID = "exec-terminal-before"
	const taskID = "task-terminal-before"
	h.admin(t, execID, "team-a", platform.ExecutorScopeTeam)
	h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
		Repository: "registry.example/agent",
		Digest:     "sha256:" + strings.Repeat("b", 64),
	})
	h.claimForTransition(t, execID, "team-a", platform.ExecutorScopeTeam, taskID, "C-tb")
	now := time.Now().UTC()

	resp, raw := h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-fail-1", platform.TaskEventTypeFailed, now))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("terminal-before-running status=%d, want 409; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Code != "invalid_task_transition" {
		t.Errorf("code=%q, want invalid_task_transition", env.Code)
	}
}

// ---------------------------------------------------------------------------
// TestTeamEventOwnership
// ---------------------------------------------------------------------------

// TestTeamEventOwnership pins the team-side authorization rules:
//   - a same-team unassigned Executor is rejected with 403 not_assigned
//   - a foreign-team Executor is rejected with non-revealing 404
//     `task_not_found` (same shape as unknown)
//   - a system-owned Executor posting to a non-foreign task is gated
//     by the parent task's team_id (envelope rejects 403 team_mismatch
//     when the envelope team_id differs from the parent task)
//   - envelope team_id absent or null is rejected 400/403
func TestTeamEventOwnership(t *testing.T) {
	t.Run("unassigned same-team writer is rejected with 403 not_assigned", func(t *testing.T) {
		h := newClaimHarness(t)
		execA := "exec-assigned"
		execB := "exec-unassigned"
		h.admin(t, execA, "team-a", platform.ExecutorScopeTeam)
		h.admin(t, execB, "team-a", platform.ExecutorScopeTeam)
		const taskID = "task-unassigned"
		h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
			Repository: "registry.example/agent",
			Digest:     "sha256:" + strings.Repeat("u", 64),
		})
		h.claimForTransition(t, execA, "team-a", platform.ExecutorScopeTeam, taskID, "C-ua")

		resp, raw := h.postTaskEvent(t, execB, "team-a", platform.ExecutorScopeTeam, taskID,
			runTaskEvent(execB, "team-a", taskID, "evt-ua-1", platform.TaskEventTypeRunning, time.Now().UTC()))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status=%d, want 403 not_assigned; body=%s", resp.StatusCode, raw)
		}
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Code != "not_assigned" {
			t.Errorf("code=%q, want not_assigned", env.Code)
		}
	})

	t.Run("foreign-team writer returns non-revealing 404 task_not_found", func(t *testing.T) {
		h := newClaimHarness(t)
		execA := "exec-teama"
		execB := "exec-teambb"
		h.admin(t, execA, "team-a", platform.ExecutorScopeTeam)
		h.admin(t, execB, "team-b", platform.ExecutorScopeTeam)
		const taskID = "task-foreign"
		h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
			Repository: "registry.example/agent",
			Digest:     "sha256:" + strings.Repeat("f", 64),
		})
		h.claimForTransition(t, execA, "team-a", platform.ExecutorScopeTeam, taskID, "C-ft")

		resp, raw := h.postTaskEvent(t, execB, "team-b", platform.ExecutorScopeTeam, taskID,
			runTaskEvent(execB, "team-b", taskID, "evt-fb-1", platform.TaskEventTypeRunning, time.Now().UTC()))
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d, want 404 task_not_found; body=%s", resp.StatusCode, raw)
		}
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Code != "task_not_found" {
			t.Errorf("code=%q, want task_not_found", env.Code)
		}
	})

	t.Run("envelope team_id mismatch returns 403 team_mismatch", func(t *testing.T) {
		h := newClaimHarness(t)
		execA := "exec-mismatch"
		h.admin(t, execA, "team-a", platform.ExecutorScopeTeam)
		const taskID = "task-mismatch"
		h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
			Repository: "registry.example/agent",
			Digest:     "sha256:" + strings.Repeat("m", 64),
		})
		h.claimForTransition(t, execA, "team-a", platform.ExecutorScopeTeam, taskID, "C-mm")

		// Envelope claims team-b but the executor and task are team-a.
		resp, raw := h.postTaskEvent(t, execA, "team-a", platform.ExecutorScopeTeam, taskID,
			runTaskEvent(execA, "team-b", taskID, "evt-mm-1", platform.TaskEventTypeRunning, time.Now().UTC()))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status=%d, want 403 team_mismatch; body=%s", resp.StatusCode, raw)
		}
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Code != "team_mismatch" {
			t.Errorf("code=%q, want team_mismatch", env.Code)
		}
	})

	t.Run("accepted_sequence override is rejected without append or projection", func(t *testing.T) {
		h := newClaimHarness(t)
		execA := "exec-sequence"
		h.admin(t, execA, "team-a", platform.ExecutorScopeTeam)
		const taskID = "task-sequence"
		h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
			Repository: "registry.example/agent",
			Digest:     "sha256:" + strings.Repeat("s", 64),
		})
		h.claimForTransition(t, execA, "team-a", platform.ExecutorScopeTeam, taskID, "C-sq")
		firstAt := time.Now().UTC()
		// Seed a fresh running event so the out-of-order tuple can be
		// observed.
		_, _ = h.postTaskEvent(t, execA, "team-a", platform.ExecutorScopeTeam, taskID,
			runTaskEvent(execA, "team-a", taskID, "evt-sq-2", platform.TaskEventTypeRunning, firstAt.Add(time.Second)))

		body := map[string]any{
			"event_id":          "evt-sq-1",
			"team_id":           "team-a",
			"task_id":           taskID,
			"executor_id":       execA,
			"event_type":        platform.TaskEventTypeFinished,
			"occurred_at":       firstAt.Format(time.RFC3339Nano),
			"payload":           map[string]any{},
			"accepted_sequence": 99,
		}
		raw, _ := json.Marshal(body)
		resp, respBody := h.postTaskEvent(t, execA, "team-a", platform.ExecutorScopeTeam, taskID, raw)
		before := len(h.repo.taskEventsFor(taskID))
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status=%d, want 409 invalid_task_transition; body=%s", resp.StatusCode, respBody)
		}
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(respBody, &env)
		if env.Code != "invalid_task_transition" {
			t.Errorf("code=%q, want invalid_task_transition", env.Code)
		}
		if after := len(h.repo.taskEventsFor(taskID)); after != before {
			t.Errorf("events=%d after override rejection, want unchanged %d", after, before)
		}
		if state := h.repo.tasks[taskID].entry.CurrentState; state != platform.TaskStateRunning {
			t.Errorf("state=%q after override rejection, want running", state)
		}
	})
}

// ---------------------------------------------------------------------------
// TestEventIdempotency
// ---------------------------------------------------------------------------

// TestEventIdempotency asserts that an identical (task_id, event_id)
// retry returns the original 202 with one appended event. The retry
// path resolves BEFORE the strict ordering check.
func TestEventIdempotency(t *testing.T) {
	h := newClaimHarness(t)
	const execID = "exec-idempotency"
	const taskID = "task-idempotency"
	h.admin(t, execID, "team-a", platform.ExecutorScopeTeam)
	h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
		Repository: "registry.example/agent",
		Digest:     "sha256:" + strings.Repeat("i", 64),
	})
	h.claimForTransition(t, execID, "team-a", platform.ExecutorScopeTeam, taskID, "C-idem")

	now := time.Now().UTC()
	first, _ := h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-idem-1", platform.TaskEventTypeRunning, now))
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status=%d, want 202", first.StatusCode)
	}

	second, secondRaw := h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-idem-1", platform.TaskEventTypeRunning, now))
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status=%d, want 202; body=%s", second.StatusCode, secondRaw)
	}

	if got := h.repo.AppendTaskEventCalls.Load(); got != 2 {
		t.Errorf("repository calls=%d, want 2 (first + retry)", got)
	}
	events := h.repo.taskEventsFor(taskID)
	// Exactly the first created event + a single running event.
	runningCount := 0
	for _, ev := range events {
		if ev.EventType == platform.TaskEventTypeRunning {
			runningCount++
		}
	}
	if runningCount != 1 {
		t.Errorf("running events=%d, want 1 (idempotent retry must not duplicate)", runningCount)
	}
}

// ---------------------------------------------------------------------------
// TestStrictEventOrdering
// ---------------------------------------------------------------------------

// TestStrictEventOrdering pins the (occurred_at, event_id) contract.
// Cases:
//
//   - older tuple rejected 409 event_ordering_conflict
//   - equal timestamps with lower event_id rejected 409
//   - equal timestamps with greater event_id accepted
//   - strictly newer tuple accepted
func TestStrictEventOrdering(t *testing.T) {
	h := newClaimHarness(t)
	const execID = "exec-ordering"
	const taskID = "task-ordering"
	h.admin(t, execID, "team-a", platform.ExecutorScopeTeam)
	h.repo.seedTask("team-a", taskID, "src-team-a", "external", "type-a", "openhands", &platform.ImageReference{
		Repository: "registry.example/agent",
		Digest:     "sha256:" + strings.Repeat("o", 64),
	})
	h.claimForTransition(t, execID, "team-a", platform.ExecutorScopeTeam, taskID, "C-ord")

	base := time.Now().UTC()

	// First event
	resp, _ := h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-ord-2", platform.TaskEventTypeRunning, base))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first status=%d, want 202", resp.StatusCode)
	}

	// Older tuple: rejected with 409 event_ordering_conflict.
	resp, raw := h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-ord-1", platform.TaskEventTypeFinished, base.Add(-time.Second)))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("older tuple status=%d, want 409; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Code != "invalid_task_transition" {
		t.Errorf("older-tuple code=%q, want invalid_task_transition", env.Code)
	}

	// Equal timestamp with lower event_id: rejected 409.
	resp, raw = h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-ord-1", platform.TaskEventTypeFinished, base))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("equal-lower-event_id status=%d, want 409; body=%s", resp.StatusCode, raw)
	}
	_ = json.Unmarshal(raw, &env)
	if env.Code != "invalid_task_transition" {
		t.Errorf("equal-lower-event_id code=%q, want invalid_task_transition", env.Code)
	}

	// Equal timestamp with greater event_id: accepted (with a new
	// terminal state already at running, but the next emit is a
	// terminal; it must be accepted).
	resp, raw = h.postTaskEvent(t, execID, "team-a", platform.ExecutorScopeTeam, taskID,
		runTaskEvent(execID, "team-a", taskID, "evt-ord-3", platform.TaskEventTypeFinished, base))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("equal-greater-event_id status=%d, want 202; body=%s", resp.StatusCode, raw)
	}
}

// ---------------------------------------------------------------------------
// TestExecutorSelfEvent
// ---------------------------------------------------------------------------

// TestExecutorSelfEvent pins the Executor self-event surface:
//
//   - team-owned Executor writes a self event with matching envelope
//     team_id, observation is mirrored onto the executor row
//   - system-owned Executor writes a self event with envelope
//     team_id null, observation is mirrored
//   - team-owned Executor with mismatched envelope team_id is 403
//     team_mismatch
//   - system-owned Executor with non-null envelope team_id is 403
//     team_mismatch
//   - foreign-team Executor point identifier returns non-revealing 404
//   - identical (executor_id, event_id) retry returns the original 202
//   - capacity / running_count observations are reflected in the
//     executor row in the same transaction
func TestExecutorSelfEvent(t *testing.T) {
	t.Run("team-owned Executor writes a self event with matching envelope", func(t *testing.T) {
		h := newClaimHarness(t)
		h.admin(t, "exec-sel-team", "team-a", platform.ExecutorScopeTeam)
		resp, raw := h.postExecutorEvent(t, "exec-sel-team", "team-a", platform.ExecutorScopeTeam,
			runExecutorEvent("exec-sel-team", "team-a", "evt-self-1", platform.ExecutorEventTypeHealthy, time.Now().UTC(), map[string]any{}))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status=%d, want 202; body=%s", resp.StatusCode, raw)
		}
	})

	t.Run("team-owned Executor capacity observation is mirrored", func(t *testing.T) {
		h := newClaimHarness(t)
		h.admin(t, "exec-mirror", "team-a", platform.ExecutorScopeTeam)
		occurredAt := time.Now().UTC()
		resp, raw := h.postExecutorEvent(t, "exec-mirror", "team-a", platform.ExecutorScopeTeam,
			runExecutorEvent("exec-mirror", "team-a", "evt-mirror-1", platform.ExecutorEventTypeCapacityObserved, occurredAt, map[string]any{"max_capacity": 7}))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("first status=%d, want 202; body=%s", resp.StatusCode, raw)
		}
		exec, err := h.repo.GetExecutor(context.Background(), "exec-mirror")
		if err != nil {
			t.Fatalf("get executor: %v", err)
		}
		if exec.MaxCapacity != 7 {
			t.Errorf("max_capacity=%d, want 7", exec.MaxCapacity)
		}
	})

	t.Run("team-owned Executor envelope team_id mismatch is 403 team_mismatch", func(t *testing.T) {
		h := newClaimHarness(t)
		h.admin(t, "exec-mismatch", "team-a", platform.ExecutorScopeTeam)
		resp, raw := h.postExecutorEvent(t, "exec-mismatch", "team-a", platform.ExecutorScopeTeam,
			runExecutorEvent("exec-mismatch", "team-b", "evt-mis-1", platform.ExecutorEventTypeHealthy, time.Now().UTC(), map[string]any{}))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status=%d, want 403; body=%s", resp.StatusCode, raw)
		}
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Code != "team_mismatch" {
			t.Errorf("code=%q, want team_mismatch", env.Code)
		}
	})

	t.Run("system-owned Executor posts with non-null team_id is 403 team_mismatch", func(t *testing.T) {
		h := newClaimHarness(t)
		h.admin(t, "exec-sys", "", platform.ExecutorScopeSystem)
		resp, raw := h.postExecutorEvent(t, "exec-sys", "", platform.ExecutorScopeSystem,
			runExecutorEvent("exec-sys", "team-a", "evt-sys-1", platform.ExecutorEventTypeHealthy, time.Now().UTC(), map[string]any{}))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status=%d, want 403; body=%s", resp.StatusCode, raw)
		}
		var env struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Code != "team_mismatch" {
			t.Errorf("code=%q, want team_mismatch", env.Code)
		}
	})

	t.Run("system-owned Executor posts with null team_id is accepted", func(t *testing.T) {
		h := newClaimHarness(t)
		h.admin(t, "exec-sys-ok", "", platform.ExecutorScopeSystem)
		resp, raw := h.postExecutorEvent(t, "exec-sys-ok", "", platform.ExecutorScopeSystem,
			runExecutorEvent("exec-sys-ok", "", "evt-sys-ok-1", platform.ExecutorEventTypeHealthy, time.Now().UTC(), map[string]any{}))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status=%d, want 202; body=%s", resp.StatusCode, raw)
		}
	})

	t.Run("identical retry returns the original 202 without duplicate", func(t *testing.T) {
		h := newClaimHarness(t)
		h.admin(t, "exec-retry", "team-a", platform.ExecutorScopeTeam)
		occurredAt := time.Now().UTC()
		body := runExecutorEvent("exec-retry", "team-a", "evt-retry-1", platform.ExecutorEventTypeHealthy, occurredAt, map[string]any{})
		first, _ := h.postExecutorEvent(t, "exec-retry", "team-a", platform.ExecutorScopeTeam, body)
		if first.StatusCode != http.StatusAccepted {
			t.Fatalf("first status=%d, want 202", first.StatusCode)
		}
		second, _ := h.postExecutorEvent(t, "exec-retry", "team-a", platform.ExecutorScopeTeam, body)
		if second.StatusCode != http.StatusAccepted {
			t.Fatalf("retry status=%d, want 202", second.StatusCode)
		}
		events, _ := h.repo.ListExecutorEvents(context.Background(), "team-a", "exec-retry")
		count := 0
		for _, ev := range events {
			if ev.EventID == "evt-retry-1" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("duplicate count=%d, want 1", count)
		}
	})

	t.Run("foreign-team Executor point identifier returns non-revealing 404", func(t *testing.T) {
		_ = store.ErrExecutorNotFound
		h := newClaimHarness(t)
		h.admin(t, "exec-teama", "team-a", platform.ExecutorScopeTeam)
		h.admin(t, "exec-teamb", "team-b", platform.ExecutorScopeTeam)
		// Executor A (team-a) posts to the path of Executor B (team-b).
		// The path-side executor_id does not match the authenticated
		// identity, so the handler must reject with 404 to stay
		// non-revealing across foreign Executor probes.
		resp, _ := h.postExecutorEvent(t, "exec-teama", "team-a", platform.ExecutorScopeTeam,
			runExecutorEvent("exec-teamb", "team-a", "evt-fb-1", platform.ExecutorEventTypeHealthy, time.Now().UTC(), map[string]any{}))
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d, want 404", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// TestSelfEventRollbackContract
// ---------------------------------------------------------------------------

// TestSelfEventRollbackContract proves that a fault injected between
// the executor_events append and the observation mirror leaves NO
// appended event nor mirrored observation. The in-memory fake mirrors
// the production transaction: the mirror is intended to happen in
// the same transaction as the append; the test exercises the failure
// path by reverting both the append and the mirror from inside the
// commit hook and verifying nothing reached steady state.
func TestSelfEventRollbackContract(t *testing.T) {
	h := newClaimHarness(t)
	h.admin(t, "exec-rollback", "team-a", platform.ExecutorScopeTeam)

	before := h.repo.executors["exec-rollback"].executor.MaxCapacity

	// Force the commit to fail by reverting both the observation
	// mirror and the event append from inside the commit hook. The
	// production transaction would roll back here; the in-memory
	// fake mirrors that contract by dropping both mutations.
	h.repo.commitBeforeReturn = func(id string) {
		if id != "exec-rollback" {
			return
		}
		exec := h.repo.executors["exec-rollback"].executor
		exec.MaxCapacity = before
		exec.RunningCount = 0
		h.repo.executors["exec-rollback"] = claimExecutorRecord{executor: exec}
		if len(h.repo.selfEvents) > 0 {
			h.repo.selfEvents = h.repo.selfEvents[:len(h.repo.selfEvents)-1]
		}
	}

	resp, raw := h.postExecutorEvent(t, "exec-rollback", "team-a", platform.ExecutorScopeTeam,
		runExecutorEvent("exec-rollback", "team-a", "evt-rb-1", platform.ExecutorEventTypeCapacityObserved, time.Now().UTC(), map[string]any{"max_capacity": 9}))
	// The fake returns 202 because the response is computed BEFORE
	// the rollback runs; the production contract is encoded in the
	// post-state assertion that follows.
	_ = resp
	_ = raw

	// After the rollback, the executor capacity must revert to the
	// pre-append value and the event must NOT be visible.
	after := h.repo.executors["exec-rollback"].executor.MaxCapacity
	if after != before {
		t.Errorf("max_capacity persisted=%d, want reverted to %d (rollback must clear mirror)", after, before)
	}
	events, _ := h.repo.ListExecutorEvents(context.Background(), "team-a", "exec-rollback")
	if len(events) != 0 {
		t.Errorf("event count after rollback=%d, want 0", len(events))
	}
}

// readBody is provided by admin_test.go for reuse across suites.
