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

func TestUIOperatorReadModelIsTeamScopedAndPaginated(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	seedOwnershipFixture(t, db)
	repository := store.New(db)
	ctx := context.Background()

	dashboard, err := repository.GetUIDashboard(ctx, "team-a", "week", "", time.Now())
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if dashboard.Counts[platform.TaskStatePending] != 1 || dashboard.Counts[platform.TaskStateRunning] != 0 {
		t.Fatalf("dashboard counts = %#v", dashboard.Counts)
	}
	frames, err := repository.ListUIStreamFrames(ctx, "team-a", "", time.Unix(0, 0), "")
	if err != nil {
		t.Fatalf("list UI stream frames: %v", err)
	}
	if len(frames) != 1 || frames[0].FrameType != "task_committed" || frames[0].TaskID == nil || *frames[0].TaskID != "task-a" {
		t.Fatalf("task commit stream frames = %#v", frames)
	}

	tasks, next, err := repository.ListUITasks(ctx, "team-a", platform.UITaskFilter{Status: "pending", Search: "external-a"}, 10, nil)
	if err != nil || next != nil || len(tasks) != 1 || tasks[0].TaskID != "task-a" {
		t.Fatalf("tasks = %#v next=%#v err=%v", tasks, next, err)
	}
	date := tasks[0].IngestedAt.UTC().Format(time.DateOnly)
	dayTasks, next, err := repository.ListUITasks(ctx, "team-a", platform.UITaskFilter{Date: date}, 10, nil)
	if err != nil || next != nil || len(dayTasks) != 1 || dayTasks[0].TaskID != "task-a" {
		t.Fatalf("date-filtered tasks = %#v next=%#v err=%v", dayTasks, next, err)
	}
	previousDayTasks, _, err := repository.ListUITasks(ctx, "team-a", platform.UITaskFilter{Date: tasks[0].IngestedAt.UTC().AddDate(0, 0, -1).Format(time.DateOnly)}, 10, nil)
	if err != nil || len(previousDayTasks) != 0 {
		t.Fatalf("previous-day tasks = %#v err=%v", previousDayTasks, err)
	}
	if _, err := repository.GetUITask(ctx, "team-b", "task-a"); !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign task error = %v", err)
	}

	executors, _, err := repository.ListUIExecutors(ctx, "team-a", platform.UIExecutorFilter{}, 10, nil)
	if err != nil {
		t.Fatalf("executors: %v", err)
	}
	if len(executors) != 2 || executors[0].ExecutorID == "exec-b" || executors[1].ExecutorID == "exec-b" {
		t.Fatalf("visible executors = %#v, want team-a plus eligible system executor", executors)
	}
	if _, err := repository.GetUIExecutor(ctx, "team-a", "exec-b"); !errors.Is(err, store.ErrUIResourceUnknown) {
		t.Fatalf("foreign executor error = %v", err)
	}

	mustExec(t, db, `INSERT INTO audit_entries
		(audit_id,team_id,actor_id,actor_type,action,resource_type,resource_id,request_id,outcome)
		VALUES ('audit-a','team-a','operator-a','operator','environment.create','environment','env-a','request-a','accepted'),
		       ('audit-b','team-b','operator-b','operator','environment.create','environment','env-b','request-b','accepted')`)
	audit, _, err := repository.ListUIAudit(ctx, "team-a", platform.UIAuditFilter{Search: "operator-a"}, 10, nil)
	if err != nil || len(audit) != 1 || audit[0].AuditID != "audit-a" {
		t.Fatalf("audit = %#v err=%v", audit, err)
	}
}
