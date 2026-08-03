package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

type taskEventRepo struct {
	events []platform.TaskEvent
	err    error
	calls  int
	teamID string
	taskID string
}

func (r *taskEventRepo) ListTaskEvents(_ context.Context, teamID, taskID string) ([]platform.TaskEvent, error) {
	r.calls++
	r.teamID = teamID
	r.taskID = taskID
	return r.events, r.err
}

func (r *taskEventRepo) AppendTaskEvent(_ context.Context, req platform.TaskEventAppendRequest, _ platform.ExecutorIdentity) (platform.TaskEvent, error) {
	if r.err != nil {
		return platform.TaskEvent{}, r.err
	}
	event := platform.TaskEvent{
		EventID:    req.EventID,
		TeamID:     req.TeamID,
		TaskID:     req.TaskID,
		ExecutorID: &req.ExecutorID,
		EventType:  req.EventType,
		OccurredAt: req.OccurredAt,
		Payload:    req.Payload,
	}
	r.events = append(r.events, event)
	return event, nil
}

func taskEventServer(repo store.TaskEventRepository) *httptest.Server {
	router := chi.NewRouter()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpapi.RegisterTaskEventReads(router, logger, repo)
	return httptest.NewServer(router)
}

func gatewayEventRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-FlowAI-Role", "gateway")
	req.Header.Set("X-FlowAI-Team-Id", "team-a")
	req.Header.Set("X-FlowAI-Operator-Id", "operator-a")
	return req
}

func TestTaskEventHistoryReturnsEmptyPage(t *testing.T) {
	repo := &taskEventRepo{events: []platform.TaskEvent{}}
	server := taskEventServer(repo)
	defer server.Close()

	resp, err := http.DefaultClient.Do(gatewayEventRequest(t, http.MethodGet, server.URL+"/v1/tasks/task-a/events"))
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var page platform.TaskEventPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.Page.Count != 0 {
		t.Fatalf("page=%+v, want non-nil empty items and count 0", page)
	}
	if repo.teamID != "team-a" || repo.taskID != "task-a" {
		t.Fatalf("repo scope=(%q,%q), want (team-a,task-a)", repo.teamID, repo.taskID)
	}
}

func TestTaskEventHistoryRejectsNonGatewayBeforeRepository(t *testing.T) {
	repo := &taskEventRepo{}
	server := taskEventServer(repo)
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/tasks/task-a/events")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
	if repo.calls != 0 {
		t.Fatalf("repository calls=%d, want 0", repo.calls)
	}
}

func TestTaskEventHistoryHidesUnknownAndForeignTask(t *testing.T) {
	repo := &taskEventRepo{err: store.ErrTaskNotFound}
	server := taskEventServer(repo)
	defer server.Close()

	resp, err := http.DefaultClient.Do(gatewayEventRequest(t, http.MethodGet, server.URL+"/v1/tasks/task-foreign/events"))
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body["code"] != "task_not_found" {
		t.Fatalf("code=%v, want task_not_found", body["code"])
	}
}
