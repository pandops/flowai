package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

type uiLaunchParameterRepo struct {
	identity platform.GatewayIdentity
	write    platform.LaunchParameterWrite
	getErr   error
	limit    int
}

func (r *uiLaunchParameterRepo) CreateLaunchParameters(_ context.Context, identity platform.GatewayIdentity, write platform.LaunchParameterWrite) (platform.LaunchParameterDefinition, error) {
	r.identity, r.write = identity, write
	return platform.LaunchParameterDefinition{EnvironmentID: "env-1", TeamID: identity.TeamID, LaunchParameterWrite: write}, nil
}
func (r *uiLaunchParameterRepo) ListLaunchParameters(_ context.Context, teamID, _ string, _ *string, limit int) ([]platform.LaunchParameterDefinition, error) {
	r.identity.TeamID, r.limit = teamID, limit
	return []platform.LaunchParameterDefinition{}, nil
}
func (r *uiLaunchParameterRepo) GetLaunchParameters(context.Context, string, string) (platform.LaunchParameterDefinition, error) {
	return platform.LaunchParameterDefinition{}, r.getErr
}
func (r *uiLaunchParameterRepo) ReplaceLaunchParameters(context.Context, platform.GatewayIdentity, string, platform.LaunchParameterWrite) (platform.LaunchParameterDefinition, error) {
	return platform.LaunchParameterDefinition{}, nil
}
func (r *uiLaunchParameterRepo) DeleteLaunchParameters(context.Context, platform.GatewayIdentity, string) error {
	return nil
}
func (r *uiLaunchParameterRepo) ListLaunchParameterRevisions(context.Context, string, string, int) ([]platform.LaunchParameterRevision, error) {
	return []platform.LaunchParameterRevision{}, nil
}

func TestUILaunchParametersCreateUsesPathTeamWithoutAuthHeaders(t *testing.T) {
	repo := &uiLaunchParameterRepo{}
	router := newUILaunchParameterTestRouter(repo)
	req := httptest.NewRequest(http.MethodPost, "/ui/v1/teams/team-alpha/launch-parameters", strings.NewReader(`{
		"scope":"task_type","task_type_id":"type-a","name":"OpenHands","env":{"MODEL":"test"},
		"image":"registry.example/agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}`))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if repo.identity.TeamID != "team-alpha" || repo.identity.OperatorID != "web-ui" {
		t.Fatalf("identity = %#v, want unauthenticated web UI scoped to team-alpha", repo.identity)
	}
	if repo.write.TaskTypeID == nil || *repo.write.TaskTypeID != "type-a" {
		t.Fatalf("task_type_id = %#v, want type-a", repo.write.TaskTypeID)
	}
}

func TestUILaunchParametersRejectTaskScopeAndUnsupportedLimit(t *testing.T) {
	repo := &uiLaunchParameterRepo{}
	router := newUILaunchParameterTestRouter(repo)
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPost, "/ui/v1/teams/team-alpha/launch-parameters", `{"scope":"task","name":"bad","env":{}}`},
		{http.MethodGet, "/ui/v1/teams/team-alpha/launch-parameters?limit=20", ""},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s %s status = %d, want 400", tc.method, tc.target, recorder.Code)
		}
	}
}

func TestUILaunchParametersForeignResourceIsNonRevealing(t *testing.T) {
	repo := &uiLaunchParameterRepo{getErr: store.ErrEnvironmentUnavailable}
	router := newUILaunchParameterTestRouter(repo)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/ui/v1/teams/team-alpha/launch-parameters/team-beta-env", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "team-beta") {
		t.Fatalf("non-revealing response contains foreign identifier: %s", recorder.Body.String())
	}
}

func TestUILaunchParametersDefaultLimitIsTen(t *testing.T) {
	repo := &uiLaunchParameterRepo{}
	router := newUILaunchParameterTestRouter(repo)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/ui/v1/teams/team-alpha/launch-parameters", nil))
	if recorder.Code != http.StatusOK || repo.limit != 10 {
		t.Fatalf("status/limit = %d/%d, want 200/10", recorder.Code, repo.limit)
	}
}

func newUILaunchParameterTestRouter(repo store.LaunchParameterRepository) http.Handler {
	router := chi.NewRouter()
	RegisterUILaunchParameters(router, slog.New(slog.NewTextHandler(io.Discard, nil)), repo)
	return router
}

var _ store.LaunchParameterRepository = (*uiLaunchParameterRepo)(nil)
