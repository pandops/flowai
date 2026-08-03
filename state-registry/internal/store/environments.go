package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/state-registry/internal/platform"
)

var ErrEnvironmentUnavailable = errors.New("environment unknown or unavailable")

type EnvironmentRepository interface {
	CreateEnvironment(context.Context, platform.GatewayIdentity, platform.EnvironmentWriteRequest) (platform.Environment, error)
	ListEnvironments(context.Context, string, *string, *string, int) ([]platform.Environment, error)
	ListEnvironmentsPaged(context.Context, string, *string, *string, int, *EnvironmentAfter) ([]platform.Environment, *EnvironmentAfter, error)
	GetEnvironment(context.Context, string, string) (platform.Environment, error)
	ReplaceEnvironment(context.Context, platform.GatewayIdentity, string, platform.EnvironmentWriteRequest) (platform.Environment, error)
	DeleteEnvironment(context.Context, platform.GatewayIdentity, string) error
}

func (s *Store) CreateEnvironment(ctx context.Context, identity platform.GatewayIdentity, req platform.EnvironmentWriteRequest) (platform.Environment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Environment{}, fmt.Errorf("begin environment create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := verifyEnvironmentTask(ctx, tx, identity.TeamID, req.Scope.TaskID); err != nil {
		return platform.Environment{}, err
	}
	if err := verifyEnvironmentParent(ctx, tx, identity.TeamID, req.Scope.ParentTaskID); err != nil {
		return platform.Environment{}, err
	}
	values, err := json.Marshal(req.Values)
	if err != nil {
		return platform.Environment{}, fmt.Errorf("marshal environment values: %w", err)
	}
	environmentID := uuid.NewString()
	var environment platform.Environment
	var projectID, taskID, parentTaskID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO environment_definitions
		 (environment_id, team_id, task_id, parent_task_id, project_id, name, values)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		 RETURNING environment_id, team_id, revision, name, project_id, task_id, parent_task_id,
		           values, deleted, created_at, updated_at`,
		environmentID, identity.TeamID, req.Scope.TaskID, req.Scope.ParentTaskID, req.Scope.ProjectID, req.Name, values,
	).Scan(&environment.EnvironmentID, &environment.TeamID, &environment.Revision, &environment.Name,
		&projectID, &taskID, &parentTaskID, &values, &environment.Deleted, &environment.CreatedAt, &environment.UpdatedAt); err != nil {
		return platform.Environment{}, fmt.Errorf("insert environment: %w", err)
	}
	if req.Scope.ParentTaskID != nil {
		result, err := tx.ExecContext(ctx, `UPDATE tasks SET environment_id = $1
			WHERE team_id = $2 AND task_id = $3 AND current_state = 'pending' AND environment_id IS NULL`,
			environmentID, identity.TeamID, *req.Scope.ParentTaskID)
		if err != nil {
			return platform.Environment{}, fmt.Errorf("attach task-owned environment: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return platform.Environment{}, ErrEnvironmentUnavailable
		}
	}
	if err := appendEnvironmentAudit(ctx, tx, identity, "environment.create", environmentID); err != nil {
		return platform.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.Environment{}, fmt.Errorf("commit environment create: %w", err)
	}
	fillEnvironment(&environment, projectID, taskID, parentTaskID, values)
	return environment, nil
}

func (s *Store) ListEnvironments(ctx context.Context, teamID string, projectID, taskID *string, limit int) ([]platform.Environment, error) {
	items, _, err := s.ListEnvironmentsPaged(ctx, teamID, projectID, taskID, limit, nil)
	return items, err
}

// ListEnvironmentsPaged is the cursor-paged variant of ListEnvironments.
// The read is bracketed by a single read-only transaction so the page
// and its associated secret summaries are observed at one snapshot.
// limit+1 rows are fetched so the caller can derive a next cursor
// without a second round trip.
func (s *Store) ListEnvironmentsPaged(ctx context.Context, teamID string, projectID, taskID *string, limit int, after *EnvironmentAfter) ([]platform.Environment, *EnvironmentAfter, error) {
	if limit < 1 {
		return nil, nil, errors.New("ListEnvironmentsPaged: limit must be >= 1")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin environment list: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT environment_id, team_id, revision, name, project_id, task_id, parent_task_id,
	                 values, deleted, created_at, updated_at
	            FROM environment_definitions WHERE team_id = $1 AND deleted = false`
	args := []any{teamID}
	if projectID != nil {
		args = append(args, *projectID)
		query += fmt.Sprintf(" AND project_id = $%d", len(args))
	}
	if taskID != nil {
		args = append(args, *taskID)
		query += fmt.Sprintf(" AND task_id = $%d", len(args))
	}
	if after != nil {
		// strict greater-than tuple predicate for the
		// (created_at ASC, environment_id ASC) order.
		afterTime := time.Unix(0, after.CreatedAtUnixNano).UTC()
		args = append(args, afterTime, afterTime, after.EnvironmentID)
		query += fmt.Sprintf(
			" AND (created_at > $%d OR (created_at = $%d AND environment_id > $%d))",
			len(args)-2, len(args)-1, len(args),
		)
	}
	fetchLimit := limit + 1
	args = append(args, fetchLimit)
	query += fmt.Sprintf(" ORDER BY created_at ASC, environment_id ASC LIMIT $%d", len(args))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()
	environments := make([]platform.Environment, 0, fetchLimit)
	var envIDs []string
	for rows.Next() {
		environment, err := scanEnvironment(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("scan environment: %w", err)
		}
		envIDs = append(envIDs, environment.EnvironmentID)
		environments = append(environments, environment)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate environments: %w", err)
	}
	rows.Close()

	// Batched secret lookup eliminates the per-environment N+1 query
	// the prior ListEnvironments used. One SELECT for every secret
	// under the page of environments keeps the page and its secret
	// summaries on a single consistent snapshot.
	if len(environments) > 0 {
		summaryByEnv, err := s.listSecretSummariesBatchedTx(ctx, tx, teamID, envIDs)
		if err != nil {
			return nil, nil, err
		}
		for i := range environments {
			environments[i].Secrets = summaryByEnv[environments[i].EnvironmentID]
		}
	}

	if len(environments) <= limit {
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit environment list: %w", err)
		}
		return environments, nil, nil
	}
	last := environments[limit-1]
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit environment list: %w", err)
	}
	return environments[:limit], &EnvironmentAfter{
		CreatedAtUnixNano: last.CreatedAt.UnixNano(),
		EnvironmentID:     last.EnvironmentID,
	}, nil
}

// listSecretSummariesBatchedTx returns a map keyed by environment_id
// covering every secret summary under the supplied environment ids for
// the same team. The caller is responsible for closing the rows.
func (s *Store) listSecretSummariesBatchedTx(ctx context.Context, tx *sql.Tx, teamID string, environmentIDs []string) (map[string][]platform.LogicalSecretSummary, error) {
	if len(environmentIDs) == 0 {
		return map[string][]platform.LogicalSecretSummary{}, nil
	}
	placeholders := make([]string, 0, len(environmentIDs))
	args := make([]any, 0, len(environmentIDs)+1)
	args = append(args, teamID)
	for i, id := range environmentIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
		args = append(args, id)
	}
	query := fmt.Sprintf(`SELECT secret_id, team_id, environment_id, name, latest_version, revoked
		FROM secrets
		WHERE team_id = $1 AND environment_id IN (%s)
		ORDER BY environment_id ASC, created_at ASC, secret_id ASC`, strings.Join(placeholders, ", "))
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list environment secret summaries batched: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]platform.LogicalSecretSummary, len(environmentIDs))
	for _, id := range environmentIDs {
		out[id] = []platform.LogicalSecretSummary{}
	}
	for rows.Next() {
		var item platform.LogicalSecretSummary
		if err := rows.Scan(&item.SecretID, &item.TeamID, &item.EnvironmentID, &item.Name, &item.LatestVersion, &item.Revoked); err != nil {
			return nil, fmt.Errorf("scan environment secret summary: %w", err)
		}
		out[item.EnvironmentID] = append(out[item.EnvironmentID], item)
	}
	return out, rows.Err()
}

func (s *Store) GetEnvironment(ctx context.Context, teamID, environmentID string) (platform.Environment, error) {
	environment, err := scanEnvironment(s.db.QueryRowContext(ctx, `
		SELECT environment_id, team_id, revision, name, project_id, task_id, parent_task_id,
		       values, deleted, created_at, updated_at
		  FROM environment_definitions
		 WHERE team_id = $1 AND environment_id = $2 AND deleted = false`, teamID, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return platform.Environment{}, ErrEnvironmentUnavailable
	}
	if err != nil {
		return platform.Environment{}, fmt.Errorf("get environment: %w", err)
	}
	environment.Secrets, err = s.listSecretSummaries(ctx, teamID, environmentID)
	if err != nil {
		return platform.Environment{}, err
	}
	return environment, nil
}

func (s *Store) ReplaceEnvironment(ctx context.Context, identity platform.GatewayIdentity, environmentID string, req platform.EnvironmentWriteRequest) (platform.Environment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.Environment{}, fmt.Errorf("begin environment replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := verifyEnvironmentTask(ctx, tx, identity.TeamID, req.Scope.TaskID); err != nil {
		return platform.Environment{}, err
	}
	if err := verifyEnvironmentParent(ctx, tx, identity.TeamID, req.Scope.ParentTaskID); err != nil {
		return platform.Environment{}, err
	}
	values, err := json.Marshal(req.Values)
	if err != nil {
		return platform.Environment{}, fmt.Errorf("marshal environment values: %w", err)
	}
	var environment platform.Environment
	var projectID, taskID, parentTaskID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		UPDATE environment_definitions
		   SET task_id = $1, parent_task_id = $2, project_id = $3, name = $4, values = $5::jsonb,
		       revision = revision + 1, updated_at = now()
		 WHERE team_id = $6 AND environment_id = $7 AND deleted = false
		 RETURNING environment_id, team_id, revision, name, project_id, task_id, parent_task_id,
		           values, deleted, created_at, updated_at`,
		req.Scope.TaskID, req.Scope.ParentTaskID, req.Scope.ProjectID, req.Name, values, identity.TeamID, environmentID,
	).Scan(&environment.EnvironmentID, &environment.TeamID, &environment.Revision, &environment.Name,
		&projectID, &taskID, &parentTaskID, &values, &environment.Deleted, &environment.CreatedAt, &environment.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.Environment{}, ErrEnvironmentUnavailable
		}
		return platform.Environment{}, fmt.Errorf("replace environment: %w", err)
	}
	if err := appendEnvironmentAudit(ctx, tx, identity, "environment.replace", environmentID); err != nil {
		return platform.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.Environment{}, fmt.Errorf("commit environment replace: %w", err)
	}
	fillEnvironment(&environment, projectID, taskID, parentTaskID, values)
	environment.Secrets, err = s.listSecretSummaries(ctx, identity.TeamID, environmentID)
	return environment, err
}

func (s *Store) DeleteEnvironment(ctx context.Context, identity platform.GatewayIdentity, environmentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin environment delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE environment_definitions
		SET deleted = true, revision = revision + 1, updated_at = now()
		WHERE team_id = $1 AND environment_id = $2 AND deleted = false`, identity.TeamID, environmentID)
	if err != nil {
		return fmt.Errorf("delete environment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted environment count: %w", err)
	}
	if rows == 0 {
		return ErrEnvironmentUnavailable
	}
	if err := appendEnvironmentAudit(ctx, tx, identity, "environment.delete", environmentID); err != nil {
		return err
	}
	return tx.Commit()
}

func verifyEnvironmentParent(ctx context.Context, tx *sql.Tx, teamID string, parentTaskID *string) error {
	return verifyEnvironmentTask(ctx, tx, teamID, parentTaskID)
}

func verifyEnvironmentTask(ctx context.Context, tx *sql.Tx, teamID string, taskID *string) error {
	if taskID == nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM tasks WHERE team_id = $1 AND task_id = $2)`, teamID, *taskID).Scan(&exists); err != nil {
		return fmt.Errorf("verify environment task: %w", err)
	}
	if !exists {
		return ErrEnvironmentUnavailable
	}
	return nil
}

func appendEnvironmentAudit(ctx context.Context, tx *sql.Tx, identity platform.GatewayIdentity, action, environmentID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_entries
		(audit_id, team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome)
		VALUES ($1, $2, $3, 'operator', $4, 'environment', $5, $6, 'accepted')`,
		uuid.NewString(), identity.TeamID, identity.OperatorID, action, environmentID, identity.RequestID)
	if err != nil {
		return fmt.Errorf("append environment audit: %w", err)
	}
	return nil
}

func scanEnvironment(row rowScanner) (platform.Environment, error) {
	var environment platform.Environment
	var projectID, taskID, parentTaskID sql.NullString
	var values []byte
	err := row.Scan(&environment.EnvironmentID, &environment.TeamID, &environment.Revision,
		&environment.Name, &projectID, &taskID, &parentTaskID, &values, &environment.Deleted,
		&environment.CreatedAt, &environment.UpdatedAt)
	if err != nil {
		return platform.Environment{}, err
	}
	fillEnvironment(&environment, projectID, taskID, parentTaskID, values)
	return environment, nil
}

func fillEnvironment(environment *platform.Environment, projectID, taskID, parentTaskID sql.NullString, values []byte) {
	if projectID.Valid {
		environment.Scope.ProjectID = &projectID.String
	}
	if taskID.Valid {
		environment.Scope.TaskID = &taskID.String
	}
	if parentTaskID.Valid {
		environment.Scope.ParentTaskID = &parentTaskID.String
	}
	if json.Unmarshal(values, &environment.Values) != nil || environment.Values == nil {
		environment.Values = map[string]string{}
	}
	environment.Secrets = make([]platform.LogicalSecretSummary, 0)
	environment.CreatedAt = environment.CreatedAt.UTC()
	environment.UpdatedAt = environment.UpdatedAt.UTC()
}

func (s *Store) listSecretSummaries(ctx context.Context, teamID, environmentID string) ([]platform.LogicalSecretSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT secret_id, team_id, environment_id, name, latest_version, revoked
		FROM secrets WHERE team_id = $1 AND environment_id = $2 ORDER BY created_at ASC, secret_id ASC`, teamID, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list environment secret summaries: %w", err)
	}
	defer rows.Close()
	items := make([]platform.LogicalSecretSummary, 0)
	for rows.Next() {
		var item platform.LogicalSecretSummary
		if err := rows.Scan(&item.SecretID, &item.TeamID, &item.EnvironmentID, &item.Name, &item.LatestVersion, &item.Revoked); err != nil {
			return nil, fmt.Errorf("scan environment secret summary: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
