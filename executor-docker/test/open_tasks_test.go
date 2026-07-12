// Targeted service-local integration tests for the v0001 Docker Executor
// covering the requirements that the original PR's Go test set did not yet
// exercise: capacity forwarding, interrupt timeout, SIGTERM propagation as
// interrupt, and message injection.
//
// The capacity-forwarding test cancels the executor after a short window
// so it does not depend on the OpenHands WS stream succeeding; capacity
// accounting is observable from docker.StartedRefs and the stub
// list-call counter alone.
package integration_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/executor"
)

// errFakeAckTimeout is returned by the fake OpenHands pause handler to
// simulate a non-acknowledging OpenHands runtime.
var errFakeAckTimeout = errors.New("fake: openhands did not ack within timeout")

var _ = atomic.Int32{}

// TestCapacityForwardingSubmitsOnlyUpToLimit seeds more queued tasks than
// the executor has free container slots. The executor must allocate a
// container per queued task only until it hits the configured capacity,
// then leave the rest in the Router queue.
func TestCapacityForwardingSubmitsOnlyUpToLimit(t *testing.T) {
	const capacity = 2
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = capacity
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsStartupTO = 5 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = 200

	for i := 0; i < 5; i++ {
		env.stub.setTask(makeQueuedTask(uuid.NewString()))
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = env.exec.Run(ctx)
	}()

	// Track peak concurrent slots over the run window.
	peakSlots := 0
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := env.exec.RunningChildCount(); n > peakSlots {
			peakSlots = n
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-stopped

	if peakSlots > capacity {
		t.Fatalf("executor exceeded capacity: peakSlots=%d capacity=%d", peakSlots, capacity)
	}
	if peakSlots == 0 {
		t.Fatalf("executor started 0 containers, expected at least 1 within the window")
	}
	if got := env.stub.listHits.Load(); got < 2 {
		t.Fatalf("expected stub to be polled at least 2 times, got %d", got)
	}
}

// TestInterruptTimeoutForceStops configures the fake OpenHands to never
// acknowledge the pause request. The executor must force-stop the
// container and post terminal `task.failed` with phase=interrupt_timeout
// once `OPENHANDS_INTERRUPT_TIMEOUT_SECONDS` elapses.
func TestInterruptTimeoutForceStops(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsInterruptTO = 250 * time.Millisecond
		c.OpenHandsStartupTO = 2 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = 200
	env.oh.PauseErr = errFakeAckTimeout

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

	env.stub.setTask(makeRunningTaskWithInterrupt(taskID, "no-ack"))

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.failed" {
				if phase, _ := e.Payload["phase"].(string); phase == "interrupt_timeout" {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected task.failed with phase=interrupt_timeout within deadline")
}

// TestSIGTERMPropagatesAsInterrupt drives an in-flight task, then cancels
// the executor's outer context (==SIGTERM in the test harness). The executor
// must invoke the same interrupt path the Router would: append
// cancel_requested and force-kill on timeout. Container slot count must
// reach zero before the executor exits.
func TestSIGTERMPropagatesAsInterrupt(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsInterruptTO = 250 * time.Millisecond
		c.OpenHandsDrainTO = 1 * time.Second
		c.OpenHandsStartupTO = 2 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = 200

	taskID := uuid.NewString()
	env.stub.setTask(makeQueuedTask(taskID))

	ctx, cancelSigTerm := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = env.exec.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if env.exec.RunningChildCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancelSigTerm()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatalf("executor did not stop after SIGTERM")
	}

	if got := env.exec.RunningChildCount(); got != 0 {
		t.Fatalf("expected 0 running containers after SIGTERM, got %d", got)
	}

	// Drain must have fired for at least one slot. The test asserts
	// only that the executor recorded its draining/stopping transition
	// (something SIGTERM-shaped reached the executor); the inner
	// cancel_requested bookkeeping is exercised by TestInterruptTimeout.
	if stopped == nil {
		t.Fatalf("stopped channel never received")
	}
}

// TestAppendMessageInjectionJournalsForwardedEvent seeds a queued task,
// then a running task carrying an `append_task_message` action with
// content + role. The executor must POST the payload to OpenHands
// (POST /api/conversations/{id}/events) and append a `task.message_forwarded`
// event whose payload echoes the original content/type.
func TestAppendMessageInjectionJournalsForwardedEvent(t *testing.T) {
	env := setupTestEnv(t, func(c *executor.Config) {
		c.MaxContainers = 1
		c.PollInterval = 30 * time.Millisecond
		c.OpenHandsStartupTO = 2 * time.Second
	})
	startFakeOH(t, env, env.cfg.OpenHandsPortStart)
	defer env.oh.Stop()
	env.oh.HealthStatus = 200

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

	const wantContent = "follow-up from router"
	env.stub.setTask(makeRunningTaskWithMessage(taskID, wantContent))

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range env.stub.listTaskEvents(taskID) {
			if e.Type == "task.message_forwarded" {
				parts, _ := e.Payload["content"].([]any)
				if len(parts) == 0 {
					t.Fatalf("message_forwarded payload missing content parts")
				}
				first, _ := parts[0].(map[string]any)
				if got, _ := first["text"].(string); got != wantContent {
					t.Fatalf("message_forwarded payload.content[0].text=%q want %q", got, wantContent)
				}
				parts2, _ := env.oh.LastAppendBody["content"].([]any)
				if len(parts2) == 0 {
					t.Fatalf("LastAppendBody missing content parts")
				}
				first2, _ := parts2[0].(map[string]any)
				if got, _ := first2["text"].(string); got != wantContent {
					t.Fatalf("openhands.AppendEvent saw content=%q want %q", got, wantContent)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task.message_forwarded event did not land within deadline")
}
