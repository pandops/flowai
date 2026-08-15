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

type ControlRepository interface {
	CreateTaskControl(context.Context, platform.GatewayIdentity, string, platform.CreateTaskControlRequest) (platform.TaskControl, error)
	GetTaskControl(context.Context, string, string, string) (platform.TaskControl, error)
	ListAssignedTaskControls(context.Context, string, platform.ExecutorIdentity) ([]platform.TaskControl, error)
}

type AuditRepository interface {
	ListAuditEntries(context.Context, string, string, string, int, *AuditAfter) ([]platform.AuditEntry, *AuditAfter, error)
	GetAuditEntry(context.Context, string, string) (platform.AuditEntry, error)
}

type AuditAfter struct {
	OccurredAtUnixNano int64
	AuditID            string
}

type StreamRepository interface {
	ListStreamTaskEvents(context.Context, string, string, time.Time, string) ([]platform.TaskEvent, error)
}

func (s *Store) CreateTaskControl(ctx context.Context, identity platform.GatewayIdentity, taskID string, req platform.CreateTaskControlRequest) (platform.TaskControl, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.TaskControl{}, fmt.Errorf("begin control transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var canonicalTaskID string
	if err := tx.QueryRowContext(ctx,
		`SELECT task_id FROM tasks WHERE team_id = $1 AND task_id = $2 FOR UPDATE`,
		identity.TeamID, taskID,
	).Scan(&canonicalTaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.TaskControl{}, ErrTaskUnknown
		}
		return platform.TaskControl{}, fmt.Errorf("lock control task: %w", err)
	}

	control, found, err := findTaskControl(ctx, tx, identity, taskID, req)
	if err != nil {
		return platform.TaskControl{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return platform.TaskControl{}, fmt.Errorf("commit control retry: %w", err)
		}
		return control, nil
	}

	controlID := uuid.NewString()
	auditID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_entries
		 (audit_id, team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome)
		 VALUES ($1, $2, $3, 'operator', $4, 'task_control_request', $5, $6, 'accepted')`,
		auditID, identity.TeamID, identity.OperatorID, "task.control."+req.Action, controlID, identity.RequestID,
	); err != nil {
		return platform.TaskControl{}, fmt.Errorf("append control audit: %w", err)
	}

	var requestedAt time.Time
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO task_control_requests
		 (control_id, team_id, task_id, operator_id, action, idempotency_key, reason, audit_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING requested_at`,
		controlID, identity.TeamID, taskID, identity.OperatorID, req.Action, req.IdempotencyKey, req.Reason, auditID,
	).Scan(&requestedAt); err != nil {
		return platform.TaskControl{}, fmt.Errorf("append task control: %w", err)
	}
	controlPayload, err := json.Marshal(map[string]any{"action": req.Action, "source": "operator"})
	if err != nil {
		return platform.TaskControl{}, fmt.Errorf("marshal requested control event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_control_events
		(control_event_id, control_id, task_id, team_id, status, occurred_at, payload)
		VALUES ($1, $2, $3, $4, 'pending', $5, $6::jsonb)`,
		uuid.NewString(), controlID, taskID, identity.TeamID, requestedAt, controlPayload); err != nil {
		return platform.TaskControl{}, fmt.Errorf("append requested control event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.TaskControl{}, fmt.Errorf("commit task control: %w", err)
	}
	return platform.TaskControl{
		ControlID: controlID, TeamID: identity.TeamID, TaskID: taskID,
		OperatorID: identity.OperatorID, Action: req.Action, IdempotencyKey: req.IdempotencyKey,
		Reason: req.Reason, Status: "pending", AuditID: auditID, RequestedAt: requestedAt.UTC(),
	}, nil
}

func findTaskControl(ctx context.Context, tx *sql.Tx, identity platform.GatewayIdentity, taskID string, req platform.CreateTaskControlRequest) (platform.TaskControl, bool, error) {
	var control platform.TaskControl
	var reason sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT control_id, team_id, task_id, operator_id, action, idempotency_key,
		       reason, status, audit_id, requested_at
		  FROM task_control_requests
		 WHERE team_id = $1 AND task_id = $2 AND operator_id = $3
		   AND action = $4 AND idempotency_key = $5`,
		identity.TeamID, taskID, identity.OperatorID, req.Action, req.IdempotencyKey,
	).Scan(&control.ControlID, &control.TeamID, &control.TaskID, &control.OperatorID,
		&control.Action, &control.IdempotencyKey, &reason, &control.Status, &control.AuditID, &control.RequestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskControl{}, false, nil
	}
	if err != nil {
		return platform.TaskControl{}, false, fmt.Errorf("read control retry: %w", err)
	}
	if reason.Valid {
		control.Reason = &reason.String
	}
	control.RequestedAt = control.RequestedAt.UTC()
	return control, true, nil
}

func (s *Store) GetTaskControl(ctx context.Context, teamID, taskID, controlID string) (platform.TaskControl, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT control_id, team_id, task_id, operator_id, action, idempotency_key,
		       reason, status, audit_id, requested_at
		  FROM task_control_requests
		 WHERE team_id = $1 AND task_id = $2 AND control_id = $3`, teamID, taskID, controlID)
	control, err := scanTaskControl(row)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskControl{}, ErrTaskUnknown
	}
	if err != nil {
		return platform.TaskControl{}, fmt.Errorf("get task control: %w", err)
	}
	return control, nil
}

func (s *Store) ListAssignedTaskControls(ctx context.Context, taskID string, identity platform.ExecutorIdentity) ([]platform.TaskControl, error) {
	var taskTeam string
	var assignedExecutor sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT team_id, executor_id FROM tasks WHERE task_id = $1`, taskID,
	).Scan(&taskTeam, &assignedExecutor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTaskUnknown
		}
		return nil, fmt.Errorf("read control task assignment: %w", err)
	}
	var storedScope string
	var storedTeam sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT scope, team_id FROM executors WHERE executor_id = $1`, identity.ExecutorID,
	).Scan(&storedScope, &storedTeam); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrExecutorNotFound
		}
		return nil, fmt.Errorf("read control executor: %w", err)
	}
	if storedScope != identity.Scope {
		return nil, ErrExecutorNotFound
	}
	if storedScope == platform.ExecutorScopeTeam {
		if identity.TeamID == nil || !storedTeam.Valid || *identity.TeamID != storedTeam.String {
			return nil, ErrExecutorNotFound
		}
		if taskTeam != storedTeam.String {
			return nil, ErrTaskUnknown
		}
	}
	if !assignedExecutor.Valid || assignedExecutor.String != identity.ExecutorID {
		return nil, ErrNotAssigned
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT control_id, team_id, task_id, operator_id, action, idempotency_key,
		       reason, status, audit_id, requested_at
		  FROM task_control_requests
		 WHERE team_id = $1 AND task_id = $2 AND status = 'pending'
		 ORDER BY requested_at ASC, control_id ASC`, taskTeam, taskID)
	if err != nil {
		return nil, fmt.Errorf("list assigned task controls: %w", err)
	}
	defer rows.Close()
	controls := make([]platform.TaskControl, 0)
	for rows.Next() {
		control, err := scanTaskControl(rows)
		if err != nil {
			return nil, fmt.Errorf("scan assigned task control: %w", err)
		}
		controls = append(controls, control)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assigned task controls: %w", err)
	}
	return controls, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanTaskControl(row rowScanner) (platform.TaskControl, error) {
	var control platform.TaskControl
	var reason sql.NullString
	err := row.Scan(&control.ControlID, &control.TeamID, &control.TaskID, &control.OperatorID,
		&control.Action, &control.IdempotencyKey, &reason, &control.Status, &control.AuditID, &control.RequestedAt)
	if err != nil {
		return platform.TaskControl{}, err
	}
	if reason.Valid {
		control.Reason = &reason.String
	}
	control.RequestedAt = control.RequestedAt.UTC()
	return control, nil
}

func (s *Store) ListAuditEntries(ctx context.Context, teamID, resourceType, resourceID string, limit int, after *AuditAfter) ([]platform.AuditEntry, *AuditAfter, error) {
	query := `
		SELECT audit_id, team_id, actor_id, actor_type, action, resource_type,
		       resource_id, request_id, outcome, occurred_at
		  FROM audit_entries
		 WHERE team_id = $1`
	args := []any{teamID}
	if resourceType != "" {
		args = append(args, resourceType)
		query += fmt.Sprintf(" AND resource_type = $%d", len(args))
	}
	if resourceID != "" {
		args = append(args, resourceID)
		query += fmt.Sprintf(" AND resource_id = $%d", len(args))
	}
	if after != nil {
		args = append(args, time.Unix(0, after.OccurredAtUnixNano).UTC())
		occurredPlaceholder := len(args)
		args = append(args, after.AuditID)
		query += fmt.Sprintf(" AND (occurred_at, audit_id) < ($%d, $%d)", occurredPlaceholder, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY occurred_at DESC, audit_id DESC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()
	entries := make([]platform.AuditEntry, 0)
	for rows.Next() {
		entry, err := scanAuditEntry(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("scan audit entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	var next *AuditAfter
	if len(entries) > limit {
		last := entries[limit-1]
		next = &AuditAfter{OccurredAtUnixNano: last.OccurredAt.UnixNano(), AuditID: last.AuditID}
		entries = entries[:limit]
	}
	return entries, next, nil
}

func (s *Store) GetAuditEntry(ctx context.Context, teamID, auditID string) (platform.AuditEntry, error) {
	entry, err := scanAuditEntry(s.db.QueryRowContext(ctx, `
		SELECT audit_id, team_id, actor_id, actor_type, action, resource_type,
		       resource_id, request_id, outcome, occurred_at
		  FROM audit_entries WHERE team_id = $1 AND audit_id = $2`, teamID, auditID))
	if errors.Is(err, sql.ErrNoRows) {
		return platform.AuditEntry{}, ErrTaskUnknown
	}
	if err != nil {
		return platform.AuditEntry{}, fmt.Errorf("get audit entry: %w", err)
	}
	return entry, nil
}

func scanAuditEntry(row rowScanner) (platform.AuditEntry, error) {
	var entry platform.AuditEntry
	err := row.Scan(&entry.AuditID, &entry.TeamID, &entry.ActorID, &entry.ActorType,
		&entry.Action, &entry.ResourceType, &entry.ResourceID, &entry.RequestID, &entry.Outcome, &entry.OccurredAt)
	entry.OccurredAt = entry.OccurredAt.UTC()
	return entry, err
}

func (s *Store) ListStreamTaskEvents(ctx context.Context, teamID, taskID string, after time.Time, afterEventID string) ([]platform.TaskEvent, error) {
	query := `
		SELECT event_id, team_id, task_id, executor_id, event_type, occurred_at, payload
		  FROM task_events
		 WHERE team_id = $1 AND (occurred_at, event_id) > ($2, $3)`
	args := []any{teamID, after, afterEventID}
	if taskID != "" {
		query += ` AND task_id = $4`
		args = append(args, taskID)
	}
	query += ` ORDER BY occurred_at ASC, event_id ASC LIMIT 100`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list stream task events: %w", err)
	}
	defer rows.Close()
	events := make([]platform.TaskEvent, 0)
	for rows.Next() {
		var event platform.TaskEvent
		var executorID sql.NullString
		var occurredAt time.Time
		if err := rows.Scan(&event.EventID, &event.TeamID, &event.TaskID, &executorID,
			&event.EventType, &occurredAt, &event.Payload); err != nil {
			return nil, fmt.Errorf("scan stream task event: %w", err)
		}
		if executorID.Valid {
			event.ExecutorID = &executorID.String
		}
		event.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stream task events: %w", err)
	}
	return events, nil
}
