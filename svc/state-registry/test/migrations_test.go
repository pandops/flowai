//go:build integration

// Package test holds tagged PostgreSQL-backed integration tests for
// the State Registry. These tests are NOT part of the default
// `go test ./...` run; they require the `integration` build tag plus
// a working Docker or Podman CLI plus the `postgres:16` image (or a
// usable local mirror).
//
// The harness targets OpenSpec Section 2: it starts an ephemeral
// PostgreSQL 16 container, applies the service-local Goose migrations,
// and verifies the normalized domain schema and constraints.
package test

import (
	"context"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/migrations"
)

// domainTables is the authoritative Section 2 list of canonical
// State Registry tables (excluding Goose metadata). Assertions compare
// against this list so the harness never drifts
// from the OpenSpec contract.
var domainTables = []string{
	"teams",
	"executors",
	"tasks",
	"task_events",
	"executor_events",
	"environment_definitions",
	"secrets",
	"secret_versions",
	"audit_entries",
	"task_control_requests",
	"source_systems",
	"task_types",
	"environment_revisions",
	"task_control_events",
	"task_log_chunks",
	"task_launch_parameter_snapshots",
	"task_launch_parameter_secret_refs",
}

// ---------------------------------------------------------------------------
// Section 2 migration tests
// ---------------------------------------------------------------------------

// TestMigrations proves the migration runner applies the complete domain schema.
func TestMigrations(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := migrations.CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("read goose current version: %v", err)
	}
	if version != 3 {
		t.Fatalf("goose current version = %d, want 3 (v0007 schema applied)", version)
	}

	for _, table := range domainTables {
		if !tableExists(t, db, table) {
			t.Errorf("migration did not create canonical State Registry domain table %q", table)
		}
	}
}

// TestTeamOwnedSchema asserts the canonical domain tables are present.
func TestTeamOwnedSchema(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	for _, table := range domainTables {
		if !tableExists(t, db, table) {
			t.Errorf("missing canonical State Registry domain table %q (Section 2 requires all 12 tables)", table)
			return
		}
	}
}

// TestNormalizedRelationships asserts the Section 2 normalized
// parent relationships and behavioral same-team rejection plan. It
// asserts (a) every team-owning child table has a `team_id` column
// referencing the `teams` table and (b) the catalog reports each
// expected foreign-key constraint name as defined in the migration.
// Failures name the exact missing column or constraint.
func TestNormalizedRelationships(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	// teams table is the parent; every team-owned child MUST
	// carry a team_id column referencing teams.
	teamOwnedChildren := []string{
		"executors",
		"tasks",
		"task_events",
		"executor_events",
		"environment_definitions",
		"secrets",
		"secret_versions",
		"audit_entries",
		"task_control_requests",
		"source_systems",
		"task_types",
	}

	for _, child := range teamOwnedChildren {
		if !tableExists(t, db, child) {
			t.Errorf("normalized relationship: missing child table %q (Section 2 requires a team_id column referencing teams)", child)
			continue
		}
		if !columnExists(t, db, child, "team_id") {
			t.Errorf("normalized relationship: missing team_id column on child table %q (Section 2 requires every team-owned row to carry an immutable team_id referencing teams)", child)
		}
	}

	// secrets -> secret_versions relationship is required even when
	// the secrets table itself is present: secret_versions.secret_id
	// references secrets.id and secret_versions.team_id must match
	// secrets.team_id.
	if tableExists(t, db, "secrets") && tableExists(t, db, "secret_versions") {
		if !columnExists(t, db, "secret_versions", "secret_id") {
			t.Errorf("normalized relationship: missing secret_versions.secret_id column referencing secrets")
		}
	}

	relationships := []struct {
		child  string
		parent string
	}{
		{"executors", "teams"},
		{"tasks", "teams"},
		{"tasks", "source_systems"},
		{"tasks", "task_types"},
		{"tasks", "executors"},
		{"task_events", "tasks"},
		{"task_events", "executors"},
		{"executor_events", "executors"},
		{"environment_definitions", "teams"},
		{"environment_definitions", "task_types"},
		{"secrets", "teams"},
		{"secrets", "environment_definitions"},
		{"secret_versions", "secrets"},
		{"audit_entries", "teams"},
		{"task_control_requests", "tasks"},
		{"source_systems", "teams"},
		{"task_types", "teams"},
	}
	for _, relationship := range relationships {
		if !fkFromToExists(t, db, relationship.child, relationship.parent) {
			t.Errorf("normalized relationship: missing foreign key %s -> %s", relationship.child, relationship.parent)
		}
	}
	assertCrossTeamRejections(t, db)
}

// TestPersistenceConstraints asserts the unique / NOT NULL /
// pre-claim-nullable persistence constraints required by Section 2.
// Failures name the missing constraint (unique key on
// (team_id, source_system_id, source_id) on tasks, unique key on
// (task_id, event_id) on task_events, non-empty tag fields, and
// pre-claim-nullable executor_id / owner_command_id on tasks).
func TestPersistenceConstraints(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	// unique (team_id, source_system_id, source_id) on tasks
	if !tableExists(t, db, "tasks") {
		t.Errorf("persistence constraints: tasks table missing (cannot assert unique (team_id, source_system_id, source_id))")
	} else {
		has := indexOnColumns(t, db, "tasks", []string{"team_id", "source_system_id", "source_id"}, true)
		if !has {
			t.Errorf("persistence constraints: missing UNIQUE index on tasks(team_id, source_system_id, source_id)")
		}
	}

	// unique (task_id, event_id) on task_events
	if !tableExists(t, db, "task_events") {
		t.Errorf("persistence constraints: task_events table missing (cannot assert unique (task_id, event_id))")
	} else {
		has := indexOnColumns(t, db, "task_events", []string{"task_id", "event_id"}, true)
		if !has {
			t.Errorf("persistence constraints: missing UNIQUE index on task_events(task_id, event_id)")
		}
		// task_events.executor_id must be NOT NULL.
		if !columnIsNotNull(t, db, "task_events", "executor_id") {
			t.Errorf("persistence constraints: task_events.executor_id is not NOT NULL (Section 2 requires NOT NULL)")
		}
	}

	// executor_events.executor_id must be NOT NULL.
	if !tableExists(t, db, "executor_events") {
		t.Errorf("persistence constraints: executor_events table missing")
	} else if !columnIsNotNull(t, db, "executor_events", "executor_id") {
		t.Errorf("persistence constraints: executor_events.executor_id is not NOT NULL (Section 2 requires NOT NULL)")
	}

	// tasks.executor_id and tasks.owner_command_id MUST be
	// nullable BEFORE claim (claim transitions them to NOT NULL).
	if !tableExists(t, db, "tasks") {
		t.Errorf("persistence constraints: tasks table missing")
	} else {
		if !columnExists(t, db, "tasks", "executor_id") {
			t.Errorf("persistence constraints: tasks.executor_id column missing")
		} else if !columnIsNullable(t, db, "tasks", "executor_id") {
			t.Errorf("persistence constraints: tasks.executor_id is NOT NULL but Section 2 requires pre-claim NULLABLE")
		}
		if !columnExists(t, db, "tasks", "owner_command_id") {
			t.Errorf("persistence constraints: tasks.owner_command_id column missing")
		} else if !columnIsNullable(t, db, "tasks", "owner_command_id") {
			t.Errorf("persistence constraints: tasks.owner_command_id is NOT NULL but Section 2 requires pre-claim NULLABLE")
		}
		if !columnExists(t, db, "tasks", "required_tag") {
			t.Errorf("persistence constraints: tasks.required_tag column missing")
		} else if columnIsNullable(t, db, "tasks", "required_tag") {
			t.Errorf("persistence constraints: tasks.required_tag is NULLABLE but Section 2 requires non-empty tag")
		}
	}

	// executors.authorized_tag MUST exist and be non-empty (NOT NULL).
	if !tableExists(t, db, "executors") {
		t.Errorf("persistence constraints: executors table missing")
	} else if !columnExists(t, db, "executors", "authorized_tag") {
		t.Errorf("persistence constraints: executors.authorized_tag column missing (Section 2 requires exactly one tag per Executor)")
	} else if columnIsNullable(t, db, "executors", "authorized_tag") {
		t.Errorf("persistence constraints: executors.authorized_tag is NULLABLE but Section 2 requires non-empty tag")
	}
	assertPersistenceBehavior(t, db)
}

// TestTaskOwnedEnvironmentForeignKey asserts that
// environment_definitions carries the parent-task same-team
// relationship behaviour Section 2 requires. The test does NOT
// commit to a physical column name (task_id vs parent_task_id);
// GREEN may choose either as long as a column references tasks and
// the same-team rejection is encoded at the constraint level. The test
// Task-type launch parameters must use the composite team/task-type
// relationship; task-owned definitions were removed by v0006.
func TestTaskTypeEnvironmentForeignKey(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	if !tableExists(t, db, "environment_definitions") {
		t.Fatalf("task-type environment: environment_definitions table missing")
	}
	if !tableExists(t, db, "task_types") {
		t.Fatalf("task-type environment: task_types table missing")
	}
	if !fkFromToExists(t, db, "environment_definitions", "task_types") {
		t.Errorf("task-type environment: missing composite ownership reference to task_types")
	}
}

// TestAdminTables asserts the three admin-only onboarding tables
// (teams, source_systems, task_types) exist. Section 2 requires
// these tables to be the only path through which a team, source
// system, or task type is registered, so the harness checks for
// their presence.
func TestAdminTables(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	adminTables := []string{"teams", "source_systems", "task_types"}
	for _, table := range adminTables {
		if !tableExists(t, db, table) {
			t.Errorf("admin tables: missing %q (Section 2 requires /admin/* to create-only onboard teams, source_systems, and task_types through this table)", table)
			return
		}
	}
}

// TestImageColumns asserts the Section 2 image column presence and
// nullability contract:
//   - teams.default_image MUST be NOT NULL.
//   - task_types.default_image and source_systems.default_image MUST
//     exist and be NULLABLE.
//   - tasks.image, tasks.resolved_image, tasks.image_source MUST
//     exist and be NULLABLE.
//
// Failures name the exact column whose presence or nullability
// is wrong.
func TestImageColumns(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	if !tableExists(t, db, "teams") {
		t.Fatalf("image columns: teams table missing (cannot assert default_image NOT NULL)")
	}
	if !columnExists(t, db, "teams", "default_image") {
		t.Errorf("image columns: teams.default_image column missing (Section 2 requires teams.default_image NOT NULL)")
	} else if !columnIsNotNull(t, db, "teams", "default_image") {
		t.Errorf("image columns: teams.default_image is nullable but Section 2 requires NOT NULL")
	}

	for _, table := range []string{"task_types", "source_systems"} {
		if !tableExists(t, db, table) {
			t.Errorf("image columns: %s table missing (cannot assert default_image NULLABLE)", table)
			continue
		}
		if !columnExists(t, db, table, "default_image") {
			t.Errorf("image columns: %s.default_image column missing (Section 2 requires default_image column to exist and be NULLABLE)", table)
			continue
		}
		if !columnIsNullable(t, db, table, "default_image") {
			t.Errorf("image columns: %s.default_image is NOT NULL but Section 2 requires NULLABLE", table)
		}
	}

	if !tableExists(t, db, "tasks") {
		t.Fatalf("image columns: tasks table missing (cannot assert image/resolved_image/image_source NULLABLE)")
	}
	for _, column := range []string{"image", "resolved_image", "image_source"} {
		if !columnExists(t, db, "tasks", column) {
			t.Errorf("image columns: tasks.%s column missing (Section 2 requires the column to exist and be NULLABLE)", column)
			continue
		}
		if !columnIsNullable(t, db, "tasks", column) {
			t.Errorf("image columns: tasks.%s is NOT NULL but Section 2 requires NULLABLE", column)
		}
	}
}

// TestRequiredTeamDefaultImage asserts the required teams.default_image
// constraint and the Goose Up -> DownTo(0) -> Up rollback round trip.
func TestRequiredTeamDefaultImage(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)

	// Exercise rollback before schema assertions so migration plumbing is
	// verified independently from the catalog checks below.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrations.ApplyDownTo(ctx, db, 0); err != nil {
		t.Fatalf("goose down-to 0: %v", err)
	}
	version, err := migrations.CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("read goose current version after down: %v", err)
	}
	if version != 0 {
		t.Fatalf("goose version after down-to 0 = %d, want 0", version)
	}
	if err := migrations.ApplyUp(ctx, db); err != nil {
		t.Fatalf("goose up after down: %v", err)
	}
	version, err = migrations.CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("read goose current version after re-up: %v", err)
	}
	if version != 3 {
		t.Fatalf("goose version after re-up = %d, want 3", version)
	}

	if !tableExists(t, db, "teams") {
		t.Fatalf("required team default image: teams table missing (cannot assert teams.default_image NOT NULL)")
	}
	if !columnExists(t, db, "teams", "default_image") {
		t.Errorf("required team default image: teams.default_image column missing (Section 2 requires default_image to be REQUIRED at /admin/teams registration)")
	} else if !columnIsNotNull(t, db, "teams", "default_image") {
		t.Errorf("required team default image: teams.default_image is nullable but Section 2 requires NOT NULL (REQUIRED at /admin/teams)")
	}
}
