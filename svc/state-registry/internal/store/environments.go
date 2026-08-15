package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
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

// The legacy environment surface is retained as a team-scope compatibility
// alias. v0006 deliberately rejects the removed project/task/parent-task
// scopes and stores accepted writes in the canonical launch-parameter model.
func (s *Store) CreateEnvironment(ctx context.Context, identity platform.GatewayIdentity, req platform.EnvironmentWriteRequest) (platform.Environment, error) {
	if hasLegacyNarrowScope(req.Scope) {
		return platform.Environment{}, ErrEnvironmentUnavailable
	}
	item, err := s.CreateLaunchParameters(ctx, identity, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTeam,
		Name:  req.Name,
		Env:   req.Values,
	})
	if err != nil {
		return platform.Environment{}, err
	}
	return legacyEnvironment(item), nil
}

func (s *Store) ListEnvironments(ctx context.Context, teamID string, projectID, taskID *string, limit int) ([]platform.Environment, error) {
	items, _, err := s.ListEnvironmentsPaged(ctx, teamID, projectID, taskID, limit, nil)
	return items, err
}

func (s *Store) ListEnvironmentsPaged(ctx context.Context, teamID string, projectID, taskID *string, limit int, after *EnvironmentAfter) ([]platform.Environment, *EnvironmentAfter, error) {
	if limit < 1 {
		return nil, nil, errors.New("ListEnvironmentsPaged: limit must be >= 1")
	}
	if projectID != nil || taskID != nil {
		return []platform.Environment{}, nil, nil
	}
	items, err := s.ListLaunchParameters(ctx, teamID, platform.LaunchParameterScopeTeam, nil, limit+1)
	if err != nil {
		return nil, nil, err
	}
	legacy := make([]platform.Environment, 0, len(items))
	for _, item := range items {
		if after != nil {
			createdNano := item.CreatedAt.UnixNano()
			if createdNano < after.CreatedAtUnixNano || (createdNano == after.CreatedAtUnixNano && item.EnvironmentID <= after.EnvironmentID) {
				continue
			}
		}
		legacy = append(legacy, legacyEnvironment(item))
	}
	if len(legacy) <= limit {
		return legacy, nil, nil
	}
	last := legacy[limit-1]
	return legacy[:limit], &EnvironmentAfter{
		CreatedAtUnixNano: last.CreatedAt.UnixNano(),
		EnvironmentID:     last.EnvironmentID,
	}, nil
}

func (s *Store) GetEnvironment(ctx context.Context, teamID, environmentID string) (platform.Environment, error) {
	item, err := s.GetLaunchParameters(ctx, teamID, environmentID)
	if err != nil {
		return platform.Environment{}, err
	}
	return legacyEnvironment(item), nil
}

func (s *Store) ReplaceEnvironment(ctx context.Context, identity platform.GatewayIdentity, environmentID string, req platform.EnvironmentWriteRequest) (platform.Environment, error) {
	if hasLegacyNarrowScope(req.Scope) {
		return platform.Environment{}, ErrEnvironmentUnavailable
	}
	item, err := s.ReplaceLaunchParameters(ctx, identity, environmentID, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTeam,
		Name:  req.Name,
		Env:   req.Values,
	})
	if err != nil {
		return platform.Environment{}, err
	}
	return legacyEnvironment(item), nil
}

func (s *Store) DeleteEnvironment(ctx context.Context, identity platform.GatewayIdentity, environmentID string) error {
	return s.DeleteLaunchParameters(ctx, identity, environmentID)
}

func hasLegacyNarrowScope(scope platform.EnvironmentScope) bool {
	return scope.ProjectID != nil || scope.TaskID != nil || scope.ParentTaskID != nil
}

func legacyEnvironment(item platform.LaunchParameterDefinition) platform.Environment {
	return platform.Environment{
		EnvironmentID: item.EnvironmentID,
		TeamID:        item.TeamID,
		Revision:      item.Revision,
		Name:          item.Name,
		Values:        item.Env,
		Secrets:       item.Secrets,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
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

var _ EnvironmentRepository = (*Store)(nil)
