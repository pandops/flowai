package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/google/uuid"
)

// ErrTaskNotFound is returned for unknown and foreign-team task identifiers.
var ErrTaskNotFound = errors.New("task not found")

// TaskEventRepository provides same-team ordered task-event history
// plus the task event append surface honored by the
// Executor-emitted lifecycle event route.
type TaskEventRepository interface {
	ListTaskEvents(context.Context, string, string) ([]platform.TaskEvent, error)
	AppendTaskEvent(context.Context, platform.TaskEventAppendRequest, platform.ExecutorIdentity) (platform.TaskEvent, error)
}

// ExecutorEventRepository provides the Executor self-event append and
// ordered history surfaces. The append enforces the scope-conditional
// team predicate and transactionally mirrors capacity observations
// onto the executor row.
type ExecutorEventRepository interface {
	AppendExecutorEvent(context.Context, platform.ExecutorEventAppendRequest, platform.ExecutorIdentity) (platform.ExecutorEvent, error)
	ListExecutorEvents(context.Context, string, string) ([]platform.ExecutorEvent, error)
}

// allowedTaskEventTypes is the closed set the POST route accepts.
// The first lifecycle event `created` is Registry-appended on claim
// and is NEVER accepted on this endpoint.
var allowedTaskEventTypes = map[string]struct{}{
	platform.TaskEventTypeRunning:  {},
	platform.TaskEventTypeFinished: {},
	platform.TaskEventTypeFailed:   {},
}

// allowedExecutorEventTypes is the closed set of ExecutorEventType
// values the POST route accepts. Capacity observations are
// transactional observations; they never gate scheduling.
var allowedExecutorEventTypes = map[string]struct{}{
	platform.ExecutorEventTypeRegistered:           {},
	platform.ExecutorEventTypeStarted:              {},
	platform.ExecutorEventTypeHealthy:              {},
	platform.ExecutorEventTypeBusy:                 {},
	platform.ExecutorEventTypeIdle:                 {},
	platform.ExecutorEventTypeStopping:             {},
	platform.ExecutorEventTypeStopped:              {},
	platform.ExecutorEventTypeFailed:               {},
	platform.ExecutorEventTypeCapacityObserved:     {},
	platform.ExecutorEventTypeRunningCountObserved: {},
}

// runningEventType is the only pre-terminal emit accepted on the
// POST route. Anything outside the allowed set is rejected with
// 400 invalid_request before a transaction starts.
const runningEventType = platform.TaskEventTypeRunning

// ListTaskEvents returns one task's event history after verifying task
// ownership. The team_id predicate runs before pagination, ordering,
// and serialization so foreign rows never contribute.
func (s *Store) ListTaskEvents(ctx context.Context, teamID, taskID string) ([]platform.TaskEvent, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE team_id = $1 AND task_id = $2)`,
		teamID, taskID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check task event ownership: %w", err)
	}
	if !exists {
		return nil, ErrTaskNotFound
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, team_id, task_id, executor_id, event_type, occurred_at, payload
		FROM task_events
		WHERE team_id = $1 AND task_id = $2
		ORDER BY occurred_at ASC, event_id ASC`, teamID, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task events: %w", err)
	}
	defer rows.Close()

	events := make([]platform.TaskEvent, 0)
	for rows.Next() {
		var (
			event      platform.TaskEvent
			executorID sql.NullString
			occurredAt sql.NullTime
			payload    []byte
		)
		if err := rows.Scan(
			&event.EventID,
			&event.TeamID,
			&event.TaskID,
			&executorID,
			&event.EventType,
			&occurredAt,
			&payload,
		); err != nil {
			return nil, fmt.Errorf("scan task event: %w", err)
		}
		if executorID.Valid {
			event.ExecutorID = &executorID.String
		}
		if occurredAt.Valid {
			event.OccurredAt = formatIngestedAt(occurredAt.Time)
		}
		event.Payload = normalizeJSONPayload(payload)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task events: %w", err)
	}
	return events, nil
}

// AppendTaskEvent atomically appends one Executor-emitted lifecycle
// event to the canonical task event log and updates the task's
// projected current_state in the same transaction. The function:
//
//  1. Resolves the assigned task (must be claimed by the same
//     authenticated Executor; foreign-task probes return ErrEventNotFound).
//  2. Verifies the envelope team_id matches the authenticated
//     Executor team (team-owned) or the parent task's team (system-owned).
//  3. Resolves an identical (task_id, event_id) retry BEFORE the
//     ordering check: returns the original accepted row without
//     appending or projecting.
//  4. Verifies the strict (occurred_at, event_id) ordering against
//     the latest accepted tuple; rejects out-of-order tuples with
//     ErrEventConflict.
//  5. Pre-validates the lifecycle transition; running is the only
//     pre-terminal emit accepted; running -> finished|failed, and no
//     emit follows the terminal state.
//  6. Inserts the event row and updates tasks.current_state in the
//     same transaction.
func (s *Store) AppendTaskEvent(ctx context.Context, req platform.TaskEventAppendRequest, identity platform.ExecutorIdentity) (platform.TaskEvent, error) {
	if _, ok := allowedTaskEventTypes[req.EventType]; !ok {
		return platform.TaskEvent{}, fmt.Errorf("event type %q is not accepted on the task event surface", req.EventType)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, req.OccurredAt)
	if err != nil {
		return platform.TaskEvent{}, fmt.Errorf("parse occurred_at: %w", err)
	}
	payload, err := normalizeAppendedPayload(req.Payload)
	if err != nil {
		return platform.TaskEvent{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.TaskEvent{}, fmt.Errorf("begin task event append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Lock the executor row first so the team/scope check is stable
	// for the duration of the transaction.
	var (
		scope  string
		teamID sql.NullString
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT scope, team_id FROM executors WHERE executor_id = $1 FOR UPDATE`,
		identity.ExecutorID,
	).Scan(&scope, &teamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.TaskEvent{}, ErrExecutorNotFound
		}
		return platform.TaskEvent{}, fmt.Errorf("read executor for task event: %w", err)
	}
	if identity.Scope != scope {
		return platform.TaskEvent{}, ErrExecutorNotFound
	}
	if scope == platform.ExecutorScopeTeam {
		if !teamID.Valid || identity.TeamID == nil || *identity.TeamID != teamID.String {
			return platform.TaskEvent{}, ErrExecutorNotFound
		}
	}

	// Lock the task row for the same-clock ordering and projection.
	var (
		taskTeam     string
		currentState string
		assignedTo   sql.NullString
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT team_id, current_state, executor_id FROM tasks WHERE task_id = $1 FOR UPDATE`,
		req.TaskID,
	).Scan(&taskTeam, &currentState, &assignedTo); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.TaskEvent{}, ErrEventNotFound
		}
		return platform.TaskEvent{}, fmt.Errorf("lock task for task event: %w", err)
	}

	// Foreign-team task identifiers collapse to the same non-revealing
	// 404 shape as unknown tasks.
	if scope == platform.ExecutorScopeTeam {
		if !teamID.Valid || teamID.String != taskTeam {
			return platform.TaskEvent{}, ErrEventNotFound
		}
	}
	// Envelope team_id verification is conditional on the assigned
	// Executor's ownership scope.
	if scope == platform.ExecutorScopeTeam {
		if req.TeamID != teamID.String {
			return platform.TaskEvent{}, ErrEventTeamMismatch
		}
	} else {
		if req.TeamID != taskTeam {
			return platform.TaskEvent{}, ErrEventTeamMismatch
		}
	}

	// The Executor must be the assigned one. Foreign-task point
	// identifiers reach here only when the system-owned Executor
	// shares the same team as the task; an unassigned same-team
	// Executor is rejected without mutation.
	if !assignedTo.Valid || assignedTo.String != identity.ExecutorID {
		return platform.TaskEvent{}, ErrNotAssigned
	}

	// Idempotent retry: same (task_id, event_id) returns the
	// original accepted row without appending or projecting.
	var (
		existingOccurredAt sql.NullTime
		existingEventType  sql.NullString
		existingPayload    []byte
	)
	err = tx.QueryRowContext(ctx,
		`SELECT occurred_at, event_type, payload
		   FROM task_events
		  WHERE task_id = $1 AND event_id = $2
		  FOR UPDATE`,
		req.TaskID, req.EventID,
	).Scan(&existingOccurredAt, &existingEventType, &existingPayload)
	switch {
	case err == nil:
		if existingEventType.String != req.EventType ||
			!existingOccurredAt.Time.Equal(occurredAt) ||
			!jsonEqual(existingPayload, []byte(payload)) {
			return platform.TaskEvent{}, ErrEventConflict
		}
		if err := tx.Commit(); err != nil {
			return platform.TaskEvent{}, fmt.Errorf("commit task event retry: %w", err)
		}
		accepted := existingOccurredAt.Time.UTC().Format(time.RFC3339Nano)
		return platform.TaskEvent{
			EventID:    req.EventID,
			TeamID:     taskTeam,
			TaskID:     req.TaskID,
			ExecutorID: &identity.ExecutorID,
			EventType:  existingEventType.String,
			OccurredAt: accepted,
			Payload:    normalizeJSONPayload(existingPayload),
		}, nil
	case errors.Is(err, sql.ErrNoRows):
		// continue
	default:
		return platform.TaskEvent{}, fmt.Errorf("scan task event retry: %w", err)
	}

	// Lifecycle guard runs BEFORE the strict ordering check so a
	// terminal-after-terminal tuple surfaces ErrInvalidTaskTransition
	// rather than ErrEventConflict. The strict ordering check still
	// fires for fresh tuples that are merely out of order.
	switch req.EventType {
	case runningEventType:
		if currentState != platform.TaskStateCreated {
			return platform.TaskEvent{}, ErrInvalidTaskTransition
		}
	case platform.TaskEventTypeFinished, platform.TaskEventTypeFailed:
		if currentState != platform.TaskStateRunning {
			return platform.TaskEvent{}, ErrInvalidTaskTransition
		}
	}

	// Strict ordering against the latest accepted tuple.
	var (
		latestOccurredAt sql.NullTime
		latestEventID    sql.NullString
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT occurred_at, event_id
		   FROM task_events
		  WHERE task_id = $1
		  ORDER BY occurred_at DESC, event_id DESC
		  LIMIT 1`, req.TaskID,
	).Scan(&latestOccurredAt, &latestEventID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return platform.TaskEvent{}, fmt.Errorf("read latest task event: %w", err)
	}
	if latestOccurredAt.Valid && latestEventID.Valid {
		latest := latestOccurredAt.Time.UTC()
		switch {
		case occurredAt.After(latest):
			// strictly newer occurred_at passes
		case occurredAt.Equal(latest):
			// equal timestamps require strictly greater event_id
			if req.EventID <= latestEventID.String {
				return platform.TaskEvent{}, ErrEventConflict
			}
		default:
			return platform.TaskEvent{}, ErrEventConflict
		}
	} else {
		// No prior event: the only legal pre-existing event for a
		// transition into running is the Registry-appended `created`
		// event. The current state MUST be `created` and the event
		// type MUST be `running`.
		if currentState != platform.TaskStateCreated || req.EventType != runningEventType {
			return platform.TaskEvent{}, ErrInvalidTaskTransition
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_events
		 (event_id, team_id, task_id, executor_id, event_type, occurred_at, payload)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		req.EventID, taskTeam, req.TaskID, identity.ExecutorID, req.EventType, occurredAt, payload,
	); err != nil {
		return platform.TaskEvent{}, fmt.Errorf("append task event: %w", err)
	}

	// Lifecycle guard: pre-terminal `running` only transitions out of
	// `created`; terminal events only transition out of `running`;
	// no emit follows the terminal state.
	switch req.EventType {
	case runningEventType:
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET current_state = $1 WHERE task_id = $2`,
			platform.TaskStateRunning, req.TaskID,
		); err != nil {
			return platform.TaskEvent{}, fmt.Errorf("project task running state: %w", err)
		}
	case platform.TaskEventTypeFinished, platform.TaskEventTypeFailed:
		terminal := platform.TaskStateFinished
		if req.EventType == platform.TaskEventTypeFailed {
			terminal = platform.TaskStateFailed
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET current_state = $1 WHERE task_id = $2`,
			terminal, req.TaskID,
		); err != nil {
			return platform.TaskEvent{}, fmt.Errorf("project task terminal state: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_entries
		 (audit_id, team_id, actor_id, actor_type, action, resource_type,
		  resource_id, request_id, outcome, executor_scope)
		 VALUES ($1, $2, $3, 'executor', $4, 'task_event', $5, $6, 'accepted', $7)`,
		uuid.NewString(), taskTeam, identity.ExecutorID, "task.event."+req.EventType,
		req.EventID, identity.RequestID, identity.Scope,
	); err != nil {
		return platform.TaskEvent{}, fmt.Errorf("append task event audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return platform.TaskEvent{}, fmt.Errorf("commit task event append: %w", err)
	}

	return platform.TaskEvent{
		EventID:    req.EventID,
		TeamID:     taskTeam,
		TaskID:     req.TaskID,
		ExecutorID: &identity.ExecutorID,
		EventType:  req.EventType,
		OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
		Payload:    normalizeJSONPayload([]byte(payload)),
	}, nil
}

// AppendExecutorEvent atomically appends one Executor self event and
// transactionally mirrors the latest capacity / running_count
// observations onto the executor row. The function:
//
//  1. Resolves the executor (must be the same authenticated Executor);
//     foreign-Executor probes return ErrEventNotFound.
//  2. Verifies the envelope team_id matches the authenticated
//     Executor team (team-owned) or is null (system-owned).
//  3. Resolves an identical (executor_id, event_id) retry BEFORE
//     ordering: returns the original accepted row without appending.
//  4. Inserts the executor_event row and mirrors capacity values
//     onto executors in the same transaction; the mirror is
//     informational only and never gates work.
func (s *Store) AppendExecutorEvent(ctx context.Context, req platform.ExecutorEventAppendRequest, identity platform.ExecutorIdentity) (platform.ExecutorEvent, error) {
	if _, ok := allowedExecutorEventTypes[req.EventType]; !ok {
		return platform.ExecutorEvent{}, fmt.Errorf("event type %q is not accepted on the executor event surface", req.EventType)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, req.OccurredAt)
	if err != nil {
		return platform.ExecutorEvent{}, fmt.Errorf("parse occurred_at: %w", err)
	}
	payload, err := normalizeAppendedPayload(req.Payload)
	if err != nil {
		return platform.ExecutorEvent{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.ExecutorEvent{}, fmt.Errorf("begin executor event append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		scope  string
		teamID sql.NullString
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT scope, team_id FROM executors WHERE executor_id = $1 FOR UPDATE`,
		identity.ExecutorID,
	).Scan(&scope, &teamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.ExecutorEvent{}, ErrExecutorNotFound
		}
		return platform.ExecutorEvent{}, fmt.Errorf("read executor for executor event: %w", err)
	}
	if identity.Scope != scope {
		return platform.ExecutorEvent{}, ErrExecutorNotFound
	}

	// Envelope team_id verification is conditional on the assigned
	// Executor's ownership scope.
	switch scope {
	case platform.ExecutorScopeTeam:
		if !teamID.Valid || req.TeamID == nil || *req.TeamID != teamID.String {
			return platform.ExecutorEvent{}, ErrEventTeamMismatch
		}
	case platform.ExecutorScopeSystem:
		if req.TeamID != nil {
			return platform.ExecutorEvent{}, ErrEventTeamMismatch
		}
	}

	// Idempotent retry: same (executor_id, event_id) returns the
	// original accepted row.
	var (
		existingOccurredAt sql.NullTime
		existingEventType  sql.NullString
		existingPayload    []byte
		existingTeamID     sql.NullString
		existingTaskID     sql.NullString
		existingExecutorID sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT executor_id, team_id, task_id, event_type, occurred_at, payload
		   FROM executor_events
		  WHERE executor_id = $1 AND event_id = $2
		  FOR UPDATE`,
		identity.ExecutorID, req.EventID,
	).Scan(&existingExecutorID, &existingTeamID, &existingTaskID, &existingEventType, &existingOccurredAt, &existingPayload)
	switch {
	case err == nil:
		if existingEventType.String != req.EventType ||
			!existingOccurredAt.Time.Equal(occurredAt) ||
			!jsonEqual(existingPayload, []byte(payload)) ||
			!sameNullableString(existingTeamID, req.TeamID) ||
			!sameNullableString(existingTaskID, req.TaskID) {
			return platform.ExecutorEvent{}, ErrEventConflict
		}
		if err := tx.Commit(); err != nil {
			return platform.ExecutorEvent{}, fmt.Errorf("commit executor event retry: %w", err)
		}
		event := buildExecutorEventRow(req.EventID, existingExecutorID.String, existingTeamID, existingTaskID, existingEventType.String, existingOccurredAt.Time, existingPayload)
		return event, nil
	case errors.Is(err, sql.ErrNoRows):
		// continue
	default:
		return platform.ExecutorEvent{}, fmt.Errorf("scan executor event retry: %w", err)
	}

	// Optional task_id is null when the self event is not bound to a
	// specific task. Task-bound events resolve the parent task before
	// append so unknown/foreign task identifiers remain non-revealing
	// and system-scope audits carry the parent task's immutable team.
	var taskIDForRow any
	var auditTeamID any
	if scope == platform.ExecutorScopeTeam && teamID.Valid {
		auditTeamID = teamID.String
	}
	if req.TaskID != nil {
		taskIDForRow = *req.TaskID
		var parentTaskTeam string
		if err := tx.QueryRowContext(ctx,
			`SELECT team_id FROM tasks WHERE task_id = $1`, *req.TaskID,
		).Scan(&parentTaskTeam); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return platform.ExecutorEvent{}, ErrEventNotFound
			}
			return platform.ExecutorEvent{}, fmt.Errorf("read parent task for executor event: %w", err)
		}
		if scope == platform.ExecutorScopeTeam && parentTaskTeam != teamID.String {
			return platform.ExecutorEvent{}, ErrEventNotFound
		}
		if scope == platform.ExecutorScopeSystem {
			auditTeamID = parentTaskTeam
		}
	}
	var teamIDForRow any
	if scope == platform.ExecutorScopeTeam && teamID.Valid {
		teamIDForRow = teamID.String
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO executor_events
		 (event_id, executor_id, team_id, task_id, event_type, occurred_at, payload)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		req.EventID, identity.ExecutorID, teamIDForRow, taskIDForRow, req.EventType, occurredAt, payload,
	); err != nil {
		return platform.ExecutorEvent{}, fmt.Errorf("append executor event: %w", err)
	}

	// Transactional observation mirror. The mirror is informational
	// only; it never gates discovery or claim.
	switch req.EventType {
	case platform.ExecutorEventTypeCapacityObserved:
		max, err := decodePayloadInt(payload, "max_capacity")
		if err != nil {
			return platform.ExecutorEvent{}, fmt.Errorf("decode capacity observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE executors SET max_capacity = $1, updated_at = now() WHERE executor_id = $2`,
			max, identity.ExecutorID,
		); err != nil {
			return platform.ExecutorEvent{}, fmt.Errorf("mirror capacity observation: %w", err)
		}
	case platform.ExecutorEventTypeRunningCountObserved:
		running, err := decodePayloadInt(payload, "running_count")
		if err != nil {
			return platform.ExecutorEvent{}, fmt.Errorf("decode running-count observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE executors SET running_count = $1, updated_at = now() WHERE executor_id = $2`,
			running, identity.ExecutorID,
		); err != nil {
			return platform.ExecutorEvent{}, fmt.Errorf("mirror running-count observation: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_entries
		 (audit_id, team_id, actor_id, actor_type, action, resource_type,
		  resource_id, request_id, outcome, executor_scope)
		 VALUES ($1, $2, $3, 'executor', $4, 'executor_event', $5, $6, 'accepted', $7)`,
		uuid.NewString(), auditTeamID, identity.ExecutorID, "executor.event."+req.EventType,
		req.EventID, identity.RequestID, identity.Scope,
	); err != nil {
		return platform.ExecutorEvent{}, fmt.Errorf("append executor event audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return platform.ExecutorEvent{}, fmt.Errorf("commit executor event append: %w", err)
	}

	event := platform.ExecutorEvent{
		EventID:    req.EventID,
		ExecutorID: identity.ExecutorID,
		EventType:  req.EventType,
		OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
		Payload:    normalizeJSONPayload([]byte(payload)),
	}
	if scope == platform.ExecutorScopeTeam && teamID.Valid {
		team := teamID.String
		event.TeamID = &team
	}
	if req.TaskID != nil {
		task := *req.TaskID
		event.TaskID = &task
	}
	return event, nil
}

// ListExecutorEvents returns one Executor's self-event history
// after verifying executor ownership. The team scope check is the
// same predicate the trusted-Gateway exec route uses: foreign-team
// or unknown Executor IDs collapse to the same non-revealing shape.
func (s *Store) ListExecutorEvents(ctx context.Context, teamID, executorID string) ([]platform.ExecutorEvent, error) {
	var (
		scope  string
		rowTID sql.NullString
	)
	if err := s.db.QueryRowContext(ctx,
		`SELECT scope, team_id FROM executors WHERE executor_id = $1`,
		executorID,
	).Scan(&scope, &rowTID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrExecutorNotFound
		}
		return nil, fmt.Errorf("read executor for event list: %w", err)
	}
	if scope != platform.ExecutorScopeTeam || !rowTID.Valid || rowTID.String != teamID {
		return nil, ErrExecutorNotFound
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT event_id, executor_id, team_id, task_id, event_type, occurred_at, payload
		   FROM executor_events
		  WHERE executor_id = $1
		  ORDER BY occurred_at ASC, event_id ASC`,
		executorID,
	)
	if err != nil {
		return nil, fmt.Errorf("list executor events: %w", err)
	}
	defer rows.Close()

	out := make([]platform.ExecutorEvent, 0)
	for rows.Next() {
		var (
			eventID    string
			execID     string
			teamIDCol  sql.NullString
			taskIDCol  sql.NullString
			eventType  string
			occurredAt sql.NullTime
			payload    []byte
		)
		if err := rows.Scan(&eventID, &execID, &teamIDCol, &taskIDCol, &eventType, &occurredAt, &payload); err != nil {
			return nil, fmt.Errorf("scan executor event: %w", err)
		}
		out = append(out, buildExecutorEventRow(eventID, execID, teamIDCol, taskIDCol, eventType, occurredAt.Time, payload))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate executor events: %w", err)
	}
	return out, nil
}

func buildExecutorEventRow(eventID, executorID string, teamID, taskID sql.NullString, eventType string, occurredAt time.Time, payload []byte) platform.ExecutorEvent {
	event := platform.ExecutorEvent{
		EventID:    eventID,
		ExecutorID: executorID,
		EventType:  eventType,
		OccurredAt: formatIngestedAt(occurredAt),
		Payload:    normalizeJSONPayload(payload),
	}
	if teamID.Valid {
		team := teamID.String
		event.TeamID = &team
	}
	if taskID.Valid {
		task := taskID.String
		event.TaskID = &task
	}
	return event
}

func jsonEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func sameNullableString(stored sql.NullString, submitted *string) bool {
	return stored.Valid == (submitted != nil) && (!stored.Valid || stored.String == *submitted)
}

// normalizeAppendedPayload returns the JSON payload as a string
// suitable for pgx jsonb cast. A null/empty payload is encoded as
// the literal "{}" so the column default is mirrored for callers
// that omit the field.
func normalizeAppendedPayload(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	trimmed := []byte{}
	for _, b := range raw {
		if b == ' ' || b == '\n' || b == '\t' || b == '\r' {
			continue
		}
		trimmed = append(trimmed, b)
	}
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "{}", nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("payload is not valid JSON")
	}
	return string(raw), nil
}

// decodePayloadInt extracts an integer field from a JSON payload
// document. The function returns ErrInvalidTaskTransition-style
// errors via the wrapped error so the caller can surface them as
// 400 invalid_request at the HTTP boundary.
func decodePayloadInt(payload string, field string) (int, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return 0, fmt.Errorf("payload is not a JSON object: %w", err)
	}
	raw, ok := decoded[field]
	if !ok {
		return 0, fmt.Errorf("payload field %q is missing", field)
	}
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("payload field %q is not an integer", field)
	}
}
