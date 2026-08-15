package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

type UIStreamRepository interface {
	ListUIStreamFrames(context.Context, string, string, time.Time, string) ([]platform.UIStreamFrame, error)
}

func (s *Store) ListUIStreamFrames(ctx context.Context, teamID, taskID string, after time.Time, afterFrameID string) ([]platform.UIStreamFrame, error) {
	rows, err := s.db.QueryContext(ctx, `WITH frames AS (
		SELECT 'task_committed:' || task_id AS frame_id, team_id, 'task_committed' AS frame_type,
		       task_id, NULL::text AS control_id, ingested_at AS occurred_at,
		       jsonb_build_object('current_state', current_state) AS data
		FROM tasks WHERE team_id = $1
		UNION ALL
		SELECT 'task_event:' || event_id AS frame_id, team_id, 'task_event' AS frame_type,
		       task_id, NULL::text AS control_id, occurred_at, payload AS data
		FROM task_events WHERE team_id = $1
		UNION ALL
		SELECT 'control_event:' || control_event_id, team_id, 'control_event', task_id,
		       control_id, occurred_at,
		       jsonb_build_object('event_type', 'task.control.requested', 'status', status,
		                          'executor_id', executor_id, 'payload', payload)
		FROM task_control_events WHERE team_id = $1
		UNION ALL
		SELECT 'task_log:' || log_chunk_id, team_id, 'task_log', task_id,
		       NULL::text, occurred_at,
		       jsonb_build_object('log_offset', log_offset, 'stream', stream, 'content', content)
		FROM task_log_chunks WHERE team_id = $1
	)
	SELECT frame_id, team_id, frame_type, task_id, control_id, occurred_at, data
	FROM frames WHERE ($2 = '' OR task_id = $2)
	AND (occurred_at, frame_id) > ($3, $4)
	ORDER BY occurred_at ASC, frame_id ASC LIMIT 100`, teamID, taskID, after.UTC(), afterFrameID)
	if err != nil {
		return nil, fmt.Errorf("list UI stream frames: %w", err)
	}
	defer rows.Close()
	frames := make([]platform.UIStreamFrame, 0)
	for rows.Next() {
		var frame platform.UIStreamFrame
		var task, control sql.NullString
		var occurredAt time.Time
		if err := rows.Scan(&frame.FrameID, &frame.TeamID, &frame.FrameType, &task, &control, &occurredAt, &frame.Data); err != nil {
			return nil, fmt.Errorf("scan UI stream frame: %w", err)
		}
		if task.Valid {
			frame.TaskID = &task.String
		}
		if control.Valid {
			frame.ControlID = &control.String
		}
		frame.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
		frames = append(frames, frame)
	}
	return frames, rows.Err()
}

var _ UIStreamRepository = (*Store)(nil)
