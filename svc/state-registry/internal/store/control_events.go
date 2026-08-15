package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

var ErrControlEventConflict = errors.New("control event conflicts with current projection")

type ControlEventRepository interface {
	AppendTaskControlEvent(context.Context, string, string, platform.TaskControlEventAppendRequest, platform.ExecutorIdentity) (platform.TaskControlEvent, error)
	ListTaskControlEvents(context.Context, string, string, string, int) ([]platform.TaskControlEvent, error)
}

func (s *Store) AppendTaskControlEvent(ctx context.Context, taskID, controlID string, req platform.TaskControlEventAppendRequest, identity platform.ExecutorIdentity) (platform.TaskControlEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.TaskControlEvent{}, fmt.Errorf("begin control event append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var teamID, assignedExecutor, currentStatus string
	err = tx.QueryRowContext(ctx, `SELECT control.team_id, task.executor_id, control.status
		FROM task_control_requests control JOIN tasks task
		ON task.team_id = control.team_id AND task.task_id = control.task_id
		WHERE control.task_id = $1 AND control.control_id = $2 FOR UPDATE`, taskID, controlID).Scan(&teamID, &assignedExecutor, &currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskControlEvent{}, ErrTaskUnknown
	}
	if err != nil {
		return platform.TaskControlEvent{}, fmt.Errorf("lock task control for event: %w", err)
	}
	if assignedExecutor != identity.ExecutorID || !s.executorIdentityMatches(ctx, tx, teamID, identity) {
		return platform.TaskControlEvent{}, ErrTaskUnknown
	}
	if existing, found, err := readTaskControlEvent(ctx, tx, teamID, taskID, controlID, req.ControlEventID); err != nil {
		return platform.TaskControlEvent{}, err
	} else if found {
		if existing.Status != req.Status || !existing.OccurredAt.Equal(req.OccurredAt) || !equalControlPayload(existing.Payload, req.Payload) {
			return platform.TaskControlEvent{}, ErrControlEventConflict
		}
		if err := tx.Commit(); err != nil {
			return platform.TaskControlEvent{}, fmt.Errorf("commit control event retry: %w", err)
		}
		return existing, nil
	}
	if !validControlTransition(currentStatus, req.Status) {
		return platform.TaskControlEvent{}, ErrControlEventConflict
	}
	var lastAt time.Time
	var lastID string
	err = tx.QueryRowContext(ctx, `SELECT occurred_at, control_event_id FROM task_control_events
		WHERE team_id = $1 AND task_id = $2 AND control_id = $3
		ORDER BY occurred_at DESC, control_event_id DESC LIMIT 1`, teamID, taskID, controlID).Scan(&lastAt, &lastID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return platform.TaskControlEvent{}, fmt.Errorf("read last control event: %w", err)
	}
	if !errors.Is(err, sql.ErrNoRows) && (req.OccurredAt.Before(lastAt) || (req.OccurredAt.Equal(lastAt) && req.ControlEventID <= lastID)) {
		return platform.TaskControlEvent{}, ErrControlEventConflict
	}
	payload := normalizeControlPayload(req.Payload)
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_control_events
		(control_event_id, control_id, task_id, team_id, executor_id, status, occurred_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`, req.ControlEventID, controlID, taskID,
		teamID, identity.ExecutorID, req.Status, req.OccurredAt.UTC(), payload); err != nil {
		return platform.TaskControlEvent{}, fmt.Errorf("insert control event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_control_requests SET status = $1
		WHERE team_id = $2 AND task_id = $3 AND control_id = $4`, req.Status, teamID, taskID, controlID); err != nil {
		return platform.TaskControlEvent{}, fmt.Errorf("update task control projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_entries
		(audit_id, team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome, executor_scope)
		VALUES ($1,$2,$3,'executor',$4,'task_control_request',$5,$6,'accepted',$7)`, uuid.NewString(),
		teamID, identity.ExecutorID, "task.control."+req.Status, controlID, identity.RequestID, identity.Scope); err != nil {
		return platform.TaskControlEvent{}, fmt.Errorf("append control event audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.TaskControlEvent{}, fmt.Errorf("commit control event: %w", err)
	}
	executorID := identity.ExecutorID
	return platform.TaskControlEvent{ControlEventID: req.ControlEventID, ControlID: controlID,
		TaskID: taskID, TeamID: teamID, ExecutorID: &executorID, EventType: platform.TaskControlEventTypeRequested,
		Status: req.Status, OccurredAt: req.OccurredAt.UTC(), Payload: payload}, nil
}

func (s *Store) ListTaskControlEvents(ctx context.Context, teamID, taskID, controlID string, limit int) ([]platform.TaskControlEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT control_event_id, control_id, task_id, team_id,
		executor_id, status, occurred_at, payload FROM task_control_events
		WHERE team_id = $1 AND task_id = $2 AND control_id = $3
		ORDER BY occurred_at ASC, control_event_id ASC LIMIT $4`, teamID, taskID, controlID, limit)
	if err != nil {
		return nil, fmt.Errorf("list task control events: %w", err)
	}
	defer rows.Close()
	items := make([]platform.TaskControlEvent, 0)
	for rows.Next() {
		var item platform.TaskControlEvent
		var executorID sql.NullString
		if err := rows.Scan(&item.ControlEventID, &item.ControlID, &item.TaskID, &item.TeamID,
			&executorID, &item.Status, &item.OccurredAt, &item.Payload); err != nil {
			return nil, fmt.Errorf("scan task control event: %w", err)
		}
		if executorID.Valid {
			item.ExecutorID = &executorID.String
		}
		item.EventType = platform.TaskControlEventTypeRequested
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) executorIdentityMatches(ctx context.Context, tx *sql.Tx, taskTeamID string, identity platform.ExecutorIdentity) bool {
	var scope string
	var teamID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT scope, team_id FROM executors WHERE executor_id = $1`, identity.ExecutorID).Scan(&scope, &teamID); err != nil {
		return false
	}
	if scope != identity.Scope {
		return false
	}
	if scope == platform.ExecutorScopeTeam {
		return identity.TeamID != nil && teamID.Valid && *identity.TeamID == teamID.String && teamID.String == taskTeamID
	}
	return !teamID.Valid && identity.TeamID == nil
}

func readTaskControlEvent(ctx context.Context, tx *sql.Tx, teamID, taskID, controlID, eventID string) (platform.TaskControlEvent, bool, error) {
	var item platform.TaskControlEvent
	var executorID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT control_event_id, control_id, task_id, team_id,
		executor_id, status, occurred_at, payload FROM task_control_events
		WHERE team_id = $1 AND task_id = $2 AND control_id = $3 AND control_event_id = $4`,
		teamID, taskID, controlID, eventID).Scan(&item.ControlEventID, &item.ControlID, &item.TaskID,
		&item.TeamID, &executorID, &item.Status, &item.OccurredAt, &item.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.TaskControlEvent{}, false, nil
	}
	if err != nil {
		return platform.TaskControlEvent{}, false, fmt.Errorf("read control event retry: %w", err)
	}
	if executorID.Valid {
		item.ExecutorID = &executorID.String
	}
	item.EventType = platform.TaskControlEventTypeRequested
	return item, true, nil
}

func validControlTransition(current, next string) bool {
	return (current == "pending" && next == "acknowledged") ||
		(current == "acknowledged" && (next == "completed" || next == "failed"))
}

func normalizeControlPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || !json.Valid(payload) {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), payload...)
}

func equalControlPayload(left, right json.RawMessage) bool {
	var compactLeft, compactRight bytes.Buffer
	if json.Compact(&compactLeft, normalizeControlPayload(left)) != nil || json.Compact(&compactRight, normalizeControlPayload(right)) != nil {
		return false
	}
	return bytes.Equal(compactLeft.Bytes(), compactRight.Bytes())
}

var _ ControlEventRepository = (*Store)(nil)
