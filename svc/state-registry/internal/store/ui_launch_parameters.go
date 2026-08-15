package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

var ErrLaunchParameterConflict = errors.New("active launch parameters already exist for scope")

type LaunchParameterRepository interface {
	CreateLaunchParameters(context.Context, platform.GatewayIdentity, platform.LaunchParameterWrite) (platform.LaunchParameterDefinition, error)
	ListLaunchParameters(context.Context, string, string, *string, int) ([]platform.LaunchParameterDefinition, error)
	GetLaunchParameters(context.Context, string, string) (platform.LaunchParameterDefinition, error)
	ReplaceLaunchParameters(context.Context, platform.GatewayIdentity, string, platform.LaunchParameterWrite) (platform.LaunchParameterDefinition, error)
	DeleteLaunchParameters(context.Context, platform.GatewayIdentity, string) error
	ListLaunchParameterRevisions(context.Context, string, string, int) ([]platform.LaunchParameterRevision, error)
}

func (s *Store) CreateLaunchParameters(ctx context.Context, identity platform.GatewayIdentity, req platform.LaunchParameterWrite) (platform.LaunchParameterDefinition, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("begin launch parameters create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := verifyLaunchParameterScope(ctx, tx, identity.TeamID, req); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	values, err := json.Marshal(req.Env)
	if err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("marshal launch parameter env: %w", err)
	}
	environmentID := uuid.NewString()
	var item platform.LaunchParameterDefinition
	var taskTypeID, image sql.NullString
	err = tx.QueryRowContext(ctx, `INSERT INTO environment_definitions
		(environment_id, team_id, task_type_id, scope_kind, name, values, image)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
		RETURNING environment_id, team_id, revision, scope_kind, task_type_id, name,
		          values, image, created_at, updated_at`,
		environmentID, identity.TeamID, req.TaskTypeID, req.Scope, req.Name, values, req.Image,
	).Scan(&item.EnvironmentID, &item.TeamID, &item.Revision, &item.Scope,
		&taskTypeID, &item.Name, &values, &image, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return platform.LaunchParameterDefinition{}, classifyLaunchParameterWrite(err)
	}
	fillLaunchParameters(&item, taskTypeID, image, values)
	if err := appendLaunchParameterRevision(ctx, tx, identity, item, false); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	if err := appendEnvironmentAudit(ctx, tx, identity, "environment.create", environmentID); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("commit launch parameters create: %w", err)
	}
	item.Secrets = []platform.LogicalSecretSummary{}
	return item, nil
}

func (s *Store) ListLaunchParameters(ctx context.Context, teamID, scope string, taskTypeID *string, limit int) ([]platform.LaunchParameterDefinition, error) {
	query := `SELECT environment_id, COALESCE(team_id, ''), revision, scope_kind, task_type_id, name,
		values, image, created_at, updated_at FROM environment_definitions
		WHERE ((team_id = $1 AND scope_kind IN ('team','task_type')) OR
		       (team_id IS NULL AND scope_kind = 'global')) AND deleted = false`
	args := []any{teamID}
	if scope != "" {
		args = append(args, scope)
		query += fmt.Sprintf(" AND scope_kind = $%d", len(args))
	}
	if taskTypeID != nil {
		args = append(args, *taskTypeID)
		query += fmt.Sprintf(" AND task_type_id = $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at ASC, environment_id ASC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list launch parameters: %w", err)
	}
	defer rows.Close()
	items := make([]platform.LaunchParameterDefinition, 0)
	for rows.Next() {
		item, err := scanLaunchParameters(rows)
		if err != nil {
			return nil, fmt.Errorf("scan launch parameters: %w", err)
		}
		item.Secrets, err = s.listUILaunchParameterSecretSummaries(ctx, item.EnvironmentID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) listUILaunchParameterSecretSummaries(ctx context.Context, environmentID string) ([]platform.LogicalSecretSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT secret_id, COALESCE(team_id, ''), environment_id,
		name, latest_version, revoked FROM secrets WHERE environment_id = $1 AND revoked = false
		ORDER BY created_at ASC, secret_id ASC`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list UI launch parameter secrets: %w", err)
	}
	defer rows.Close()
	items := make([]platform.LogicalSecretSummary, 0)
	for rows.Next() {
		var item platform.LogicalSecretSummary
		if err := rows.Scan(&item.SecretID, &item.TeamID, &item.EnvironmentID, &item.Name,
			&item.LatestVersion, &item.Revoked); err != nil {
			return nil, fmt.Errorf("scan UI launch parameter secret: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetLaunchParameters(ctx context.Context, teamID, environmentID string) (platform.LaunchParameterDefinition, error) {
	item, err := scanLaunchParameters(s.db.QueryRowContext(ctx, `SELECT environment_id, team_id,
		revision, scope_kind, task_type_id, name, values, image, created_at, updated_at
		FROM environment_definitions WHERE team_id = $1 AND environment_id = $2
		AND scope_kind IS NOT NULL AND deleted = false`, teamID, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return platform.LaunchParameterDefinition{}, ErrEnvironmentUnavailable
	}
	if err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("get launch parameters: %w", err)
	}
	item.Secrets, err = s.listSecretSummaries(ctx, teamID, environmentID)
	return item, err
}

func (s *Store) ReplaceLaunchParameters(ctx context.Context, identity platform.GatewayIdentity, environmentID string, req platform.LaunchParameterWrite) (platform.LaunchParameterDefinition, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("begin launch parameters replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := verifyLaunchParameterScope(ctx, tx, identity.TeamID, req); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	values, err := json.Marshal(req.Env)
	if err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("marshal launch parameter env: %w", err)
	}
	var item platform.LaunchParameterDefinition
	var taskTypeID, image sql.NullString
	err = tx.QueryRowContext(ctx, `UPDATE environment_definitions SET name = $1, values = $2::jsonb,
		image = $3, revision = revision + 1, updated_at = now()
		WHERE team_id = $4 AND environment_id = $5 AND scope_kind = $6
		AND task_type_id IS NOT DISTINCT FROM $7 AND deleted = false
		RETURNING environment_id, team_id, revision, scope_kind, task_type_id, name,
		          values, image, created_at, updated_at`, req.Name, values, req.Image,
		identity.TeamID, environmentID, req.Scope, req.TaskTypeID,
	).Scan(&item.EnvironmentID, &item.TeamID, &item.Revision, &item.Scope,
		&taskTypeID, &item.Name, &values, &image, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.LaunchParameterDefinition{}, ErrEnvironmentUnavailable
	}
	if err != nil {
		return platform.LaunchParameterDefinition{}, classifyLaunchParameterWrite(err)
	}
	fillLaunchParameters(&item, taskTypeID, image, values)
	if err := appendLaunchParameterRevision(ctx, tx, identity, item, false); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	if err := appendEnvironmentAudit(ctx, tx, identity, "environment.replace", environmentID); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.LaunchParameterDefinition{}, fmt.Errorf("commit launch parameters replace: %w", err)
	}
	item.Secrets, err = s.listSecretSummaries(ctx, identity.TeamID, environmentID)
	return item, err
}

func (s *Store) DeleteLaunchParameters(ctx context.Context, identity platform.GatewayIdentity, environmentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin launch parameters delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var item platform.LaunchParameterDefinition
	var taskTypeID, image sql.NullString
	var values []byte
	err = tx.QueryRowContext(ctx, `UPDATE environment_definitions SET deleted = true,
		revision = revision + 1, updated_at = now() WHERE team_id = $1 AND environment_id = $2
		AND scope_kind IS NOT NULL AND deleted = false RETURNING environment_id, team_id,
		revision, scope_kind, task_type_id, name, values, image, created_at, updated_at`,
		identity.TeamID, environmentID).Scan(&item.EnvironmentID, &item.TeamID, &item.Revision,
		&item.Scope, &taskTypeID, &item.Name, &values, &image, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEnvironmentUnavailable
	}
	if err != nil {
		return fmt.Errorf("delete launch parameters: %w", err)
	}
	fillLaunchParameters(&item, taskTypeID, image, values)
	if err := appendLaunchParameterRevision(ctx, tx, identity, item, true); err != nil {
		return err
	}
	if err := appendEnvironmentAudit(ctx, tx, identity, "environment.delete", environmentID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListLaunchParameterRevisions(ctx context.Context, teamID, environmentID string, limit int) ([]platform.LaunchParameterRevision, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM environment_definitions
		WHERE team_id = $1 AND environment_id = $2 AND scope_kind IS NOT NULL)`, teamID, environmentID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("verify launch parameter history owner: %w", err)
	}
	if !exists {
		return nil, ErrEnvironmentUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT environment_id, revision, team_id, scope_kind,
		task_type_id, name, values, secret_keys, image, deleted, actor_id, request_id, created_at
		FROM environment_revisions WHERE team_id = $1 AND environment_id = $2
		ORDER BY revision DESC LIMIT $3`, teamID, environmentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list launch parameter revisions: %w", err)
	}
	defer rows.Close()
	items := make([]platform.LaunchParameterRevision, 0)
	for rows.Next() {
		var item platform.LaunchParameterRevision
		var taskTypeID, image sql.NullString
		var values, secretKeys []byte
		if err := rows.Scan(&item.EnvironmentID, &item.Revision, &item.TeamID, &item.Scope,
			&taskTypeID, &item.Name, &values, &secretKeys, &image, &item.Deleted,
			&item.ActorID, &item.RequestID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan launch parameter revision: %w", err)
		}
		if taskTypeID.Valid {
			item.TaskTypeID = &taskTypeID.String
		}
		if image.Valid {
			item.Image = &image.String
		}
		if err := json.Unmarshal(values, &item.Env); err != nil {
			return nil, fmt.Errorf("decode revision env: %w", err)
		}
		if err := json.Unmarshal(secretKeys, &item.SecretKeys); err != nil {
			return nil, fmt.Errorf("decode revision secret keys: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func verifyLaunchParameterScope(ctx context.Context, tx *sql.Tx, teamID string, req platform.LaunchParameterWrite) error {
	if req.Scope == platform.LaunchParameterScopeTeam && req.TaskTypeID == nil {
		return nil
	}
	if req.Scope != platform.LaunchParameterScopeTaskType || req.TaskTypeID == nil || *req.TaskTypeID == "" {
		return ErrEnvironmentUnavailable
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM task_types
		WHERE team_id = $1 AND task_type_id = $2)`, teamID, *req.TaskTypeID).Scan(&exists); err != nil {
		return fmt.Errorf("verify launch parameter task type: %w", err)
	}
	if !exists {
		return ErrEnvironmentUnavailable
	}
	return nil
}

func appendLaunchParameterRevision(ctx context.Context, tx *sql.Tx, identity platform.GatewayIdentity, item platform.LaunchParameterDefinition, deleted bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM secrets WHERE team_id = $1 AND environment_id = $2 AND revoked = false ORDER BY name`, item.TeamID, item.EnvironmentID)
	if err != nil {
		return fmt.Errorf("list revision secret keys: %w", err)
	}
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return fmt.Errorf("scan revision secret key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close revision secret keys: %w", err)
	}
	values, err := json.Marshal(item.Env)
	if err != nil {
		return fmt.Errorf("marshal revision env: %w", err)
	}
	secretKeys, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("marshal revision secret keys: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO environment_revisions
		(environment_id, revision, team_id, scope_kind, task_type_id, name, values,
		 secret_keys, image, deleted, actor_id, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11,$12)`,
		item.EnvironmentID, item.Revision, item.TeamID, item.Scope, item.TaskTypeID,
		item.Name, values, secretKeys, item.Image, deleted, identity.OperatorID, identity.RequestID)
	if err != nil {
		return fmt.Errorf("append launch parameter revision: %w", err)
	}
	return nil
}

func scanLaunchParameters(row rowScanner) (platform.LaunchParameterDefinition, error) {
	var item platform.LaunchParameterDefinition
	var taskTypeID, image sql.NullString
	var values []byte
	if err := row.Scan(&item.EnvironmentID, &item.TeamID, &item.Revision, &item.Scope,
		&taskTypeID, &item.Name, &values, &image, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return platform.LaunchParameterDefinition{}, err
	}
	fillLaunchParameters(&item, taskTypeID, image, values)
	return item, nil
}

func fillLaunchParameters(item *platform.LaunchParameterDefinition, taskTypeID, image sql.NullString, values []byte) {
	if taskTypeID.Valid {
		item.TaskTypeID = &taskTypeID.String
	}
	if image.Valid {
		item.Image = &image.String
	}
	if len(values) == 0 {
		item.Env = map[string]string{}
		return
	}
	_ = json.Unmarshal(values, &item.Env)
}

func classifyLaunchParameterWrite(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrLaunchParameterConflict
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrEnvironmentUnavailable
	}
	return fmt.Errorf("write launch parameters: %w", err)
}

var _ LaunchParameterRepository = (*Store)(nil)
