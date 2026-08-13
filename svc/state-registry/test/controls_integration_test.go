//go:build integration

package test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

func TestTeamControl(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-control")
	identity := platform.GatewayIdentity{TeamID: "team-a", OperatorID: "operator-a", RequestID: "request-a"}
	req := platform.CreateTaskControlRequest{Action: "cancel", IdempotencyKey: "control-key"}
	first, err := h.repo.CreateTaskControl(context.Background(), identity, "task-control", req)
	if err != nil {
		t.Fatalf("create control: %v", err)
	}
	retry, err := h.repo.CreateTaskControl(context.Background(), identity, "task-control", req)
	if err != nil {
		t.Fatalf("retry control: %v", err)
	}
	if retry.ControlID != first.ControlID || retry.AuditID != first.AuditID {
		t.Fatalf("retry returned control=%q audit=%q, want control=%q audit=%q", retry.ControlID, retry.AuditID, first.ControlID, first.AuditID)
	}
	assertQueryCount(t, h, `SELECT count(*) FROM task_control_requests WHERE task_id = 'task-control'`, 1)
	assertQueryCount(t, h, `SELECT count(*) FROM audit_entries WHERE resource_id = $1`, 1, first.ControlID)
}

func TestTeamAudit(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-audit")
	reason := "operator requested cancellation"
	identity := platform.GatewayIdentity{TeamID: "team-a", OperatorID: "operator-audit", RequestID: "request-audit"}
	control, err := h.repo.CreateTaskControl(context.Background(), identity, "task-audit", platform.CreateTaskControlRequest{
		Action: "cancel", IdempotencyKey: "audit-key", Reason: &reason,
	})
	if err != nil {
		t.Fatalf("create audited control: %v", err)
	}
	var teamID, actorID, actorType, action, resourceType, resourceID, requestID, outcome string
	if err := h.db.QueryRow(`
		SELECT team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome
		  FROM audit_entries WHERE audit_id = $1`, control.AuditID,
	).Scan(&teamID, &actorID, &actorType, &action, &resourceType, &resourceID, &requestID, &outcome); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if teamID != "team-a" || actorID != "operator-audit" || actorType != "operator" ||
		action != "task.control.cancel" || resourceType != "task_control_request" ||
		resourceID != control.ControlID || requestID != "request-audit" || outcome != "accepted" {
		t.Fatalf("unexpected audit projection: team=%q actor=%q type=%q action=%q resource=%q/%q request=%q outcome=%q",
			teamID, actorID, actorType, action, resourceType, resourceID, requestID, outcome)
	}
}

func TestForeignResourceNotFound(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-owned")
	identity := platform.GatewayIdentity{TeamID: "team-b", OperatorID: "operator-b", RequestID: "request-b"}
	_, err := h.repo.CreateTaskControl(context.Background(), identity, "task-owned", platform.CreateTaskControlRequest{
		Action: "cancel", IdempotencyKey: "foreign-key",
	})
	if !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign control error=%v, want ErrTaskUnknown", err)
	}
	assertQueryCount(t, h, `SELECT count(*) FROM task_control_requests`, 0)
	assertQueryCount(t, h, `SELECT count(*) FROM audit_entries`, 0)
}

func TestTeamAuditRollback(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-audit-rollback")
	mustExec(t, h.db, `
		CREATE FUNCTION fail_control_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected control failure'; END;
		$$;
		CREATE TRIGGER fail_control_insert BEFORE INSERT ON task_control_requests
		FOR EACH ROW EXECUTE FUNCTION fail_control_insert()`)
	identity := platform.GatewayIdentity{TeamID: "team-a", OperatorID: "operator-a", RequestID: "request-a"}
	_, err := h.repo.CreateTaskControl(context.Background(), identity, "task-audit-rollback", platform.CreateTaskControlRequest{
		Action: "cancel", IdempotencyKey: "rollback-key",
	})
	if err == nil {
		t.Fatal("CreateTaskControl succeeded despite injected control failure")
	}
	assertQueryCount(t, h, `SELECT count(*) FROM task_control_requests`, 0)
	assertQueryCount(t, h, `SELECT count(*) FROM audit_entries`, 0)
}

func TestTeamFilterBeforeShape(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-stream-a")
	mustExec(t, h.db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		VALUES ('source-stream-b', 'team-b', 'listener-stream-b')`)
	mustExec(t, h.db, `INSERT INTO task_types (task_type_id, team_id, execution_tag)
		VALUES ('type-stream-b', 'team-b', 'openhands')`)
	seedPendingTask(t, h.db, "task-stream-b", "team-b", "source-stream-b", "task-stream-b-source",
		"type-stream-b", "openhands", time.Now().UTC(), nil)
	teamB := "team-b"
	if _, err := h.repo.ClaimTask(context.Background(), platform.ClaimRequest{TaskID: "task-stream-b", CommandID: "command-stream-b"},
		"exec-b", platform.ExecutorIdentity{ExecutorID: "exec-b", Scope: platform.ExecutorScopeTeam, TeamID: &teamB}); err != nil {
		t.Fatalf("claim team-b task: %v", err)
	}
	events, err := h.repo.ListStreamTaskEvents(context.Background(), "team-a", "", time.Unix(0, 0).UTC(), "")
	if err != nil {
		t.Fatalf("list team stream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("team-a stream returned no own-team events")
	}
	for _, event := range events {
		if event.TeamID != "team-a" {
			t.Fatalf("stream leaked team_id=%q", event.TeamID)
		}
	}
}

func TestAssignedExecutorReadsPendingControls(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-assigned-control")
	gateway := platform.GatewayIdentity{TeamID: "team-a", OperatorID: "operator-a", RequestID: "request-a"}
	created, err := h.repo.CreateTaskControl(context.Background(), gateway, "task-assigned-control", platform.CreateTaskControlRequest{
		Action: "interrupt", IdempotencyKey: "assigned-key",
	})
	if err != nil {
		t.Fatalf("create assigned control: %v", err)
	}
	teamA := "team-a"
	controls, err := h.repo.ListAssignedTaskControls(context.Background(), "task-assigned-control",
		platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &teamA})
	if err != nil {
		t.Fatalf("list assigned controls: %v", err)
	}
	if len(controls) != 1 || controls[0].ControlID != created.ControlID {
		t.Fatalf("assigned controls=%v, want control %q", controls, created.ControlID)
	}
	_, err = h.repo.ListAssignedTaskControls(context.Background(), "task-assigned-control",
		platform.ExecutorIdentity{ExecutorID: "exec-a2", Scope: platform.ExecutorScopeTeam, TeamID: &teamA})
	if !errors.Is(err, store.ErrNotAssigned) {
		t.Fatalf("unassigned error=%v, want ErrNotAssigned", err)
	}
	teamB := "team-b"
	_, err = h.repo.ListAssignedTaskControls(context.Background(), "task-assigned-control",
		platform.ExecutorIdentity{ExecutorID: "exec-b", Scope: platform.ExecutorScopeTeam, TeamID: &teamB})
	if !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign error=%v, want ErrTaskUnknown", err)
	}
}

func TestAuditTeamIsolation(t *testing.T) {
	h := newLifecycleIntegrationHarness(t, "task-audit-isolation")
	gateway := platform.GatewayIdentity{TeamID: "team-a", OperatorID: "operator-a", RequestID: "request-a"}
	control, err := h.repo.CreateTaskControl(context.Background(), gateway, "task-audit-isolation", platform.CreateTaskControlRequest{
		Action: "cancel", IdempotencyKey: "isolation-key",
	})
	if err != nil {
		t.Fatalf("create control: %v", err)
	}
	entries, _, err := h.repo.ListAuditEntries(context.Background(), "team-a", "task_control_request", control.ControlID, 50, nil)
	if err != nil {
		t.Fatalf("list own audit: %v", err)
	}
	if len(entries) != 1 || entries[0].AuditID != control.AuditID {
		t.Fatalf("own audit entries=%v, want audit %q", entries, control.AuditID)
	}
	foreignEntries, _, err := h.repo.ListAuditEntries(context.Background(), "team-b", "", "", 50, nil)
	if err != nil {
		t.Fatalf("list foreign audit: %v", err)
	}
	if len(foreignEntries) != 0 {
		t.Fatalf("foreign audit entries=%d, want 0", len(foreignEntries))
	}
	if _, err := h.repo.GetAuditEntry(context.Background(), "team-b", control.AuditID); !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign audit point error=%v, want ErrTaskUnknown", err)
	}
}

func assertQueryCount(t *testing.T, h *claimIntegrationHarness, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := h.db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if got != want {
		t.Fatalf("count=%d, want %d", got, want)
	}
}
