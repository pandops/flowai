package executor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor/docker_openhands/internal/openhands"
	"github.com/flowai/platform/executor/docker_openhands/internal/platform"
)

func (e *Executor) watchTaskControls(ctx context.Context, slot *taskSlot, client *openhands.Client, conversationID string) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			controls, err := e.v0002.ListAssignedControls(ctx, slotTaskID(slot))
			if err != nil {
				e.logger.Warn("read task controls failed", "task_id", slotTaskID(slot), "err", err.Error())
				continue
			}
			for _, control := range controls {
				if control.Status != "pending" || control.Action != "cancel" {
					continue
				}
				e.applyCancelControl(ctx, slot, client, conversationID, control)
				return
			}
		}
	}
}

func (e *Executor) applyCancelControl(ctx context.Context, slot *taskSlot, client *openhands.Client, conversationID string, control platform.V0002TaskControl) {
	taskID := slotTaskID(slot)
	acknowledgedAt := time.Now().UTC()
	if err := e.appendControlEvent(ctx, taskID, control.ControlID, "acknowledged", acknowledgedAt, "cancellation received"); err != nil {
		e.logger.Warn("acknowledge cancellation failed", "task_id", taskID, "control_id", control.ControlID, "err", err.Error())
		return
	}
	if err := client.PauseConversation(ctx, conversationID); err != nil {
		_ = e.appendControlEvent(ctx, taskID, control.ControlID, "failed", acknowledgedAt.Add(time.Microsecond), "cancellation could not be sent")
		e.logger.Warn("send cancellation failed", "task_id", taskID, "control_id", control.ControlID, "err", err.Error())
		return
	}
	if err := e.appendControlEvent(ctx, taskID, control.ControlID, "completed", acknowledgedAt.Add(time.Microsecond), "cancellation sent to OpenHands"); err != nil {
		e.logger.Warn("complete cancellation failed", "task_id", taskID, "control_id", control.ControlID, "err", err.Error())
		return
	}
	if slot.claimTerminal() {
		slot.terminalCause = "control"
		e.appendV0002TaskEventQuiet(ctx, taskID, platform.TaskEventTypeFailed, map[string]string{"phase": "control", "control": "cancel"})
		slot.recordAcceptedTerminal(platform.TaskEventTypeFailed)
	}
	if slot.cancel != nil {
		slot.cancel()
	}
}

func (e *Executor) appendControlEvent(ctx context.Context, taskID, controlID, status string, occurredAt time.Time, message string) error {
	payload, _ := json.Marshal(map[string]string{"message": message})
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return e.v0002.AppendTaskControlEvent(postCtx, taskID, controlID, platform.TaskControlEventAppendRequest{
		ControlEventID: "control-event-" + uuid.NewString(), Status: status,
		OccurredAt: occurredAt, Payload: payload,
	})
}
