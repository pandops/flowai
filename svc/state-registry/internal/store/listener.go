package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

var (
	// ErrListenerSourceMismatch is returned when the supplied listener
	// identity does not match the registered source-system binding. The
	// caller is bound to exactly one (team_id, source_system_id,
	// listener_identity) triple; any deviation is rejected without
	// persistence. The error carries no listener identity value so the
	// HTTP boundary can render a non-revealing 403 envelope.
	ErrListenerSourceMismatch = errors.New("listener source-system binding mismatch")
	// ErrListenerTaskTypeUnknown is returned when the requested task
	// type does not exist or belongs to another team. The two cases are
	// collapsed into a single non-revealing sentinel so the HTTP
	// boundary cannot leak the existence of foreign task types.
	ErrListenerTaskTypeUnknown = errors.New("task type unknown to listener team")
)

// ListenerRepository is the narrow contract the listener HTTP boundary
// consumes. IngestTask verifies the source-system binding, derives the
// required tag from the referenced task type, and inserts one canonical
// pending task row inside a single transaction. The interface returns
// (created, task, error) so the handler can render 201 / 200 without
// distinguishing the two paths except by HTTP status.
type ListenerRepository interface {
	IngestTask(ctx context.Context, req platform.TaskIngestionRequest, ident platform.ListenerIdentity) (platform.TaskListEntry, bool, error)
}

// IngestTask persists one listener task and returns the canonical task
// row plus a `created` flag the handler uses to choose between 201 and
// 200. The flow is exactly:
//
//  1. Verify the (team_id, source_system_id) pair matches the
//     registered listener identity — ErrListenerSourceMismatch otherwise.
//  2. Resolve the (team_id, task_type_id) task-type row and read its
//     execution_tag — ErrListenerTaskTypeUnknown otherwise.
//  3. INSERT ... ON CONFLICT (team_id, source_system_id, source_id)
//     DO NOTHING RETURNING every task column. The INSERT body omits
//     ingested_at and current_state so PostgreSQL sets both via the
//     column DEFAULTs; the FIRST insert wins, the conflict path yields
//     zero rows.
//  4. On zero rows, SELECT the canonical row inside the same
//     transaction by the dedupe tuple; never UPDATE.
//  5. Never insert into task_events.
func (s *Store) IngestTask(
	ctx context.Context,
	req platform.TaskIngestionRequest,
	ident platform.ListenerIdentity,
) (platform.TaskListEntry, bool, error) {
	if req.TeamID != ident.TeamID || req.SourceSystemID != ident.SourceSystemID {
		return platform.TaskListEntry{}, false, ErrListenerSourceMismatch
	}

	// Marshaling happens before the transaction so a malformed JSON
	// payload fails closed without consuming a connection or holding a
	// row lock.
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("marshal listener payload: %w", err)
	}
	image, err := nullableImage(req.Image)
	if err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("marshal listener image: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("begin ingest task: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Step 1: source-system binding. The composite (team_id,
	// source_system_id) plus the listener identity are checked in one
	// scoped query so a wrong source-system under the same team is
	// rejected without persistence.
	var bindingExists bool
	err = tx.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM source_systems
			WHERE team_id = $1
			  AND source_system_id = $2
			  AND listener_identity = $3
		)`,
		ident.TeamID, ident.SourceSystemID, ident.Identity,
	).Scan(&bindingExists)
	if err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("verify source binding: %w", err)
	}
	if !bindingExists {
		return platform.TaskListEntry{}, false, ErrListenerSourceMismatch
	}

	// Step 2: tag derivation. The composite (team_id, task_type_id)
	// lookup collapses the foreign-team and unknown cases into the
	// single non-revealing sentinel.
	var executionTag string
	err = tx.QueryRowContext(ctx,
		`SELECT execution_tag
		   FROM task_types
		  WHERE team_id = $1 AND task_type_id = $2`,
		ident.TeamID, req.TaskTypeID,
	).Scan(&executionTag)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskListEntry{}, false, ErrListenerTaskTypeUnknown
	}
	if err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("resolve task type tag: %w", err)
	}

	// Step 3: idempotent insert. uuid.NewString sets task_id; PostgreSQL
	// sets ingested_at and current_state via the column defaults.
	taskID := uuid.NewString()
	entry, _, err := scanTaskRow(tx.QueryRowContext(ctx, `
		INSERT INTO tasks (
			task_id, team_id, source_system_id, source_id,
			task_type_id, required_tag, payload, project_id,
			environment_id, image, ingested_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10::jsonb,
		          date_trunc('second', transaction_timestamp()))
		ON CONFLICT (team_id, source_system_id, source_id) DO NOTHING
		RETURNING
			task_id, team_id, source_system_id, source_id,
			task_type_id, required_tag, payload, current_state,
			owner_command_id, executor_id, project_id, environment_id,
			image, resolved_image, image_source, ingested_at, claimed_at`,
		taskID,
		ident.TeamID, ident.SourceSystemID, req.SourceID,
		req.TaskTypeID, executionTag, payload,
		req.ProjectID, req.EnvironmentID, image,
	))
	if errors.Is(err, sql.ErrNoRows) {
		// Step 4: dedupe. The conflict path yields no rows; re-select
		// the canonical row inside the same transaction. We never
		// UPDATE — the existing image, payload, and required_tag stay
		// untouched.
		entry, _, err = scanTaskRow(tx.QueryRowContext(ctx, `
			SELECT
				task_id, team_id, source_system_id, source_id,
				task_type_id, required_tag, payload, current_state,
				owner_command_id, executor_id, project_id, environment_id,
				image, resolved_image, image_source, ingested_at, claimed_at
			FROM tasks
			WHERE team_id = $1 AND source_system_id = $2 AND source_id = $3`,
			ident.TeamID, ident.SourceSystemID, req.SourceID,
		))
		if err != nil {
			return platform.TaskListEntry{}, false, fmt.Errorf("re-read canonical task: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return platform.TaskListEntry{}, false, fmt.Errorf("commit dedupe: %w", err)
		}
		return entry, false, nil
	}
	if err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("insert pending task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.TaskListEntry{}, false, fmt.Errorf("commit ingest: %w", err)
	}
	return entry, true, nil
}

// scanTaskRow materializes one canonical task row from the supplied
// scanner. The projection matches the canonical 17-column SELECT used
// by ListTasks and IngestTask so nullable fields, JSON images, and
// RFC3339Nano timestamps cannot drift between code paths.
func scanTaskRow(row interface {
	Scan(dest ...any) error
}) (platform.TaskListEntry, time.Time, error) {
	var (
		entry       platform.TaskListEntry
		payload     []byte
		owner       sql.NullString
		exec        sql.NullString
		projectID   sql.NullString
		environment sql.NullString
		image       []byte
		resolvedImg []byte
		imageSrc    sql.NullString
		ingestedAt  sql.NullTime
		claimedAt   sql.NullTime
	)
	if err := row.Scan(
		&entry.TaskID, &entry.TeamID, &entry.SourceSystemID, &entry.SourceID,
		&entry.TaskTypeID, &entry.RequiredTag, &payload, &entry.CurrentState,
		&owner, &exec, &projectID, &environment,
		&image, &resolvedImg, &imageSrc, &ingestedAt, &claimedAt,
	); err != nil {
		return platform.TaskListEntry{}, time.Time{}, err
	}
	entry.Payload = normalizeJSONPayload(payload)
	if owner.Valid {
		s := owner.String
		entry.OwnerCommandID = &s
	}
	if exec.Valid {
		s := exec.String
		entry.ExecutorID = &s
	}
	if projectID.Valid {
		s := projectID.String
		entry.ProjectID = &s
	}
	if environment.Valid {
		s := environment.String
		entry.EnvironmentID = &s
	}
	if err := decodeNullableImage(image, &entry.Image); err != nil {
		return platform.TaskListEntry{}, time.Time{}, fmt.Errorf("decode task image: %w", err)
	}
	if err := decodeNullableImage(resolvedImg, &entry.ResolvedImage); err != nil {
		return platform.TaskListEntry{}, time.Time{}, fmt.Errorf("decode task resolved_image: %w", err)
	}
	if imageSrc.Valid {
		s := imageSrc.String
		entry.ImageSource = &s
	}
	if ingestedAt.Valid {
		entry.IngestedAt = formatIngestedAt(ingestedAt.Time)
	}
	if claimedAt.Valid {
		formatted := formatIngestedAt(claimedAt.Time)
		entry.ClaimedAt = &formatted
	}
	return entry, ingestedAt.Time, nil
}

var _ ListenerRepository = (*Store)(nil)
