//go:build integration

package test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

func TestTaskEventProjectionFailureRollsBackAppend(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-event-rollback")
	mustExec(t, h.db, `
		CREATE FUNCTION fail_task_projection() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected task projection failure'; END;
		$$;
		CREATE TRIGGER fail_task_projection
		BEFORE UPDATE OF current_state ON tasks
		FOR EACH ROW EXECUTE FUNCTION fail_task_projection()`)

	teamID := "team-a"
	_, err := h.repo.AppendTaskEvent(context.Background(), platform.TaskEventAppendRequest{
		EventID:    "event-running-rollback",
		TeamID:     teamID,
		TaskID:     "task-event-rollback",
		ExecutorID: "exec-a",
		EventType:  platform.TaskEventTypeRunning,
		OccurredAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		Payload:    json.RawMessage(`{}`),
	}, platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamID, RequestID: "request-projection-rollback"})
	if err == nil {
		t.Fatal("AppendTaskEvent succeeded despite injected projection failure")
	}

	var state string
	if err := h.db.QueryRow(`SELECT current_state FROM tasks WHERE task_id = 'task-event-rollback'`).Scan(&state); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if state != platform.TaskStateCreated {
		t.Fatalf("current_state=%q, want created", state)
	}
	if got := countEvents(t, h.db, "task-event-rollback"); got != 1 {
		t.Fatalf("task event count=%d, want only the created event", got)
	}
}

func TestExecutorObservationFailureRollsBackAppend(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-self-rollback")
	mustExec(t, h.db, `
		CREATE FUNCTION fail_capacity_mirror() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected capacity mirror failure'; END;
		$$;
		CREATE TRIGGER fail_capacity_mirror
		BEFORE UPDATE OF max_capacity ON executors
		FOR EACH ROW EXECUTE FUNCTION fail_capacity_mirror()`)

	teamID := "team-a"
	_, err := h.repo.AppendExecutorEvent(context.Background(), platform.ExecutorEventAppendRequest{
		EventID:    "event-capacity-rollback",
		TeamID:     &teamID,
		ExecutorID: "exec-a",
		EventType:  platform.ExecutorEventTypeCapacityObserved,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:    json.RawMessage(`{"max_capacity":9}`),
	}, platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamID, RequestID: "request-observation-rollback"})
	if err == nil {
		t.Fatal("AppendExecutorEvent succeeded despite injected mirror failure")
	}

	var capacity, eventCount int
	if err := h.db.QueryRow(`SELECT max_capacity FROM executors WHERE executor_id = 'exec-a'`).Scan(&capacity); err != nil {
		t.Fatalf("read executor capacity: %v", err)
	}
	if capacity != 1 {
		t.Fatalf("max_capacity=%d, want 1", capacity)
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM executor_events WHERE event_id = 'event-capacity-rollback'`).Scan(&eventCount); err != nil {
		t.Fatalf("count executor events: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("executor event count=%d, want 0", eventCount)
	}
}

func TestChangedTaskEventRetryConflicts(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-event-retry")
	teamID := "team-a"
	identity := platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamID, RequestID: "request-task-retry"}
	occurredAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	req := platform.TaskEventAppendRequest{
		EventID: "event-running-retry", TeamID: teamID, TaskID: "task-event-retry",
		ExecutorID: "exec-a", EventType: platform.TaskEventTypeRunning,
		OccurredAt: occurredAt, Payload: json.RawMessage(`{"attempt":1}`),
	}
	if _, err := h.repo.AppendTaskEvent(context.Background(), req, identity); err != nil {
		t.Fatalf("first append: %v", err)
	}
	req.Payload = json.RawMessage(`{"attempt":2}`)
	if _, err := h.repo.AppendTaskEvent(context.Background(), req, identity); !errors.Is(err, store.ErrEventConflict) {
		t.Fatalf("changed retry error=%v, want ErrEventConflict", err)
	}
	if got := countEvents(t, h.db, "task-event-retry"); got != 2 {
		t.Fatalf("task event count=%d, want created plus one running event", got)
	}
}

func TestChangedExecutorEventRetryConflicts(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-self-retry")
	teamID := "team-a"
	identity := platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamID, RequestID: "request-self-retry"}
	req := platform.ExecutorEventAppendRequest{
		EventID: "event-self-retry", TeamID: &teamID, ExecutorID: "exec-a",
		EventType:  platform.ExecutorEventTypeHealthy,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: json.RawMessage(`{"attempt":1}`),
	}
	if _, err := h.repo.AppendExecutorEvent(context.Background(), req, identity); err != nil {
		t.Fatalf("first append: %v", err)
	}
	req.Payload = json.RawMessage(`{"attempt":2}`)
	if _, err := h.repo.AppendExecutorEvent(context.Background(), req, identity); !errors.Is(err, store.ErrEventConflict) {
		t.Fatalf("changed retry error=%v, want ErrEventConflict", err)
	}
	var eventCount int
	if err := h.db.QueryRow(`SELECT count(*) FROM executor_events WHERE event_id = 'event-self-retry'`).Scan(&eventCount); err != nil {
		t.Fatalf("count executor events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("executor event count=%d, want 1", eventCount)
	}
}

func TestAcceptedExecutorEventsRecordScopeAudit(t *testing.T) {
	h := newClaimIntegrationHarness(t)
	seedClaimFixture(t, h, seededClaim{
		team: "team-a", listenerID: "listener-event-audit", sourceSystem: "source-event-audit",
		taskType: "type-event-audit", tag: "openhands",
		image: &platform.ImageReference{Repository: "registry.example/agent", Digest: "sha256:" + strings.Repeat("e", 64)},
	})
	seedPendingTask(t, h.db, "task-event-audit", "team-a", "source-event-audit", "event-audit-source",
		"type-event-audit", "openhands", time.Now().UTC(), nil)
	identity := platform.ExecutorIdentity{
		ExecutorID: "exec-sys", Scope: platform.ExecutorScopeSystem, RequestID: "request-event-audit",
	}
	if _, err := h.repo.ClaimTask(context.Background(),
		platform.ClaimRequest{TaskID: "task-event-audit", CommandID: "command-event-audit"},
		"exec-sys", identity); err != nil {
		t.Fatalf("claim task as system executor: %v", err)
	}

	teamID := "team-a"
	if _, err := h.repo.AppendTaskEvent(context.Background(), platform.TaskEventAppendRequest{
		EventID: "event-running-audit", TeamID: teamID, TaskID: "task-event-audit",
		ExecutorID: "exec-sys", EventType: platform.TaskEventTypeRunning,
		OccurredAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), Payload: json.RawMessage(`{}`),
	}, identity); err != nil {
		t.Fatalf("append task event: %v", err)
	}
	taskID := "task-event-audit"
	if _, err := h.repo.AppendExecutorEvent(context.Background(), platform.ExecutorEventAppendRequest{
		EventID: "event-self-audit", TeamID: nil, TaskID: &taskID, ExecutorID: "exec-sys",
		EventType:  platform.ExecutorEventTypeBusy,
		OccurredAt: time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano), Payload: json.RawMessage(`{}`),
	}, identity); err != nil {
		t.Fatalf("append self event: %v", err)
	}

	for _, auditID := range []string{"event-running-audit", "event-self-audit"} {
		var gotTeam, actorType, resourceID, requestID, outcome, scope string
		if err := h.db.QueryRow(`
			SELECT team_id, actor_type, resource_id, request_id, outcome, executor_scope
			  FROM audit_entries
			 WHERE resource_id = $1`, auditID,
		).Scan(&gotTeam, &actorType, &resourceID, &requestID, &outcome, &scope); err != nil {
			t.Fatalf("read audit %s: %v", auditID, err)
		}
		if gotTeam != "team-a" || actorType != "executor" || resourceID != auditID || requestID != identity.RequestID || outcome != "accepted" || scope != platform.ExecutorScopeSystem {
			t.Fatalf("audit %s mismatch: team=%q actor=%q resource=%q request=%q outcome=%q scope=%q", auditID, gotTeam, actorType, resourceID, requestID, outcome, scope)
		}
	}
}

func TestEventAuditFailureRollsBackTaskEvent(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-event-audit-rollback")
	mustExec(t, h.db, `
		CREATE FUNCTION fail_event_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected event audit failure'; END;
		$$;
		CREATE TRIGGER fail_event_audit
		BEFORE INSERT ON audit_entries
		FOR EACH ROW EXECUTE FUNCTION fail_event_audit()`)

	teamID := "team-a"
	_, err := h.repo.AppendTaskEvent(context.Background(), platform.TaskEventAppendRequest{
		EventID: "event-running-audit-rollback", TeamID: teamID, TaskID: "task-event-audit-rollback",
		ExecutorID: "exec-a", EventType: platform.TaskEventTypeRunning,
		OccurredAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), Payload: json.RawMessage(`{}`),
	}, platform.ExecutorIdentity{
		ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamID, RequestID: "request-audit-rollback",
	})
	if err == nil {
		t.Fatal("AppendTaskEvent succeeded despite injected audit failure")
	}
	var state string
	if err := h.db.QueryRow(`SELECT current_state FROM tasks WHERE task_id = 'task-event-audit-rollback'`).Scan(&state); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if state != platform.TaskStateCreated {
		t.Fatalf("current_state=%q, want created", state)
	}
	if got := countEvents(t, h.db, "task-event-audit-rollback"); got != 1 {
		t.Fatalf("task event count=%d, want only created event", got)
	}
}

func newLifecycleIntegrationHarness(t *testing.T, taskID string) *claimIntegrationHarness {
	t.Helper()
	h := newClaimIntegrationHarness(t)
	seedClaimFixture(t, h, seededClaim{
		team: "team-a", listenerID: "listener-events", sourceSystem: "source-events",
		taskType: "type-events", tag: "openhands",
		image: &platform.ImageReference{
			Repository: "registry.example/agent:default",
			Digest:     "sha256:" + strings.Repeat("e", 64),
		},
	})
	seedPendingTask(t, h.db, taskID, "team-a", "source-events", taskID+"-source",
		"type-events", "openhands", time.Now().UTC(), nil)
	teamID := "team-a"
	if _, err := h.repo.ClaimTask(context.Background(),
		platform.ClaimRequest{TaskID: taskID, CommandID: "command-" + taskID},
		"exec-a", platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamID}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	return h
}
