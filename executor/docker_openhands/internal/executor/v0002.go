// v0002 state path: team/system-scoped registration, FIFO
// discovery, atomic FIFO claim with stable command_id, task-bound
// environment open via scope token, and ordered v0002 lifecycle
// events. The Router and legacy Env Registry are NEVER consulted on
// this path.
//
// Wire invariants (encoded here and enforced in stateregistryclient):
//
//   - The Executor only calls Discover / Claim while a local slot is
//     free. Local capacity is the only capacity gate; the State
//     Registry never gates discovery or claim.
//   - The Executor starts a container only after a successful 200
//     claim response. 404, 409 older_task_must_be_claimed_first,
//     409 task_already_claimed, or any other non-2xx response means
//     "do not start a runtime".
//   - The Executor reuses the SAME command_id for every retry of
//     the same (task_id). The State Registry returns the original
//     200 envelope on an identical retry without appending an event.
//   - The container image is the claim response's resolved_image
//     verbatim. The Executor never falls back to a local image.
//   - The Executor appends exactly one ordered pair: `running`
//     after claim, then exactly one of `finished` or `failed` at
//     terminal state. The `created` event is Registry-appended and
//     is NEVER sent by the Executor.
//   - The event envelope carries the Executor team binding (team
//     scope) or the parent task's team_id (system scope), and
//     never `team_name` as authorisation.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor/docker_openhands/internal/dockerclient"
	"github.com/flowai/platform/executor/docker_openhands/internal/openhands"
	"github.com/flowai/platform/executor/docker_openhands/internal/platform"
	"github.com/flowai/platform/executor/docker_openhands/internal/stateregistryclient"
)

// tickV0002 is one poll iteration on the v0002 State Registry path.
func (e *Executor) tickV0002(ctx context.Context) error {
	if e.v2002FreeSlots() == 0 {
		return nil
	}
	items, err := e.v0002.DiscoverTasks(ctx, e.cfg.AuthorizedTag, 100)
	if err != nil {
		if stateregistryclient.IsNotFound(err) {
			return nil
		}
		e.logger.Warn("v0002 discover failed", "err", err.Error())
		return err
	}
	if len(items) == 0 {
		return nil
	}
	// FIFO ordering is the Registry contract: (ingested_at ASC,
	// task_id ASC). The Registry already returns it sorted; we
	// re-sort defensively so a future server change cannot break
	// the local contract.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IngestedAt.Equal(items[j].IngestedAt) {
			return items[i].TaskID < items[j].TaskID
		}
		return items[i].IngestedAt.Before(items[j].IngestedAt)
	})
	for _, summary := range items {
		if e.v2002FreeSlots() <= 0 {
			break
		}
		if e.taskLifecycle(summary.TaskID) != taskLifecycleNone {
			continue
		}
		if _, ok := e.getSlot(summary.TaskID); ok {
			continue
		}
		// Only the oldest eligible is worth claiming: a non-oldest
		// attempt would return 409 older_task_must_be_claimed_first.
		// We still call Claim directly because the local capacity
		// gate means at most one outstanding claim attempt.
		if err := e.startTaskV0002(ctx, summary); err != nil {
			e.logger.Warn("v0002 start task failed", "task_id", summary.TaskID, "err", err.Error())
			break
		}
	}
	return nil
}

// v2002FreeSlots returns the number of additional v0002 tasks
// the Executor can accept. The local capacity gate is
// len(slots) + slotReservations; when no slot is installed yet
// the reservation still counts so a failed container start does
// not let the next tick claim the next FIFO task before the
// current one is terminalized.
func (e *Executor) v2002FreeSlots() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.MaxContainers - len(e.slots) - e.slotReservations
}

// startTaskV0002 claims the task, opens the environment, and
// starts the container. Every step is conditional on the previous
// step's 200 response: a 404, 409, or other error leaves the slot
// free and the runtime unstarted.
func (e *Executor) startTaskV0002(ctx context.Context, summary platform.V0002TaskSummary) error {
	taskID := summary.TaskID
	commandID := e.commandIDFor(taskID)

	claimCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	claim, err := e.v0002.ClaimTask(claimCtx, platform.V0002ClaimRequest{
		TaskID:    taskID,
		CommandID: commandID,
	})
	cancel()
	if err != nil {
		switch {
		case stateregistryclient.IsOlderTaskMustBeClaimedFirst(err),
			stateregistryclient.IsTaskAlreadyClaimed(err):
			// No mutation, no event, no container.
			return nil
		case stateregistryclient.IsNotFound(err):
			// Non-revealing 404: the task is foreign or unknown.
			// Discard the candidate without probing for its
			// existence.
			return nil
		default:
			return fmt.Errorf("claim %s: %w", taskID, err)
		}
	}
	if claim == nil || claim.Claim != "claimed" {
		return fmt.Errorf("claim %s: unexpected envelope", taskID)
	}

	// Record the slot up-front so a second tick does not race
	// to claim the same task_id again before runTask installs
	// the slot. claim.Task.TeamID is canonical and immutable
	// for the row's lifetime.
	if err := e.markAcceptedV0002(taskID); err != nil {
		return err
	}
	// Reserve the local slot count BEFORE the container start
	// so a failed start does not let the next tick claim the
	// next FIFO task. The reservation is released by
	// terminalizeAcceptedV0002 on every pre-slot failure path.
	e.reserveLocalSlot()
	e.observeSlotCount()
	slotInstalled := false
	defer func() {
		// If we never installed the real slot, release the
		// reservation so the next tick can attempt the next
		// FIFO task.
		if !slotInstalled {
			e.releaseLocalSlotReservation()
		}
	}()
	if claim.ClaimedAt != nil {
		claimedAt, parseErr := time.Parse(time.RFC3339Nano, *claim.ClaimedAt)
		if parseErr == nil {
			target := claimedAt.Truncate(time.Millisecond).Add(time.Millisecond)
			if delay := time.Until(target); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					e.terminalizeAcceptedV0002(taskID)
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
	}

	runningPayload, err := json.Marshal(map[string]string{"phase": "running"})
	if err != nil {
		e.terminalizeAcceptedV0002(taskID)
		return fmt.Errorf("encode running event: %w", err)
	}
	if err := e.appendV0002TaskEventForTeam(
		ctx,
		taskID,
		claim.Task.TeamID,
		platform.TaskEventTypeRunning,
		runningPayload,
	); err != nil {
		e.terminalizeAcceptedV0002(taskID)
		return fmt.Errorf("append running event: %w", err)
	}

	// Open the environment with the claim-issued scope token.
	// When the claim carries no environment_id or scope_token, the
	// task has no environment binding and the Executor uses an
	// empty value set (no legacy /v1/env call).
	envValues := map[string]string{}
	if claim.LaunchParameters {
		if claim.ScopeToken == nil || *claim.ScopeToken == "" {
			return fmt.Errorf("claim %s: launch parameters require scope_token", taskID)
		}
		openCtx, openCancel := context.WithTimeout(ctx, 10*time.Second)
		envValues, err = e.v2002OpenEnvironment(openCtx, claim, taskID)
		openCancel()
		if err != nil {
			e.terminalizeAcceptedV0002(taskID)
			e.appendV0002TaskEventQuietForTeam(ctx, taskID, claim.Task.TeamID, platform.TaskEventTypeFailed, map[string]string{
				"phase": "environment_open",
			})
			return fmt.Errorf("open environment: %w", err)
		}
	}

	// Resolve the image. resolved_image is always non-null on a
	// successful claim (the four-level precedence ends at
	// teams.default_image which is REQUIRED at admin
	// registration). The Executor uses it verbatim and refuses to
	// substitute a local fallback image. The Registry returns the
	// image both at the top level of the claim response and on
	// the canonical task row; the top-level field is the
	// canonical reference per the OpenAPI contract.
	image := claim.ResolvedImage
	if image == nil || image.Repository == "" {
		image = claim.Task.ResolvedImage
	}
	if image == nil || image.Repository == "" {
		e.terminalizeAcceptedV0002(taskID)
		return fmt.Errorf("claim %s: missing resolved_image", taskID)
	}
	imageRef := image.Repository
	if image.Tag != "" {
		imageRef += ":" + image.Tag
	} else if image.Digest != "" {
		imageRef += "@" + image.Digest
	}
	imageSource := ""
	if claim.ImageSource != nil {
		imageSource = *claim.ImageSource
	}

	port, err := e.allocatePort()
	if err != nil {
		e.terminalizeAcceptedV0002(taskID)
		return err
	}

	spec := dockerclient.ContainerSpec{
		Name:  "oh-" + shortID(taskID),
		Image: imageRef,
		Env:   dockerEnvWithSessionAPIKey(envValues, e.cfg.OpenHandsAPIKey),
		Labels: map[string]string{
			dockerclient.LabelExecutorID:          e.cfg.ExecutorID,
			dockerclient.LabelCleanupID:           e.cleanup,
			dockerclient.LabelRuntime:             dockerclient.RuntimeOpenHands,
			dockerclient.LabelTaskID:              taskID,
			dockerclient.LabelTeamID:              claim.Task.TeamID,
			dockerclient.LabelExecutorScope:       e.cfg.Scope,
			dockerclient.LabelCommandID:           commandID,
			dockerclient.LabelResolvedImageSource: imageSource,
		},
		Ports: []dockerclient.PortMapping{
			{HostPort: port, ContainerPort: 8000, Protocol: "tcp"},
		},
	}
	if err := e.docker.PullImage(ctx, imageRef, e.cfg.ImagePullPolicy); err != nil {
		e.releasePort(port)
		e.terminalizeAcceptedV0002(taskID)
		e.appendV0002TaskEventQuietForTeam(ctx, taskID, claim.Task.TeamID, platform.TaskEventTypeFailed, map[string]string{
			"phase": "image_pull",
		})
		return fmt.Errorf("pull %s: %w", imageRef, err)
	}
	ref, err := e.docker.StartContainer(ctx, spec)
	if err != nil {
		e.releasePort(port)
		e.terminalizeAcceptedV0002(taskID)
		e.appendV0002TaskEventQuietForTeam(ctx, taskID, claim.Task.TeamID, platform.TaskEventTypeFailed, map[string]string{
			"phase": "container_create",
		})
		return fmt.Errorf("start container: %w", err)
	}

	rctx, runCancel := context.WithCancel(ctx)
	slot := &taskSlot{
		v0002Task: &v0002TaskRef{
			task:          claim.Task,
			commandID:     commandID,
			resolvedImage: imageRef,
			imageSource:   imageSource,
			llmAPIKey:     envValues["OPENAI_API_KEY"],
			llmBaseURL:    envValues["OPENAI_BASE_URL"],
			llmModel:      envValues["OPENAI_MODEL"],
		},
		containerID:   ref.ID,
		containerName: ref.Name,
		hostPort:      port,
		startedAt:     time.Now().UTC(),
		doneCh:        make(chan struct{}),
		exitCh:        make(chan struct{}),
		cancel:        runCancel,
	}
	e.addSlot(taskID, slot)
	slotInstalled = true
	// The slot is now real; release the reservation so
	// v2002FreeSlots accounts for it as one of the live slots.
	e.releaseLocalSlotReservation()
	e.logger.Info("v0002 container started",
		"task_id", taskID, "container_id", ref.ID, "host_port", port,
		"image", imageRef, "image_source", imageSource, "slot_count", e.slotCount())

	go e.watchContainerExitV0002(rctx, slot)
	go e.runTaskV0002(rctx, slot)
	return nil
}

// v2002OpenEnvironment wraps the v0002 environment.open call with
// the documented 404 = non-revealing semantics: a foreign or
// unknown environment returns an empty value set and the
// Executor continues without a legacy /v1/env fallback.
func (e *Executor) v2002OpenEnvironment(ctx context.Context, claim *platform.V0002ClaimResponse, taskID string) (map[string]string, error) {
	token := *claim.ScopeToken
	vals, err := e.v0002.OpenEnvironment(ctx, taskID, token)
	if err != nil {
		if stateregistryclient.IsNotFound(err) {
			e.logger.Info("v0002 environment open: non-revealing 404", "task_id", taskID)
			return nil, errors.New("environment is unknown or unavailable")
		}
		return nil, err
	}
	return vals, nil
}

// commandIDFor returns the stable command_id for the given task_id.
// The first time a task_id is observed, a fresh UUID is minted and
// cached. Every retry (and every internal caller that wants the
// same command for the same task_id) sees the same value, so the
// State Registry's (task_id, command_id) idempotency handler
// returns the original 200 without appending an additional event.
func (e *Executor) commandIDFor(taskID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v, ok := e.commandIDs[taskID]; ok && v != "" {
		return v
	}
	id := "cmd-" + uuid.NewString()
	e.commandIDs[taskID] = id
	return id
}

func (e *Executor) markAcceptedV0002(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.acceptedTasks[taskID] {
	case taskLifecycleRunning, taskLifecycleTerminal:
		return errors.New("task already accepted")
	default:
		e.acceptedTasks[taskID] = taskLifecycleRunning
	}
	return nil
}

func (e *Executor) terminalizeAcceptedV0002(taskID string) {
	e.mu.Lock()
	delete(e.acceptedTasks, taskID)
	delete(e.commandIDs, taskID)
	e.mu.Unlock()
}

// reserveLocalSlot increments the local slot reservation counter.
// tickV0002 checks len(slots) + slotReservations against
// MaxContainers so a claimed task reserves capacity before the
// container is started.
func (e *Executor) reserveLocalSlot() {
	e.mu.Lock()
	e.slotReservations++
	e.mu.Unlock()
}

// releaseLocalSlotReservation decrements the local slot
// reservation counter. Called when a claimed task never
// installs a real slot.
func (e *Executor) releaseLocalSlotReservation() {
	e.mu.Lock()
	if e.slotReservations > 0 {
		e.slotReservations--
	}
	e.mu.Unlock()
}

// appendV0002TaskEvent posts a task lifecycle event with the v0002
// envelope. The envelope team_id is the parent task's team_id (per
// the claim response) for both team-owned and system-owned
// Executors; system scope carries the same team_id the parent task
// has, not a null value, on TASK-scoped events.
func (e *Executor) appendV0002TaskEvent(ctx context.Context, taskID, eventType string, payload []byte) error {
	return e.appendV0002TaskEventForTeam(ctx, taskID, e.v0002TaskTeamID(taskID), eventType, payload)
}

func (e *Executor) appendV0002TaskEventForTeam(ctx context.Context, taskID, teamID, eventType string, payload []byte) error {
	if e.v0002 == nil {
		return errors.New("v0002 client is not configured")
	}
	eventID := "evt-" + uuid.NewString()
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := e.v0002.AppendTaskEvent(postCtx, platform.V0002TaskEventEnvelope{
		EventID:    eventID,
		TeamID:     teamID,
		TaskID:     taskID,
		ExecutorID: e.cfg.ExecutorID,
		EventType:  eventType,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:    payload,
	})
	return err
}

// appendV0002TaskEventQuiet is the best-effort variant used by
// failure phases. It logs but does not return errors.
func (e *Executor) appendV0002TaskEventQuiet(ctx context.Context, taskID, eventType string, payloadMap map[string]string) {
	e.appendV0002TaskEventQuietForTeam(ctx, taskID, e.v0002TaskTeamID(taskID), eventType, payloadMap)
}

func (e *Executor) appendV0002TaskEventQuietForTeam(ctx context.Context, taskID, teamID, eventType string, payloadMap map[string]string) {
	payload, _ := json.Marshal(payloadMap)
	if err := e.appendV0002TaskEventForTeam(ctx, taskID, teamID, eventType, payload); err != nil {
		e.logger.Warn("v0002 task event append failed",
			"task_id", taskID, "type", eventType, "err", err.Error())
	}
}

// v0002TaskTeamID returns the canonical team_id of the parent task
// for envelope purposes. For team-owned Executors the parent task's
// team_id always equals the Executor's binding. For system-owned
// Executors it is the parent task's team_id; we read the cached
// slot's claim record first and fall back to the immutable binding
// (the only safe system-scope choice when no slot is present).
func (e *Executor) v0002TaskTeamID(taskID string) string {
	if slot, ok := e.getSlot(taskID); ok && slot.v0002Task != nil {
		return slot.v0002Task.task.TeamID
	}
	if e.cfg.Scope == "team" {
		return e.cfg.TeamID
	}
	return ""
}

// appendV0002ExecutorEvent posts a self event. The envelope team_id
// is the Executor binding for team scope, null for system scope.
func (e *Executor) appendV0002ExecutorEvent(ctx context.Context, eventType platform.V0002ExecutorSelfEventType, payload map[string]interface{}) {
	if e.v0002 == nil {
		return
	}
	rawPayload, _ := json.Marshal(payload)
	var teamID *string
	if e.cfg.Scope == "team" {
		t := e.cfg.TeamID
		teamID = &t
	}
	postCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := e.v0002.AppendExecutorEvent(postCtx, platform.V0002ExecutorEventEnvelope{
		EventID:    "evt-" + uuid.NewString(),
		TeamID:     teamID,
		ExecutorID: e.cfg.ExecutorID,
		EventType:  string(eventType),
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:    rawPayload,
	}); err != nil {
		e.logger.Warn("v0002 executor self event append failed",
			"type", string(eventType), "err", err.Error())
	}
}

// runTaskV0002 is the per-task lifecycle: health check, submit
// prompt, WS stream, terminal event. The v0002 event emission is
// driven by terminal-state transitions only; intermediate OpenHands
// frames are observed but no v0002 task events are written for
// them (the v0002 contract is `running` -> terminal).
func (e *Executor) runTaskV0002(ctx context.Context, slot *taskSlot) {
	defer slot.signalExit()
	defer slot.signalDone()
	defer e.setReadyIfLast()
	defer e.removeSlot(slot.v0002Task.task.TaskID)
	// Cleanup, including any configured terminal retention delay, runs
	// before removeSlot so the retained container continues to occupy a
	// local capacity slot for the whole delay.
	defer e.cleanupContainer(slot)
	defer e.terminalizeV0002Slot(slot)

	addr := e.docker.ContainerURL(slot.hostPort)
	client := openhands.NewClient(addr, e.cfg.OpenHandsAPIKey, nil)

	if err := client.HealthCheck(ctx, e.cfg.OpenHandsStartupTO); err != nil {
		e.failV0002Slot(ctx, slot, "health_check", err.Error())
		return
	}
	e.openHandsOK.Store(true)
	if slot.v0002Task.llmAPIKey == "" || slot.v0002Task.llmBaseURL == "" || slot.v0002Task.llmModel == "" {
		e.failV0002Slot(ctx, slot, "submit", "task launch parameters require OPENAI_API_KEY, OPENAI_BASE_URL, and OPENAI_MODEL")
		return
	}

	convCfg := e.conversationConfig(promptFromV0002Payload(slot.v0002Task.task.Payload), slot.v0002Task.llmAPIKey, slot.v0002Task.llmBaseURL, slot.v0002Task.llmModel)
	conv, err := client.StartConversation(ctx, convCfg)
	if err != nil {
		e.failV0002Slot(ctx, slot, "submit", err.Error())
		return
	}
	slot.setOpenHandsID(conv.ID, addr)
	controlsCtx, stopControls := context.WithCancel(ctx)
	defer stopControls()
	go e.watchTaskControls(controlsCtx, slot, client, conv.ID)

	if err := e.streamOpenHandsEvents(ctx, slot, conv.ID); err != nil {
		if slot.claimTerminal() {
			slot.terminalCause = "stream"
			e.appendV0002TaskEventQuiet(ctx, slot.v0002Task.task.TaskID, platform.TaskEventTypeFailed, map[string]string{
				"phase": "stream",
				"error": err.Error(),
			})
		}
		return
	}
}

// watchContainerExitV0002 turns a Docker container exit before the
// authoritative OpenHands finished signal into one terminal failure. The
// slot terminal guard makes the watcher race-safe with the conversation
// stream and shutdown paths.
func (e *Executor) watchContainerExitV0002(ctx context.Context, slot *taskSlot) {
	if slot == nil {
		return
	}
	events, errs := e.docker.SubscribeEvents(ctx, dockerclient.EventFilter{Entries: []dockerclient.EventFilterEntry{
		{Key: dockerclient.EventFilterKeyType, Value: "container"},
		{Key: dockerclient.EventFilterKeyEvent, Value: "die"},
		{Key: dockerclient.EventFilterKeyContainer, Value: slot.containerID},
	}})
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errs:
			if ok && err != nil {
				e.logger.Warn("Docker exit watcher failed", "task_id", slotTaskID(slot), "err", err.Error())
			}
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Action != "die" || (event.ActorID != slot.containerID && event.ActorName != slot.containerName) {
				continue
			}
			if !slot.claimTerminal() {
				return
			}
			slot.terminalCause = "container_died"
			payload, _ := json.Marshal(map[string]string{
				"phase":          "container_exit",
				"failure_reason": "container_exited_before_finish",
			})
			if err := e.appendV0002TaskEvent(ctx, slotTaskID(slot), platform.TaskEventTypeFailed, payload); err != nil {
				e.logger.Warn("v0002 task event append failed", "task_id", slotTaskID(slot), "type", platform.TaskEventTypeFailed, "err", err.Error())
			} else {
				slot.recordAcceptedTerminal(platform.TaskEventTypeFailed)
			}
			if slot.cancel != nil {
				slot.cancel()
			}
			return
		}
	}
}

func (e *Executor) failV0002Slot(ctx context.Context, slot *taskSlot, phase, errMsg string) {
	if slot.claimTerminal() {
		slot.terminalCause = phase
		e.appendV0002TaskEventQuiet(ctx, slot.v0002Task.task.TaskID, platform.TaskEventTypeFailed, map[string]string{
			"phase": phase,
			"error": errMsg,
		})
	}
}

func (e *Executor) terminalizeV0002Slot(slot *taskSlot) {
	if slot == nil {
		return
	}
	if slot.claimTerminal() {
		e.markAccepted(slot.v0002Task.task.TaskID, taskLifecycleTerminal)
	}
	e.releasePort(slot.hostPort)
	// Free the cached command_id so memory does not grow over the
	// Executor's lifetime.
	e.mu.Lock()
	delete(e.commandIDs, slot.v0002Task.task.TaskID)
	e.mu.Unlock()
}

// promptFromV0002Payload extracts a human-readable prompt from a
// v0002 task payload. The payload is opaque to the Executor; the
// v0002 contract does not constrain its shape, so the helper
// recognises the common `prompt` / `instructions` keys and falls
// back to the raw payload when neither is present.
func promptFromV0002Payload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err == nil {
		for _, key := range []string{"prompt", "instructions", "input", "message"} {
			if v, ok := obj[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return string(payload)
}

// refreshRegistrationV0002 re-PUTs the v0002 Executor record with
// the current running_count observation. The capacity numbers are
// informational; the State Registry does not gate work on them.
func (e *Executor) refreshRegistrationV0002(running int) {
	if e.v0002 == nil {
		return
	}
	md, _ := json.Marshal(map[string]interface{}{
		"runtime":    "docker",
		"tool":       "openhands",
		"cleanup_id": e.cleanup,
	})
	var teamID *string
	if e.cfg.Scope == "team" {
		t := e.cfg.TeamID
		teamID = &t
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := e.v0002.RegisterExecutor(ctx, stateregistryclient.RegisterExecutorRequest{
		Scope:           e.cfg.Scope,
		TeamID:          teamID,
		ExecutorType:    platform.ExecutorTypeDockerOpenHands,
		AuthorizedTag:   e.cfg.AuthorizedTag,
		MaxCapacity:     e.cfg.MaxContainers,
		RunningCount:    running,
		RuntimeMetadata: md,
	}); err != nil {
		e.logger.Warn("v0002 registration refresh failed", "err", err.Error())
	}
}
