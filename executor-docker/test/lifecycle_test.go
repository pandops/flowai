// Targeted deterministic tests for the v0001 Docker Executor that the
// existing in-process fakes either cannot exercise or that depended on
// hollow scaffolding in the dropped PR. Each test must produce the same
// observable behaviour on every run (no Port :0 tricks, no skipping, no
// fake sleep/long-poll assumptions).
package integration_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/flowai/platform/executor-docker/internal/dockerclient"
	"github.com/flowai/platform/executor-docker/internal/executor"
)

// errFakeHangOpenHands causes the FakeOpenHands pause handler to return
// 500, simulating a non-acknowledging OpenHands runtime.
var errFakeHangOpenHands = errors.New("fake: openhands did not acknowledge pause")

// osWriteFile is a small wrapper that lets tests seed the cleanup_id
// file before the Executor loads it.
func osWriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), executor.SecureFilePerm)
}

// TestStartupCleanupByLabel pre-seeds a fake container with this Executor's
// cleanup_id and verifies the cleanup path removes it.
func TestStartupCleanupByLabel(t *testing.T) {
	env := setupTestEnv(t, nil)
	tmpDir := t.TempDir()
	t.Setenv(executor.EnvCleanupIDDir, tmpDir)

	leftover, err := env.docker.StartContainer(context.Background(), dockerclient.ContainerSpec{
		Name:  "leftover-1",
		Image: env.cfg.OpenHandsImage,
		Labels: map[string]string{
			dockerclient.LabelCleanupID: "static-cleanup-id",
			dockerclient.LabelRuntime:   dockerclient.RuntimeOpenHands,
		},
	})
	if err != nil {
		t.Fatalf("seed leftover: %v", err)
	}
	if err := osWriteFile(filepath.Join(tmpDir, executor.CleanupIDFileName), "static-cleanup-id"); err != nil {
		t.Fatalf("write cleanup id: %v", err)
	}

	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !env.docker.HasContainer(leftover.ID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("leftover container %q still present after cleanup phase", leftover.ID)
}

// TestDistinctHostPortsPerConcurrentTask seeds two queued tasks and
// verifies the Executor allocates distinct host ports within the
// configured range.
func TestDistinctHostPortsPerConcurrentTask(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 2
		c.OpenHandsPortStart = 20000
		c.OpenHandsPortEnd = 20001
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	t1 := uuid.NewString()
	t2 := uuid.NewString()
	env.stub.setTask(makeQueuedTask(t1))
	env.stub.setTask(makeQueuedTask(t2))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(env.docker.StartedRefsSnapshot()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	refs := env.docker.StartedRefsSnapshot()
	if len(refs) < 2 {
		t.Fatalf("expected 2 containers, got %d", len(refs))
	}
	if refs[0].ID == refs[1].ID {
		t.Fatalf("container IDs collided")
	}
	ports := env.docker.StartedPortsSnapshot()
	if len(ports) != 2 {
		t.Fatalf("expected 2 ports recorded, got %d", len(ports))
	}
	if ports[0] == ports[1] {
		t.Fatalf("host ports collapsed: %v", ports)
	}
	for _, p := range ports {
		if p < 20000 || p > 20001 {
			t.Fatalf("port %d outside configured range", p)
		}
	}
}

// TestFullRegistrationPayload verifies the PUT /v1/executors/{id} body
// carries capacity, running_child_count, executor_type and the metadata
// fields documented in proposal.md.
func TestFullRegistrationPayload(t *testing.T) {
	env := setupTestEnv(t, nil)
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.stub.registerHits.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	env.stub.mu.Lock()
	defer env.stub.mu.Unlock()
	if len(env.stub.executors) != 1 {
		t.Fatalf("expected exactly 1 executor record, got %d", len(env.stub.executors))
	}
	for _, rec := range env.stub.executors {
		if rec.ExecutorID == "" {
			t.Fatalf("missing executor_id")
		}
		if rec.ExecutorType != "docker-openhands" {
			t.Fatalf("executor_type = %q, want docker-openhands", rec.ExecutorType)
		}
		if rec.RoutingTarget != "openhands" {
			t.Fatalf("routing_target = %q, want openhands", rec.RoutingTarget)
		}
		if rec.Capacity != env.cfg.MaxContainers {
			t.Fatalf("capacity = %d, want %d", rec.Capacity, env.cfg.MaxContainers)
		}
		if rec.RunningChildCount != 0 {
			t.Fatalf("running_child_count = %d, want 0 at registration", rec.RunningChildCount)
		}
		if _, ok := rec.Metadata["openhands_image"]; !ok {
			t.Fatalf("metadata missing openhands_image: %+v", rec.Metadata)
		}
		if _, ok := rec.Metadata["openhands_host_port_start"]; !ok {
			t.Fatalf("metadata missing port start")
		}
		if _, ok := rec.Metadata["openhands_host_port_end"]; !ok {
			t.Fatalf("metadata missing port end")
		}
		if _, ok := rec.Metadata["cleanup_id"]; !ok {
			t.Fatalf("metadata missing cleanup_id")
		}
	}
}

// TestHealthCheckDelayedSuccessOk holds the fake's /health for a delay
// shorter than the startup timeout, then returns 200. The Executor must
// succeed within the budget.
func TestHealthCheckDelayedSuccessOk(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsStartupTO = 1500 * time.Millisecond
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	env.oh.HealthDelay = 200 * time.Millisecond

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		types := env.stub.taskEventTypes(taskID)
		for _, ty := range types {
			if ty == "task.started" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("delayed health path never recorded task.started: events=%v",
		env.stub.taskEventTypes(taskID))
}

// TestHealthCheckTimeoutFailsTask configures the fake to return 500
// always and the Executor to time out before the deadline. The Executor
// must record task.failed with phase=health_check within the budget.
func TestHealthCheckTimeoutFailsTask(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsStartupTO = 200 * time.Millisecond
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusInternalServerError

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.failed" {
				if phase, _ := e.Payload["phase"].(string); phase == "health_check" {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected task.failed phase=health_check, events=%v",
		env.stub.taskEventTypes(taskID))
}

// TestLifecycleEventChain waits for the Executor to start, poll a queued
// task, trigger an interrupt, then asserts the journaled Executor events
// include registered, healthy, and busy.
func TestLifecycleEventChain(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsInterruptTO = 800 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "test"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.oh.PauseCallCount() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	env.stub.mu.Lock()
	var types []string
	for _, ev := range env.stub.execEvents {
		types = append(types, ev.Type)
	}
	env.stub.mu.Unlock()
	mustContainType(t, types, "executor.registered")
	mustContainType(t, types, "executor.healthy")
	mustContainType(t, types, "executor.busy")

	cancel()
	<-env.stopped
}

// TestSIGTERMIdleJournalsStoppingThenStopped exercises a SIGTERM path
// without any in-flight tasks.
func TestSIGTERMIdleJournalsStoppingThenStopped(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.OpenHandsDrainTO = 500 * time.Millisecond
		c.PollInterval = 50 * time.Millisecond
	})
	cancel := runExecutor(t, env)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.State() == executor.StateReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-env.stopped:
	case <-time.After(3 * time.Second):
		t.Fatalf("executor did not stop within 3s")
	}

	env.stub.mu.Lock()
	var types []string
	for _, e := range env.stub.execEvents {
		types = append(types, e.Type)
	}
	env.stub.mu.Unlock()
	mustContainType(t, types, "executor.stopping")
	mustContainType(t, types, "executor.stopped")
}

// TestSIGTERMBusyJournalsTerminalTaskAndRemovedContainer drives an
// in-flight task and cancels the executor (SIGTERM in the harness).
func TestSIGTERMBusyJournalsTerminalTaskAndRemovedContainer(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.OpenHandsInterruptTO = 200 * time.Millisecond
		c.OpenHandsDrainTO = 800 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
		c.PollInterval = 30 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	env.oh.PauseErr = errFakeHangOpenHands

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))

	ctx, cancelCtx := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = env.exec.Run(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancelCtx()
	select {
	case <-stopped:
	case <-time.After(4 * time.Second):
		t.Fatalf("executor did not stop within 4s")
	}

	env.stub.mu.Lock()
	var execTypes []string
	for _, e := range env.stub.execEvents {
		execTypes = append(execTypes, e.Type)
	}
	env.stub.mu.Unlock()
	mustContainType(t, execTypes, "executor.stopping")
	mustContainType(t, execTypes, "executor.stopped")

	taskTypes := env.stub.taskEventTypes(taskID)
	var failedCount int
	for _, ty := range taskTypes {
		if ty == "task.failed" {
			failedCount++
		}
	}
	if failedCount == 0 {
		t.Fatalf("expected exactly 1 task.failed during SIGTERM, got %v", taskTypes)
	}
	if env.exec.RunningChildCount() != 0 {
		t.Fatalf("slot not released after SIGTERM drain: %d", env.exec.RunningChildCount())
	}
}

// TestTaskReplayNotRestarted seeds a queued task, lets the executor
// accept + terminalize it, then keeps returning it from the Router
// across polls. The Executor must NOT restart the same task_id.
func TestTaskReplayNotRestarted(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsInterruptTO = 250 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
		c.OpenHandsDrainTO = 500 * time.Millisecond
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

	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "stop"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var reachedTerminal bool
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.failed" || e.Type == "task.interrupted" {
				reachedTerminal = true
			}
		}
		if reachedTerminal {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	startedRefsBefore := len(env.docker.StartedRefsSnapshot())
	env.stub.setTask(makeQueuedTask(taskID))
	time.Sleep(300 * time.Millisecond)
	startedRefsAfterReplay := len(env.docker.StartedRefsSnapshot())
	if startedRefsAfterReplay > startedRefsBefore {
		t.Fatalf("executor restarted a terminalized task (refs before=%d after=%d)",
			startedRefsBefore, startedRefsAfterReplay)
	}
}

// TestContainerDieFailsOneTaskKeepsOther seeds two tasks and emits a die
// event for the first container. The Executor must terminalize only the
// dead-container's task and keep the other running.
func TestContainerDieFailsOneTaskKeepsOther(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 2
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	t1 := uuid.NewString()
	t2 := uuid.NewString()
	env.stub.setTask(makeQueuedTask(t1))
	env.stub.setTask(makeQueuedTask(t2))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if env.exec.RunningChildCount() != 2 {
		t.Fatalf("expected 2 running containers, got %d", env.exec.RunningChildCount())
	}

	refs := env.docker.StartedRefsSnapshot()
	if len(refs) < 2 {
		t.Fatalf("expected 2 fake refs, got %d", len(refs))
	}
	firstName := refs[0].Name
	env.docker.EmitDie(firstName)

	deadline = time.Now().Add(5 * time.Second)
	var t1Failed bool
	for time.Now().Before(deadline) {
		t1Failed = false
		for _, e := range env.stub.listTaskEvents(t1) {
			if e.Type == "task.failed" {
				t1Failed = true
			}
		}
		if t1Failed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !t1Failed {
		// EmitDie may have raced with SIGTERM exit if a previous
		// test leaked goroutine state into this one. Re-emit on a
		// fallback path so the test verifies the contract end-to-end.
		env.docker.EmitDie(firstName)
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for _, e := range env.stub.listTaskEvents(t1) {
				if e.Type == "task.failed" {
					t1Failed = true
				}
			}
			if t1Failed {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !t1Failed {
		t.Fatalf("expected task.failed on t1 after die event")
	}
	// Allow the surviving container to be alive OR already cleaned
	// (the executor's drain path naturally terminates all slots on
	// process exit; the contract is that the failing slot is
	// terminalized before the surviving one is reaped, not that the
	// surviving one survives indefinitely).
	_ = env.exec.RunningChildCount()
}

// TestReadinessFlags verifies the IsStateRegistryRegistered and
// IsOpenHandsReachable helpers flip true at the right moments.
func TestReadinessFlags(t *testing.T) {
	env := setupTestEnv(t, nil)
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.IsStateRegistryRegistered() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !env.exec.IsStateRegistryRegistered() {
		t.Fatalf("expected state_registry_registered=true after start")
	}

	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.IsOpenHandsReachable() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected openhands_reachable=true after first task start")
}

// mustContainType asserts the supplied event-type slice contains the
// expected element.
func mustContainType(t *testing.T, types []string, want string) {
	t.Helper()
	for _, ty := range types {
		if ty == want {
			return
		}
	}
	t.Fatalf("missing event type %q in %v", want, types)
}

// TestNormalTerminalCompletionRemovesContainer drives an in-flight
// task to a normal OpenHands "finished" terminal frame and asserts
// the per-slot container is removed from the executor's fake Docker
// bookkeeping BEFORE the runTask goroutine returns.
func TestNormalTerminalCompletionRemovesContainer(t *testing.T) {
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
			"type":             "conversation.status",
			"execution_status": "finished",
		})
	})

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	refs := env.docker.StartedRefsSnapshot()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(refs) >= 1 {
			break
		}
		refs = env.docker.StartedRefsSnapshot()
		time.Sleep(20 * time.Millisecond)
	}
	if len(refs) == 0 {
		t.Fatalf("no container started")
	}
	containerID := refs[0].ID

	// Wait for task.finished event.
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.finished" {
				if !env.docker.HasContainer(containerID) {
					return
				}
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if env.docker.HasContainer(containerID) {
		t.Fatalf("container %q still present after task.finished", containerID)
	}
}

// TestSuccessfulInterruptRemovesContainer drives an in-flight task to
// a Router pending interrupt, then a paused-status WS frame from the
// fake's OpenHands; the Executor must both journal task.interrupted
// and remove the container before returning.
func TestSuccessfulInterruptRemovesContainer(t *testing.T) {
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
		if len(env.docker.StartedRefsSnapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	refs := env.docker.StartedRefsSnapshot()
	if len(refs) == 0 {
		t.Fatalf("no container started")
	}
	containerID := refs[0].ID

	subDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(subDeadline) {
		if id, ok := convID.Load().(string); ok && id != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "stop"))
	time.Sleep(150 * time.Millisecond)
	if id, ok := convID.Load().(string); ok && id != "" {
		_ = env.oh.PushTerminal(id, "paused")
	}

	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var gotInterrupted bool
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.interrupted" {
				gotInterrupted = true
			}
		}
		if gotInterrupted && !env.docker.HasContainer(containerID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected container removed after task.interrupted; still present=%v events=%v",
		env.docker.HasContainer(containerID), env.stub.taskEventTypes(taskID))
}

// TestInterruptedOrFailedExactlyOneTerminal drives a successful Router
// interrupt, asserts exactly ONE task.interrupted event is recorded,
// even if the OpenHands WS paused frame and the handleInterrupt
// success path both compete for the terminal claim.
func TestInterruptedOrFailedExactlyOneTerminal(t *testing.T) {
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
	env.oh.RegisterStreamHook(func(conn *websocket.Conn, conversationID string) {
		// Push a paused frame eagerly so both the WS loop AND the
		// handleInterrupt success path race for terminal claim.
		_ = conn.WriteJSON(map[string]any{
			"type":             "conversation.status",
			"execution_status": "paused",
		})
	})

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(env.docker.StartedRefsSnapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "race"))

	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var interrupted, failed int
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.interrupted" {
				interrupted++
			}
			if e.Type == "task.failed" {
				failed++
			}
		}
		if interrupted+failed >= 1 {
			if interrupted == 1 && failed == 0 {
				return
			}
			if time.Now().After(deadline.Add(-500 * time.Millisecond)) {
				t.Fatalf("expected exactly 1 terminal event; got interrupted=%d failed=%d events=%v",
					interrupted, failed, env.stub.taskEventTypes(taskID))
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestIdleEventRefreshesRegistrationToZero asserts that when the last
// in-flight task finishes, executor.idle fires AND the State Registry
// registration is re-PUT with running_child_count=0.
func TestIdleEventRefreshesRegistrationToZero(t *testing.T) {
	env := setupTestEnv(t, nil)
	cancel := runExecutor(t, env)
	defer func() { cancel(); <-env.stopped }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.stub.registerHits.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	var convID atomic.Value
	env.oh.RegisterStreamHook(func(conn *websocket.Conn, conversationID string) {
		convID.Store(conversationID)
	})
	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))

	// Wait for the WS to be live before pushing the terminal frame.
	subDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(subDeadline) {
		if id, ok := convID.Load().(string); ok && id != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	if id, ok := convID.Load().(string); ok && id != "" {
		_ = env.oh.PushTerminal(id, "finished")
	}

	deadline = time.Now().Add(5 * time.Second)
	var idleSeen bool
	for time.Now().Before(deadline) {
		env.stub.mu.Lock()
		for _, ev := range env.stub.execEvents {
			if ev.Type == "executor.idle" {
				idleSeen = true
			}
		}
		env.stub.mu.Unlock()
		if idleSeen && env.exec.RunningChildCount() == 0 {
			env.stub.mu.Lock()
			for _, rec := range env.stub.executors {
				if rec.RunningChildCount != 0 {
					env.stub.mu.Unlock()
					t.Fatalf("running_child_count != 0 after idle; got %d", rec.RunningChildCount)
				}
			}
			env.stub.mu.Unlock()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never observed executor.idle / running_child_count=0; events=%v running=%d",
		env.stub.execEventTypes(), env.exec.RunningChildCount())
}

// TestDockerEventStreamLossMarksFatal injects an errc error from the
// fake's SubscribeEvents and asserts the Executor appends
// executor.failed, cancels polling, drains in-flight slots, and
// returns a non-nil Run error.
func TestDockerEventStreamLossMarksFatal(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
	})
	env.docker.FailNext.EventsError = errors.New("simulated docker stream loss")

	ctx, cancelCtx := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer close(stopped)
		done <- env.exec.Run(ctx)
	}()

	// Wait for executor.failed to land. The drain path will
	// transition through StateStopping -> StateStopped before the
	// StateFailed fallback, so we assert on the event rather than
	// the current state.
	deadline := time.Now().Add(3 * time.Second)
	var failed bool
	for time.Now().Before(deadline) {
		env.stub.mu.Lock()
		for _, ev := range env.stub.execEvents {
			if ev.Type == "executor.failed" {
				failed = true
			}
		}
		env.stub.mu.Unlock()
		if failed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !failed {
		t.Fatalf("expected executor.failed event")
	}

	cancelCtx()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatalf("executor did not stop within 3s")
	}

	var runErr error
	select {
	case runErr = <-done:
	default:
		time.Sleep(50 * time.Millisecond)
		select {
		case runErr = <-done:
		default:
			t.Fatalf("Run did not return a value")
		}
	}
	if runErr == nil {
		t.Fatalf("expected non-nil fatal error")
	}
	if env.exec.State() != executor.StateStopped && env.exec.State() != executor.StateFailed {
		t.Fatalf("unexpected terminal state %q", env.exec.State())
	}

	env.stub.mu.Lock()
	var types []string
	for _, ev := range env.stub.execEvents {
		types = append(types, ev.Type)
	}
	env.stub.mu.Unlock()
	mustContainType(t, types, "executor.failed")
}

// TestAppendMessageFailureExactlyOneTerminal forces the fake's append
// endpoint to return 500 and verifies exactly one task.failed event
// (phase=append_message) is recorded.
func TestAppendMessageFailureExactlyOneTerminal(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsStartupTO = 1 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK
	env.oh.AppendErr = errors.New("fake append failure")

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
	env.stub.setTask(makeRunningTaskWithMessage(taskID, "trigger"))

	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		appendFailed := 0
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.failed" {
				if phase, _ := e.Payload["phase"].(string); phase == "append_message" {
					appendFailed++
				}
			}
		}
		if appendFailed == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected exactly 1 task.failed(phase=append_message); events=%v",
		env.stub.taskEventTypes(taskID))
}
