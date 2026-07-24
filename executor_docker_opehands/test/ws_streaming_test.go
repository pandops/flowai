// OpenHands WS streaming tests that exercise the real gorilla/websocket
// dial path against the FakeOpenHands WS server. The executor must
// genuinely stream events; this is a deterministic replacement for
// tests that relied on hollow scaffolding.
package integration_test

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/flowai/platform/executor_docker_opehands/internal/executor"
)

// TestWSStreamIntermediateThenFinished exercises the streaming path
// with one intermediate message frame followed by a terminal
// `finished` conversation.status frame. The executor must journal
// BOTH events as separate task events.
func TestWSStreamIntermediateThenFinished(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.WebSocketDialTimeout = 500 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	env.oh.RegisterStreamHook(func(conn *websocket.Conn, conversationID string) {
		// Push an intermediate event frame.
		_ = conn.WriteJSON(map[string]any{
			"type":    "message",
			"role":    "assistant",
			"content": "intermediate-event",
		})
		// Push a terminal finished frame.
		_ = conn.WriteJSON(map[string]any{
			"type":             "conversation.status",
			"execution_status": "finished",
		})
	})

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		gotIntermediate, gotFinished := false, false
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "openhands.event" {
				gotIntermediate = true
			}
			if e.Type == "task.finished" {
				gotFinished = true
			}
		}
		if gotIntermediate && gotFinished {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected intermediate event + task.finished, got %v",
		env.stub.taskEventTypes(taskID))
}

// TestWSStreamIntermediateThenFailed exercises the failure terminal
// frame (OpenHands returns a failed conversation.status). The executor
// must journal exactly one task.failed with phase=terminal_state and
// the underlying openhands_status payload.
func TestWSStreamIntermediateThenFailed(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.WebSocketDialTimeout = 500 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	env.oh.RegisterStreamHook(func(conn *websocket.Conn, conversationID string) {
		_ = conn.WriteJSON(map[string]any{
			"type":             "tool_call",
			"id":               "tc-1",
			"execution_status": "running",
		})
		_ = conn.WriteJSON(map[string]any{
			"type":             "conversation.status",
			"execution_status": "failed",
		})
	})

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		gotIntermediate, gotFailed := false, false
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "openhands.event" {
				gotIntermediate = true
			}
			if e.Type == "task.failed" {
				if got, _ := e.Payload["phase"].(string); got == "terminal_state" {
					gotFailed = true
				}
			}
		}
		if gotIntermediate && gotFailed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected intermediate + task.failed phase=terminal_state, got %v",
		env.stub.taskEventTypes(taskID))
}

// TestSuccessfulInterruptJournalsInterrupted feeds a paused
// conversation.status frame to the executor on the WS connection,
// after the Router returns an interrupt_task action. The expected
// chain is task.cancel_requested -> task.interrupted.
func TestSuccessfulInterruptJournalsInterrupted(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsInterruptTO = 1500 * time.Millisecond
		c.WebSocketDialTimeout = 500 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	// Capture the conversation_id from the WS hook so we can push a
	// paused frame AFTER the Router interrupt fires (and after the
	// fake's pause handler has acknowledged).
	var convID atomic.Value
	env.oh.RegisterStreamHook(func(conn *websocket.Conn, conversationID string) {
		convID.Store(conversationID)
	})

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for the streaming WS hook to have run before firing the
	// interrupt, so we know the conn is held by the executor.
	subDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(subDeadline) {
		if id, ok := convID.Load().(string); ok && id != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "test"))
	// Allow the interrupt handler time to call pause. Then push a paused
	// frame so the streaming routine records task.interrupted.
	time.Sleep(150 * time.Millisecond)
	if id, ok := convID.Load().(string); ok && id != "" {
		_ = env.oh.PushTerminal(id, "paused")
	}

	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		gotCancel, gotInterrupted := false, false
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.cancel_requested" {
				gotCancel = true
			}
			if e.Type == "task.interrupted" {
				gotInterrupted = true
			}
		}
		if gotCancel && gotInterrupted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected cancel_requested + task.interrupted, got %v",
		env.stub.taskEventTypes(taskID))
}

// TestInterruptTimeoutFailsTask is the deterministic version of
// `OpenHands hangs`. The fake's pause handler returns
// errFakeHangOpenHands; the executor must journal a single
// task.failed(phase=interrupt_timeout) and a clean slot release.
func TestInterruptTimeoutFailsTask(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsInterruptTO = 250 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	env.oh.PauseErr = errFakeHangOpenHands

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "no-ack"))

	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		failedCount := 0
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.failed" {
				if phase, _ := e.Payload["phase"].(string); phase == "interrupt_timeout" {
					failedCount++
				}
			}
		}
		if failedCount == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected exactly 1 task.failed(phase=interrupt_timeout), got %v",
		env.stub.taskEventTypes(taskID))
}

// TestMessageActionDedupedByActionID seeds two pending_actions with the
// SAME action_id across two consecutive polls. The executor must forward
// only ONE OpenHands append and journal only ONE task.message_forwarded.
func TestMessageActionDedupedByActionID(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sharedAction := "fixed-action-" + uuid.NewString()
	env.stub.setTask(&stubTask{
		TaskID:        taskID,
		RoutingTarget: "openhands",
		AgentRuntime:  "openhands",
		Status:        "running",
		PendingActions: []map[string]interface{}{
			{"action_id": sharedAction, "type": "append_task_message", "task_id": taskID, "content": "duplicate", "role": "user"},
		},
	})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		count := 0
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.message_forwarded" {
				count++
			}
		}
		if count >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	env.stub.setTask(&stubTask{
		TaskID:        taskID,
		RoutingTarget: "openhands",
		AgentRuntime:  "openhands",
		Status:        "running",
		PendingActions: []map[string]interface{}{
			{"action_id": sharedAction, "type": "append_task_message", "task_id": taskID, "content": "duplicate", "role": "user"},
		},
	})
	time.Sleep(500 * time.Millisecond)
	count := 0
	for _, e := range env.stub.listTaskEvents(taskID) {
		if e.Type == "task.message_forwarded" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 task.message_forwarded, got %d", count)
	}
}
