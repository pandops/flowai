//go:build integration

package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

func TestAdminCreateTeam(t *testing.T) {
	db := migratedDB(t)
	repo := store.New(db)
	ctx := context.Background()

	team, err := repo.CreateTeam(ctx, platform.CreateTeamRequest{
		TeamName:     "Team Store",
		DefaultImage: adminTestImage(),
	}, platform.AdminIdentity{Subject: "admin-store", RequestID: "req-store-team"})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.TeamID == "" || team.TeamName != "Team Store" {
		t.Fatalf("CreateTeam returned unexpected team: %+v", team)
	}
	if team.CreatedAt.IsZero() || team.UpdatedAt.IsZero() {
		t.Fatalf("CreateTeam timestamps must be populated: %+v", team)
	}

	_, err = repo.CreateTeam(ctx, platform.CreateTeamRequest{
		TeamName:     "Team Store",
		DefaultImage: adminTestImage(),
	}, platform.AdminIdentity{Subject: "admin-store", RequestID: "req-store-team-duplicate"})
	if !errors.Is(err, store.ErrTeamNameConflict) {
		t.Fatalf("duplicate CreateTeam error=%v, want ErrTeamNameConflict", err)
	}
	assertRowCount(t, db, "teams", 1)
}

func TestAdminCreateSourceSystem(t *testing.T) {
	db := migratedDB(t)
	mustInsertAdminTeam(t, db, "team-source", "Team Source")
	repo := store.New(db)

	source, err := repo.CreateSourceSystem(context.Background(), platform.CreateSourceSystemRequest{
		TeamID:           "team-source",
		ListenerIdentity: "listener-store",
	}, platform.AdminIdentity{Subject: "admin-store", RequestID: "req-store-source"})
	if err != nil {
		t.Fatalf("CreateSourceSystem: %v", err)
	}
	if source.SourceSystemID == "" || source.TeamID != "team-source" || source.ListenerIdentity != "listener-store" {
		t.Fatalf("CreateSourceSystem returned unexpected source: %+v", source)
	}
	if source.DefaultImage != nil {
		t.Fatalf("CreateSourceSystem default_image=%+v, want nil", source.DefaultImage)
	}
}

func TestAdminCreateTaskType(t *testing.T) {
	db := migratedDB(t)
	mustInsertAdminTeam(t, db, "team-type", "Team Type")
	repo := store.New(db)

	taskType, err := repo.CreateTaskType(context.Background(), platform.CreateTaskTypeRequest{
		TeamID:       "team-type",
		ExecutionTag: "openhands",
	}, platform.AdminIdentity{Subject: "admin-store", RequestID: "req-store-type"})
	if err != nil {
		t.Fatalf("CreateTaskType: %v", err)
	}
	if taskType.TaskTypeID == "" || taskType.TeamID != "team-type" || taskType.ExecutionTag != "openhands" {
		t.Fatalf("CreateTaskType returned unexpected task type: %+v", taskType)
	}
}

func TestAdminSourceSystemRejectsDuplicateListenerIdentity(t *testing.T) {
	testDuplicateListenerIdentity(t, "team-a")
}

func TestAdminSourceSystemRejectsCrossTeamListenerIdentity(t *testing.T) {
	testDuplicateListenerIdentity(t, "team-b")
}

func testDuplicateListenerIdentity(t *testing.T, duplicateTeamID string) {
	t.Helper()
	db := migratedDB(t)
	mustInsertAdminTeam(t, db, "team-a", "Team A")
	mustInsertAdminTeam(t, db, "team-b", "Team B")
	repo := store.New(db)
	ctx := context.Background()
	identity := platform.AdminIdentity{Subject: "admin-store", RequestID: "req-store-listener"}

	if _, err := repo.CreateSourceSystem(ctx, platform.CreateSourceSystemRequest{
		TeamID: "team-a", ListenerIdentity: "listener-unique",
	}, identity); err != nil {
		t.Fatalf("seed source system: %v", err)
	}
	_, err := repo.CreateSourceSystem(ctx, platform.CreateSourceSystemRequest{
		TeamID: duplicateTeamID, ListenerIdentity: "listener-unique",
	}, identity)
	if !errors.Is(err, store.ErrListenerIdentityConflict) {
		t.Fatalf("duplicate listener error=%v, want ErrListenerIdentityConflict", err)
	}
	assertRowCount(t, db, "source_systems", 1)
}

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db := freshDB(t)
	applyMigrations(t, db)
	preflight(t, db)
	return db
}

func mustInsertAdminTeam(t *testing.T, db *sql.DB, teamID, teamName string) {
	t.Helper()
	image, err := json.Marshal(adminTestImage())
	if err != nil {
		t.Fatalf("marshal admin test image: %v", err)
	}
	mustExec(t, db, `INSERT INTO teams (team_id, team_name, default_image) VALUES ($1, $2, $3::jsonb)`, teamID, teamName, image)
}

func assertRowCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	if !isSafeIdentifier(table) {
		t.Fatalf("assertRowCount: unsafe table %q", table)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM "%s"`, table)).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s row count=%d, want %d", table, got, want)
	}
}

func adminTestImage() platform.ImageReference {
	return platform.ImageReference{
		Repository: "registry.example/agent:stable",
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}
}
