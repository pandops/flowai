//go:build integration

package test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

func TestCancellationControlEventsAreOrderedAndDoNotChangeLifecycle(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	repository := store.New(db)
	seedOwnershipFixture(t, db)
	mustExec(t, db, `UPDATE tasks SET current_state = 'running', owner_command_id = 'command-a',
		executor_id = 'exec-a', resolved_image = $1::jsonb, image_source = 'team_default', claimed_at = now()
		WHERE task_id = 'task-a'`, testImage)

	control, err := repository.CreateTaskControl(context.Background(), platform.GatewayIdentity{
		TeamID: "team-a", OperatorID: "web-ui", RequestID: "cancel-request",
	}, "task-a", platform.CreateTaskControlRequest{Action: "cancel", IdempotencyKey: "cancel-request"})
	if err != nil {
		t.Fatalf("create cancellation: %v", err)
	}
	events, err := repository.ListTaskControlEvents(context.Background(), "team-a", "task-a", control.ControlID, 10)
	if err != nil {
		t.Fatalf("list pending event: %v", err)
	}
	if len(events) != 1 || events[0].Status != "pending" || events[0].ExecutorID != nil {
		t.Fatalf("pending events = %#v", events)
	}
	identity := platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: stringPointer("team-a"), RequestID: "executor-result"}
	ackAt := control.RequestedAt.Add(time.Millisecond)
	ack, err := repository.AppendTaskControlEvent(context.Background(), "task-a", control.ControlID, platform.TaskControlEventAppendRequest{
		ControlEventID: "control-event-ack", Status: "acknowledged", OccurredAt: ackAt, Payload: json.RawMessage(`{"message":"cancellation received"}`),
	}, identity)
	if err != nil {
		t.Fatalf("append acknowledged: %v", err)
	}
	completedAt := ackAt.Add(time.Millisecond)
	_, err = repository.AppendTaskControlEvent(context.Background(), "task-a", control.ControlID, platform.TaskControlEventAppendRequest{
		ControlEventID: "control-event-complete", Status: "completed", OccurredAt: completedAt, Payload: json.RawMessage(`{"message":"runtime stopped"}`),
	}, identity)
	if err != nil {
		t.Fatalf("append completed: %v", err)
	}
	retry, err := repository.AppendTaskControlEvent(context.Background(), "task-a", control.ControlID, platform.TaskControlEventAppendRequest{
		ControlEventID: "control-event-ack", Status: "acknowledged", OccurredAt: ackAt, Payload: json.RawMessage(`{"message":"cancellation received"}`),
	}, identity)
	if err != nil || retry.ControlEventID != ack.ControlEventID {
		t.Fatalf("idempotent retry = %#v / %v", retry, err)
	}
	_, err = repository.AppendTaskControlEvent(context.Background(), "task-a", control.ControlID, platform.TaskControlEventAppendRequest{
		ControlEventID: "foreign-event", Status: "failed", OccurredAt: completedAt.Add(time.Millisecond), Payload: json.RawMessage(`{}`),
	}, platform.ExecutorIdentity{ExecutorID: "exec-b", Scope: platform.ExecutorScopeTeam, TeamID: stringPointer("team-b")})
	if !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign append error = %v, want non-revealing task unknown", err)
	}
	events, err = repository.ListTaskControlEvents(context.Background(), "team-a", "task-a", control.ControlID, 10)
	if err != nil {
		t.Fatalf("list final events: %v", err)
	}
	if len(events) != 3 || events[0].Status != "pending" || events[1].Status != "acknowledged" || events[2].Status != "completed" {
		t.Fatalf("ordered events = %#v", events)
	}
	var lifecycle string
	if err := db.QueryRow(`SELECT current_state FROM tasks WHERE task_id = 'task-a'`).Scan(&lifecycle); err != nil {
		t.Fatalf("read lifecycle: %v", err)
	}
	if lifecycle != platform.TaskStateRunning {
		t.Fatalf("lifecycle = %q, want running", lifecycle)
	}
}

func stringPointer(value string) *string { return &value }
