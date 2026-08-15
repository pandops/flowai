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

func TestTaskLogsReplayWorkAndReasoningWithAssignmentIsolation(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	repository := store.New(db)
	seedOwnershipFixture(t, db)
	mustExec(t, db, `UPDATE tasks SET current_state = 'running', owner_command_id = 'command-a',
		executor_id = 'exec-a', resolved_image = $1::jsonb, image_source = 'team_default', claimed_at = now()
		WHERE task_id = 'task-a'`, testImage)
	identity := platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: stringPointer("team-a")}
	started := time.Now().UTC().Truncate(time.Microsecond)
	work, err := repository.AppendTaskLog(context.Background(), "task-a", platform.TaskLogAppendRequest{
		LogChunkID: "log-work-1", Stream: platform.TaskLogStreamWork,
		Content: "Запускаю анализ репозитория", OccurredAt: started,
	}, identity)
	if err != nil {
		t.Fatalf("append work log: %v", err)
	}
	reasoning, err := repository.AppendTaskLog(context.Background(), "task-a", platform.TaskLogAppendRequest{
		LogChunkID: "log-reasoning-1", Stream: platform.TaskLogStreamReasoning,
		Content: "Проверяю зависимости перед изменением", OccurredAt: started.Add(time.Millisecond),
	}, identity)
	if err != nil {
		t.Fatalf("append reasoning log: %v", err)
	}
	if work.LogOffset != 1 || reasoning.LogOffset != 2 {
		t.Fatalf("offsets = %d/%d, want 1/2", work.LogOffset, reasoning.LogOffset)
	}
	retry, err := repository.AppendTaskLog(context.Background(), "task-a", platform.TaskLogAppendRequest{
		LogChunkID: "log-work-1", Stream: platform.TaskLogStreamWork,
		Content: "Запускаю анализ репозитория", OccurredAt: started,
	}, identity)
	if err != nil || retry.LogOffset != 1 {
		t.Fatalf("idempotent retry = %#v / %v", retry, err)
	}
	_, err = repository.AppendTaskLog(context.Background(), "task-a", platform.TaskLogAppendRequest{
		LogChunkID: "foreign-log", Stream: platform.TaskLogStreamWork, Content: "foreign", OccurredAt: started,
	}, platform.ExecutorIdentity{ExecutorID: "exec-b", Scope: platform.ExecutorScopeTeam, TeamID: stringPointer("team-b")})
	if !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign append error = %v, want non-revealing task unknown", err)
	}
	items, err := repository.ListTaskLogs(context.Background(), "team-a", "task-a", 0, 10)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(items) != 2 || items[0].Stream != "work" || items[1].Stream != "reasoning" {
		t.Fatalf("logs = %#v", items)
	}
	if _, err := repository.ListTaskLogs(context.Background(), "team-b", "task-a", 0, 10); !errors.Is(err, store.ErrTaskUnknown) {
		t.Fatalf("foreign replay error = %v, want non-revealing task unknown", err)
	}
}
