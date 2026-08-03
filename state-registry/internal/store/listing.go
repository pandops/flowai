package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flowai/platform/state-registry/internal/platform"
)

// ListRepository is the read-only contract used by the admin and
// Gateway pagination handlers.
type ListRepository interface {
	AdminRepository
	ListTaskTypes(ctx context.Context, filter platform.AdminTagFilter, limit int, after *TaskTypeAfter) ([]platform.AdminTagEntry, *TaskTypeAfter, error)
	ListTasks(ctx context.Context, filter platform.AdminTaskFilter, limit int, after *TaskAfter) ([]platform.TaskListEntry, *TaskAfter, error)
}

// TaskTypeAfter is the after-cursor position for the admin tag
// endpoint, ordered by (team_id ASC, execution_tag ASC, task_type_id ASC).
type TaskTypeAfter struct {
	TeamID       string
	ExecutionTag string
	TaskTypeID   string
}

// TaskAfter is the after-cursor position for the deterministic
// (ingested_at DESC, task_id DESC) ordering.
type TaskAfter struct {
	IngestedAtUnixNano int64
	TaskID             string
}

// EnvironmentAfter is the after-cursor position for the deterministic
// (created_at ASC, environment_id ASC) ordering of the
// GET /v1/environments collection.
type EnvironmentAfter struct {
	CreatedAtUnixNano int64
	EnvironmentID     string
}

// SecretAfter is the after-cursor position for the deterministic
// (created_at ASC, secret_id ASC) ordering of the logical-secret
// collection under one environment.
type SecretAfter struct {
	CreatedAtUnixNano int64
	SecretID          string
}

// SecretVersionAfter is the after-cursor position for the deterministic
// (version ASC, secret_id ASC) ordering of the secret_versions
// collection under one logical secret.
type SecretVersionAfter struct {
	Version  int
	SecretID string
}

// ListTaskTypes implements ListRepository on the PostgreSQL Store.
// The select must always project exactly the three documented fields
// (team_id, task_type_id, execution_tag). The deterministic
// (team_id ASC, execution_tag ASC, task_type_id ASC) ordering is
// applied before pagination so multiple pages never overlap or skip
// rows.
func (s *Store) ListTaskTypes(ctx context.Context, filter platform.AdminTagFilter, limit int, after *TaskTypeAfter) ([]platform.AdminTagEntry, *TaskTypeAfter, error) {
	if limit < 1 {
		return nil, nil, errors.New("ListTaskTypes: limit must be >= 1")
	}
	var (
		clauses []string
		args    []any
	)
	if after != nil {
		// strict greater-than tuple predicate for the
		// (team_id ASC, execution_tag ASC, task_type_id ASC) order.
		// Six placeholders; the values must align with the $N
		// references below.
		args = append(args,
			after.TeamID,       // $1: team_id > after.TeamID
			after.TeamID,       // $2: team_id = after.TeamID
			after.ExecutionTag, // $3: execution_tag > after.ExecutionTag
			after.TeamID,       // $4: team_id = after.TeamID
			after.ExecutionTag, // $5: execution_tag = after.ExecutionTag
			after.TaskTypeID,   // $6: task_type_id > after.TaskTypeID
		)
		clauses = append(clauses, fmt.Sprintf(
			"(team_id > $%d OR (team_id = $%d AND execution_tag > $%d) OR (team_id = $%d AND execution_tag = $%d AND task_type_id > $%d))",
			len(args)-5, len(args)-4, len(args)-3, len(args)-2, len(args)-1, len(args),
		))
	}
	if filter.TeamID != "" {
		args = append(args, filter.TeamID)
		clauses = append(clauses, fmt.Sprintf("team_id = $%d", len(args)))
	}
	if filter.Tag != "" {
		args = append(args, filter.Tag)
		clauses = append(clauses, fmt.Sprintf("execution_tag = $%d", len(args)))
	}
	fetchLimit := limit + 1
	args = append(args, fetchLimit)
	limitPlaceholder := fmt.Sprintf("$%d", len(args))

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT team_id, task_type_id, execution_tag
		FROM task_types
		%s
		ORDER BY team_id ASC, execution_tag ASC, task_type_id ASC
		LIMIT %s`,
		where, limitPlaceholder,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list task types: %w", err)
	}
	defer rows.Close()

	out := make([]platform.AdminTagEntry, 0, fetchLimit)
	var last TaskTypeAfter
	for rows.Next() {
		var entry platform.AdminTagEntry
		if err := rows.Scan(&entry.TeamID, &entry.TaskTypeID, &entry.ExecutionTag); err != nil {
			return nil, nil, fmt.Errorf("scan task type row: %w", err)
		}
		if len(out) < limit {
			last.TeamID = entry.TeamID
			last.ExecutionTag = entry.ExecutionTag
			last.TaskTypeID = entry.TaskTypeID
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate task types: %w", err)
	}
	if len(out) <= limit {
		return out, nil, nil
	}
	return out[:limit], &last, nil
}

// ListTasks implements ListRepository on the PostgreSQL Store. The
// SELECT projects every column required to materialize the canonical
// TaskListEntry row so the Gateway handler can mirror the documented
// OpenAPI Task shape without ever widening the projection at the
// handler boundary; the admin handler then narrows the same row onto
// the exact 11-field AdminTaskEntry allowlist.
//
// The (ingested_at DESC, task_id DESC) ordering is preserved by the
// strict-less-than tuple predicate, the team predicate is applied
// before pagination and counts, and the limit+1 fetch is preserved so
// the handler can derive next-cursor without a second round trip.
func (s *Store) ListTasks(ctx context.Context, filter platform.AdminTaskFilter, limit int, after *TaskAfter) ([]platform.TaskListEntry, *TaskAfter, error) {
	if limit < 1 {
		return nil, nil, errors.New("ListTasks: limit must be >= 1")
	}
	var (
		clauses []string
		args    []any
	)
	if after != nil {
		// strict-less-than + equal-tie-break predicate for
		// (ingested_at DESC, task_id DESC).
		afterTime := time.Unix(0, after.IngestedAtUnixNano).UTC()
		args = append(args, afterTime, afterTime, after.TaskID)
		clauses = append(clauses, fmt.Sprintf(
			"(t.ingested_at < $%d OR (t.ingested_at = $%d AND t.task_id < $%d))",
			len(args)-2, len(args)-1, len(args),
		))
	}
	if filter.TeamID != "" {
		args = append(args, filter.TeamID)
		clauses = append(clauses, fmt.Sprintf("t.team_id = $%d", len(args)))
	}
	if filter.State != "" {
		args = append(args, filter.State)
		clauses = append(clauses, fmt.Sprintf("t.current_state = $%d", len(args)))
	}
	if filter.Tag != "" {
		args = append(args, filter.Tag)
		clauses = append(clauses, fmt.Sprintf("t.required_tag = $%d", len(args)))
	}
	if filter.TaskTypeID != "" {
		args = append(args, filter.TaskTypeID)
		clauses = append(clauses, fmt.Sprintf("t.task_type_id = $%d", len(args)))
	}

	fetchLimit := limit + 1
	args = append(args, fetchLimit)
	limitPlaceholder := fmt.Sprintf("$%d", len(args))

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT t.task_id, t.team_id, t.source_system_id, t.source_id, t.task_type_id,
		       t.required_tag, t.payload, t.current_state, t.owner_command_id, t.executor_id,
		       t.project_id, t.environment_id,
		       t.image, t.resolved_image, t.image_source,
		       t.ingested_at, t.claimed_at
		FROM tasks t
		%s
		ORDER BY t.ingested_at DESC, t.task_id DESC
		LIMIT %s`,
		where, limitPlaceholder,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	out := make([]platform.TaskListEntry, 0, fetchLimit)
	var last TaskAfter
	for rows.Next() {
		entry, ingestedAt, err := scanTaskRow(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("scan task row: %w", err)
		}
		if len(out) < limit {
			last.IngestedAtUnixNano = ingestedAt.UnixNano()
			last.TaskID = entry.TaskID
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate tasks: %w", err)
	}
	if len(out) <= limit {
		return out, nil, nil
	}
	return out[:limit], &last, nil
}

// normalizeJSONPayload returns the raw JSON bytes from a jsonb column
// as a json.RawMessage when they parse, the JSON literal `null` when
// the column was NULL, or `nil` when the bytes fail to parse so the
// caller never ships an unvalidated outward response. The decoded
// bytes are not re-encoded: a round-trip on wire-equivalent JSON would
// mutate field ordering and whitespace, and the canonical task row
// promises to render the persisted payload verbatim.
func normalizeJSONPayload(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return []byte("null")
	}
	if !json.Valid(raw) {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

// formatIngestedAt renders the timestamptz as the wire RFC3339Nano
// string.
func formatIngestedAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
