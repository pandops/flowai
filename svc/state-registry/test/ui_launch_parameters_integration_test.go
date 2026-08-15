//go:build integration

package test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

func TestUILaunchParametersScopesHistoryAndIsolation(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	repository := store.New(db)
	ctx := context.Background()
	image := platform.ImageReference{Repository: "registry.example/agent", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	alpha, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Alpha", DefaultImage: image}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	beta, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Beta", DefaultImage: image}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	alphaType, err := repository.CreateTaskType(ctx, platform.CreateTaskTypeRequest{TeamID: alpha.TeamID, ExecutionTag: "openhands"}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create alpha task type: %v", err)
	}
	betaType, err := repository.CreateTaskType(ctx, platform.CreateTaskTypeRequest{TeamID: beta.TeamID, ExecutionTag: "openhands"}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create beta task type: %v", err)
	}

	identity := platform.GatewayIdentity{TeamID: alpha.TeamID, OperatorID: "web-ui", RequestID: "request-1"}
	teamDefinition, err := repository.CreateLaunchParameters(ctx, identity, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTeam, Name: "Team defaults", Env: map[string]string{"SHARED": "team"},
	})
	if err != nil {
		t.Fatalf("create team launch parameters: %v", err)
	}
	taskTypeImage := "registry.example/openhands@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	taskTypeDefinition, err := repository.CreateLaunchParameters(ctx, identity, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTaskType, TaskTypeID: &alphaType.TaskTypeID,
		Name: "OpenHands", Env: map[string]string{"SHARED": "task-type"}, Image: &taskTypeImage,
	})
	if err != nil {
		t.Fatalf("create task-type launch parameters: %v", err)
	}
	_, err = repository.CreateLaunchParameters(ctx, identity, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTaskType, TaskTypeID: &betaType.TaskTypeID,
		Name: "Foreign", Env: map[string]string{},
	})
	if !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("foreign task type error = %v, want ErrEnvironmentUnavailable", err)
	}

	identity.RequestID = "request-2"
	taskTypeDefinition, err = repository.ReplaceLaunchParameters(ctx, identity, taskTypeDefinition.EnvironmentID, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTaskType, TaskTypeID: &alphaType.TaskTypeID,
		Name: "OpenHands", Env: map[string]string{"SHARED": "changed"}, Image: &taskTypeImage,
	})
	if err != nil {
		t.Fatalf("replace task-type launch parameters: %v", err)
	}
	if taskTypeDefinition.Revision != 2 {
		t.Fatalf("revision = %d, want 2", taskTypeDefinition.Revision)
	}
	identity.RequestID = "request-3"
	if err := repository.DeleteLaunchParameters(ctx, identity, taskTypeDefinition.EnvironmentID); err != nil {
		t.Fatalf("delete task-type launch parameters: %v", err)
	}
	revisions, err := repository.ListLaunchParameterRevisions(ctx, alpha.TeamID, taskTypeDefinition.EnvironmentID, 10)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 3 || !revisions[0].Deleted || revisions[0].Revision != 3 {
		t.Fatalf("revisions = %#v, want revision 3 tombstone plus immutable history", revisions)
	}
	if _, err := repository.GetLaunchParameters(ctx, beta.TeamID, teamDefinition.EnvironmentID); !errors.Is(err, store.ErrEnvironmentUnavailable) {
		t.Fatalf("cross-team get error = %v, want non-revealing unavailable", err)
	}
}

func TestClaimFreezesMergedLaunchParametersAndImage(t *testing.T) {
	db := freshDB(t)
	applyMigrations(t, db)
	keyring, err := store.NewScopeTokenKeyring("scope-v1", strings.Repeat("11", 32), "", store.ScopeTokenAlgHS256)
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	repository := store.NewWithScopeTokenKeyring(db, make([]byte, 32), keyring)
	ctx := context.Background()
	defaultImage := platform.ImageReference{Repository: "registry.example/default", Digest: "sha256:" + strings.Repeat("a", 64)}
	team, err := repository.CreateTeam(ctx, platform.CreateTeamRequest{TeamName: "Alpha", DefaultImage: defaultImage}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	source, err := repository.CreateSourceSystem(ctx, platform.CreateSourceSystemRequest{TeamID: team.TeamID, ListenerIdentity: "listener"}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	taskType, err := repository.CreateTaskType(ctx, platform.CreateTaskTypeRequest{TeamID: team.TeamID, ExecutionTag: "openhands"}, platform.AdminIdentity{Subject: "admin"})
	if err != nil {
		t.Fatalf("create task type: %v", err)
	}
	mustExec(t, db, `INSERT INTO environment_definitions
		(environment_id, team_id, scope_kind, name, values)
		VALUES ('global-env', NULL, 'global', 'Global', '{"GLOBAL":"yes","SHARED":"global"}'::jsonb)`)
	identity := platform.GatewayIdentity{TeamID: team.TeamID, OperatorID: "web-ui", RequestID: "request"}
	teamDefinition, err := repository.CreateLaunchParameters(ctx, identity, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTeam, Name: "Team", Env: map[string]string{"TEAM": "yes", "SHARED": "team"},
	})
	if err != nil {
		t.Fatalf("create team parameters: %v", err)
	}
	secret, err := repository.CreateSecret(ctx, identity, teamDefinition.EnvironmentID, platform.SecretCreateRequest{Name: "API_TOKEN", Value: "first-secret"})
	if err != nil {
		t.Fatalf("create team secret: %v", err)
	}
	launchImage := "registry.example/openhands@sha256:" + strings.Repeat("b", 64)
	_, err = repository.CreateLaunchParameters(ctx, identity, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTaskType, TaskTypeID: &taskType.TaskTypeID,
		Name: "Type", Env: map[string]string{"TYPE": "yes", "SHARED": "type"}, Image: &launchImage,
	})
	if err != nil {
		t.Fatalf("create task-type parameters: %v", err)
	}
	mustExec(t, db, `INSERT INTO executors
		(executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
		VALUES ('exec-a', 'team', $1, 'executor_docker_openhands', 'exec-a', 'openhands', 1, 0)`, team.TeamID)
	mustExec(t, db, `INSERT INTO tasks
		(task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload)
		VALUES ('task-a', $1, $2, 'external-a', $3, 'openhands', '{}'::jsonb)`, team.TeamID, source.SourceSystemID, taskType.TaskTypeID)
	executorIdentity := platform.ExecutorIdentity{ExecutorID: "exec-a", Scope: platform.ExecutorScopeTeam, TeamID: &team.TeamID, RequestID: "claim"}
	claim, err := repository.ClaimTask(ctx, platform.ClaimRequest{TaskID: "task-a", CommandID: "command-a"}, "exec-a", executorIdentity)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if !claim.LaunchParameters || claim.ScopeToken == nil {
		t.Fatalf("claim launch parameters/token = %v/%v", claim.LaunchParameters, claim.ScopeToken)
	}
	if claim.ImageSource == nil || *claim.ImageSource != platform.ImageSourceTaskTypeParameters {
		t.Fatalf("image source = %v, want task-type launch parameters", claim.ImageSource)
	}
	opened, err := repository.OpenEnvironment(ctx, store.OpenEnvironmentRequest{
		TaskID: "task-a", Token: *claim.ScopeToken, Identity: executorIdentity, RequestID: "open",
	})
	if err != nil {
		t.Fatalf("open launch parameters: %v", err)
	}
	want := map[string]string{"GLOBAL": "yes", "TEAM": "yes", "TYPE": "yes", "SHARED": "type", "API_TOKEN": "first-secret"}
	for key, value := range want {
		if opened.Values[key] != value {
			t.Errorf("values[%s] = %q, want %q", key, opened.Values[key], value)
		}
	}
	identity.RequestID = "later-edit"
	teamDefinitions, err := repository.ListLaunchParameters(ctx, team.TeamID, platform.LaunchParameterScopeTeam, nil, 10)
	if err != nil || len(teamDefinitions) != 1 {
		t.Fatalf("list team parameters: %v / %d", err, len(teamDefinitions))
	}
	_, err = repository.ReplaceLaunchParameters(ctx, identity, teamDefinitions[0].EnvironmentID, platform.LaunchParameterWrite{
		Scope: platform.LaunchParameterScopeTeam, Name: "Team", Env: map[string]string{"TEAM": "changed"},
	})
	if err != nil {
		t.Fatalf("edit after claim: %v", err)
	}
	if _, err := repository.ReplaceSecret(ctx, identity, teamDefinition.EnvironmentID, secret.Secret.SecretID, platform.SecretReplaceRequest{Value: "second-secret"}); err != nil {
		t.Fatalf("replace secret after claim: %v", err)
	}
	if _, err := repository.RevokeSecret(ctx, identity, teamDefinition.EnvironmentID, secret.Secret.SecretID); err != nil {
		t.Fatalf("revoke secret after claim: %v", err)
	}
	currentDefinitions, err := repository.ListLaunchParameters(ctx, team.TeamID, platform.LaunchParameterScopeTeam, nil, 10)
	if err != nil || len(currentDefinitions) != 1 {
		t.Fatalf("list parameters after revoke: %v / %d", err, len(currentDefinitions))
	}
	if len(currentDefinitions[0].Secrets) != 0 {
		t.Fatalf("revoked secret remains in current UI projection: %#v", currentDefinitions[0].Secrets)
	}
	openedAgain, err := repository.OpenEnvironment(ctx, store.OpenEnvironmentRequest{
		TaskID: "task-a", Token: *claim.ScopeToken, Identity: executorIdentity, RequestID: "open-again",
	})
	if err != nil {
		t.Fatalf("reopen snapshot: %v", err)
	}
	if openedAgain.Values["TEAM"] != "yes" {
		t.Fatalf("snapshot changed after definition edit: %#v", openedAgain.Values)
	}
	if openedAgain.Values["API_TOKEN"] != "first-secret" {
		t.Fatalf("snapshot secret version changed after replacement: %#v", openedAgain.Values)
	}
}
