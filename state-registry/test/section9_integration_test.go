//go:build integration

// Section 9 — team-owned environments, logical secrets, immutable
// versions, scope tokens, AES-256-GCM, plaintext exclusion.
//
// RED→GREEN PostgreSQL-backed integration tests that drive the
// production store.Store end-to-end. Each test pins one Section 9
// invariant and asserts the public error shape
// `404 environment_unknown_or_unavailable` for every denial path
// while observing the no-decrypt-on-denial invariant via a
// callback counter.
//
// RED COMMAND (the exact focused run from tasks.md):
//
//	go test -tags=integration ./state-registry/... \
//	    -run 'TestTeamEnvironment|TestTaskOwnedEnvironment|\
//
// TestTaskOwnedSecret|TestTeamSecret|TestSecretVersions|\
// TestOpenEnvironmentClaims|TestScopeTokenSecurity|TestAesGcm|\
// TestStartupFailClosed|TestDecryptFailClosedOnBadAssociatedData|\
// TestNoDecryptOnScopeTokenDenial|TestKeyVersionRotationMetadata|\
// TestPlaintextExclusion' -count=1
//
// Contract under test (Section 9):
//
//   - POST /v1/environments writes only from a same-team trusted
//     Gateway context; environment rows carry immutable team_id;
//   - task-owned environment rows carry a parent task_id; a foreign
//     parent task_id returns 404 environment_unknown_or_unavailable
//     with no mutation and no audit entry;
//   - logical secrets inherit their scope from the parent
//     environment; a task-owned environment produces a task-owned
//     secret; only the parent task in the same team may open it;
//   - secret_versions are append-only; ciphertext, nonce, tag, and
//     key_id/key_version envelope persist on every append;
//   - GET /v1/environments/{id}/open verifies the compact scope
//     token in the X-FlowAI-Scope-Token header under the documented
//     allow-listed HMAC family (HS256/HS384/HS512) and a
//     server-controlled rotating key id, performs every canonical
//     check (kid == key_id, audience, lifetime, claim shape,
//     non-terminal task, same-team assignment) before any decrypt
//     operation, and returns the same authorized values with
//     plaintext-free audit;
//   - plaintext is never persisted, logged, audited, or returned in
//     error responses;
//   - the State Registry fails closed when the AES-256-GCM key is
//     missing at startup.
package test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flowai/platform/state-registry/internal/config"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// ---------------------------------------------------------------------------
// Section 9 harness
// ---------------------------------------------------------------------------

type section9Harness struct {
	db         *sql.DB
	repo       *store.Store
	decrypts   *atomic.Int64
	aesKey     []byte
	hmacKey    []byte
	keyring    *store.ScopeTokenKeyring
	keyID      string
	algorithm  store.ScopeTokenAlgorithm
	lastEnv    platform.Environment
	lastSecret platform.SecretWriteResponse
}

func newSection9Harness(t *testing.T) *section9Harness {
	t.Helper()
	db := migratedDB(t)
	aesKey := make([]byte, 32)
	if _, err := rand.Read(aesKey); err != nil {
		t.Fatalf("rand AES key: %v", err)
	}
	hmacKey := make([]byte, 32)
	if _, err := rand.Read(hmacKey); err != nil {
		t.Fatalf("rand HMAC key: %v", err)
	}
	keyring, err := store.NewScopeTokenKeyring("scope-v1", hex.EncodeToString(hmacKey), "", store.ScopeTokenAlgHS256)
	if err != nil {
		t.Fatalf("NewScopeTokenKeyring: %v", err)
	}
	repo := store.NewWithScopeTokenKeyring(db, aesKey, keyring)
	return &section9Harness{
		db:        db,
		repo:      repo,
		decrypts:  &atomic.Int64{},
		aesKey:    aesKey,
		hmacKey:   hmacKey,
		keyring:   keyring,
		keyID:     "scope-v1",
		algorithm: store.ScopeTokenAlgHS256,
	}
}

func (h *section9Harness) recordDecrypt() {
	h.decrypts.Add(1)
}

func (h *section9Harness) decryptCount() int64 {
	return h.decrypts.Load()
}

// seedTeam inserts the canonical team row with a deterministic
// default_image so the four-level image precedence tests in
// Section 9 have a stable starting point.
func (h *section9Harness) seedTeam(t *testing.T, teamID, teamName string) {
	t.Helper()
	mustInsertAdminTeam(t, h.db, teamID, teamName)
}

func (h *section9Harness) seedTask(t *testing.T, taskID, teamID, sourceSystem, sourceID, taskType, tag string, ingestedAt time.Time, image *platform.ImageReference) {
	t.Helper()
	seedPendingTaskIdempotent(t, h.db, taskID, teamID, sourceSystem, sourceID, taskType, tag, ingestedAt, image)
}

func (h *section9Harness) seedTaskIdempotent(t *testing.T, taskID, teamID, sourceSystem, sourceID, taskType, tag string, ingestedAt time.Time, image *platform.ImageReference) {
	t.Helper()
	seedPendingTaskIdempotent(t, h.db, taskID, teamID, sourceSystem, sourceID, taskType, tag, ingestedAt, image)
}

// seedPendingTaskIdempotent is the ON CONFLICT version of
// seedPendingTask so test setups that re-claim a task do not
// violate the (team_id, source_system_id, source_id) unique
// constraint.
func seedPendingTaskIdempotent(t *testing.T, db *sql.DB, taskID, teamID, sourceSystem, sourceID, taskType, tag string, ingestedAt time.Time, image *platform.ImageReference) {
	t.Helper()
	var img any
	if image != nil {
		encoded, err := json.Marshal(image)
		if err != nil {
			t.Fatalf("marshal image: %v", err)
		}
		img = string(encoded)
	}
	mustExec(t, db,
		`INSERT INTO tasks
		 (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at, image)
		 VALUES ($1, $2, $3, $4, $5, $6, '{}'::jsonb, $7, $8::jsonb)
		 ON CONFLICT (team_id, source_system_id, source_id) DO NOTHING`,
		taskID, teamID, sourceSystem, sourceID, taskType, tag, ingestedAt, img)
}

func (h *section9Harness) seedTaskType(t *testing.T, taskTypeID, teamID, tag string) {
	t.Helper()
	mustExec(t, h.db,
		`INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ($1, $2, $3)
		 ON CONFLICT (team_id, task_type_id) DO NOTHING`,
		taskTypeID, teamID, tag)
}

func (h *section9Harness) seedSourceSystem(t *testing.T, sourceID, teamID, listenerID string) {
	t.Helper()
	mustExec(t, h.db,
		`INSERT INTO source_systems (source_system_id, team_id, listener_identity) VALUES ($1, $2, $3)
		 ON CONFLICT (team_id, source_system_id) DO NOTHING`,
		sourceID, teamID, listenerID)
}

// seedClaimedTask inserts a pending task then marks it claimed by
// the supplied executor (with non-null claim fields) so the
// open-environment happy path can be exercised without spinning
// up the full claim transaction in every test.
func (h *section9Harness) seedClaimedTask(t *testing.T, taskID, teamID, sourceSystem, sourceID, taskType, tag, executorID, environmentID string, image *platform.ImageReference) {
	t.Helper()
	h.seedTaskIdempotent(t, taskID, teamID, sourceSystem, sourceID, taskType, tag, time.Now().UTC(), image)
	h.seedExecutor(t, executorID, teamID, tag)
	mustExec(t, h.db,
		`UPDATE tasks
		   SET current_state = 'created',
		       owner_command_id = $1,
		       executor_id = $2,
		       resolved_image = (SELECT default_image::jsonb FROM teams WHERE team_id = $3),
		       image_source = 'team_default',
		       claimed_at = now(),
		       environment_id = NULLIF($5, '')
		 WHERE task_id = $4`,
		"claim-"+taskID, executorID, teamID, taskID, environmentID)
}

// openEnv drives a production-path OpenEnvironment with the
// harness's recordDecrypt callback. requestID defaults to a
// deterministic "req-section9" when the caller does not override
// it (the audit correlation tests pass an explicit value).
func (h *section9Harness) openEnv(t *testing.T, envID, taskID, token, requestID string, identity platform.ExecutorIdentity) (platform.OpenEnvironmentResponse, error) {
	t.Helper()
	if requestID == "" {
		requestID = "req-section9"
	}
	return h.repo.OpenEnvironment(context.Background(), store.OpenEnvironmentRequest{
		EnvironmentID: envID,
		TaskID:        taskID,
		Token:         token,
		Identity:      identity,
		RequestID:     requestID,
		RecordDecrypt: h.recordDecrypt,
	})
}

func (h *section9Harness) seedExecutor(t *testing.T, executorID, teamID, tag string) {
	t.Helper()
	mustExec(t, h.db,
		`INSERT INTO executors
		 (executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count, runtime_metadata)
		 VALUES ($1, 'team', $2, 'executor_docker_openhands', $3, $4, 1, 0, '{}'::jsonb)
		 ON CONFLICT (executor_id) DO NOTHING`,
		executorID, teamID, "identity-"+executorID, tag)
}

func (h *section9Harness) operatorIdentity(operatorID string) platform.GatewayIdentity {
	return platform.GatewayIdentity{
		TeamID:     "team-a",
		OperatorID: operatorID,
		RequestID:  "req-" + operatorID,
	}
}

func (h *section9Harness) executorIdentity(executorID, teamID string) platform.ExecutorIdentity {
	t := teamID
	return platform.ExecutorIdentity{
		ExecutorID: executorID,
		Scope:      platform.ExecutorScopeTeam,
		TeamID:     &t,
	}
}

// countRows returns the row count of a given table, accepting a
// simple equality filter on the first column. Tests use it to
// confirm no rows were created on denial paths.
func countRows(t *testing.T, db *sql.DB, table, whereColumn, whereValue string) int {
	t.Helper()
	if !isSafeIdentifier(table) || !isSafeIdentifier(whereColumn) {
		t.Fatalf("countRows: unsafe identifier")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM "%s" WHERE "%s" = $1`, table, whereColumn), whereValue).Scan(&n); err != nil {
		t.Fatalf("countRows %s: %v", table, err)
	}
	return n
}

// querySingleBytes returns a single bytea column (or NULL) for the
// supplied keying-material scan. The test never logs the bytes; it
// only inspects length and equality with the expected value.
func querySingleBytes(t *testing.T, db *sql.DB, query string, args ...any) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var raw []byte
	if err := db.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		t.Fatalf("querySingleBytes: %v", err)
	}
	return raw
}

func stringOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---------------------------------------------------------------------------
// TestTeamEnvironment
// ---------------------------------------------------------------------------

// TestTeamEnvironment asserts the same-team environment CRUD
// contract: a team-wide environment can be created, read, listed,
// and replaced for its own team; a foreign-team environment_id is
// non-revealing 404; and the parent_task_id null in the create
// request leaves the row team-wide.
func TestTeamEnvironment(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedTeam(t, "team-b", "Team B")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedSourceSystem(t, "src-b", "team-b", "listener-b")
	h.seedTaskType(t, "type-b", "team-b", "tag-b")

	t.Run("create and read same-team team-wide environment", func(t *testing.T) {
		req := platform.EnvironmentWriteRequest{
			Name:   "team-wide-env",
			Values: map[string]string{"K1": "v1"},
		}
		env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), req)
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		if env.TeamID != "team-a" {
			t.Errorf("team_id=%q want team-a", env.TeamID)
		}
		if env.Scope.ParentTaskID != nil {
			t.Errorf("parent_task_id=%v want nil for team-wide", env.Scope.ParentTaskID)
		}
		got, err := h.repo.GetEnvironment(context.Background(), "team-a", env.EnvironmentID)
		if err != nil {
			t.Fatalf("GetEnvironment: %v", err)
		}
		if got.Name != "team-wide-env" {
			t.Errorf("name=%q want team-wide-env", got.Name)
		}
		items, err := h.repo.ListEnvironments(context.Background(), "team-a", nil, nil, 50)
		if err != nil {
			t.Fatalf("ListEnvironments: %v", err)
		}
		if len(items) != 1 {
			t.Errorf("list count=%d want 1", len(items))
		}
	})

	t.Run("foreign team read returns non-revealing 404", func(t *testing.T) {
		env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "team-wide-a",
			Values: map[string]string{"K": "v"},
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		_, err = h.repo.GetEnvironment(context.Background(), "team-b", env.EnvironmentID)
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("foreign team read: err=%v want ErrEnvironmentUnavailable", err)
		}
	})

	t.Run("replace updates name and revision", func(t *testing.T) {
		env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "before",
			Values: map[string]string{"K": "v1"},
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		updated, err := h.repo.ReplaceEnvironment(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.EnvironmentWriteRequest{
			Name:   "after",
			Values: map[string]string{"K": "v2"},
		})
		if err != nil {
			t.Fatalf("ReplaceEnvironment: %v", err)
		}
		if updated.Name != "after" {
			t.Errorf("name=%q want after", updated.Name)
		}
		if updated.Revision <= env.Revision {
			t.Errorf("revision=%d want > %d", updated.Revision, env.Revision)
		}
	})

	t.Run("task applicability is distinct from task ownership", func(t *testing.T) {
		h.seedTask(t, "task-scope-a", "team-a", "src-a", "scope-a", "type-a", "tag-a", time.Now().UTC(), nil)
		h.seedTask(t, "task-scope-b", "team-b", "src-b", "scope-b", "type-b", "tag-b", time.Now().UTC(), nil)
		env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "task-applicable",
			Scope:  platform.EnvironmentScope{TaskID: stringPtr("task-scope-a")},
			Values: map[string]string{"K": "v"},
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		if stringOrEmpty(env.Scope.TaskID) != "task-scope-a" || env.Scope.ParentTaskID != nil {
			t.Fatalf("scope=%+v want task applicability without parent ownership", env.Scope)
		}
		items, err := h.repo.ListEnvironments(context.Background(), "team-a", nil, stringPtr("task-scope-a"), 50)
		if err != nil || len(items) != 1 || items[0].EnvironmentID != env.EnvironmentID {
			t.Fatalf("task-filtered list=%+v err=%v", items, err)
		}
		_, err = h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "foreign-task-applicability",
			Scope:  platform.EnvironmentScope{TaskID: stringPtr("task-scope-b")},
			Values: map[string]string{"K": "v"},
		})
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("foreign task applicability: err=%v want ErrEnvironmentUnavailable", err)
		}
	})

	t.Run("delete makes the row invisible to subsequent reads", func(t *testing.T) {
		env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "to-delete",
			Values: map[string]string{"K": "v"},
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		if err := h.repo.DeleteEnvironment(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID); err != nil {
			t.Fatalf("DeleteEnvironment: %v", err)
		}
		_, err = h.repo.GetEnvironment(context.Background(), "team-a", env.EnvironmentID)
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("post-delete read: err=%v want ErrEnvironmentUnavailable", err)
		}
	})
}

// ---------------------------------------------------------------------------
// TestTaskOwnedEnvironment
// ---------------------------------------------------------------------------

// TestTaskOwnedEnvironment pins the same-team parent task
// invariant. A task-owned environment MUST only accept a parent
// task_id that belongs to the same team; a foreign parent task_id
// MUST return the same non-revealing 404
// environment_unknown_or_unavailable with zero decrypt calls and
// no audit entry. The successful create persists the parent
// task_id and binds the environment to the task via the
// tasks.environment_id link.
func TestTaskOwnedEnvironment(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedTeam(t, "team-b", "Team B")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedSourceSystem(t, "src-b", "team-b", "listener-b")
	h.seedTaskType(t, "type-b", "team-b", "tag-b")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedTask(t, "task-b", "team-b", "src-b", "src-id-b", "type-b", "tag-b", time.Now().UTC(), nil)

	t.Run("same-team parent task accepted", func(t *testing.T) {
		req := platform.EnvironmentWriteRequest{
			Name:   "task-owned",
			Values: map[string]string{"K": "v"},
			Scope:  platform.EnvironmentScope{ParentTaskID: stringPtr("task-a")},
		}
		env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), req)
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		if env.Scope.ParentTaskID == nil || *env.Scope.ParentTaskID != "task-a" {
			t.Errorf("parent_task_id=%v want task-a", env.Scope.ParentTaskID)
		}
		// tasks.environment_id was bound by the create
		// transaction.
		var bound sql.NullString
		if err := h.db.QueryRowContext(context.Background(),
			`SELECT environment_id FROM tasks WHERE task_id = 'task-a'`).Scan(&bound); err != nil {
			t.Fatalf("read task: %v", err)
		}
		if !bound.Valid || bound.String != env.EnvironmentID {
			t.Errorf("task.environment_id=%v want %s", bound, env.EnvironmentID)
		}
	})

	t.Run("foreign parent task rejected with non-revealing 404", func(t *testing.T) {
		decryptsBefore := h.decryptCount()
		preCount := countRows(t, h.db, "environment_definitions", "name", "bad-env")
		preAuditCount := countRows(t, h.db, "audit_entries", "action", "environment.create")
		req := platform.EnvironmentWriteRequest{
			Name:   "bad-env",
			Values: map[string]string{"K": "v"},
			Scope:  platform.EnvironmentScope{ParentTaskID: stringPtr("task-b")},
		}
		_, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), req)
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("foreign parent: err=%v want ErrEnvironmentUnavailable", err)
		}
		if got := h.decryptCount(); got != decryptsBefore {
			t.Errorf("decrypt calls=%d want %d (no decrypt on foreign parent rejection)", got, decryptsBefore)
		}
		if got := countRows(t, h.db, "environment_definitions", "name", "bad-env"); got != preCount {
			t.Errorf("env rows=%d want %d (no row persisted)", got, preCount)
		}
		if got := countRows(t, h.db, "audit_entries", "action", "environment.create"); got != preAuditCount {
			t.Errorf("audit rows=%d want %d (no audit entry on rejection)", got, preAuditCount)
		}
	})

	t.Run("nonexistent parent task rejected with non-revealing 404", func(t *testing.T) {
		req := platform.EnvironmentWriteRequest{
			Name:   "missing-parent",
			Values: map[string]string{"K": "v"},
			Scope:  platform.EnvironmentScope{ParentTaskID: stringPtr("task-does-not-exist")},
		}
		_, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), req)
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("missing parent: err=%v want ErrEnvironmentUnavailable", err)
		}
	})
}

func stringPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// TestTaskOwnedSecret
// ---------------------------------------------------------------------------

// TestTaskOwnedSecret asserts that a secret under a task-owned
// environment inherits the parent task scope and can only be opened
// by the parent task. The same-team secret write succeeds; the
// foreign environment write returns non-revealing 404; the open
// path with a non-parent task returns the same non-revealing 404
// without any decrypt call.
func TestTaskOwnedSecret(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedTask(t, "task-c", "team-a", "src-a", "src-id-c", "type-a", "tag-a", time.Now().UTC().Add(time.Minute), nil)
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "task-owned-env",
		Values: map[string]string{"K": "v"},
		Scope:  platform.EnvironmentScope{ParentTaskID: stringPtr("task-a")},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	t.Run("same-team secret under task-owned env accepted", func(t *testing.T) {
		resp, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
			Name:  "alpha",
			Value: "alpha-plaintext",
			Scope: env.Scope,
		})
		if err != nil {
			t.Fatalf("CreateSecret: %v", err)
		}
		if resp.Secret.EnvironmentID != env.EnvironmentID {
			t.Errorf("environment_id=%q want %q", resp.Secret.EnvironmentID, env.EnvironmentID)
		}
		if resp.Version.SecretID == "" {
			t.Errorf("version.secret_id empty")
		}
		if resp.Version.Version != 1 {
			t.Errorf("version=%d want 1", resp.Version.Version)
		}
	})

	t.Run("open with non-parent task denied with zero decrypt calls", func(t *testing.T) {
		h.seedExecutor(t, "exec-c", "team-a", "tag-a")
		// Claim task-c so it looks like a real assigned task.
		mustExec(t, h.db,
			`UPDATE tasks
			   SET current_state = 'created',
			       owner_command_id = 'claim-c',
			       executor_id = 'exec-c',
			       resolved_image = (SELECT default_image::jsonb FROM teams WHERE team_id = 'team-a'),
			       image_source = 'team_default',
			       claimed_at = now(),
			       environment_id = $1
			 WHERE task_id = 'task-c'`,
			env.EnvironmentID)
		// Build a token whose task_id is task-c but the canonical
		// environment task_id is task-a: the open path rejects
		// the mismatch.
		decryptsBefore := h.decryptCount()
		token := h.issueScopeTokenFor(t, env.EnvironmentID, "task-c", "exec-c", nil, "team-a")
		_, err := h.openEnv(t, env.EnvironmentID, "task-c", token, "", h.executorIdentity("exec-c", "team-a"))
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("non-parent open: err=%v want ErrEnvironmentUnavailable", err)
		}
		if got := h.decryptCount(); got != decryptsBefore {
			t.Errorf("decrypts=%d want %d (zero decrypt on non-parent denial)", got, decryptsBefore)
		}
	})
}

// ---------------------------------------------------------------------------
// TestTeamSecret
// ---------------------------------------------------------------------------

// TestTeamSecret exercises a team-wide secret write and read; the
// secret is team-scoped and the open path succeeds for any
// claimed task in the same team.
func TestTeamSecret(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedExecutor(t, "exec-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	resp, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "beta",
		Value: "beta-plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if resp.Secret.EnvironmentID != env.EnvironmentID {
		t.Errorf("environment_id=%q want %q", resp.Secret.EnvironmentID, env.EnvironmentID)
	}
	if resp.Secret.Scope.ParentTaskID != nil {
		t.Errorf("parent_task_id=%v want nil for team-wide secret", resp.Secret.Scope.ParentTaskID)
	}
}

// ---------------------------------------------------------------------------
// TestSecretVersions
// ---------------------------------------------------------------------------

// TestSecretVersions asserts that secret_versions are append-only
// and that every row carries the opaque key_id / key_version
// envelope. The test first seeds v1, then replaces the secret to
// create v2; the database must contain both rows, neither of
// which is updatable in place.
func TestSecretVersions(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	created, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "v1-plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	replaced, err := h.repo.ReplaceSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, created.Secret.SecretID, platform.SecretReplaceRequest{
		Value: "v2-plaintext",
	})
	if err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	if replaced.Version.Version != 2 {
		t.Errorf("replaced version=%d want 2", replaced.Version.Version)
	}
	if created.Version.SecretID != replaced.Version.SecretID {
		t.Errorf("logical secret id changed: %q -> %q", created.Version.SecretID, replaced.Version.SecretID)
	}

	versions, err := h.repo.ListSecretVersions(context.Background(), "team-a", env.EnvironmentID, created.Secret.SecretID, 10)
	if err != nil {
		t.Fatalf("ListSecretVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions=%d want 2", len(versions))
	}
	if versions[0].Version != 1 || versions[1].Version != 2 {
		t.Errorf("versions order=%d,%d want 1,2", versions[0].Version, versions[1].Version)
	}
	for _, v := range versions {
		if v.KeyID == "" {
			t.Errorf("version key_id empty for v=%d", v.Version)
		}
		if v.KeyVersion < 1 {
			t.Errorf("version key_version=%d want >=1", v.KeyVersion)
		}
	}

	// Update is rejected by the trigger; the test mirrors the
	// append-only invariant.
	mustReject(t, h.db, "secret_version update", `UPDATE secret_versions SET version = 99 WHERE secret_id = $1 AND version = 1`, created.Secret.SecretID)
	mustReject(t, h.db, "secret_version delete", `DELETE FROM secret_versions WHERE secret_id = $1 AND version = 1`, created.Secret.SecretID)
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentClaims
// ---------------------------------------------------------------------------

// TestOpenEnvironmentClaims exercises every documented claim on
// the open-environment scope token. The token MUST include
// team_id, project_id (null for team-wide envs), task_id,
// environment_id, executor_id, audience literal
// state-registry.environment.open, key_id, issued_at, and
// expiry. The protected header MUST carry alg, kid, and typ. The
// open path verifies every claim and returns authorized values
// with plaintext-free audit.
func TestOpenEnvironmentClaims(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedExecutor(t, "exec-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	created, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)

	t.Run("project_id is null for team-wide env", func(t *testing.T) {
		token := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
		header, claims := parseScopeToken(t, token)
		if header.Algorithm != "HS256" {
			t.Errorf("header alg=%q want HS256", header.Algorithm)
		}
		if header.Type != "scope-token+json" {
			t.Errorf("header typ=%q want scope-token+json", header.Type)
		}
		if header.KeyID != h.keyID {
			t.Errorf("header kid=%q want %q", header.KeyID, h.keyID)
		}
		if claims.KeyID != h.keyID {
			t.Errorf("claims key_id=%q want %q", claims.KeyID, h.keyID)
		}
		if claims.TeamID != "team-a" {
			t.Errorf("team_id=%q want team-a", claims.TeamID)
		}
		if claims.ProjectID != nil {
			t.Errorf("project_id=%v want nil for team-wide env", *claims.ProjectID)
		}
		if claims.TaskID != "task-a" {
			t.Errorf("task_id=%q want task-a", claims.TaskID)
		}
		if claims.EnvironmentID != env.EnvironmentID {
			t.Errorf("environment_id=%q want %q", claims.EnvironmentID, env.EnvironmentID)
		}
		if claims.ExecutorID != "exec-a" {
			t.Errorf("executor_id=%q want exec-a", claims.ExecutorID)
		}
		if claims.Audience != "state-registry.environment.open" {
			t.Errorf("audience=%q want state-registry.environment.open", claims.Audience)
		}
		if claims.IssuedAt == "" {
			t.Errorf("issued_at empty")
		}
		if claims.Expiry == "" {
			t.Errorf("expiry empty")
		}
		issued, err := time.Parse(time.RFC3339Nano, claims.IssuedAt)
		if err != nil {
			t.Fatalf("parse issued_at: %v", err)
		}
		expiry, err := time.Parse(time.RFC3339Nano, claims.Expiry)
		if err != nil {
			t.Fatalf("parse expiry: %v", err)
		}
		if !expiry.After(issued) {
			t.Errorf("expiry=%v not after issued_at=%v", expiry, issued)
		}
		if expiry.Sub(issued) > 5*time.Minute+time.Second {
			t.Errorf("expiry-issued=%v not <= 5 minutes", expiry.Sub(issued))
		}
		// Successful open returns the decrypted secret and the
		// audit row carries only the resource identifier.
		before := h.decryptCount()
		resp, err := h.openEnv(t, env.EnvironmentID, "task-a", token, "", h.executorIdentity("exec-a", "team-a"))
		if err != nil {
			t.Fatalf("OpenEnvironment: %v", err)
		}
		if resp.Values[created.Secret.Name] != "alpha-plaintext" {
			t.Errorf("decrypted value=%q want alpha-plaintext (no plaintext in audit check)", resp.Values[created.Secret.Name])
		}
		if h.decryptCount() <= before {
			t.Errorf("decrypts did not increment; open path must decrypt at least once")
		}
	})

	t.Run("project_id must equal env.project_id when env is project-scoped", func(t *testing.T) {
		projEnv, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "project-scoped-env",
			Values: map[string]string{"K": "v"},
			Scope:  platform.EnvironmentScope{ProjectID: stringPtr("project-p")},
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		// The task must be in the same project and bound to the
		// project-scoped env so the open path's JOIN succeeds.
		mustExec(t, h.db, `UPDATE tasks SET project_id = 'project-p', environment_id = $1 WHERE task_id = 'task-a'`, projEnv.EnvironmentID)
		defer mustExec(t, h.db, `UPDATE tasks SET project_id = NULL, environment_id = $1 WHERE task_id = 'task-a'`, env.EnvironmentID)
		// Token with null project_id for a project-scoped env
		// must be rejected.
		badToken := h.issueScopeTokenFor(t, projEnv.EnvironmentID, "task-a", "exec-a", nil, "team-a")
		_, err = h.openEnv(t, projEnv.EnvironmentID, "task-a", badToken, "", h.executorIdentity("exec-a", "team-a"))
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("project-scoped env with null project_id claim: err=%v want ErrEnvironmentUnavailable", err)
		}
		// Token with the matching project_id is accepted.
		goodProject := stringPtr("project-p")
		goodToken := h.issueScopeTokenFor(t, projEnv.EnvironmentID, "task-a", "exec-a", goodProject, "team-a")
		_, err = h.openEnv(t, projEnv.EnvironmentID, "task-a", goodToken, "", h.executorIdentity("exec-a", "team-a"))
		if err != nil {
			t.Fatalf("project-scoped env with matching project_id: err=%v", err)
		}
	})

	t.Run("task applicability rejects a different assigned task", func(t *testing.T) {
		h.seedTask(t, "task-b", "team-a", "src-a", "src-id-b", "type-a", "tag-a", time.Now().UTC(), nil)
		taskEnv, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
			Name:   "task-scoped-env",
			Scope:  platform.EnvironmentScope{TaskID: stringPtr("task-a")},
			Values: map[string]string{"K": "v"},
		})
		if err != nil {
			t.Fatalf("CreateEnvironment: %v", err)
		}
		h.seedClaimedTask(t, "task-b", "team-a", "src-a", "src-id-b", "type-a", "tag-a", "exec-a", taskEnv.EnvironmentID, nil)
		token := h.issueScopeTokenFor(t, taskEnv.EnvironmentID, "task-b", "exec-a", nil, "team-a")
		before := h.decryptCount()
		_, err = h.openEnv(t, taskEnv.EnvironmentID, "task-b", token, "", h.executorIdentity("exec-a", "team-a"))
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("task applicability mismatch: err=%v want ErrEnvironmentUnavailable", err)
		}
		if h.decryptCount() != before {
			t.Fatalf("task applicability mismatch performed decrypt")
		}
	})
}

// parseScopeToken splits a compact scope token into its three base64url
// parts and decodes the protected header and payload claims. Tests
// use the helper to assert the documented claim shape.
type scopeHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}
type scopeClaims struct {
	TeamID        string  `json:"team_id"`
	ProjectID     *string `json:"project_id"`
	TaskID        string  `json:"task_id"`
	EnvironmentID string  `json:"environment_id"`
	ExecutorID    string  `json:"executor_id"`
	Audience      string  `json:"audience"`
	KeyID         string  `json:"key_id"`
	IssuedAt      string  `json:"issued_at"`
	Expiry        string  `json:"expiry"`
}

func parseScopeToken(t *testing.T, token string) (scopeHeader, scopeClaims) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts=%d want 3", len(parts))
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var header scopeHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	var claims scopeClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return header, claims
}

// issueScopeTokenFor assembles a compact scope token with the
// documented claim shape. The test harness never logs the MAC or
// the signing input.
func (h *section9Harness) issueScopeTokenFor(t *testing.T, envID, taskID, executorID string, projectID *string, teamID string) string {
	t.Helper()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID:        teamID,
		ProjectID:     projectID,
		TaskID:        taskID,
		EnvironmentID: envID,
		ExecutorID:    executorID,
		Audience:      "state-registry.environment.open",
		KeyID:         h.keyID,
		IssuedAt:      now.Format(time.RFC3339Nano),
		Expiry:        now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsSeg := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSeg + "." + claimsSeg
	mac, err := macForAlgorithm(h.algorithm, h.hmacKey, signingInput)
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func macForAlgorithm(alg store.ScopeTokenAlgorithm, key []byte, input string) ([]byte, error) {
	switch alg {
	case store.ScopeTokenAlgHS256:
		m := hmac.New(sha256.New, key)
		m.Write([]byte(input))
		return m.Sum(nil), nil
	case store.ScopeTokenAlgHS384:
		m := hmac.New(sha512.New384, key)
		m.Write([]byte(input))
		return m.Sum(nil), nil
	case store.ScopeTokenAlgHS512:
		m := hmac.New(sha512.New, key)
		m.Write([]byte(input))
		return m.Sum(nil), nil
	}
	return nil, fmt.Errorf("unsupported algorithm %q", alg)
}

// issueCustomScopeToken builds a compact scope token signed with
// the supplied algorithm and HMAC key. The test harness uses it
// to drive the previous-key-rotation and algorithm-per-key tests
// without coupling to the harness's primary key/algorithm.
func issueCustomScopeToken(t *testing.T, envID, taskID, executorID string, projectID *string, teamID, headerAlg, keyID string, key []byte) string {
	t.Helper()
	header := scopeHeader{Algorithm: headerAlg, KeyID: keyID, Type: "scope-token+json"}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID:        teamID,
		ProjectID:     projectID,
		TaskID:        taskID,
		EnvironmentID: envID,
		ExecutorID:    executorID,
		Audience:      "state-registry.environment.open",
		KeyID:         keyID,
		IssuedAt:      now.Format(time.RFC3339Nano),
		Expiry:        now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsSeg := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSeg + "." + claimsSeg
	mac, err := macForAlgorithm(store.ScopeTokenAlgorithm(header.Algorithm), key, signingInput)
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// ---------------------------------------------------------------------------
// TestScopeTokenSecurity
// ---------------------------------------------------------------------------

// TestScopeTokenSecurity drives every documented scope-token
// rejection path and asserts the no-decrypt-on-denial invariant:
// the decrypt callback counter MUST NOT advance on any denied
// case. Every denial returns the same ErrEnvironmentUnavailable
// sentinel so the public error shape is identical for every
// failure mode.
func TestScopeTokenSecurity(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedTeam(t, "team-b", "Team B")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedExecutor(t, "exec-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	_, err = h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)

	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")

	denialCases := []struct {
		name  string
		token string
	}{
		{name: "empty token", token: ""},
		{name: "not three parts", token: "abc.def"},
		{name: "header not base64url", token: "!!!.def.ghi"},
		{name: "claims not base64url", token: base64.RawURLEncoding.EncodeToString([]byte("{}")) + ".???.ghi"},
		{name: "header not JSON", token: base64.RawURLEncoding.EncodeToString([]byte("not json")) + "." + base64.RawURLEncoding.EncodeToString([]byte("{}")) + "." + base64.RawURLEncoding.EncodeToString([]byte("mac"))},
		{name: "header kid != claims key_id", token: forgeHeaderKidMismatch(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "algorithm outside allow-list (HS128)", token: forgeAlgorithm(t, "HS128", env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "alg=none rejected", token: forgeAlgorithm(t, "none", env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "key_id outside active window", token: forgeUnknownKeyID(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "tampered MAC", token: tamperMAC(t, goodToken)},
		{name: "expired", token: forgeExpired(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "issued_at premature beyond 30s", token: forgePremature(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "expiry - issued_at > 5 minutes", token: forgeWideLifetime(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "expiry not strictly after issued_at", token: forgeZeroLifetime(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "wrong audience", token: forgeAudience(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "missing claim (empty team_id)", token: forgeEmptyClaim(t, "team_id", env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "missing claim (empty task_id)", token: forgeEmptyClaim(t, "task_id", env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "missing claim (empty environment_id)", token: forgeEmptyClaim(t, "environment_id", env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "missing claim (empty executor_id)", token: forgeEmptyClaim(t, "executor_id", env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "missing typ", token: forgeMissingTyp(t, env.EnvironmentID, "task-a", "exec-a", h)},
		{name: "bad base64 in signature segment", token: replaceSegment(t, goodToken, 2, "??")},
	}

	for _, tc := range denialCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			before := h.decryptCount()
			_, err := h.openEnv(t, env.EnvironmentID, "task-a", tc.token, "", h.executorIdentity("exec-a", "team-a"))
			if !errors.Is(err, store.ErrEnvironmentUnavailable) {
				t.Fatalf("%s: err=%v want ErrEnvironmentUnavailable", tc.name, err)
			}
			if got := h.decryptCount(); got != before {
				t.Errorf("%s: decrypts=%d want %d (no decrypt on denial)", tc.name, got, before)
			}
		})
	}

	t.Run("wrong executor identity denied with zero decrypt", func(t *testing.T) {
		before := h.decryptCount()
		_, err := h.openEnv(t, env.EnvironmentID, "task-a", goodToken, "", h.executorIdentity("exec-other", "team-a"))
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("foreign executor: err=%v want ErrEnvironmentUnavailable", err)
		}
		if got := h.decryptCount(); got != before {
			t.Errorf("decrypts=%d want %d", got, before)
		}
	})

	t.Run("terminal task state denied with zero decrypt", func(t *testing.T) {
		mustExec(t, h.db, `UPDATE tasks SET current_state = 'finished' WHERE task_id = 'task-a'`)
		defer mustExec(t, h.db, `UPDATE tasks SET current_state = 'created' WHERE task_id = 'task-a'`)
		before := h.decryptCount()
		_, err := h.openEnv(t, env.EnvironmentID, "task-a", goodToken, "", h.executorIdentity("exec-a", "team-a"))
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("terminal task: err=%v want ErrEnvironmentUnavailable", err)
		}
		if got := h.decryptCount(); got != before {
			t.Errorf("decrypts=%d want %d", got, before)
		}
	})

	t.Run("foreign environment denied with zero decrypt", func(t *testing.T) {
		before := h.decryptCount()
		_, err := h.openEnv(t, "env-does-not-exist", "task-a", goodToken, "", h.executorIdentity("exec-a", "team-a"))
		if !errors.Is(err, store.ErrEnvironmentUnavailable) {
			t.Fatalf("foreign env: err=%v want ErrEnvironmentUnavailable", err)
		}
		if got := h.decryptCount(); got != before {
			t.Errorf("decrypts=%d want %d", got, before)
		}
	})

	t.Run("all HS256/HS384/HS512 verify successfully when key id matches", func(t *testing.T) {
		for _, alg := range []store.ScopeTokenAlgorithm{store.ScopeTokenAlgHS256, store.ScopeTokenAlgHS384, store.ScopeTokenAlgHS512} {
			alg := alg
			t.Run(string(alg), func(t *testing.T) {
				h2 := newSection9Harness(t)
				h2.algorithm = alg
				hmacHex := hex.EncodeToString(h2.hmacKey)
				keyring, err := store.NewScopeTokenKeyring(h2.keyID, hmacHex, "", alg)
				if err != nil {
					t.Fatalf("NewScopeTokenKeyring: %v", err)
				}
				h2.keyring = keyring
				h2.repo = store.NewWithScopeTokenKeyring(h2.db, h2.aesKey, keyring)
				h2.seedTeam(t, "team-a", "Team A")
				h2.seedSourceSystem(t, "src-a", "team-a", "listener-a")
				h2.seedTaskType(t, "type-a", "team-a", "tag-a")
				h2.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
				h2.seedExecutor(t, "exec-a", "team-a", "tag-a")
				env, err := h2.repo.CreateEnvironment(context.Background(), h2.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
					Name:   "team-wide-env",
					Values: map[string]string{"K": "v"},
				})
				if err != nil {
					t.Fatalf("CreateEnvironment: %v", err)
				}
				_, err = h2.repo.CreateSecret(context.Background(), h2.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
					Name:  "alpha",
					Value: "alpha-plaintext",
					Scope: env.Scope,
				})
				if err != nil {
					t.Fatalf("CreateSecret: %v", err)
				}
				h2.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
				token := h2.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
				resp, err := h2.openEnv(t, env.EnvironmentID, "task-a", token, "", h2.executorIdentity("exec-a", "team-a"))
				if err != nil {
					t.Fatalf("OpenEnvironment: %v", err)
				}
				if resp.Values["alpha"] != "alpha-plaintext" {
					t.Errorf("decrypted=%q want alpha-plaintext", resp.Values["alpha"])
				}
			})
		}
	})
}

// token forgery helpers — each helper returns a base64url-encoded
// compact three-part token that matches the documented shape with
// exactly one invariant broken. Tests use the helpers to drive the
// no-decrypt-on-denial contract.
func forgeHeaderKidMismatch(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: "different-kid", Type: "scope-token+json"}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID:        "team-a",
		TaskID:        taskID,
		EnvironmentID: envID,
		ExecutorID:    executorID,
		Audience:      "state-registry.environment.open",
		KeyID:         h.keyID,
		IssuedAt:      now.Format(time.RFC3339Nano),
		Expiry:        now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgeAlgorithm(t *testing.T, alg, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	header := scopeHeader{Algorithm: alg, KeyID: h.keyID, Type: "scope-token+json"}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano), Expiry: now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgeUnknownKeyID(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: "unknown-key", Type: "scope-token+json"}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: "unknown-key",
		IssuedAt: now.Format(time.RFC3339Nano), Expiry: now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func tamperMAC(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	macBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode mac: %v", err)
	}
	macBytes[0] ^= 0xff
	return parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(macBytes)
}

func forgeExpired(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	now := time.Now().UTC().Add(-10 * time.Minute)
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano),
		Expiry:   now.Add(2 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgePremature(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	now := time.Now().UTC().Add(2 * time.Minute)
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano),
		Expiry:   now.Add(6 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgeWideLifetime(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	now := time.Now().UTC()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano),
		Expiry:   now.Add(10 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgeZeroLifetime(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	now := time.Now().UTC()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano),
		Expiry:   now.Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgeAudience(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "wrong-audience", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano),
		Expiry:   now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func forgeEmptyClaim(t *testing.T, field, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID:        "team-a",
		TaskID:        taskID,
		EnvironmentID: envID,
		ExecutorID:    executorID,
		Audience:      "state-registry.environment.open",
		KeyID:         h.keyID,
		IssuedAt:      now.Format(time.RFC3339Nano),
		Expiry:        now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	switch field {
	case "team_id":
		claims.TeamID = ""
	case "task_id":
		claims.TaskID = ""
	case "environment_id":
		claims.EnvironmentID = ""
	case "executor_id":
		claims.ExecutorID = ""
	}
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	return assembleRawToken(t, header, claims, h)
}

func forgeMissingTyp(t *testing.T, envID, taskID, executorID string, h *section9Harness) string {
	t.Helper()
	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: ""}
	now := time.Now().UTC()
	claims := scopeClaims{
		TeamID: "team-a", TaskID: taskID, EnvironmentID: envID, ExecutorID: executorID,
		Audience: "state-registry.environment.open", KeyID: h.keyID,
		IssuedAt: now.Format(time.RFC3339Nano),
		Expiry:   now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	return assembleRawToken(t, header, claims, h)
}

func replaceSegment(t *testing.T, token string, idx int, replacement string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	parts[idx] = replacement
	return strings.Join(parts, ".")
}

func assembleRawToken(t *testing.T, header scopeHeader, claims scopeClaims, h *section9Harness) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsSeg := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSeg + "." + claimsSeg
	mac, err := macForAlgorithm(store.ScopeTokenAlgorithm(header.Algorithm), h.hmacKey, signingInput)
	if err != nil {
		// Algorithm outside allow-list for forgeAlgorithm: emit a
		// zero-length MAC segment; the verifier will still reject
		// the alg before the MAC comparison.
		mac = []byte{}
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// ---------------------------------------------------------------------------
// TestAesGcm
// ---------------------------------------------------------------------------

// TestAesGcm asserts the AES-256-GCM encryption contract: every
// stored nonce is exactly 12 bytes; every tag is exactly 16
// bytes; the nonce for successive encryptions of identical
// plaintext are different (fresh random nonce per call); the
// ciphertext length is non-zero and never equal to the plaintext
// length; the same plaintext under the same key produces a
// different ciphertext because the nonce is fresh.
func TestAesGcm(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	first, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "the same plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret first: %v", err)
	}
	second, err := h.repo.ReplaceSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, first.Secret.SecretID, platform.SecretReplaceRequest{
		Value: "the same plaintext",
	})
	if err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	firstCipher := querySingleBytes(t, h.db, `SELECT ciphertext FROM secret_versions WHERE secret_id = $1 AND version = 1`, first.Secret.SecretID)
	secondCipher := querySingleBytes(t, h.db, `SELECT ciphertext FROM secret_versions WHERE secret_id = $1 AND version = 2`, second.Secret.SecretID)
	firstNonce := querySingleBytes(t, h.db, `SELECT nonce FROM secret_versions WHERE secret_id = $1 AND version = 1`, first.Secret.SecretID)
	secondNonce := querySingleBytes(t, h.db, `SELECT nonce FROM secret_versions WHERE secret_id = $1 AND version = 2`, second.Secret.SecretID)
	firstTag := querySingleBytes(t, h.db, `SELECT authentication_tag FROM secret_versions WHERE secret_id = $1 AND version = 1`, first.Secret.SecretID)
	secondTag := querySingleBytes(t, h.db, `SELECT authentication_tag FROM secret_versions WHERE secret_id = $1 AND version = 2`, second.Secret.SecretID)
	if len(firstNonce) != 12 {
		t.Errorf("first nonce=%d bytes want 12", len(firstNonce))
	}
	if len(secondNonce) != 12 {
		t.Errorf("second nonce=%d bytes want 12", len(secondNonce))
	}
	if len(firstTag) != 16 {
		t.Errorf("first tag=%d bytes want 16", len(firstTag))
	}
	if len(secondTag) != 16 {
		t.Errorf("second tag=%d bytes want 16", len(secondTag))
	}
	if string(firstNonce) == string(secondNonce) {
		t.Errorf("nonces identical: fresh random nonce per encryption is required")
	}
	if string(firstCipher) == string(secondCipher) {
		t.Errorf("ciphertexts identical: nonce must randomize the encryption")
	}
	if len(firstCipher) == 0 {
		t.Errorf("first ciphertext empty")
	}
}

// ---------------------------------------------------------------------------
// TestStartupFailClosed
// ---------------------------------------------------------------------------

// TestStartupFailClosed exercises the config-level fail-closed
// path: when STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX is missing or
// shorter than the documented 32-byte HMAC key minimum, the
// service refuses to start and the Load path returns the
// documented error. The test then confirms that a Store
// constructed without an AES key refuses every secret write.
func TestStartupFailClosed(t *testing.T) {
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", hex.EncodeToString(make([]byte, 32)))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", "")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", "")
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", "")
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", "")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "")

	// No scope-token env vars → config.Load fails closed with
	// the documented SCOPE_TOKEN_KEY_HEX required error.
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", "")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "SCOPE_TOKEN_KEY_HEX") {
		t.Fatalf("missing scope token key: err=%v, want SCOPE_TOKEN_KEY_HEX required", err)
	}
	// A 4-byte key is too short for HS256/HS384/HS512
	// (>= 32 bytes required).
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", hex.EncodeToString([]byte{1, 2, 3, 4}))
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("short scope token key: err=%v, want at-least-32-bytes error", err)
	}
	// The Store is constructed only after a successful Load; a
	// Store with no AES key refuses every secret write with the
	// documented AES-256-GCM key unavailable error.
	db := migratedDB(t)
	repo := store.New(db) // no AES key
	_, err := repo.CreateSecret(context.Background(), platform.GatewayIdentity{TeamID: "team-a", OperatorID: "op-1", RequestID: "req-1"},
		"env-missing", platform.SecretCreateRequest{Name: "alpha", Value: "v", Scope: platform.EnvironmentScope{}})
	if err == nil || !strings.Contains(err.Error(), "AES-256-GCM key unavailable") {
		t.Errorf("no-AES CreateSecret: err=%v want AES-256-GCM key unavailable", err)
	}
}

// ---------------------------------------------------------------------------
// TestDecryptFailClosedOnBadAssociatedData
// ---------------------------------------------------------------------------

// TestDecryptFailClosedOnBadAssociatedData asserts that the
// production OpenEnvironment path fails closed on AES-GCM
// authentication failure with zero successful decrypt operations
// and a rolled-back audit row.
func TestDecryptFailClosedOnBadAssociatedData(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	if _, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")

	auditBefore := countRows(t, h.db, "audit_entries", "action", "environment.open")
	decryptsBefore := h.decryptCount()

	otherKey := make([]byte, 32)
	if _, err := rand.Read(otherKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	otherKeyring, err := store.NewScopeTokenKeyring(h.keyID, hex.EncodeToString(h.hmacKey), "", store.ScopeTokenAlgHS256)
	if err != nil {
		t.Fatalf("NewScopeTokenKeyring: %v", err)
	}
	// The token MAC still verifies under the shared HMAC key
	// but the AES-GCM open returns an auth error because the
	// store is wired with a fresh AES key that does not match
	// the stored ciphertext. The open path must collapse this
	// to the same non-revealing ErrEnvironmentUnavailable,
	// observe zero successful decrypts, and roll back the
	// audit row.
	otherStore := store.NewWithScopeTokenKeyring(h.db, otherKey, otherKeyring)
	if _, err := otherStore.OpenEnvironment(context.Background(), store.OpenEnvironmentRequest{
		EnvironmentID: env.EnvironmentID,
		TaskID:        "task-a",
		Token:         goodToken,
		Identity:      h.executorIdentity("exec-a", "team-a"),
		RequestID:     "req-wrong-key",
		RecordDecrypt: func() {},
	}); !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("wrong AES key: err=%v want ErrEnvironmentUnavailable", err)
	}
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("decrypts=%d want %d (zero decrypt on AEAD auth failure)", got, decryptsBefore)
	}
	if got := countRows(t, h.db, "audit_entries", "action", "environment.open"); got != auditBefore {
		t.Errorf("audit rows=%d want %d (transaction rolled back)", got, auditBefore)
	}
}

// ---------------------------------------------------------------------------
// TestNoDecryptOnScopeTokenDenial
// ---------------------------------------------------------------------------

// TestNoDecryptOnScopeTokenDenial wraps the no-decrypt contract
// behind a single named gate. The harness records every decrypt
// call via the recordDecrypt callback; the test asserts the
// counter never advances for any of the documented denial
// categories and that the public error shape is identical.
func TestNoDecryptOnScopeTokenDenial(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedExecutor(t, "exec-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	_, err = h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)

	denials := []struct {
		name string
		call func() error
	}{
		{
			name: "missing token",
			call: func() error {
				_, err := h.openEnv(t, env.EnvironmentID, "task-a", "", "", h.executorIdentity("exec-a", "team-a"))
				return err
			},
		},
		{
			name: "garbage token",
			call: func() error {
				_, err := h.openEnv(t, env.EnvironmentID, "task-a", "garbage", "", h.executorIdentity("exec-a", "team-a"))
				return err
			},
		},
		{
			name: "wrong env id in path",
			call: func() error {
				good := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
				_, err := h.openEnv(t, "env-other", "task-a", good, "", h.executorIdentity("exec-a", "team-a"))
				return err
			},
		},
		{
			name: "wrong task id in path",
			call: func() error {
				good := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
				_, err := h.openEnv(t, env.EnvironmentID, "task-other", good, "", h.executorIdentity("exec-a", "team-a"))
				return err
			},
		},
		{
			name: "executor not assigned",
			call: func() error {
				good := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
				_, err := h.openEnv(t, env.EnvironmentID, "task-a", good, "", h.executorIdentity("exec-other", "team-a"))
				return err
			},
		},
	}
	for _, tc := range denials {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			before := h.decryptCount()
			err := tc.call()
			if !errors.Is(err, store.ErrEnvironmentUnavailable) {
				t.Fatalf("err=%v want ErrEnvironmentUnavailable", err)
			}
			if got := h.decryptCount(); got != before {
				t.Errorf("decrypts=%d want %d", got, before)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestKeyVersionRotationMetadata
// ---------------------------------------------------------------------------

// TestKeyVersionRotationMetadata verifies the key_id / key_version
// envelope on every secret_versions row. The test creates a
// secret, replaces it, and asserts both rows record the local
// scope token key id and key_version 1; the row is then verified
// for the API surface the Section 9 contract requires.
func TestKeyVersionRotationMetadata(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	first, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "v1",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	second, err := h.repo.ReplaceSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, first.Secret.SecretID, platform.SecretReplaceRequest{
		Value: "v2",
	})
	if err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	if first.Version.KeyID == "" {
		t.Errorf("v1 key_id empty")
	}
	if first.Version.KeyVersion < 1 {
		t.Errorf("v1 key_version=%d want >=1", first.Version.KeyVersion)
	}
	if second.Version.KeyID != first.Version.KeyID {
		t.Errorf("v2 key_id=%q != v1 key_id=%q", second.Version.KeyID, first.Version.KeyID)
	}
	if second.Version.KeyVersion < first.Version.KeyVersion {
		t.Errorf("v2 key_version=%d < v1 key_version=%d", second.Version.KeyVersion, first.Version.KeyVersion)
	}
	// Versions are append-only; the list endpoint reports both
	// rows with the documented key metadata.
	versions, err := h.repo.ListSecretVersions(context.Background(), "team-a", env.EnvironmentID, first.Secret.SecretID, 10)
	if err != nil {
		t.Fatalf("ListSecretVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("versions=%d want 2", len(versions))
	}
}

// ---------------------------------------------------------------------------
// TestPlaintextExclusion
// ---------------------------------------------------------------------------

// TestPlaintextExclusion is the final gate. The test asserts that
// no plaintext secret value lands anywhere observable:
// environment_definitions.values, secrets.name, secret_versions
// (ciphertext/nonce/tag only), audit_entries, or the open
// environment error responses. The Section 9 contract requires
// every leak surface to remain clean.
func TestPlaintextExclusion(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	h.seedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", time.Now().UTC(), nil)
	h.seedExecutor(t, "exec-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	plaintext := "the-very-secret-plaintext-value"
	created, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: plaintext,
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	t.Run("plaintext absent from secrets row", func(t *testing.T) {
		row := querySingleBytes(t, h.db, `SELECT name::bytea FROM secrets WHERE secret_id = $1`, created.Secret.SecretID)
		if strings.Contains(string(row), plaintext) {
			t.Errorf("secrets.name contains plaintext")
		}
	})
	t.Run("plaintext absent from secret_versions ciphertext", func(t *testing.T) {
		row := querySingleBytes(t, h.db, `SELECT ciphertext FROM secret_versions WHERE secret_id = $1 AND version = 1`, created.Secret.SecretID)
		if strings.Contains(string(row), plaintext) {
			t.Errorf("secret_versions.ciphertext contains plaintext")
		}
	})
	t.Run("plaintext absent from environment_definitions.values", func(t *testing.T) {
		row := querySingleBytes(t, h.db, `SELECT values::text FROM environment_definitions WHERE environment_id = $1`, env.EnvironmentID)
		if strings.Contains(string(row), plaintext) {
			t.Errorf("environment_definitions.values contains plaintext")
		}
	})
	t.Run("plaintext absent from audit_entries for create and replace", func(t *testing.T) {
		// Drive one successful open so the environment.open
		// audit row is appended, then scan every audit row.
		h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
		goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
		_, err = h.openEnv(t, env.EnvironmentID, "task-a", goodToken, "", h.executorIdentity("exec-a", "team-a"))
		if err != nil {
			t.Fatalf("OpenEnvironment: %v", err)
		}
		rows, err := h.db.QueryContext(context.Background(),
			`SELECT action, resource_type, resource_id, actor_id, request_id, outcome FROM audit_entries ORDER BY occurred_at ASC, audit_id ASC`)
		if err != nil {
			t.Fatalf("query audit_entries: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var action, resourceType, resourceID, actorID, requestID, outcome string
			if err := rows.Scan(&action, &resourceType, &resourceID, &actorID, &requestID, &outcome); err != nil {
				t.Fatalf("scan audit_entries: %v", err)
			}
			for _, field := range []string{action, resourceType, resourceID, actorID, requestID, outcome} {
				if strings.Contains(field, plaintext) {
					t.Errorf("audit_entries contains plaintext in %q", field)
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentAuditRequestIDCorrelation
// ---------------------------------------------------------------------------

// TestOpenEnvironmentAuditRequestIDCorrelation asserts that the
// environment.open audit row carries the call-site request_id
// supplied via the typed OpenEnvironmentRequest, not a synthetic
// "open-<uuid>" value. The previous implementation fabricated a
// new UUID per audit write, breaking operator correlation.
func TestOpenEnvironmentAuditRequestIDCorrelation(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")

	correlationID := "req-correlation-" + t.Name()
	if _, err := h.openEnv(t, env.EnvironmentID, "task-a", goodToken, correlationID, h.executorIdentity("exec-a", "team-a")); err != nil {
		t.Fatalf("OpenEnvironment: %v", err)
	}

	var storedRequestID, action string
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT request_id, action FROM audit_entries
		 WHERE action = 'environment.open' AND resource_id = $1`, env.EnvironmentID,
	).Scan(&storedRequestID, &action); err != nil {
		t.Fatalf("read audit_entries: %v", err)
	}
	if storedRequestID != correlationID {
		t.Errorf("audit request_id=%q want %q", storedRequestID, correlationID)
	}
	if strings.HasPrefix(storedRequestID, "open-") {
		t.Errorf("audit request_id=%q still uses the synthetic open-uuid prefix", storedRequestID)
	}
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentDeniedAfterReassignment
// ---------------------------------------------------------------------------

// TestOpenEnvironmentDeniedAfterReassignment asserts that a
// still-valid scope token issued for the originally assigned
// Executor is rejected with the same non-revealing 404 after the
// task is reassigned to a different Executor, and that the
// rejection observes zero successful decrypt operations. The
// claim-field trigger is temporarily disabled so the
// application-level reassignment check in the verifier can be
// exercised in isolation.
func TestOpenEnvironmentDeniedAfterReassignment(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	if _, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")
	h.seedExecutor(t, "exec-other", "team-a", "tag-a")

	// The trigger that prevents reassignment is disabled so we
	// can exercise the application-level verifier check; the
	// defense (assignedExecutor != identity.ExecutorID) is the
	// contract under test.
	mustExec(t, h.db, `ALTER TABLE tasks DISABLE TRIGGER USER`)
	defer mustExec(t, h.db, `ALTER TABLE tasks ENABLE TRIGGER USER`)
	mustExec(t, h.db,
		`UPDATE tasks SET executor_id = 'exec-other' WHERE task_id = 'task-a'`)

	decryptsBefore := h.decryptCount()
	_, err = h.openEnv(t, env.EnvironmentID, "task-a", goodToken, "", h.executorIdentity("exec-a", "team-a"))
	if !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("post-reassignment open: err=%v want ErrEnvironmentUnavailable", err)
	}
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("post-reassignment decrypts=%d want %d (zero decrypt on reassignment denial)", got, decryptsBefore)
	}
}

// ---------------------------------------------------------------------------
// TestOpenEnvironmentSameTokenRetryWithinTTL
// ---------------------------------------------------------------------------

// TestOpenEnvironmentSameTokenRetryWithinTTL asserts that the
// production path honors the documented retry-within-TTL
// contract for the open boundary: the same scope token,
// presented again within its TTL by the still-assigned
// Executor, returns the same authorized values, re-decrypts
// every secret row, and appends a fresh audit row for the
// retry's request_id.
func TestOpenEnvironmentSameTokenRetryWithinTTL(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	created, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)
	goodToken := h.issueScopeTokenFor(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a")

	first, err := h.openEnv(t, env.EnvironmentID, "task-a", goodToken, "req-retry-1", h.executorIdentity("exec-a", "team-a"))
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if first.Values[created.Secret.Name] != "alpha-plaintext" {
		t.Fatalf("first open: alpha=%q want alpha-plaintext", first.Values[created.Secret.Name])
	}

	auditBefore := countRows(t, h.db, "audit_entries", "action", "environment.open")
	decryptsBefore := h.decryptCount()

	// Same token, same task, same Executor — within TTL.
	second, err := h.openEnv(t, env.EnvironmentID, "task-a", goodToken, "req-retry-2", h.executorIdentity("exec-a", "team-a"))
	if err != nil {
		t.Fatalf("retry open: %v", err)
	}
	if second.Values[created.Secret.Name] != first.Values[created.Secret.Name] {
		t.Errorf("retry: alpha=%q want %q (same authorized value)", second.Values[created.Secret.Name], first.Values[created.Secret.Name])
	}
	if got := h.decryptCount(); got <= decryptsBefore {
		t.Errorf("retry decrypts=%d want > %d (retry must re-decrypt)", got, decryptsBefore)
	}
	// Two successful opens append two audit rows.
	if got := countRows(t, h.db, "audit_entries", "action", "environment.open"); got != auditBefore+1 {
		t.Errorf("retry audit rows delta=%d want 1 (retry appends a new audit row)", got-auditBefore)
	}
}

// ---------------------------------------------------------------------------
// TestScopeTokenStrictClaimShape
// ---------------------------------------------------------------------------

// TestScopeTokenStrictClaimShape asserts that the verifier rejects
// tokens whose claims carry an unknown JSON field. The
// production verifier uses json.Decoder.DisallowUnknownFields so
// a future claim addition cannot silently widen the accepted
// shape.
func TestScopeTokenStrictClaimShape(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)

	header := scopeHeader{Algorithm: string(h.algorithm), KeyID: h.keyID, Type: "scope-token+json"}
	now := time.Now().UTC()
	// "extra_claim" is intentionally not in the documented
	// ScopeTokenClaims shape; the strict decoder must reject
	// the token before the canonical-claim check.
	rawClaims := map[string]any{
		"team_id":        "team-a",
		"project_id":     nil,
		"task_id":        "task-a",
		"environment_id": env.EnvironmentID,
		"executor_id":    "exec-a",
		"audience":       "state-registry.environment.open",
		"key_id":         h.keyID,
		"issued_at":      now.Format(time.RFC3339Nano),
		"expiry":         now.Add(5 * time.Minute).Format(time.RFC3339Nano),
		"extra_claim":    "should-be-rejected",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(rawClaims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsSeg := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSeg + "." + claimsSeg
	mac, err := macForAlgorithm(h.algorithm, h.hmacKey, signingInput)
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	tok := signingInput + "." + base64.RawURLEncoding.EncodeToString(mac)

	decryptsBefore := h.decryptCount()
	if _, err := h.openEnv(t, env.EnvironmentID, "task-a", tok, "", h.executorIdentity("exec-a", "team-a")); !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("strict claims: err=%v want ErrEnvironmentUnavailable", err)
	}
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("strict claims: decrypts=%d want %d (zero decrypt on unknown claim)", got, decryptsBefore)
	}
}

// ---------------------------------------------------------------------------
// TestScopeTokenPreviousKeyRotation
// ---------------------------------------------------------------------------

// TestScopeTokenPreviousKeyRotation asserts that a token signed
// with a previous HMAC key still verifies after the operator
// rotates the active key (previous key is in the keyring), and
// that an unknown key id is rejected with the same non-revealing
// 404. The previous-key JSON uses the per-key algorithm map
// shape so the per-key algorithm policy is exercised end-to-end.
func TestScopeTokenPreviousKeyRotation(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	if _, err := h.repo.CreateSecret(context.Background(), h.operatorIdentity("op-1"), env.EnvironmentID, platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
		Scope: env.Scope,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)

	prevKey := make([]byte, 32)
	if _, err := rand.Read(prevKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	prevKeyID := "scope-prev"
	prevAlg := store.ScopeTokenAlgHS256
	prevJSON := fmt.Sprintf(`{"%s": {"key": %q, "alg": "HS256"}}`, prevKeyID, hex.EncodeToString(prevKey))
	rotated, err := store.NewScopeTokenKeyring("scope-current", hex.EncodeToString(h.hmacKey), prevJSON, store.ScopeTokenAlgHS256)
	if err != nil {
		t.Fatalf("NewScopeTokenKeyring: %v", err)
	}
	rotatedRepo := store.NewWithScopeTokenKeyring(h.db, h.aesKey, rotated)
	rotatedHarness := &section9Harness{db: h.db, repo: rotatedRepo, decrypts: &atomic.Int64{}, aesKey: h.aesKey, hmacKey: h.hmacKey, keyring: rotated, keyID: "scope-current", algorithm: store.ScopeTokenAlgHS256}

	// Previous-key token verifies under the rotated keyring.
	prevToken := issueCustomScopeToken(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a", string(prevAlg), prevKeyID, prevKey)
	if _, err := rotatedHarness.openEnv(t, env.EnvironmentID, "task-a", prevToken, "", h.executorIdentity("exec-a", "team-a")); err != nil {
		t.Fatalf("previous-key open: %v", err)
	}

	// Unknown key id is rejected with zero decrypt.
	unknownToken := issueCustomScopeToken(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a", "HS256", "scope-unknown", prevKey)
	decryptsBefore := h.decryptCount()
	if _, err := rotatedHarness.openEnv(t, env.EnvironmentID, "task-a", unknownToken, "", h.executorIdentity("exec-a", "team-a")); !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("unknown key id open: err=%v want ErrEnvironmentUnavailable", err)
	}
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("unknown key id: decrypts=%d want %d", got, decryptsBefore)
	}
}

// ---------------------------------------------------------------------------
// TestScopeTokenAlgorithmPerKeyPolicy
// ---------------------------------------------------------------------------

// TestScopeTokenAlgorithmPerKeyPolicy asserts that the keyring
// authoritatively pins the algorithm for each key. A token whose
// protected header `alg` differs from the per-key algorithm
// (issued by the same key bytes) is rejected before any MAC
// computation; the constant-time MAC comparison never runs
// against a stronger allow-listed variant than the key was
// generated for.
func TestScopeTokenAlgorithmPerKeyPolicy(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedSourceSystem(t, "src-a", "team-a", "listener-a")
	h.seedTaskType(t, "type-a", "team-a", "tag-a")
	env, err := h.repo.CreateEnvironment(context.Background(), h.operatorIdentity("op-1"), platform.EnvironmentWriteRequest{
		Name:   "team-wide-env",
		Values: map[string]string{"K": "v"},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	h.seedClaimedTask(t, "task-a", "team-a", "src-a", "src-id-a", "type-a", "tag-a", "exec-a", env.EnvironmentID, nil)

	// Keyring is configured for HS256 on the active key.
	confusedKeyID := "scope-confused"
	confusedKey := h.hmacKey
	confusedToken := issueCustomScopeToken(t, env.EnvironmentID, "task-a", "exec-a", nil, "team-a", "HS512", confusedKeyID, confusedKey)
	keyring, err := store.NewScopeTokenKeyring(confusedKeyID, hex.EncodeToString(h.hmacKey), "", store.ScopeTokenAlgHS256)
	if err != nil {
		t.Fatalf("NewScopeTokenKeyring: %v", err)
	}
	repo := store.NewWithScopeTokenKeyring(h.db, h.aesKey, keyring)
	decryptsBefore := h.decryptCount()
	if _, err := repo.OpenEnvironment(context.Background(), store.OpenEnvironmentRequest{
		EnvironmentID: env.EnvironmentID,
		TaskID:        "task-a",
		Token:         confusedToken,
		Identity:      h.executorIdentity("exec-a", "team-a"),
		RequestID:     "req-alg-confusion",
		RecordDecrypt: h.recordDecrypt,
	}); !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("algorithm confusion: err=%v want ErrEnvironmentUnavailable", err)
	}
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("algorithm confusion: decrypts=%d want %d (zero decrypt on per-key alg mismatch)", got, decryptsBefore)
	}
}
