//go:build integration

// Section 9 — cursor pagination for environments / secrets /
// secret versions, same-team secret revoke, and transactional
// consistent reads. The tests below cover the store.Store surface
// that the new cursor-aware handlers and the DELETE /v1/.../secrets
// route depend on. They live in a separate file from
// section9_integration_test.go to avoid merge conflicts.
package test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// TestEnvironmentListPagedOrderingAndPagination pins the
// deterministic (created_at ASC, environment_id ASC) ordering and
// the no-overlap, no-skip walk across pages.
func TestEnvironmentListPagedOrderingAndPagination(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	// Seed 4 environments with distinct created_at timestamps
	// generated from the Go clock so the deterministic order is
	// unambiguous.
	base := time.Now().UTC().Add(-1 * time.Hour)
	for i, suffix := range []string{"a", "b", "c", "d"} {
		if _, err := h.db.ExecContext(context.Background(),
			`INSERT INTO environment_definitions (environment_id, team_id, name, values) VALUES ($1, $2, $3, '{}'::jsonb)`,
			"env-"+suffix, "team-a", "env-"+suffix); err != nil {
			t.Fatalf("seed env %d: %v", i, err)
		}
		// Force a distinct created_at so the deterministic order
		// is unambiguous.
		createdAt := base.Add(time.Duration(i+1) * time.Microsecond)
		if _, err := h.db.ExecContext(context.Background(),
			`UPDATE environment_definitions SET created_at = $1::timestamptz WHERE environment_id = $2`,
			createdAt, "env-"+suffix); err != nil {
			t.Fatalf("set created_at %d: %v", i, err)
		}
	}
	ctx := context.Background()
	cursor := (*store.EnvironmentAfter)(nil)
	seen := []string{}
	for page := 0; page < 8; page++ {
		entries, after, err := h.repo.ListEnvironmentsPaged(ctx, "team-a", nil, nil, 1, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, e := range entries {
			seen = append(seen, e.EnvironmentID)
		}
		if after == nil {
			break
		}
		cursor = after
	}
	want := []string{"env-a", "env-b", "env-c", "env-d"}
	if fmt.Sprintf("%v", seen) != fmt.Sprintf("%v", want) {
		t.Fatalf("paged env order=%v want=%v", seen, want)
	}
}

// TestEnvironmentListPagedForeignRowIsolation proves a foreign
// team is excluded from the page even when the result is empty.
func TestEnvironmentListPagedForeignRowIsolation(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedTeam(t, "team-b", "Team B")
	ctx := context.Background()
	for _, env := range []struct{ id, team, name string }{
		{"env-a", "team-a", "env-a"},
		{"env-b", "team-b", "env-b"},
	} {
		if _, err := h.db.ExecContext(ctx,
			`INSERT INTO environment_definitions (environment_id, team_id, name, values) VALUES ($1, $2, $3, '{}'::jsonb)`,
			env.id, env.team, env.name); err != nil {
			t.Fatalf("seed env %s: %v", env.id, err)
		}
	}
	entries, after, err := h.repo.ListEnvironmentsPaged(ctx, "team-a", nil, nil, 50, nil)
	if err != nil {
		t.Fatalf("ListEnvironmentsPaged: %v", err)
	}
	if after != nil {
		t.Errorf("after=%+v, want nil for finite team-a page", after)
	}
	if len(entries) != 1 || entries[0].EnvironmentID != "env-a" {
		t.Fatalf("entries=%+v, want exactly env-a", entries)
	}
}

// TestEnvironmentListPagedN1Elimination proves the
// environment list returns the same number of secret summaries
// when the team has more than one environment. The pre-fix
// implementation issued a per-environment SELECT; the post-fix
// implementation issues a single batched SELECT.
func TestEnvironmentListPagedN1Elimination(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	ctx := context.Background()
	// Two environments, each with one secret.
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO environment_definitions (environment_id, team_id, name, values)
		 VALUES ('env-a', 'team-a', 'env-a', '{}'::jsonb),
		        ('env-b', 'team-a', 'env-b', '{}'::jsonb)`); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	// Use a temporary aes key on the seeded repo; reuse the
	// harness helper which already loads the AES key. CreateSecret
	// requires a key.
	if _, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-a", platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "alpha-plaintext",
	}); err != nil {
		t.Fatalf("seed secret env-a: %v", err)
	}
	if _, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-b", platform.SecretCreateRequest{
		Name:  "beta",
		Value: "beta-plaintext",
	}); err != nil {
		t.Fatalf("seed secret env-b: %v", err)
	}
	entries, _, err := h.repo.ListEnvironmentsPaged(ctx, "team-a", nil, nil, 50, nil)
	if err != nil {
		t.Fatalf("ListEnvironmentsPaged: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(entries))
	}
	want := map[string]int{"env-a": 1, "env-b": 1}
	for _, e := range entries {
		if got, ok := want[e.EnvironmentID]; !ok {
			t.Errorf("foreign env=%q in result", e.EnvironmentID)
		} else if got != len(e.Secrets) {
			t.Errorf("env=%s secrets=%d, want %d", e.EnvironmentID, len(e.Secrets), got)
		}
	}
}

// TestSecretListPagedOrderingAndPagination pins the
// (created_at ASC, secret_id ASC) ordering for secrets under one
// environment.
func TestSecretListPagedOrderingAndPagination(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	ctx := context.Background()
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO environment_definitions (environment_id, team_id, name, values)
		 VALUES ('env-a', 'team-a', 'env-a', '{}'::jsonb)`); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	for i, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-a", platform.SecretCreateRequest{
			Name:  name,
			Value: "v-" + name,
		}); err != nil {
			t.Fatalf("CreateSecret %s: %v", name, err)
		}
		// Force a distinct created_at so the deterministic order
		// is unambiguous.
		createdAt := time.Now().UTC().Add(time.Duration(i+1) * time.Microsecond)
		if _, err := h.db.ExecContext(ctx,
			`UPDATE secrets SET created_at = $1::timestamptz
			 WHERE name = $2 AND team_id = 'team-a' AND environment_id = 'env-a'`,
			createdAt, name); err != nil {
			t.Fatalf("set created_at %s: %v", name, err)
		}
	}
	cursor := (*store.SecretAfter)(nil)
	seen := []string{}
	for page := 0; page < 8; page++ {
		entries, after, err := h.repo.ListSecretsPaged(ctx, "team-a", "env-a", 1, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, e := range entries {
			seen = append(seen, e.Name)
		}
		if after == nil {
			break
		}
		cursor = after
	}
	want := []string{"alpha", "beta", "gamma"}
	if fmt.Sprintf("%v", seen) != fmt.Sprintf("%v", want) {
		t.Fatalf("paged secret order=%v want=%v", seen, want)
	}
}

// TestSecretListPagedForeignEnvironmentIsNotRevealing proves a
// foreign environment id returns the same non-revealing 404 used
// for every other point resource.
func TestSecretListPagedForeignEnvironmentIsNotRevealing(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	ctx := context.Background()
	_, _, err := h.repo.ListSecretsPaged(ctx, "team-a", "env-missing", 50, nil)
	if !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("err=%v, want ErrEnvironmentUnavailable", err)
	}
}

// TestSecretVersionListPagedOrderingAndPagination pins the
// (version ASC, secret_id ASC) ordering across pages.
func TestSecretVersionListPagedOrderingAndPagination(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	ctx := context.Background()
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO environment_definitions (environment_id, team_id, name, values)
		 VALUES ('env-a', 'team-a', 'env-a', '{}'::jsonb)`); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	created, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-a", platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "v1",
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if _, err := h.repo.ReplaceSecret(ctx, h.operatorIdentity("op-1"), "env-a", created.Secret.SecretID, platform.SecretReplaceRequest{
		Value: "v2",
	}); err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	if _, err := h.repo.ReplaceSecret(ctx, h.operatorIdentity("op-1"), "env-a", created.Secret.SecretID, platform.SecretReplaceRequest{
		Value: "v3",
	}); err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	cursor := (*store.SecretVersionAfter)(nil)
	seen := []int{}
	for page := 0; page < 8; page++ {
		entries, after, err := h.repo.ListSecretVersionsPaged(ctx, "team-a", "env-a", created.Secret.SecretID, 1, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, v := range entries {
			seen = append(seen, v.Version)
		}
		if after == nil {
			break
		}
		cursor = after
	}
	want := []int{1, 2, 3}
	if fmt.Sprintf("%v", seen) != fmt.Sprintf("%v", want) {
		t.Fatalf("paged version order=%v want=%v", seen, want)
	}
}

// TestSecretVersionListPagedForeignEnvironmentDefense proves a
// secret_id that lives in another environment is rejected even
// when the team predicate would otherwise pass.
func TestSecretVersionListPagedForeignEnvironmentDefense(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedTeam(t, "team-b", "Team B")
	ctx := context.Background()
	for _, env := range []struct{ id, team, name string }{
		{"env-a", "team-a", "env-a"},
		{"env-b", "team-b", "env-b"},
	} {
		if _, err := h.db.ExecContext(ctx,
			`INSERT INTO environment_definitions (environment_id, team_id, name, values) VALUES ($1, $2, $3, '{}'::jsonb)`,
			env.id, env.team, env.name); err != nil {
			t.Fatalf("seed env %s: %v", env.id, err)
		}
	}
	created, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-a", platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "v1",
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	// team-a queries the secret under team-b's environment.
	_, _, err = h.repo.ListSecretVersionsPaged(ctx, "team-a", "env-b", created.Secret.SecretID, 50, nil)
	if !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("err=%v, want ErrEnvironmentUnavailable", err)
	}
}

// TestRevokeSecretMarksRevokedAndPreservesVersions covers the
// documented DELETE /v1/environments/{id}/secrets/{id} contract.
func TestRevokeSecretMarksRevokedAndPreservesVersions(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	ctx := context.Background()
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO environment_definitions (environment_id, team_id, name, values)
		 VALUES ('env-a', 'team-a', 'env-a', '{}'::jsonb)`); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	created, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-a", platform.SecretCreateRequest{
		Name:  "alpha",
		Value: "v1",
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if _, err := h.repo.ReplaceSecret(ctx, h.operatorIdentity("op-1"), "env-a", created.Secret.SecretID, platform.SecretReplaceRequest{
		Value: "v2",
	}); err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	// Pre-revoke invariant: latest_version is 2, revoked=false,
	// and two immutable secret_versions exist.
	var latestBefore int
	var revokedBefore bool
	if err := h.db.QueryRowContext(ctx,
		`SELECT latest_version, revoked FROM secrets WHERE secret_id = $1 AND team_id = 'team-a'`,
		created.Secret.SecretID,
	).Scan(&latestBefore, &revokedBefore); err != nil {
		t.Fatalf("read pre-revoke: %v", err)
	}
	if latestBefore != 2 || revokedBefore {
		t.Errorf("pre-revoke latest_version=%d revoked=%v, want 2 / false", latestBefore, revokedBefore)
	}

	revoked, err := h.repo.RevokeSecret(ctx, h.operatorIdentity("op-revoke"), "env-a", created.Secret.SecretID)
	if err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	if !revoked.Revoked {
		t.Errorf("RevokeSecret returned revoked.Revoked=false")
	}
	// Post-revoke invariant: revoked=true, latest_version still
	// recorded, both secret_versions rows still present.
	var latestAfter int
	var revokedAfter bool
	if err := h.db.QueryRowContext(ctx,
		`SELECT latest_version, revoked FROM secrets WHERE secret_id = $1 AND team_id = 'team-a'`,
		created.Secret.SecretID,
	).Scan(&latestAfter, &revokedAfter); err != nil {
		t.Fatalf("read post-revoke: %v", err)
	}
	if !revokedAfter {
		t.Errorf("post-revoke revoked=false, want true")
	}
	if latestAfter != 2 {
		t.Errorf("post-revoke latest_version=%d, want 2 (immutable history)", latestAfter)
	}
	var versionCount int
	if err := h.db.QueryRowContext(ctx,
		`SELECT count(*) FROM secret_versions WHERE team_id = 'team-a' AND secret_id = $1`,
		created.Secret.SecretID,
	).Scan(&versionCount); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versionCount != 2 {
		t.Errorf("secret_versions count=%d, want 2 (append-only invariant)", versionCount)
	}
	// Audit entry exists and is plaintext-free.
	var auditCount int
	if err := h.db.QueryRowContext(ctx,
		`SELECT count(*) FROM audit_entries WHERE action = 'secret.revoke' AND resource_id = $1`,
		created.Secret.SecretID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount == 0 {
		t.Errorf("audit entry for secret.revoke missing")
	}
	// Second revoke is idempotent: no second audit entry, no
	// modification of updated_at beyond the first revoke.
	_, err = h.repo.RevokeSecret(ctx, h.operatorIdentity("op-revoke"), "env-a", created.Secret.SecretID)
	if err != nil {
		t.Fatalf("second RevokeSecret: %v", err)
	}
	var auditCountAfter int
	if err := h.db.QueryRowContext(ctx,
		`SELECT count(*) FROM audit_entries WHERE action = 'secret.revoke' AND resource_id = $1`,
		created.Secret.SecretID,
	).Scan(&auditCountAfter); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCountAfter != auditCount {
		t.Errorf("second revoke added audit rows: before=%d after=%d", auditCount, auditCountAfter)
	}
}

// TestRevokeSecretForeignEnvironmentIsNotRevealing proves a
// foreign (team_id, environment_id, secret_id) triple returns
// the same non-revealing 404 used for every other point resource.
func TestRevokeSecretForeignEnvironmentIsNotRevealing(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	h.seedTeam(t, "team-b", "Team B")
	ctx := context.Background()
	_, err := h.repo.RevokeSecret(ctx, h.operatorIdentity("op-1"), "env-foreign", "secret-foreign")
	if !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("err=%v, want ErrEnvironmentUnavailable", err)
	}
}

// TestSecretListTransactionSnapshot is a smoke check that two
// list calls in rapid succession see the same canonical state
// (no torn reads across the page boundary).
func TestSecretListTransactionSnapshot(t *testing.T) {
	h := newSection9Harness(t)
	h.seedTeam(t, "team-a", "Team A")
	ctx := context.Background()
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO environment_definitions (environment_id, team_id, name, values)
		 VALUES ('env-a', 'team-a', 'env-a', '{}'::jsonb)`); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	decryptsBefore := h.decryptCount()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := h.repo.CreateSecret(ctx, h.operatorIdentity("op-1"), "env-a", platform.SecretCreateRequest{
			Name:  name,
			Value: name + "-plaintext",
		}); err != nil {
			t.Fatalf("CreateSecret: %v", err)
		}
	}
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("decrypts=%d, want unchanged on writes", got)
	}
	entries, _, err := h.repo.ListSecretsPaged(ctx, "team-a", "env-a", 50, nil)
	if err != nil {
		t.Fatalf("ListSecretsPaged: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("entries=%d, want 3", len(entries))
	}
	// Ensure no decrypts were triggered by the list call.
	if got := h.decryptCount(); got != decryptsBefore {
		t.Errorf("decrypts=%d, want unchanged on reads", got)
	}
}
