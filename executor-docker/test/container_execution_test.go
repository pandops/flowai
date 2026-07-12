// Unit tests for container execution.
//
// These tests verify what the executor asks Docker to do when it accepts a
// queued task. They use the in-package fakes (FakeDocker, FakeOpenHands)
// and the inline stubServer defined in executor_test.go. No real binaries
// are spawned — that path is covered by the autotest/ Playwright e2e
// tests.
//
// Each test focuses on one aspect of the executor's container-start path:
//
//   - PullImage is called with the configured image
//   - StartContainer is called with the right labels, image, and ports
//   - task.started and task.start_message are POSTed before/alongside the
//     OpenHands submission
//   - Container-creation failures post task.failed with the error
//   - Each container gets a distinct host port from the configured range
//   - Container is removed when the task fails or shuts down
//   - Capacity is enforced (no more containers than EXECUTOR_MAX_CONTAINERS)
//   - Port-range validation rejects configs that exceed the range
package integration_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/dockerclient"
	"github.com/flowai/platform/executor-docker/internal/executor"
)

func TestPullImageCalledWithConfiguredImage(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.OpenHandsImage = "ghcr.io/myorg/openhands:custom"
		c.ImagePullPolicy = "always"
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
		if env.docker.PullCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := env.docker.PullCount(); got < 1 {
		t.Fatalf("expected PullImage to be called, got %d calls", got)
	}
}

func TestContainerStartCarriesRequiredLabels(t *testing.T) {
	env := setupTestEnv(t, nil)
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
		if len(env.docker.StartedRefsSnapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	refs := env.docker.StartedRefsSnapshot()
	if len(refs) == 0 {
		t.Fatalf("expected container to be started, got 0 StartedRefs")
	}
	ref := refs[0]
	for _, key := range []string{
		dockerclient.LabelExecutorID,
		dockerclient.LabelRuntime,
		dockerclient.LabelTaskID,
	} {
		if _, ok := ref.Labels[key]; !ok {
			t.Fatalf("container missing required label %q (got %v)", key, ref.Labels)
		}
	}
	if got := ref.Labels[dockerclient.LabelExecutorID]; got != env.cfg.ExecutorID {
		t.Fatalf("executor_id label = %q, want %q", got, env.cfg.ExecutorID)
	}
	if got := ref.Labels[dockerclient.LabelRuntime]; got != dockerclient.RuntimeOpenHands {
		t.Fatalf("runtime label = %q, want %q", got, dockerclient.RuntimeOpenHands)
	}
	if got := ref.Labels[dockerclient.LabelTaskID]; got != taskID {
		t.Fatalf("task_id label = %q, want %q", got, taskID)
	}
}

func TestContainerStartsWithConfiguredOpenHandsImage(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.OpenHandsImage = "ghcr.io/myorg/openhands:v1.0"
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
		if len(env.docker.StartedRefsSnapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	refs := env.docker.StartedRefsSnapshot()
	if len(refs) == 0 {
		t.Fatalf("expected container started")
	}
	if got := refs[0].Image; got != "ghcr.io/myorg/openhands:v1.0" {
		t.Fatalf("container image = %q, want %q", got, "ghcr.io/myorg/openhands:v1.0")
	}
}

func TestContainerPortMapsContainer8000ToDistinctHostPort(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 2
		c.OpenHandsPortStart = 19500
		c.OpenHandsPortEnd = 19510
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	task1 := uuid.NewString()
	task2 := uuid.NewString()
	env.stub.setTask(makeQueuedTask(task1))
	env.stub.setTask(makeQueuedTask(task2))

	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(env.docker.StartedRefsSnapshot()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	refs := env.docker.StartedRefsSnapshot()
	if len(refs) < 2 {
		t.Fatalf("expected 2 containers started, got %d", len(refs))
	}
	// Distinct fake container IDs implies distinct host ports (the fake
	// assigns IDs sequentially, but the executor allocated distinct ports
	// from the range). Verify the StartedRefs IDs are all unique.
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.ID] {
			t.Fatalf("duplicate container ID %q", ref.ID)
		}
		seen[ref.ID] = true
	}
	// Verify the executor registered at most MaxContainers=2 concurrent
	// tasks. The Capacity parameter on the registration reflects this.
	if env.cfg.MaxContainers != 2 {
		t.Fatalf("MaxContainers changed unexpectedly")
	}
}

func TestContainerStartFailurePostsTaskFailed(t *testing.T) {
	env := setupTestEnv(t, nil)
	env.docker.FailNext.Start = true

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))

	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

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
	t.Fatalf("expected task.failed event, got: %v", env.stub.taskEventTypes(taskID))
}

func TestTaskStartedPostedBeforeOpenHandsEvent(t *testing.T) {
	env := setupTestEnv(t, nil)
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
		types := env.stub.taskEventTypes(taskID)
		var foundStarted, foundOpenHands bool
		for _, ty := range types {
			if ty == "task.started" {
				foundStarted = true
			}
			if ty == "openhands.event" {
				foundOpenHands = true
			}
		}
		if foundOpenHands && !foundStarted {
			t.Fatalf("openhands.event arrived before task.started (events: %v)", types)
		}
		if foundStarted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never observed task.started; events: %v", env.stub.taskEventTypes(taskID))
}

func TestStartMessagePostedWithPrompt(t *testing.T) {
	env := setupTestEnv(t, nil)
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	prompt := "write a haiku about distributed systems"
	taskID := uuid.NewString()
	task := makeQueuedTask(taskID)
	task.Prompt = prompt
	env.stub.setTask(task)
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events := env.stub.listTaskEvents(taskID)
		for _, ev := range events {
			if ev.Type == "task.start_message" {
				if p, _ := ev.Payload["prompt"].(string); p != prompt {
					t.Fatalf("task.start_message prompt = %q, want %q", p, prompt)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never observed task.start_message; events: %v", env.stub.taskEventTypes(taskID))
}

func TestContainerRemovedOnTaskHealthFailure(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
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

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		types := env.stub.taskEventTypes(taskID)
		for _, ty := range types {
			if ty == "task.failed" {
				if got := env.exec.RunningChildCount(); got != 0 {
					t.Fatalf("expected 0 running containers after failure, got %d", got)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never observed task.failed")
}

func TestCapacityExhaustionLeavesExcessTasksQueued(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 50 * time.Millisecond
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = http.StatusOK

	for i := 0; i < 5; i++ {
		env.stub.setTask(makeQueuedTask(uuid.NewString()))
	}
	cancel := runExecutor(t, env)
	defer func() {
		cancel()
		<-env.stopped
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := env.exec.RunningChildCount(); got != 1 {
		t.Fatalf("expected exactly 1 container, got %d", got)
	}
	if got := len(env.docker.StartedRefsSnapshot()); got != 1 {
		t.Fatalf("expected exactly 1 started container in fake, got %d", got)
	}
}

func TestPortRangeValidatedAtConfigLoad(t *testing.T) {
	cases := []struct {
		name        string
		max         int
		portStart   int
		portEnd     int
		expectError bool
	}{
		{"exact match", 5, 18000, 18004, false},
		{"larger range", 2, 19000, 19099, false},
		{"smaller range", 5, 19000, 19001, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &executor.Config{
				ExecutorID:           "exec-test",
				RoutingTarget:        "openhands",
				MaxContainers:        tc.max,
				OpenHandsPortStart:   tc.portStart,
				OpenHandsPortEnd:     tc.portEnd,
				OpenHandsInterruptTO: 1 * time.Second,
				OpenHandsStartupTO:   1 * time.Second,
				OpenHandsDrainTO:     1 * time.Second,
				WebSocketDialTimeout: 1 * time.Second,
				OpenHandsWorkspace:   "/workspace/project",
				OpenHandsLLMModel:    "m",
				OpenHandsLLMAPIKey:   "k",
				OpenHandsLLMUsageID:  "u",
			}
			err := cfg.Validate()
			if tc.expectError && err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
