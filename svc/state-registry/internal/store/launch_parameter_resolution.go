package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

type resolvedLaunchParameters struct {
	HasValues   bool
	Image       *string
	ImageSource string
}

type resolvedSecretReference struct {
	Key      string
	SecretID string
	Version  int
}

// resolveLaunchParametersLocked freezes the global -> team -> task_type
// projection for one claimed task. Later edits affect later claims only.
func resolveLaunchParametersLocked(ctx context.Context, tx *sql.Tx, teamID, taskTypeID, taskID string) (resolvedLaunchParameters, error) {
	rows, err := tx.QueryContext(ctx, `SELECT environment_id, scope_kind, values, image
		FROM environment_definitions
		WHERE deleted = false AND (
			scope_kind = 'global' OR
			(scope_kind = 'team' AND team_id = $1) OR
			(scope_kind = 'task_type' AND team_id = $1 AND task_type_id = $2)
		)
		ORDER BY CASE scope_kind WHEN 'global' THEN 1 WHEN 'team' THEN 2 ELSE 3 END`, teamID, taskTypeID)
	if err != nil {
		return resolvedLaunchParameters{}, fmt.Errorf("list applicable launch parameters: %w", err)
	}
	defer rows.Close()
	type definition struct {
		environmentID string
		scope         string
		values        []byte
		image         sql.NullString
	}
	definitions := make([]definition, 0, 3)
	for rows.Next() {
		var item definition
		if err := rows.Scan(&item.environmentID, &item.scope, &item.values, &item.image); err != nil {
			return resolvedLaunchParameters{}, fmt.Errorf("scan applicable launch parameters: %w", err)
		}
		definitions = append(definitions, item)
	}
	if err := rows.Err(); err != nil {
		return resolvedLaunchParameters{}, fmt.Errorf("iterate applicable launch parameters: %w", err)
	}
	if err := rows.Close(); err != nil {
		return resolvedLaunchParameters{}, fmt.Errorf("close applicable launch parameters: %w", err)
	}
	values := make(map[string]string)
	secretRefs := make(map[string]resolvedSecretReference)
	result := resolvedLaunchParameters{}
	for _, definition := range definitions {
		var definitionValues map[string]string
		if err := json.Unmarshal(definition.values, &definitionValues); err != nil {
			return resolvedLaunchParameters{}, fmt.Errorf("decode applicable launch parameters: %w", err)
		}
		for key, value := range definitionValues {
			values[key] = value
			delete(secretRefs, key)
		}
		secretRows, err := tx.QueryContext(ctx, `SELECT name, secret_id, latest_version
			FROM secrets WHERE environment_id = $1 AND revoked = false ORDER BY name`, definition.environmentID)
		if err != nil {
			return resolvedLaunchParameters{}, fmt.Errorf("list applicable secret references: %w", err)
		}
		for secretRows.Next() {
			var ref resolvedSecretReference
			if err := secretRows.Scan(&ref.Key, &ref.SecretID, &ref.Version); err != nil {
				secretRows.Close()
				return resolvedLaunchParameters{}, fmt.Errorf("scan applicable secret reference: %w", err)
			}
			secretRefs[ref.Key] = ref
			delete(values, ref.Key)
		}
		if err := secretRows.Close(); err != nil {
			return resolvedLaunchParameters{}, fmt.Errorf("close applicable secret references: %w", err)
		}
		if definition.image.Valid {
			result.Image = &definition.image.String
			switch definition.scope {
			case platform.LaunchParameterScopeGlobal:
				result.ImageSource = platform.ImageSourceGlobalParameters
			case platform.LaunchParameterScopeTeam:
				result.ImageSource = platform.ImageSourceTeamParameters
			case platform.LaunchParameterScopeTaskType:
				result.ImageSource = platform.ImageSourceTaskTypeParameters
			}
		}
	}
	encodedValues, err := json.Marshal(values)
	if err != nil {
		return resolvedLaunchParameters{}, fmt.Errorf("marshal task launch parameter snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_launch_parameter_snapshots (task_id, team_id, values)
		VALUES ($1, $2, $3::jsonb) ON CONFLICT (task_id) DO NOTHING`, taskID, teamID, encodedValues); err != nil {
		return resolvedLaunchParameters{}, fmt.Errorf("insert task launch parameter snapshot: %w", err)
	}
	for _, ref := range secretRefs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_launch_parameter_secret_refs
			(task_id, team_id, key, secret_id, version) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (task_id, key) DO NOTHING`, taskID, teamID, ref.Key, ref.SecretID, ref.Version); err != nil {
			return resolvedLaunchParameters{}, fmt.Errorf("insert task secret reference snapshot: %w", err)
		}
	}
	result.HasValues = len(values) > 0 || len(secretRefs) > 0
	return result, nil
}
