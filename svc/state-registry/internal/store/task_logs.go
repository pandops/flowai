package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

var ErrTaskLogConflict = errors.New("task log chunk conflicts with stored data")

type TaskLogRepository interface {
	AppendTaskLog(context.Context, string, platform.TaskLogAppendRequest, platform.ExecutorIdentity) (platform.TaskLogChunk, error)
	ListTaskLogs(context.Context, string, string, int64, int) ([]platform.TaskLogChunk, error)
}

func (s *Store) AppendTaskLog(ctx context.Context, taskID string, req platform.TaskLogAppendRequest, identity platform.ExecutorIdentity) (platform.TaskLogChunk, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.TaskLogChunk{}, fmt.Errorf("begin task log append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var teamID, assignedExecutor, state string
	err = tx.QueryRowContext(ctx, `SELECT team_id, executor_id, current_state FROM tasks
		WHERE task_id = $1 FOR UPDATE`, taskID).Scan(&teamID, &assignedExecutor, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskLogChunk{}, ErrTaskUnknown
	}
	if err != nil {
		return platform.TaskLogChunk{}, fmt.Errorf("lock task for log append: %w", err)
	}
	if assignedExecutor != identity.ExecutorID || !s.executorIdentityMatches(ctx, tx, teamID, identity) ||
		(state != platform.TaskStateCreated && state != platform.TaskStateRunning) {
		return platform.TaskLogChunk{}, ErrTaskUnknown
	}
	if existing, found, err := readTaskLogChunk(ctx, tx, taskID, req.LogChunkID); err != nil {
		return platform.TaskLogChunk{}, err
	} else if found {
		if existing.Stream != req.Stream || existing.Content != req.Content || !existing.OccurredAt.Equal(req.OccurredAt) {
			return platform.TaskLogChunk{}, ErrTaskLogConflict
		}
		if err := tx.Commit(); err != nil {
			return platform.TaskLogChunk{}, fmt.Errorf("commit task log retry: %w", err)
		}
		return existing, nil
	}
	var nextOffset int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(log_offset), 0) + 1
		FROM task_log_chunks WHERE task_id = $1`, taskID).Scan(&nextOffset); err != nil {
		return platform.TaskLogChunk{}, fmt.Errorf("allocate task log offset: %w", err)
	}
	chunk := platform.TaskLogChunk{LogChunkID: req.LogChunkID, TaskID: taskID, TeamID: teamID,
		ExecutorID: identity.ExecutorID, LogOffset: nextOffset, Stream: req.Stream,
		Content: req.Content, OccurredAt: req.OccurredAt.UTC()}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_log_chunks
		(log_chunk_id, task_id, team_id, executor_id, log_offset, stream, content, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, chunk.LogChunkID, chunk.TaskID, chunk.TeamID,
		chunk.ExecutorID, chunk.LogOffset, chunk.Stream, chunk.Content, chunk.OccurredAt); err != nil {
		return platform.TaskLogChunk{}, fmt.Errorf("insert task log chunk: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.TaskLogChunk{}, fmt.Errorf("commit task log chunk: %w", err)
	}
	return chunk, nil
}

func (s *Store) ListTaskLogs(ctx context.Context, teamID, taskID string, afterOffset int64, limit int) ([]platform.TaskLogChunk, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE team_id = $1 AND task_id = $2)`, teamID, taskID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("verify task log owner: %w", err)
	}
	if !exists {
		return nil, ErrTaskUnknown
	}
	rows, err := s.db.QueryContext(ctx, `SELECT log_chunk_id, task_id, team_id, executor_id,
		log_offset, stream, content, occurred_at FROM task_log_chunks
		WHERE team_id = $1 AND task_id = $2 AND log_offset > $3
		ORDER BY log_offset ASC LIMIT $4`, teamID, taskID, afterOffset, limit)
	if err != nil {
		return nil, fmt.Errorf("list task logs: %w", err)
	}
	defer rows.Close()
	items := make([]platform.TaskLogChunk, 0)
	for rows.Next() {
		var chunk platform.TaskLogChunk
		if err := rows.Scan(&chunk.LogChunkID, &chunk.TaskID, &chunk.TeamID, &chunk.ExecutorID,
			&chunk.LogOffset, &chunk.Stream, &chunk.Content, &chunk.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan task log chunk: %w", err)
		}
		items = append(items, chunk)
	}
	return items, rows.Err()
}

func readTaskLogChunk(ctx context.Context, tx *sql.Tx, taskID, chunkID string) (platform.TaskLogChunk, bool, error) {
	var chunk platform.TaskLogChunk
	err := tx.QueryRowContext(ctx, `SELECT log_chunk_id, task_id, team_id, executor_id,
		log_offset, stream, content, occurred_at FROM task_log_chunks
		WHERE task_id = $1 AND log_chunk_id = $2`, taskID, chunkID).Scan(&chunk.LogChunkID,
		&chunk.TaskID, &chunk.TeamID, &chunk.ExecutorID, &chunk.LogOffset, &chunk.Stream,
		&chunk.Content, &chunk.OccurredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskLogChunk{}, false, nil
	}
	if err != nil {
		return platform.TaskLogChunk{}, false, fmt.Errorf("read task log retry: %w", err)
	}
	return chunk, true, nil
}

var _ TaskLogRepository = (*Store)(nil)
