//go:build integration

package test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

func TestV0007TeamLifecyclePersistsExactStateAndAudit(t *testing.T) {
	db := migratedDB(t)
	repository := store.New(db)
	ctx := context.Background()
	admin := platform.AdminIdentity{Subject: "admin-subject", RequestID: "request-create"}
	firstImage := "registry.example/agent@sha256:" + repeatHex("a")
	team, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Original Name", DefaultImage: firstImage}, admin)
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.IngestedAt.IsZero() || team.ArchivedAt != nil {
		t.Fatalf("created team timestamps=%+v", team)
	}

	secondImage := "registry.example/agent@sha512:" + repeatHex("b")
	renamed := "Renamed Team"
	updated, err := repository.UpdateTeam(ctx, team.TeamID, platform.UpdateTeamRequest{TeamName: &renamed, DefaultImage: &secondImage}, platform.AdminIdentity{Subject: "admin-subject", RequestID: "request-update"})
	if err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	if updated.TeamID != team.TeamID || updated.TeamName != renamed || updated.DefaultImage != secondImage || !updated.IngestedAt.Equal(team.IngestedAt) {
		t.Fatalf("updated team=%+v, original=%+v", updated, team)
	}
	if _, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Original Name", DefaultImage: firstImage}, admin); err != nil {
		t.Fatalf("released old name was not reusable: %v", err)
	}

	archived, created, err := repository.ArchiveTeam(ctx, team.TeamID, platform.AdminIdentity{Subject: "admin-subject", RequestID: "request-archive"})
	if err != nil || !created || archived.ArchivedAt == nil {
		t.Fatalf("first archive team=%+v created=%v err=%v", archived, created, err)
	}
	retry, created, err := repository.ArchiveTeam(ctx, team.TeamID, platform.AdminIdentity{Subject: "admin-subject", RequestID: "request-archive-retry"})
	if err != nil || created || retry.ArchivedAt == nil || !retry.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Fatalf("archive retry team=%+v created=%v err=%v", retry, created, err)
	}

	var dbName, dbImage string
	var archivedSet bool
	if err := db.QueryRow(`SELECT team_name, default_image, archived_at IS NOT NULL FROM teams WHERE team_id=$1`, team.TeamID).Scan(&dbName, &dbImage, &archivedSet); err != nil {
		t.Fatalf("read exact team row: %v", err)
	}
	if dbName != renamed || dbImage != secondImage || !archivedSet {
		t.Fatalf("database team name=%q image=%q archived=%v", dbName, dbImage, archivedSet)
	}
	var oldName, newName string
	if err := db.QueryRow(`SELECT old_team_name, new_team_name FROM team_audit_details details JOIN audit_entries audit USING (audit_id) WHERE audit.team_id=$1 AND audit.action='team.update'`, team.TeamID).Scan(&oldName, &newName); err != nil {
		t.Fatalf("read update audit: %v", err)
	}
	if oldName != "Original Name" || newName != renamed {
		t.Fatalf("audit old=%q new=%q", oldName, newName)
	}
}

func TestV0007ConcurrentTeamNameWritersHaveOneWinner(t *testing.T) {
	db := migratedDB(t)
	repository := store.New(db)
	ctx := context.Background()
	image := "registry.example/agent@sha256:" + repeatHex("c")
	left, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Left", DefaultImage: image}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	right, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Right", DefaultImage: image}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	winnerName := "One Winner"
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, teamID := range []string{left.TeamID, right.TeamID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, err := repository.UpdateTeam(ctx, id, platform.UpdateTeamRequest{TeamName: &winnerName}, platform.AdminIdentity{Subject: "admin"})
			results <- err
		}(teamID)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, store.ErrTeamNameConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected writer error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM teams WHERE team_name=$1`, winnerName).Scan(&count); err != nil || count != 1 {
		t.Fatalf("winning rows=%d err=%v", count, err)
	}
}

func TestV0007ArchivedTeamRejectsNewAndDedupeTaskIngestion(t *testing.T) {
	db := migratedDB(t)
	repository := store.New(db)
	ctx := context.Background()
	team, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Archive ingestion", DefaultImage: "registry.example/agent@sha256:" + repeatHex("d")}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := repository.CreateSourceSystem(ctx, platform.CreateSourceSystemRequest{TeamID: team.TeamID, ListenerIdentity: "listener-archive"}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	taskType, err := repository.CreateTaskType(ctx, platform.CreateTaskTypeRequest{TeamID: team.TeamID, ExecutionTag: "openhands"}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	identity := platform.ListenerIdentity{TeamID: team.TeamID, SourceSystemID: source.SourceSystemID, Identity: source.ListenerIdentity}
	request := platform.TaskIngestionRequest{TeamID: team.TeamID, SourceSystemID: source.SourceSystemID, SourceID: "existing-source", TaskTypeID: taskType.TaskTypeID, Payload: json.RawMessage(`{"value":"original"}`)}
	existing, created, err := repository.IngestTask(ctx, request, identity)
	if err != nil || !created {
		t.Fatalf("pre-archive ingest created=%v err=%v", created, err)
	}
	if _, _, err := repository.ArchiveTeam(ctx, team.TeamID, platform.AdminIdentity{Subject: "admin"}); err != nil {
		t.Fatal(err)
	}
	executorID := "executor-archived-team"
	identityExecutor := platform.ExecutorIdentity{ExecutorID: executorID, Scope: platform.ExecutorScopeTeam, TeamID: &team.TeamID}
	if _, err := repository.RegisterExecutor(ctx, executorID, platform.ExecutorRegistrationRequest{Scope: platform.ExecutorScopeTeam, TeamID: &team.TeamID, ExecutorType: "executor_docker_openhands", AuthorizedTag: "openhands", MaxCapacity: 1, RuntimeMetadata: json.RawMessage(`{}`)}, identityExecutor); err != nil {
		t.Fatalf("register executor: %v", err)
	}
	claim, err := repository.ClaimTask(ctx, platform.ClaimRequest{TaskID: existing.TaskID, CommandID: "command-archived"}, executorID, identityExecutor)
	if err != nil {
		t.Fatalf("claim pending task after archive: %v", err)
	}
	if claim.Task.TeamID != team.TeamID || claim.ResolvedImage == nil || claim.ResolvedImage.Repository != "registry.example/agent" || claim.ImageSource == nil || *claim.ImageSource != platform.ImageSourceTeamDefault {
		t.Fatalf("archived claim=%+v", claim)
	}
	request.Payload = json.RawMessage(`{"value":"changed"}`)
	if _, _, err := repository.IngestTask(ctx, request, identity); !errors.Is(err, store.ErrTeamArchived) {
		t.Fatalf("dedupe retry error=%v, want ErrTeamArchived", err)
	}
	request.SourceID = "new-source"
	if _, _, err := repository.IngestTask(ctx, request, identity); !errors.Is(err, store.ErrTeamArchived) {
		t.Fatalf("new ingest error=%v, want ErrTeamArchived", err)
	}
	var count int
	var payload string
	if err := db.QueryRow(`SELECT count(*), min(payload::text) FROM tasks WHERE team_id=$1`, team.TeamID).Scan(&count, &payload); err != nil {
		t.Fatal(err)
	}
	if count != 1 || existing.TaskID == "" || payload != `{"value": "original"}` {
		t.Fatalf("count=%d task=%q payload=%s", count, existing.TaskID, payload)
	}
}

func repeatHex(value string) string {
	result := ""
	for i := 0; i < 64; i++ {
		result += value
	}
	return result
}
