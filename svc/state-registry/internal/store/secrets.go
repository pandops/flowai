package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

const localKeyID = "local-aes-v1"

type SecretRepository interface {
	CreateSecret(context.Context, platform.GatewayIdentity, string, platform.SecretCreateRequest) (platform.SecretWriteResponse, error)
	ReplaceSecret(context.Context, platform.GatewayIdentity, string, string, platform.SecretReplaceRequest) (platform.SecretWriteResponse, error)
	GetSecret(context.Context, string, string, string) (platform.LogicalSecret, error)
	ListSecrets(context.Context, string, string, int) ([]platform.LogicalSecret, error)
	ListSecretsPaged(context.Context, string, string, int, *SecretAfter) ([]platform.LogicalSecret, *SecretAfter, error)
	ListSecretVersions(context.Context, string, string, string, int) ([]platform.SecretVersion, error)
	ListSecretVersionsPaged(context.Context, string, string, string, int, *SecretVersionAfter) ([]platform.SecretVersion, *SecretVersionAfter, error)
	GetSecretVersion(context.Context, string, string, string, int) (platform.SecretVersion, error)
	RevokeSecret(context.Context, platform.GatewayIdentity, string, string) (platform.LogicalSecret, error)
}

func (s *Store) CreateSecret(ctx context.Context, identity platform.GatewayIdentity, environmentID string, req platform.SecretCreateRequest) (platform.SecretWriteResponse, error) {
	if len(s.aesKey) != 32 {
		return platform.SecretWriteResponse{}, errors.New("AES-256-GCM key unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.SecretWriteResponse{}, fmt.Errorf("begin secret create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	environment, err := readEnvironmentForSecret(ctx, tx, identity.TeamID, environmentID)
	if err != nil {
		return platform.SecretWriteResponse{}, err
	}
	if !sameEnvironmentScope(environment.Scope, req.Scope) {
		return platform.SecretWriteResponse{}, ErrEnvironmentUnavailable
	}
	secretID := uuid.NewString()
	ciphertext, nonce, tag, err := s.encryptSecret(identity.TeamID, secretID, 1, req.Value)
	if err != nil {
		return platform.SecretWriteResponse{}, err
	}
	var secret platform.LogicalSecret
	if err := tx.QueryRowContext(ctx, `INSERT INTO secrets
		(secret_id, team_id, environment_id, name, latest_version)
		VALUES ($1, $2, $3, $4, 1)
		RETURNING secret_id, team_id, environment_id, name, latest_version, revoked, created_at, updated_at`,
		secretID, identity.TeamID, environmentID, req.Name,
	).Scan(&secret.SecretID, &secret.TeamID, &secret.EnvironmentID, &secret.Name,
		&secret.LatestVersion, &secret.Revoked, &secret.CreatedAt, &secret.UpdatedAt); err != nil {
		return platform.SecretWriteResponse{}, fmt.Errorf("insert logical secret: %w", err)
	}
	version, err := insertSecretVersion(ctx, tx, identity.TeamID, secretID, 1, ciphertext, nonce, tag)
	if err != nil {
		return platform.SecretWriteResponse{}, err
	}
	if err := appendSecretAudit(ctx, tx, identity, "secret.create", secretID); err != nil {
		return platform.SecretWriteResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.SecretWriteResponse{}, fmt.Errorf("commit secret create: %w", err)
	}
	secret.Scope = environment.Scope
	return platform.SecretWriteResponse{Secret: secret, Version: version}, nil
}

func (s *Store) ReplaceSecret(ctx context.Context, identity platform.GatewayIdentity, environmentID, secretID string, req platform.SecretReplaceRequest) (platform.SecretWriteResponse, error) {
	if len(s.aesKey) != 32 {
		return platform.SecretWriteResponse{}, errors.New("AES-256-GCM key unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.SecretWriteResponse{}, fmt.Errorf("begin secret replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	environment, err := readEnvironmentForSecret(ctx, tx, identity.TeamID, environmentID)
	if err != nil {
		return platform.SecretWriteResponse{}, err
	}
	var secret platform.LogicalSecret
	if err := tx.QueryRowContext(ctx, `SELECT secret_id, team_id, environment_id, name,
		latest_version, revoked, created_at, updated_at FROM secrets
		WHERE team_id = $1 AND environment_id = $2 AND secret_id = $3 AND revoked = false FOR UPDATE`,
		identity.TeamID, environmentID, secretID,
	).Scan(&secret.SecretID, &secret.TeamID, &secret.EnvironmentID, &secret.Name,
		&secret.LatestVersion, &secret.Revoked, &secret.CreatedAt, &secret.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.SecretWriteResponse{}, ErrEnvironmentUnavailable
		}
		return platform.SecretWriteResponse{}, fmt.Errorf("lock logical secret: %w", err)
	}
	nextVersion := secret.LatestVersion + 1
	ciphertext, nonce, tag, err := s.encryptSecret(identity.TeamID, secretID, nextVersion, req.Value)
	if err != nil {
		return platform.SecretWriteResponse{}, err
	}
	version, err := insertSecretVersion(ctx, tx, identity.TeamID, secretID, nextVersion, ciphertext, nonce, tag)
	if err != nil {
		return platform.SecretWriteResponse{}, err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE secrets SET latest_version = $1, updated_at = now()
		WHERE team_id = $2 AND secret_id = $3 RETURNING updated_at`, nextVersion, identity.TeamID, secretID).Scan(&secret.UpdatedAt); err != nil {
		return platform.SecretWriteResponse{}, fmt.Errorf("advance logical secret version: %w", err)
	}
	secret.LatestVersion = nextVersion
	secret.Scope = environment.Scope
	if err := appendSecretAudit(ctx, tx, identity, "secret.replace", secretID); err != nil {
		return platform.SecretWriteResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return platform.SecretWriteResponse{}, fmt.Errorf("commit secret replace: %w", err)
	}
	return platform.SecretWriteResponse{Secret: secret, Version: version}, nil
}

func (s *Store) GetSecret(ctx context.Context, teamID, environmentID, secretID string) (platform.LogicalSecret, error) {
	environment, err := s.GetEnvironment(ctx, teamID, environmentID)
	if err != nil {
		return platform.LogicalSecret{}, err
	}
	var secret platform.LogicalSecret
	if err := s.db.QueryRowContext(ctx, `SELECT secret_id, team_id, environment_id, name,
		latest_version, revoked, created_at, updated_at FROM secrets
		WHERE team_id = $1 AND environment_id = $2 AND secret_id = $3`, teamID, environmentID, secretID,
	).Scan(&secret.SecretID, &secret.TeamID, &secret.EnvironmentID, &secret.Name,
		&secret.LatestVersion, &secret.Revoked, &secret.CreatedAt, &secret.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.LogicalSecret{}, ErrEnvironmentUnavailable
		}
		return platform.LogicalSecret{}, fmt.Errorf("get logical secret: %w", err)
	}
	secret.Scope = environment.Scope
	return secret, nil
}

func (s *Store) ListSecrets(ctx context.Context, teamID, environmentID string, limit int) ([]platform.LogicalSecret, error) {
	items, _, err := s.ListSecretsPaged(ctx, teamID, environmentID, limit, nil)
	return items, err
}

// ListSecretsPaged is the cursor-paged variant of ListSecrets. The read
// runs in a single read-only transaction that first re-reads the
// environment so the secret page and its scope are observed at the
// same database snapshot. limit+1 rows are fetched so the caller can
// derive a next cursor without a second round trip.
func (s *Store) ListSecretsPaged(ctx context.Context, teamID, environmentID string, limit int, after *SecretAfter) ([]platform.LogicalSecret, *SecretAfter, error) {
	if limit < 1 {
		return nil, nil, errors.New("ListSecretsPaged: limit must be >= 1")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin secret list: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	environment, err := scanEnvironment(tx.QueryRowContext(ctx, `SELECT environment_id, team_id, revision, name,
		project_id, task_id, parent_task_id, values, deleted, created_at, updated_at FROM environment_definitions
		WHERE team_id = $1 AND environment_id = $2 AND deleted = false`, teamID, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrEnvironmentUnavailable
	}
	if err != nil {
		return nil, nil, fmt.Errorf("verify environment for secret list: %w", err)
	}

	query := `SELECT secret_id, team_id, environment_id, name,
		latest_version, revoked, created_at, updated_at FROM secrets
		WHERE team_id = $1 AND environment_id = $2`
	args := []any{teamID, environmentID}
	if after != nil {
		// strict greater-than tuple predicate for the
		// (created_at ASC, secret_id ASC) order.
		afterTime := time.Unix(0, after.CreatedAtUnixNano).UTC()
		args = append(args, afterTime, afterTime, after.SecretID)
		query += fmt.Sprintf(
			" AND (created_at > $%d OR (created_at = $%d AND secret_id > $%d))",
			len(args)-2, len(args)-1, len(args),
		)
	}
	fetchLimit := limit + 1
	args = append(args, fetchLimit)
	query += fmt.Sprintf(" ORDER BY created_at ASC, secret_id ASC LIMIT $%d", len(args))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list logical secrets: %w", err)
	}
	defer rows.Close()
	items := make([]platform.LogicalSecret, 0, fetchLimit)
	for rows.Next() {
		var secret platform.LogicalSecret
		if err := rows.Scan(&secret.SecretID, &secret.TeamID, &secret.EnvironmentID, &secret.Name,
			&secret.LatestVersion, &secret.Revoked, &secret.CreatedAt, &secret.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan logical secret: %w", err)
		}
		secret.Scope = environment.Scope
		items = append(items, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate logical secrets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit secret list: %w", err)
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	last := items[limit-1]
	return items[:limit], &SecretAfter{
		CreatedAtUnixNano: last.CreatedAt.UnixNano(),
		SecretID:          last.SecretID,
	}, nil
}

func (s *Store) ListSecretVersions(ctx context.Context, teamID, environmentID, secretID string, limit int) ([]platform.SecretVersion, error) {
	items, _, err := s.ListSecretVersionsPaged(ctx, teamID, environmentID, secretID, limit, nil)
	return items, err
}

// ListSecretVersionsPaged is the cursor-paged variant of
// ListSecretVersions. The read runs in a single read-only transaction
// that re-verifies the (team_id, environment_id, secret_id) triple
// before listing versions so a foreign environment or a secret that
// moved teams cannot leak through the (team_id, secret_id) shortcut.
func (s *Store) ListSecretVersionsPaged(ctx context.Context, teamID, environmentID, secretID string, limit int, after *SecretVersionAfter) ([]platform.SecretVersion, *SecretVersionAfter, error) {
	if limit < 1 {
		return nil, nil, errors.New("ListSecretVersionsPaged: limit must be >= 1")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin secret version list: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Foreign-environment defense: a secret can technically be
	// referenced through secrets.team_id without verifying it
	// still lives in the named environment. The explicit (team_id,
	// environment_id, secret_id) lookup is the canonical defense.
	if _, err := scanEnvironment(tx.QueryRowContext(ctx, `SELECT environment_id, team_id, revision, name,
		project_id, task_id, parent_task_id, values, deleted, created_at, updated_at FROM environment_definitions
		WHERE team_id = $1 AND environment_id = $2 AND deleted = false`, teamID, environmentID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrEnvironmentUnavailable
		}
		return nil, nil, fmt.Errorf("verify environment for secret version list: %w", err)
	}
	var probe int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM secrets
		WHERE team_id = $1 AND environment_id = $2 AND secret_id = $3`, teamID, environmentID, secretID).Scan(&probe); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrEnvironmentUnavailable
		}
		return nil, nil, fmt.Errorf("verify secret for version list: %w", err)
	}

	query := `SELECT secret_id, team_id, version, key_id, key_version, created_at
		FROM secret_versions WHERE team_id = $1 AND secret_id = $2`
	args := []any{teamID, secretID}
	if after != nil {
		args = append(args, after.Version, after.SecretID)
		query += fmt.Sprintf(" AND (version, secret_id) > ($%d, $%d)", len(args)-1, len(args))
	}
	fetchLimit := limit + 1
	args = append(args, fetchLimit)
	query += fmt.Sprintf(" ORDER BY version ASC, secret_id ASC LIMIT $%d", len(args))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list secret versions: %w", err)
	}
	defer rows.Close()
	items := make([]platform.SecretVersion, 0, fetchLimit)
	for rows.Next() {
		var version platform.SecretVersion
		if err := rows.Scan(&version.SecretID, &version.TeamID, &version.Version, &version.KeyID, &version.KeyVersion, &version.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan secret version: %w", err)
		}
		items = append(items, version)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate secret versions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit secret version list: %w", err)
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	last := items[limit-1]
	return items[:limit], &SecretVersionAfter{Version: last.Version, SecretID: last.SecretID}, nil
}

func (s *Store) GetSecretVersion(ctx context.Context, teamID, environmentID, secretID string, versionNumber int) (platform.SecretVersion, error) {
	if _, err := s.GetSecret(ctx, teamID, environmentID, secretID); err != nil {
		return platform.SecretVersion{}, err
	}
	var version platform.SecretVersion
	if err := s.db.QueryRowContext(ctx, `SELECT secret_id, team_id, version, key_id, key_version, created_at
		FROM secret_versions WHERE team_id = $1 AND secret_id = $2 AND version = $3`, teamID, secretID, versionNumber,
	).Scan(&version.SecretID, &version.TeamID, &version.Version, &version.KeyID, &version.KeyVersion, &version.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.SecretVersion{}, ErrEnvironmentUnavailable
		}
		return platform.SecretVersion{}, fmt.Errorf("get secret version: %w", err)
	}
	return version, nil
}

// RevokeSecret sets revoked=true on a same-team logical secret in one
// transaction. The (team_id, environment_id, secret_id) row is locked
// with FOR UPDATE so a concurrent create/replace cannot append a new
// version between the SELECT and the UPDATE. Immutability of
// secret_versions is preserved by the append-only trigger; only the
// logical secrets row is updated. The audit entry carries the
// resource identifier but no plaintext, ciphertext, nonce, tag, key
// material, or derived key bytes. A foreign (team_id, environment_id,
// secret_id) triple returns the same non-revealing 404 shape used
// for every other point resource.
func (s *Store) RevokeSecret(ctx context.Context, identity platform.GatewayIdentity, environmentID, secretID string) (platform.LogicalSecret, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.LogicalSecret{}, fmt.Errorf("begin secret revoke: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	environment, err := readEnvironmentForSecret(ctx, tx, identity.TeamID, environmentID)
	if err != nil {
		return platform.LogicalSecret{}, err
	}
	var secret platform.LogicalSecret
	if err := tx.QueryRowContext(ctx, `SELECT secret_id, team_id, environment_id, name,
		latest_version, revoked, created_at, updated_at FROM secrets
		WHERE team_id = $1 AND environment_id = $2 AND secret_id = $3 FOR UPDATE`,
		identity.TeamID, environmentID, secretID,
	).Scan(&secret.SecretID, &secret.TeamID, &secret.EnvironmentID, &secret.Name,
		&secret.LatestVersion, &secret.Revoked, &secret.CreatedAt, &secret.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return platform.LogicalSecret{}, ErrEnvironmentUnavailable
		}
		return platform.LogicalSecret{}, fmt.Errorf("lock logical secret for revoke: %w", err)
	}
	if !secret.Revoked {
		if _, err := tx.ExecContext(ctx, `UPDATE secrets SET revoked = true, updated_at = now()
			WHERE team_id = $1 AND environment_id = $2 AND secret_id = $3`,
			identity.TeamID, environmentID, secretID); err != nil {
			return platform.LogicalSecret{}, fmt.Errorf("revoke logical secret: %w", err)
		}
		secret.Revoked = true
		secret.UpdatedAt = time.Now().UTC()
		if err := appendSecretAudit(ctx, tx, identity, "secret.revoke", secretID); err != nil {
			return platform.LogicalSecret{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return platform.LogicalSecret{}, fmt.Errorf("commit secret revoke: %w", err)
	}
	secret.Scope = environment.Scope
	return secret, nil
}

func (s *Store) encryptSecret(teamID, secretID string, version int, plaintext string) ([]byte, []byte, []byte, error) {
	block, err := aes.NewCipher(s.aesKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize AES-GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, nil, fmt.Errorf("generate AES-GCM nonce: %w", err)
	}
	aad := []byte(fmt.Sprintf("%s\x00%s\x00%d", teamID, secretID, version))
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), aad)
	tagOffset := len(sealed) - gcm.Overhead()
	return append([]byte(nil), sealed[:tagOffset]...), nonce, append([]byte(nil), sealed[tagOffset:]...), nil
}

func insertSecretVersion(ctx context.Context, tx *sql.Tx, teamID, secretID string, versionNumber int, ciphertext, nonce, tag []byte) (platform.SecretVersion, error) {
	var version platform.SecretVersion
	if err := tx.QueryRowContext(ctx, `INSERT INTO secret_versions
		(secret_id, version, team_id, ciphertext, nonce, authentication_tag, key_id, key_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1)
		RETURNING secret_id, team_id, version, key_id, key_version, created_at`,
		secretID, versionNumber, teamID, ciphertext, nonce, tag, localKeyID,
	).Scan(&version.SecretID, &version.TeamID, &version.Version, &version.KeyID, &version.KeyVersion, &version.CreatedAt); err != nil {
		return platform.SecretVersion{}, fmt.Errorf("insert secret version: %w", err)
	}
	return version, nil
}

func readEnvironmentForSecret(ctx context.Context, tx *sql.Tx, teamID, environmentID string) (platform.Environment, error) {
	environment, err := scanEnvironment(tx.QueryRowContext(ctx, `SELECT environment_id, team_id, revision, name,
		project_id, task_id, parent_task_id, values, deleted, created_at, updated_at FROM environment_definitions
		WHERE team_id = $1 AND environment_id = $2 AND deleted = false FOR UPDATE`, teamID, environmentID))
	if errors.Is(err, sql.ErrNoRows) {
		return platform.Environment{}, ErrEnvironmentUnavailable
	}
	return environment, err
}

func sameEnvironmentScope(left, right platform.EnvironmentScope) bool {
	return sameStringPtr(left.ProjectID, right.ProjectID) && sameStringPtr(left.TaskID, right.TaskID) && sameStringPtr(left.ParentTaskID, right.ParentTaskID)
}

func sameStringPtr(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func appendSecretAudit(ctx context.Context, tx *sql.Tx, identity platform.GatewayIdentity, action, secretID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_entries
		(audit_id, team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome)
		VALUES ($1, $2, $3, 'operator', $4, 'secret', $5, $6, 'accepted')`,
		uuid.NewString(), identity.TeamID, identity.OperatorID, action, secretID, identity.RequestID)
	if err != nil {
		return fmt.Errorf("append secret audit: %w", err)
	}
	return nil
}
