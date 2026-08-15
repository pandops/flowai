package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

// ErrTaskUnknown is the canonical sentinel for the trusted-Gateway
// point read surface. Unknown and foreign-team task identifiers
// collapse to the same non-revealing ErrTaskUnknown so the HTTP
// boundary cannot leak the existence of another team's row.
var ErrTaskUnknown = errors.New("task unknown")

// TaskPointReadRepository provides trusted same-team task point reads.
type TaskPointReadRepository interface {
	GetTask(context.Context, string, string) (platform.TaskListEntry, error)
}

// GetTask returns one canonical task row by team_id + task_id. The
// composite key is the only authoritative way to address a task;
// foreign-team and unknown identifiers both return ErrTaskUnknown.
//
// The function is read-only and never reads Executor capacity; the
// only fix-up work is the canonical projection of nullable image
// fields and the RFC3339Nano timestamp format which mirrors every
// other read path in this package.
func (s *Store) GetTask(ctx context.Context, teamID, taskID string) (platform.TaskListEntry, error) {
	entry, _, err := scanTaskRow(s.db.QueryRowContext(ctx, `
		SELECT task_id, team_id, source_system_id, source_id,
		       task_type_id, required_tag, payload, current_state,
		       owner_command_id, executor_id, project_id,
		       image, resolved_image, image_source, ingested_at, claimed_at
		  FROM tasks
		 WHERE team_id = $1 AND task_id = $2`,
		teamID, taskID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskListEntry{}, ErrTaskUnknown
	}
	if err != nil {
		return platform.TaskListEntry{}, fmt.Errorf("get task %s/%s: %w", teamID, taskID, err)
	}
	return entry, nil
}

var _ TaskPointReadRepository = (*Store)(nil)
