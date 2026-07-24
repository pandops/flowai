//go:build integration

package test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Catalog assertion helpers
// ---------------------------------------------------------------------------

// tableExists reports whether the given table is visible in the
// current database's public schema.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	if !isSafeIdentifier(name) {
		t.Fatalf("tableExists: unsafe identifier %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var exists bool
	row := db.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, name)
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("tableExists %s: %v", name, err)
	}
	return exists
}

// columnExists reports whether the named column exists on the named
// table in the current schema.
func columnExists(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	t.Helper()
	if !isSafeIdentifier(tableName) || !isSafeIdentifier(columnName) {
		t.Fatalf("columnExists: unsafe identifier %q/%q", tableName, columnName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var exists bool
	row := db.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, tableName, columnName)
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("columnExists %s.%s: %v", tableName, columnName, err)
	}
	return exists
}

// columnIsNullable reports whether the named column on the named
// table is nullable (NULL allowed) in the current schema.
func columnIsNullable(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	t.Helper()
	if !isSafeIdentifier(tableName) || !isSafeIdentifier(columnName) {
		t.Fatalf("columnIsNullable: unsafe identifier %q/%q", tableName, columnName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var nullable bool
	row := db.QueryRowContext(ctx,
		`SELECT is_nullable = 'YES' FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		tableName, columnName)
	if err := row.Scan(&nullable); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		t.Fatalf("columnIsNullable %s.%s: %v", tableName, columnName, err)
	}
	return nullable
}

// columnIsNotNull reports whether the named column on the named
// table is present AND required (NOT NULL) in the current schema.
// Returns false when the column is absent so tests can surface
// "column missing" rather than mis-report "nullable".
func columnIsNotNull(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	t.Helper()
	if !columnExists(t, db, tableName, columnName) {
		return false
	}
	return !columnIsNullable(t, db, tableName, columnName)
}

// indexOnColumns returns true if the named table has an index whose
// column list (in order) matches `cols` exactly. When unique is true
// only UNIQUE indexes are considered. PostgreSQL system catalogs are
// queried via parameterized statements; identifiers are validated by
// isSafeIdentifier before reaching SQL.
func indexOnColumns(t *testing.T, db *sql.DB, table string, cols []string, unique bool) bool {
	t.Helper()
	if !isSafeIdentifier(table) {
		t.Fatalf("indexOnColumns: unsafe table %q", table)
	}
	for _, c := range cols {
		if !isSafeIdentifier(c) {
			t.Fatalf("indexOnColumns: unsafe column %q", c)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const q = `
		SELECT 1
		FROM pg_index i
		JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'public'
		  AND t.relname = $1
		  AND ($2::boolean = FALSE OR i.indisunique = TRUE)
		  AND ARRAY(
			    SELECT a.attname::text
		    FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
		    JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		    ORDER BY k.ord
		  ) = $3::text[]
		LIMIT 1`
	row := db.QueryRowContext(ctx, q, table, unique, cols)
	var one int
	if err := row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		t.Fatalf("indexOnColumns %s(%v): %v", table, cols, err)
	}
	return one == 1
}

// fkFromToExists returns true if `src` has any foreign-key
// constraint whose referenced table is `dst` in the public schema.
// Tests use it to express logical relationships without
// pinning physical column names.
func fkFromToExists(t *testing.T, db *sql.DB, src, dst string) bool {
	t.Helper()
	if !isSafeIdentifier(src) || !isSafeIdentifier(dst) {
		t.Fatalf("fkFromToExists: unsafe identifier %q/%q", src, dst)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const q = `
		SELECT 1
		FROM pg_constraint c
		JOIN pg_class s ON s.oid = c.conrelid
		JOIN pg_class r ON r.oid = c.confrelid
		JOIN pg_namespace n ON n.oid = s.relnamespace
		WHERE n.nspname = 'public'
		  AND s.relname = $1
		  AND r.relname = $2
		  AND c.contype = 'f'
		LIMIT 1`
	row := db.QueryRowContext(ctx, q, src, dst)
	var one int
	if err := row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		t.Fatalf("fkFromToExists %s -> %s: %v", src, dst, err)
	}
	return one == 1
}

// ---------------------------------------------------------------------------
// Seed and behavioral assertion helpers
// ---------------------------------------------------------------------------

const testImage = `{"repository":"registry.example/agent","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`

func seedOwnershipFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO teams (team_id, team_name, default_image) VALUES
		('team-a', 'Team A', $1::jsonb), ('team-b', 'Team B', $1::jsonb)`, testImage)
	mustExec(t, db, `INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		VALUES ('source-a', 'team-a', 'listener-a'), ('source-b', 'team-b', 'listener-b')`)
	mustExec(t, db, `INSERT INTO task_types (task_type_id, team_id, execution_tag)
		VALUES ('type-a', 'team-a', 'tag-a'), ('type-b', 'team-b', 'tag-b')`)
	mustExec(t, db, `INSERT INTO executors
		(executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
		VALUES
		('exec-a', 'team', 'team-a', 'test', 'identity-a', 'tag-a', 1, 0),
		('exec-b', 'team', 'team-b', 'test', 'identity-b', 'tag-b', 1, 0),
		('exec-system', 'system', NULL, 'test', 'identity-system', 'tag-a', 1, 0)`)
	mustExec(t, db, `INSERT INTO tasks
		(task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload)
		VALUES ('task-a', 'team-a', 'source-a', 'external-a', 'type-a', 'tag-a', '{}'::jsonb)`)
}

func assertCrossTeamRejections(t *testing.T, db *sql.DB) {
	t.Helper()
	seedOwnershipFixture(t, db)
	mustReject(t, db, "task with foreign source system", `INSERT INTO tasks
		(task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload)
		VALUES ('bad-source', 'team-a', 'source-b', 'external-b', 'type-a', 'tag-a', '{}'::jsonb)`)
	mustReject(t, db, "task with foreign task type", `INSERT INTO tasks
		(task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload)
		VALUES ('bad-type', 'team-a', 'source-a', 'external-c', 'type-b', 'tag-a', '{}'::jsonb)`)
	mustReject(t, db, "task assigned to foreign team executor", `UPDATE tasks
		SET current_state = 'created', owner_command_id = 'command-b', executor_id = 'exec-b',
		resolved_image = $1::jsonb, image_source = 'team_default', claimed_at = now()
		WHERE task_id = 'task-a'`, testImage)
	mustExec(t, db, `INSERT INTO environment_definitions
		(environment_id, team_id, task_id, name) VALUES ('env-a', 'team-a', 'task-a', 'Environment A')`)
	mustReject(t, db, "environment with foreign parent task", `INSERT INTO environment_definitions
		(environment_id, team_id, task_id, name) VALUES ('env-bad', 'team-b', 'task-a', 'Bad Environment')`)
	mustExec(t, db, `INSERT INTO secrets
		(secret_id, team_id, environment_id, name) VALUES ('secret-a', 'team-a', 'env-a', 'Secret A')`)
	mustReject(t, db, "secret with foreign environment", `INSERT INTO secrets
		(secret_id, team_id, environment_id, name) VALUES ('secret-bad', 'team-b', 'env-a', 'Bad Secret')`)
	mustReject(t, db, "secret version with foreign logical secret", `INSERT INTO secret_versions
		(secret_id, version, team_id, ciphertext, nonce, authentication_tag, key_id, key_version)
		VALUES ('secret-a', 1, 'team-b', '\x01', decode(repeat('00', 12), 'hex'),
		decode(repeat('00', 16), 'hex'), 'key', 1)`)
	mustReject(t, db, "task event with foreign team", `INSERT INTO task_events
		(event_id, team_id, task_id, executor_id, event_type, occurred_at)
		VALUES ('event-bad', 'team-b', 'task-a', 'exec-a', 'created', now())`)
	mustReject(t, db, "executor self event with foreign team", `INSERT INTO executor_events
		(event_id, executor_id, team_id, event_type, occurred_at)
		VALUES ('self-bad', 'exec-a', 'team-b', 'healthy', now())`)
	mustReject(t, db, "team executor self event without team", `INSERT INTO executor_events
		(event_id, executor_id, team_id, event_type, occurred_at)
		VALUES ('self-team-null', 'exec-a', NULL, 'healthy', now())`)
	mustReject(t, db, "system executor self event with team", `INSERT INTO executor_events
		(event_id, executor_id, team_id, event_type, occurred_at)
		VALUES ('self-system-team', 'exec-system', 'team-a', 'healthy', now())`)
	mustReject(t, db, "control with foreign task", `INSERT INTO task_control_requests
		(control_id, team_id, task_id, operator_id, action, idempotency_key)
		VALUES ('control-bad', 'team-b', 'task-a', 'operator-b', 'cancel', 'idem-b')`)
}

func assertPersistenceBehavior(t *testing.T, db *sql.DB) {
	t.Helper()
	seedOwnershipFixture(t, db)
	mustReject(t, db, "duplicate source identity", `INSERT INTO source_systems
		(source_system_id, team_id, listener_identity) VALUES ('source-duplicate', 'team-a', 'listener-a')`)
	mustReject(t, db, "duplicate task dedupe key", `INSERT INTO tasks
		(task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload)
		VALUES ('task-duplicate', 'team-a', 'source-a', 'external-a', 'type-a', 'tag-a', '{}'::jsonb)`)
	mustReject(t, db, "empty task tag", `UPDATE tasks SET required_tag = '' WHERE task_id = 'task-a'`)
	mustReject(t, db, "empty executor tag", `UPDATE executors SET authorized_tag = '' WHERE executor_id = 'exec-a'`)
	mustReject(t, db, "nullable team default image", `UPDATE teams SET default_image = NULL WHERE team_id = 'team-a'`)
	mustReject(t, db, "immutable team ownership", `UPDATE source_systems SET team_id = 'team-b' WHERE source_system_id = 'source-a'`)
	mustReject(t, db, "immutable task ingestion timestamp", `UPDATE tasks SET ingested_at = ingested_at + interval '1 second' WHERE task_id = 'task-a'`)
	mustReject(t, db, "non-pending task without complete claim fields", `UPDATE tasks SET current_state = 'created' WHERE task_id = 'task-a'`)
	mustExec(t, db, `UPDATE tasks
		SET current_state = 'created', owner_command_id = 'command-a', executor_id = 'exec-a',
		resolved_image = $1::jsonb, image_source = 'team_default', claimed_at = now()
		WHERE task_id = 'task-a'`, testImage)
	mustReject(t, db, "immutable owner command", `UPDATE tasks SET owner_command_id = 'command-other' WHERE task_id = 'task-a'`)
	mustExec(t, db, `INSERT INTO task_events
		(event_id, team_id, task_id, executor_id, event_type, occurred_at)
		VALUES ('event-a', 'team-a', 'task-a', 'exec-a', 'created', now())`)
	mustReject(t, db, "duplicate task event", `INSERT INTO task_events
		(event_id, team_id, task_id, executor_id, event_type, occurred_at)
		VALUES ('event-a', 'team-a', 'task-a', 'exec-a', 'created', now())`)
	mustReject(t, db, "mutable task event", `UPDATE task_events SET event_type = 'running' WHERE event_id = 'event-a'`)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("expected SQL to succeed: %v", err)
	}
}

func mustReject(t *testing.T, db *sql.DB, behavior, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, query, args...); err == nil {
		t.Errorf("database accepted %s; expected constraint rejection", behavior)
	}
}
