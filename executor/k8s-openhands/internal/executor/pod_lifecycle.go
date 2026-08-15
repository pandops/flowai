package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/flowai/platform/executor/k8s-openhands/internal/cache"
	"github.com/flowai/platform/executor/k8s-openhands/internal/k8sclient"
	"github.com/flowai/platform/executor/k8s-openhands/internal/openhands"
	"github.com/flowai/platform/executor/k8s-openhands/internal/platform"
)

func (e *Executor) runPodTask(ctx context.Context, slot *podSlot) {
	taskCtx, cancelTask := context.WithCancel(ctx)
	defer cancelTask()
	defer close(slot.doneCh)
	defer close(slot.exitCh)
	defer e.removePod(slot.taskID)

	updates, stop, err := e.kube.WatchPod(ctx, slot.podNamespace, slot.podName)
	if err != nil {
		e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "watch", err.Error())
		return
	}
	defer stop()
	var podIP string
	for update := range updates {
		if update.Status == k8sclient.PodStatusFailed {
			e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "pod", "pod failed before OpenHands became ready")
			return
		}
		if update.Status == k8sclient.PodStatusRunning && update.PodIP != "" {
			podIP = update.PodIP
			break
		}
	}
	if podIP == "" {
		if ctx.Err() == nil {
			e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "watch", "pod watch closed before pod became ready")
		}
		return
	}
	podEnded := make(chan string, 1)
	go func() {
		for update := range updates {
			if update.Status == k8sclient.PodStatusFailed {
				podEnded <- "pod failed while OpenHands was running"
				return
			}
		}
		if taskCtx.Err() == nil {
			podEnded <- "pod watch closed while OpenHands was running"
		}
	}()

	baseURL := fmt.Sprintf("http://%s:%d", podIP, e.cfg.OpenHandsPort)
	client := openhands.NewClient(baseURL, e.cfg.OpenHandsAPIKey, nil)
	if err := client.HealthCheck(ctx, 90*time.Second); err != nil {
		e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "health_check", err.Error())
		return
	}
	conversationID := slot.conversationID
	if conversationID == "" {
		if slot.llmAPIKey == "" || slot.llmBaseURL == "" || slot.llmModel == "" {
			e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "submit", "task launch parameters require OPENAI_API_KEY, OPENAI_BASE_URL, and OPENAI_MODEL")
			return
		}
		conversation, err := client.StartConversation(ctx, e.conversationConfig(slot.prompt, slot.llmAPIKey, slot.llmBaseURL, slot.llmModel))
		if err != nil {
			e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "submit", err.Error())
			return
		}
		conversationID = conversation.ID
		slot.conversationID = conversationID
	}
	if e.cache != nil && slot.conversationID != "" {
		_ = e.cache.PutAssignment(ctx, cache.AssignmentRecord{
			TaskID: slot.taskID, TeamID: slot.teamID, ExecutorID: e.cfg.ExecutorID,
			OwnerCommandID: slot.commandID, ResolvedImage: slot.resolvedImage,
			ImageSource: slot.imageSource, RuntimeKind: "k8s",
			RuntimeRef: slot.podName + "@" + slot.podUID, ConversationID: conversationID,
			State: "running", LastObserved: time.Now().UTC(),
		})
	}
	e.logger("observing OpenHands conversation %s for task %s", conversationID, slot.taskID)
	type terminalResult struct {
		status string
		err    error
	}
	terminal := make(chan terminalResult, 1)
	go func() {
		status, err := e.waitForTerminal(taskCtx, slot.taskID, baseURL, conversationID)
		terminal <- terminalResult{status: status, err: err}
	}()
	controls := time.NewTicker(500 * time.Millisecond)
	defer controls.Stop()
	var status string
	for status == "" {
		select {
		case result := <-terminal:
			if result.err != nil {
				e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "stream", result.err.Error())
				return
			}
			status = result.status
		case <-controls.C:
			cancelled, err := e.applyPendingControls(taskCtx, client, slot, conversationID)
			if err != nil {
				e.logger("read controls for task %s: %v", slot.taskID, err)
				continue
			}
			if cancelled {
				e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "control", "cancel")
				return
			}
		case detail := <-podEnded:
			e.finishPodTask(ctx, slot, platform.TaskEventTypeFailed, "pod", detail)
			return
		case <-taskCtx.Done():
			return
		}
	}
	eventType := platform.TaskEventTypeFailed
	if status == "finished" {
		eventType = platform.TaskEventTypeFinished
	}
	e.finishPodTask(ctx, slot, eventType, "terminal_state", status)
}

func (e *Executor) applyPendingControls(ctx context.Context, client *openhands.Client, slot *podSlot, conversationID string) (bool, error) {
	controls, err := e.registry.ListAssignedControls(ctx, slot.taskID)
	if err != nil {
		return false, err
	}
	for _, control := range controls {
		if control.Status != "pending" || control.Action != "cancel" {
			continue
		}
		acknowledgedAt := time.Now().UTC()
		if err := e.appendControlEvent(ctx, slot.taskID, control.ControlID, "acknowledged", acknowledgedAt, "cancellation received"); err != nil {
			return false, err
		}
		if err := client.PauseConversation(ctx, conversationID); err != nil {
			_ = e.appendControlEvent(ctx, slot.taskID, control.ControlID, "failed", acknowledgedAt.Add(time.Microsecond), "cancellation could not be sent")
			return false, err
		}
		if err := e.appendControlEvent(ctx, slot.taskID, control.ControlID, "completed", acknowledgedAt.Add(time.Microsecond), "cancellation sent to OpenHands"); err != nil {
			return false, err
		}
		e.logger("applied control %s to task %s", control.ControlID, slot.taskID)
		return true, nil
	}
	return false, nil
}

func (e *Executor) appendControlEvent(ctx context.Context, taskID, controlID, status string, occurredAt time.Time, message string) error {
	payload, _ := json.Marshal(map[string]string{"message": message})
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return e.registry.AppendTaskControlEvent(postCtx, taskID, controlID, platform.TaskControlEventAppendRequest{
		ControlEventID: "control-event-" + uuid.NewString(), Status: status,
		OccurredAt: occurredAt, Payload: payload,
	})
}

func (e *Executor) conversationConfig(prompt, taskAPIKey, taskBaseURL, taskModel string) openhands.ConversationConfig {
	cfg := openhands.ConversationConfig{
		Workspace:      openhands.Workspace{Kind: "LocalWorkspace", WorkingDir: e.cfg.OpenHandsWorkspace},
		InitialMessage: openhands.InitialMessage{Role: "user", Content: []openhands.ContentPart{{Type: "text", Text: prompt}}, Run: e.cfg.OpenHandsInitialRun},
	}
	if e.cfg.OpenHandsAgentProfile != "" {
		cfg.AgentProfileID = e.cfg.OpenHandsAgentProfile
	} else {
		cfg.Agent = &openhands.Agent{
			Kind:  "Agent",
			LLM:   openhands.LLM{Model: taskModel, APIKey: taskAPIKey, UsageID: e.cfg.OpenHandsLLMUsageID, BaseURL: taskBaseURL},
			Tools: []openhands.Tool{{Name: "terminal"}},
		}
	}
	return cfg
}

func (e *Executor) waitForTerminal(ctx context.Context, taskID, baseURL, conversationID string) (string, error) {
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + openhands.WSPath(conversationID)
	headers := http.Header{}
	if e.cfg.OpenHandsAPIKey != "" {
		headers.Set("X-Session-API-Key", e.cfg.OpenHandsAPIKey)
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, wsURL, headers)
	if response != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	if err != nil {
		return "", err
	}
	defer conn.Close()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return "", err
		}
		var event any
		if json.Unmarshal(data, &event) != nil {
			continue
		}
		e.appendPublishedOpenHandsLogs(ctx, taskID, event)
		status := conversationStatus(event)
		switch status {
		case "finished", "failed", "error", "stuck", "paused":
			return status, nil
		}
	}
}

func conversationStatus(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if raw, ok := object["execution_status"].(string); ok {
		if status := strings.ToLower(strings.TrimSpace(raw)); status != "" {
			return status
		}
	}
	for _, raw := range object {
		if status := conversationStatus(raw); status != "" {
			return status
		}
	}
	for _, key := range []string{"status", "kind"} {
		if raw, ok := object[key].(string); ok {
			if status := strings.ToLower(strings.TrimSpace(raw)); status != "" {
				return status
			}
		}
	}
	return ""
}

func (e *Executor) finishPodTask(ctx context.Context, slot *podSlot, eventType, phase, detail string) {
	payload, _ := json.Marshal(map[string]string{"phase": phase, "detail": detail})
	if err := e.appendTaskEvent(ctx, slot.taskID, slot.teamID, eventType, payload); err != nil {
		e.logger("append terminal event for %s: %v", slot.taskID, err)
		return
	}
	// The assignment is no longer reconcilable as soon as the Registry has
	// accepted its terminal event. Keep the Pod for the configured inspection
	// delay, but prevent a concurrent Executor restart from reattaching and
	// emitting a second terminal event.
	if e.cache != nil {
		_ = e.cache.DeleteAssignment(context.WithoutCancel(ctx), slot.taskID)
	}
	delay := e.cfg.FailedCleanupDelay
	if eventType == platform.TaskEventTypeFinished {
		delay = e.cfg.FinishedCleanupDelay
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
	}
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := e.kube.DeletePod(deleteCtx, slot.podNamespace, slot.podName); err != nil {
		e.logger("delete pod %s: %v", slot.podName, err)
	}
}

func promptFromPayload(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var body map[string]any
	if json.Unmarshal(payload, &body) == nil {
		for _, key := range []string{"prompt", "instructions", "input", "message"} {
			if value, ok := body[key].(string); ok {
				return value
			}
		}
	}
	return string(payload)
}
