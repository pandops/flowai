package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/flowai/platform/state-registry/internal/platform"
)

var (
	ErrExecutorNotFound         = errors.New("executor not found")
	ErrExecutorScopeConflict    = errors.New("executor scope is immutable")
	ErrExecutorTeamConflict     = errors.New("executor team is immutable")
	ErrExecutorIdentityMismatch = errors.New("executor identity does not match registration")
	ErrExecutorTagMismatch      = errors.New("executor tag mismatch")
)

// ExecutorRepository persists registrations and performs read-only discovery.
type ExecutorRepository interface {
	RegisterExecutor(context.Context, string, platform.ExecutorRegistrationRequest, platform.ExecutorIdentity) (platform.Executor, error)
	GetExecutor(context.Context, string) (platform.Executor, error)
	DiscoverExecutorTasks(context.Context, string, string, int) ([]platform.TaskSummary, error)
}

// RegisterExecutor creates or refreshes one Executor while preserving its
// original scope, team binding, and registration timestamp.
func (s *Store) RegisterExecutor(ctx context.Context, executorID string, req platform.ExecutorRegistrationRequest, identity platform.ExecutorIdentity) (platform.Executor, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Executor{}, fmt.Errorf("begin executor registration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, executorID,
	); err != nil {
		return platform.Executor{}, fmt.Errorf("lock executor registration: %w", err)
	}

	var existingScope string
	var existingTeam sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT scope, team_id FROM executors WHERE executor_id = $1 FOR UPDATE`, executorID,
	).Scan(&existingScope, &existingTeam)
	switch {
	case err == nil:
		if existingScope != req.Scope {
			return platform.Executor{}, ErrExecutorScopeConflict
		}
		if !sameTeam(existingTeam, req.TeamID) {
			return platform.Executor{}, ErrExecutorTeamConflict
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return platform.Executor{}, fmt.Errorf("read executor registration: %w", err)
	}

	if identity.ExecutorID != executorID || identity.Scope != req.Scope || !sameTeamPointer(identity.TeamID, req.TeamID) {
		return platform.Executor{}, ErrExecutorIdentityMismatch
	}

	var executor platform.Executor
	var teamID sql.NullString
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO executors (
				executor_id, scope, team_id, executor_type, identity,
				authorized_tag, max_capacity, running_count, runtime_metadata
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
			RETURNING executor_id, scope, team_id, executor_type, identity,
				authorized_tag, max_capacity, running_count, runtime_metadata,
				registered_at, updated_at`,
			executorID, req.Scope, req.TeamID, req.ExecutorType, req.Identity,
			req.AuthorizedTag, req.MaxCapacity, req.RunningCount, req.RuntimeMetadata,
		).Scan(
			&executor.ExecutorID, &executor.Scope, &teamID, &executor.ExecutorType,
			&executor.Identity, &executor.AuthorizedTag, &executor.MaxCapacity,
			&executor.RunningCount, &executor.RuntimeMetadata,
			&executor.RegisteredAt, &executor.UpdatedAt,
		)
	} else {
		err = tx.QueryRowContext(ctx, `
			UPDATE executors
			SET executor_type = $2, identity = $3, authorized_tag = $4,
				max_capacity = $5, running_count = $6,
				runtime_metadata = $7::jsonb, updated_at = now()
			WHERE executor_id = $1
			RETURNING executor_id, scope, team_id, executor_type, identity,
				authorized_tag, max_capacity, running_count, runtime_metadata,
				registered_at, updated_at`,
			executorID, req.ExecutorType, req.Identity, req.AuthorizedTag,
			req.MaxCapacity, req.RunningCount, req.RuntimeMetadata,
		).Scan(
			&executor.ExecutorID, &executor.Scope, &teamID, &executor.ExecutorType,
			&executor.Identity, &executor.AuthorizedTag, &executor.MaxCapacity,
			&executor.RunningCount, &executor.RuntimeMetadata,
			&executor.RegisteredAt, &executor.UpdatedAt,
		)
	}
	if err != nil {
		if sqlStateIs(err, "23503") {
			return platform.Executor{}, ErrTeamNotFound
		}
		return platform.Executor{}, fmt.Errorf("persist executor registration: %w", err)
	}
	if teamID.Valid {
		executor.TeamID = &teamID.String
	}
	if err := tx.Commit(); err != nil {
		return platform.Executor{}, fmt.Errorf("commit executor registration: %w", err)
	}
	return executor, nil
}

// GetExecutor returns one canonical Executor record.
func (s *Store) GetExecutor(ctx context.Context, executorID string) (platform.Executor, error) {
	var executor platform.Executor
	var teamID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT executor_id, scope, team_id, executor_type, identity,
			authorized_tag, max_capacity, running_count, runtime_metadata,
			registered_at, updated_at
		FROM executors WHERE executor_id = $1`, executorID,
	).Scan(
		&executor.ExecutorID, &executor.Scope, &teamID, &executor.ExecutorType,
		&executor.Identity, &executor.AuthorizedTag, &executor.MaxCapacity,
		&executor.RunningCount, &executor.RuntimeMetadata,
		&executor.RegisteredAt, &executor.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.Executor{}, ErrExecutorNotFound
	}
	if err != nil {
		return platform.Executor{}, fmt.Errorf("get executor: %w", err)
	}
	if teamID.Valid {
		executor.TeamID = &teamID.String
	}
	return executor, nil
}

// DiscoverExecutorTasks returns pending summaries after applying the
// registered scope predicate and then FIFO ordering. It never reads capacity.
func (s *Store) DiscoverExecutorTasks(ctx context.Context, executorID, tag string, limit int) ([]platform.TaskSummary, error) {
	var scope, registeredTag string
	var teamID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT scope, team_id, authorized_tag FROM executors WHERE executor_id = $1`, executorID,
	).Scan(&scope, &teamID, &registeredTag); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExecutorNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read executor discovery binding: %w", err)
	}
	if tag != registeredTag {
		return nil, ErrExecutorTagMismatch
	}

	query := `
		SELECT task_id, team_id, required_tag, current_state, ingested_at
		FROM tasks
		WHERE current_state = 'pending' AND required_tag = $1`
	args := []any{registeredTag}
	if scope == platform.ExecutorScopeTeam {
		query += ` AND team_id = $2 ORDER BY ingested_at ASC, task_id ASC LIMIT $3`
		args = append(args, teamID.String, limit)
	} else {
		query += ` ORDER BY ingested_at ASC, task_id ASC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("discover executor tasks: %w", err)
	}
	defer rows.Close()

	items := make([]platform.TaskSummary, 0)
	for rows.Next() {
		var item platform.TaskSummary
		if err := rows.Scan(&item.TaskID, &item.TeamID, &item.RequiredTag, &item.CurrentState, &item.IngestedAt); err != nil {
			return nil, fmt.Errorf("scan executor task summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate executor task summaries: %w", err)
	}
	return items, nil
}

func sameTeam(existing sql.NullString, requested *string) bool {
	return existing.Valid == (requested != nil) && (!existing.Valid || existing.String == *requested)
}

func sameTeamPointer(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

var _ ExecutorRepository = (*Store)(nil)
