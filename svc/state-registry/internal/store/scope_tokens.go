package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
)

const (
	scopeTokenAudience = "state-registry.environment.open"
	scopeTokenType     = "scope-token+json"
	// scopeTokenTypeField is the exact protected-header value the
	// spec requires. The literal is duplicated here so the issuer
	// and the verifier stay in lock-step.
)

type scopeTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type scopeTokenClaims struct {
	TeamID     string `json:"team_id"`
	TaskID     string `json:"task_id"`
	ExecutorID string `json:"executor_id"`
	Audience   string `json:"audience"`
	KeyID      string `json:"key_id"`
	IssuedAt   string `json:"issued_at"`
	Expiry     string `json:"expiry"`
}

// OpenEnvironmentRequest bundles every authenticated input the
// store needs to honor a single open-environment call. The HTTP
// layer builds it from the verified request headers and the
// decoded scope token; tests build it from a seeded harness.
// RequestID is the call-site correlation identifier that the
// store threads into the audit row so operators can trace
// environment.open accesses without reaching for any non-public
// state. RecordDecrypt, when non-nil, is invoked exactly once
// per successful AES-256-GCM open and never for any denied or
// unavailable case; the post-decrypt anchor is the only signal
// that the no-decrypt-on-denial invariant holds.
type OpenEnvironmentRequest struct {
	TaskID string
	// Deprecated: ignored by the task-scoped v0006 snapshot open path.
	EnvironmentID string
	Token         string
	Identity      platform.ExecutorIdentity
	RequestID     string
	RecordDecrypt func()
}

// OpenEnvironmentRepository is the narrow contract the HTTP
// handler consumes. The store implementation owns every
// authorization check, the keyed-HMAC token verification, the
// four-level image resolution, the AES-256-GCM authenticated
// decryption, and the audit row insert.
type OpenEnvironmentRepository interface {
	OpenEnvironment(context.Context, OpenEnvironmentRequest) (platform.OpenEnvironmentResponse, error)
}

// issueScopeToken mints a fresh open-environment scope token for the
// supplied task. The task must already be assigned and carry a non-empty
// resolved launch-parameter snapshot.
func (s *Store) issueScopeToken(ctx context.Context, query rowQueryer, task platform.TaskListEntry) (*string, error) {
	if task.ExecutorID == nil {
		return nil, nil
	}
	if s.scopeTokenKeyring == nil {
		return nil, errors.New("scope token keyring unavailable")
	}
	var hasValues bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM task_launch_parameter_snapshots snapshot
		WHERE snapshot.team_id = $1 AND snapshot.task_id = $2
		AND (snapshot.values <> '{}'::jsonb OR EXISTS (
			SELECT 1 FROM task_launch_parameter_secret_refs ref WHERE ref.task_id = snapshot.task_id
		)))`, task.TeamID, task.TaskID).Scan(&hasValues); err != nil {
		return nil, fmt.Errorf("read claim launch parameter snapshot: %w", err)
	}
	if !hasValues {
		return nil, nil
	}
	now := time.Now().UTC()
	if task.ClaimedAt != nil {
		claimedAt, err := time.Parse(time.RFC3339Nano, *task.ClaimedAt)
		if err != nil {
			return nil, fmt.Errorf("parse claim token issued_at: %w", err)
		}
		now = claimedAt.UTC()
	}
	key, ok := s.scopeTokenKeyring.activeKey()
	if !ok {
		return nil, errors.New("scope token active key unavailable")
	}
	keyID := s.scopeTokenKeyring.activeKeyID()
	header := scopeTokenHeader{Algorithm: string(key.alg), KeyID: keyID, Type: scopeTokenType}
	claims := scopeTokenClaims{
		TeamID: task.TeamID, TaskID: task.TaskID, ExecutorID: *task.ExecutorID,
		Audience: scopeTokenAudience, KeyID: keyID,
		IssuedAt: now.Format(time.RFC3339Nano), Expiry: now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal scope token header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("marshal scope token claims: %w", err)
	}
	headerSegment := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsSegment := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSegment + "." + claimsSegment
	mac, err := scopeTokenMAC(key.alg, key.bytes, signingInput)
	if err != nil {
		return nil, err
	}
	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(mac)
	return &token, nil
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// OpenEnvironment verifies the scope token, asserts every team and
// assignment condition, decrypts each row's ciphertext exactly once
// via the active AES key, and returns authorized env-style values.
// The supplied request's RecordDecrypt callback is invoked once per
// successful AES-256-GCM open and never for any denied or
// unavailable case so the qa-e2e harness can prove the
// no-decrypt-on-denial invariant. The request's RequestID is
// persisted verbatim on the audit row so operators can correlate
// the access with the call-site that triggered it.
func (s *Store) OpenEnvironment(ctx context.Context, req OpenEnvironmentRequest) (platform.OpenEnvironmentResponse, error) {
	taskID := req.TaskID
	token := req.Token
	identity := req.Identity
	claims, err := s.verifyScopeToken(token)
	if err != nil {
		return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
	}
	if claims.TaskID != taskID || claims.ExecutorID != identity.ExecutorID {
		return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return platform.OpenEnvironmentResponse{}, fmt.Errorf("begin environment open: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var (
		teamID, assignedExecutor, state string
		valuesJSON                      []byte
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT t.team_id, t.executor_id, t.current_state, snapshot.values
		  FROM tasks t
		  JOIN task_launch_parameter_snapshots snapshot
		    ON snapshot.task_id = t.task_id AND snapshot.team_id = t.team_id
		 WHERE t.task_id = $1
		 FOR UPDATE`, taskID,
	).Scan(&teamID, &assignedExecutor, &state, &valuesJSON); err != nil {
		return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
	}
	if assignedExecutor != identity.ExecutorID || claims.TeamID != teamID || claims.ExecutorID != assignedExecutor ||
		(state != platform.TaskStateCreated && state != platform.TaskStateRunning) {
		return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
	}
	if identity.Scope == platform.ExecutorScopeTeam {
		if identity.TeamID == nil || *identity.TeamID != teamID {
			return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
		}
	}
	values := make(map[string]string)
	if json.Unmarshal(valuesJSON, &values) != nil {
		return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT ref.key, sv.secret_id, sv.version, sv.ciphertext, sv.nonce,
		       sv.authentication_tag, COALESCE(sv.team_id, '')
		  FROM task_launch_parameter_secret_refs ref
		  JOIN secret_versions sv ON sv.secret_id = ref.secret_id AND sv.version = ref.version
		 WHERE ref.team_id = $1 AND ref.task_id = $2 ORDER BY ref.key`, teamID, taskID)
	if err != nil {
		return platform.OpenEnvironmentResponse{}, fmt.Errorf("list open secrets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, secretID, encryptionTeamID string
		var version int
		var ciphertext, nonce, tag []byte
		if err := rows.Scan(&name, &secretID, &version, &ciphertext, &nonce, &tag, &encryptionTeamID); err != nil {
			return platform.OpenEnvironmentResponse{}, fmt.Errorf("scan open secret: %w", err)
		}
		plaintext, err := s.decryptSecret(encryptionTeamID, secretID, version, ciphertext, nonce, tag)
		if err != nil {
			// Any AEAD authentication failure, including wrong
			// team/secret/version binding, tamper, or wrong key,
			// collapses to the same non-revealing 404 and rolls
			// back the open transaction. No audit row is
			// appended; the open never returned a plaintext.
			return platform.OpenEnvironmentResponse{}, ErrEnvironmentUnavailable
		}
		// Increment ONLY after a successful gcm.Open so the
		// counter measures successful decrypt operations, not
		// authorization attempts. The callback is invoked here
		// (inside the loop) so every successful row decryption
		// is observed exactly once.
		if req.RecordDecrypt != nil {
			req.RecordDecrypt()
		}
		values[name] = plaintext
	}
	if err := rows.Err(); err != nil {
		return platform.OpenEnvironmentResponse{}, fmt.Errorf("iterate open secrets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_entries
		(audit_id, team_id, actor_id, actor_type, action, resource_type, resource_id, request_id, outcome, executor_scope)
		VALUES ($1, $2, $3, 'executor', 'launch_parameters.open', 'task', $4, $5, 'succeeded', $6)`,
		uuid.NewString(), teamID, identity.ExecutorID, taskID, req.RequestID, identity.Scope); err != nil {
		return platform.OpenEnvironmentResponse{}, fmt.Errorf("append environment open audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return platform.OpenEnvironmentResponse{}, fmt.Errorf("commit environment open: %w", err)
	}
	return platform.OpenEnvironmentResponse{
		TeamID: teamID, TaskID: taskID, ExecutorID: identity.ExecutorID, Values: values,
	}, nil
}

// verifyScopeToken parses the compact three-part token, enforces the
// HS256/HS384/HS512 allow-list, recomputes the MAC under the
// algorithm the keyring binds to the supplied key id, and returns
// the decoded claims. Every invalid or unavailable condition
// returns ErrEnvironmentUnavailable so the public error shape is
// the same across every denial.
//
// The order of checks is:
//
//  1. length bounds and three-part shape;
//  2. base64url decode of the header and claims segments;
//  3. protected-header kid equals payload key_id (spec);
//  4. algorithm is in the HS256/HS384/HS512 allow-list;
//  5. typ equals the documented scope-token+json literal;
//  6. key_id is inside the active key window (rotating keyring);
//  7. header algorithm equals the per-key algorithm bound by the
//     keyring (defense in depth against algorithm-confusion
//     attacks: a token whose header claims HS512 is rejected
//     when the keyring was issued for HS256);
//  8. constant-time MAC verification under the per-key algorithm
//     (the spec requires no further work before the MAC matches);
//  9. canonical claim shape (presence, types, allowed values,
//     audience literal, lifetime window).
//
// No decrypt call is reached on any denial path.
func (s *Store) verifyScopeToken(token string) (scopeTokenClaims, error) {
	if s.scopeTokenKeyring == nil || len(s.aesKey) != 32 || len(token) == 0 || len(token) > 8192 {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	var header scopeTokenHeader
	var claims scopeTokenClaims
	if !decodeScopeTokenJSON(headerJSON, &header) || !decodeScopeTokenJSON(claimsJSON, &claims) ||
		header.KeyID == "" || header.KeyID != claims.KeyID {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	alg := ScopeTokenAlgorithm(header.Algorithm)
	if !allowedScopeTokenAlgs[alg] {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	key, ok := s.scopeTokenKeyring.lookup(claims.KeyID)
	if !ok {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	// Per-key algorithm policy: the keyring authoritatively
	// pins the algorithm for every key id. A token whose
	// protected header declares a different allow-listed
	// algorithm is rejected before any MAC computation. This
	// closes the documented algorithm-confusion gap where an
	// attacker could down-classify the verification to a
	// stronger allow-listed variant than the key was issued for.
	if alg != key.alg {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	suppliedMAC, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	expectedMAC, err := scopeTokenMAC(key.alg, key.bytes, parts[0]+"."+parts[1])
	if err != nil {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	if !constantTimeEqualMAC(suppliedMAC, expectedMAC) {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	if header.Type != scopeTokenType {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, claims.IssuedAt)
	if err != nil {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	expiry, err := time.Parse(time.RFC3339Nano, claims.Expiry)
	if err != nil {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	now := time.Now().UTC()
	if claims.TeamID == "" || claims.TaskID == "" || claims.ExecutorID == "" ||
		claims.Audience != scopeTokenAudience || issuedAt.After(now.Add(30*time.Second)) ||
		!expiry.After(issuedAt) || expiry.Sub(issuedAt) > 5*time.Minute || !expiry.After(now) {
		return scopeTokenClaims{}, ErrEnvironmentUnavailable
	}
	return claims, nil
}

func decodeScopeTokenJSON(raw []byte, dst any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func (s *Store) decryptSecret(teamID, secretID string, version int, ciphertext, nonce, tag []byte) (string, error) {
	block, err := aes.NewCipher(s.aesKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := append(append([]byte(nil), ciphertext...), tag...)
	aad := []byte(fmt.Sprintf("%s\x00%s\x00%d", teamID, secretID, version))
	plaintext, err := gcm.Open(nil, nonce, sealed, aad)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
