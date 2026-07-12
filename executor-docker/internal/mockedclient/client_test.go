// Unit tests for the mockedclient package. The mocked-task-server is stubbed
// inline using stdlib HTTP — these tests do NOT import the mocked-task-server
// package, since that would violate Go's internal-package boundary between
// sibling services.
package mockedclient_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/mockedclient"
	"github.com/flowai/platform/executor-docker/internal/platform"
)

// minimalStub implements just enough of the mocked-task-server wire format
// for the client tests to verify request shape and response decoding.
type minimalStub struct {
	server *httptest.Server
}

func newStub() *minimalStub {
	stub := &minimalStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks", stub.handleTasks)
	mux.HandleFunc("/v1/tasks/", stub.handleTaskEvents)
	mux.HandleFunc("/v1/executors/", stub.handleExecutors)
	mux.HandleFunc("/v1/env", stub.handleEnv)
	stub.server = httptest.NewServer(mux)
	return stub
}

func (s *minimalStub) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var ev platform.TaskEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"event_id":    ev.EventID,
		"accepted_at": time.Now().UTC(),
	})
}

func (s *minimalStub) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []platform.RouterTask{}})
}

func (s *minimalStub) handleExecutors(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/v1/executors/"):]
	if idx := strings.Index(path, "/"); idx >= 0 {
		id := path[:idx]
		suffix := path[idx+1:]
		switch suffix {
		case "events":
			if r.Method != http.MethodPost {
				http.Error(w, "method", http.StatusMethodNotAllowed)
				return
			}
			var ev platform.ExecutorEvent
			if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ev.ExecutorID = id
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"event_id":    ev.EventID,
				"accepted_at": time.Now().UTC(),
			})
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPut {
		var rec platform.ExecutorRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"executor_id":   path,
			"registered_at": time.Now().UTC(),
		})
		return
	}
	http.NotFound(w, r)
}

func (s *minimalStub) handleEnv(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *minimalStub) URL() string { return s.server.URL }
func (s *minimalStub) Close()      { s.server.Close() }

func TestListTasksEmpty(t *testing.T) {
	stub := newStub()
	defer stub.Close()
	c := mockedclient.New(stub.URL()+"/v1", nil)
	got, err := c.ListTasks(context.Background(), "openhands")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(got))
	}
}

func TestRegisterExecutor(t *testing.T) {
	stub := newStub()
	defer stub.Close()
	c := mockedclient.New(stub.URL()+"/v1", nil)
	rec := &platform.ExecutorRecord{
		ExecutorID:        "exec-test",
		ExecutorType:      platform.ExecutorTypeDockerOpenHands,
		RoutingTarget:     "openhands",
		Capacity:          2,
		RunningChildCount: 0,
		Metadata:          map[string]interface{}{"openhands_image": "img:latest"},
	}
	out, err := c.RegisterExecutor(context.Background(), rec)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if out.ExecutorID != "exec-test" {
		t.Fatalf("executor_id mismatch: %s", out.ExecutorID)
	}
}

func TestAppendExecutorEvent(t *testing.T) {
	stub := newStub()
	defer stub.Close()
	c := mockedclient.New(stub.URL()+"/v1", nil)
	ev := mockedclient.NewExecutorEvent("exec-1", platform.ExecutorEventHealthy, nil)
	if err := c.AppendExecutorEvent(context.Background(), ev); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestAppendTaskEvent(t *testing.T) {
	stub := newStub()
	defer stub.Close()
	c := mockedclient.New(stub.URL()+"/v1", nil)
	ev := mockedclient.NewTaskEvent(uuid.NewString(), "exec-1", platform.TaskSourceExecutor, "task.started", nil)
	if err := c.AppendTaskEvent(context.Background(), ev); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestOpenEnvNoContent(t *testing.T) {
	stub := newStub()
	defer stub.Close()
	c := mockedclient.New(stub.URL()+"/v1", nil)
	vals, err := c.OpenEnv(context.Background(), "exec-1", "openhands", "", "missing")
	if err != nil {
		t.Fatalf("open env: %v", err)
	}
	if len(vals) != 0 {
		t.Fatalf("expected empty values, got %d", len(vals))
	}
}

func TestExecutorEventJSONShape(t *testing.T) {
	ev := mockedclient.NewExecutorEvent("exec-1", platform.ExecutorEventBusy, map[string]interface{}{"k": 1})
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, `"type":"executor.busy"`) {
		t.Fatalf("missing type field: %s", s)
	}
	if !strings.Contains(s, `"executor_id":"exec-1"`) {
		t.Fatalf("missing executor_id: %s", s)
	}
}

func TestListTasksSetsRequestID(t *testing.T) {
	var seenID string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		seenID = r.Header.Get("X-Request-Id")
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []platform.RouterTask{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := mockedclient.New(srv.URL+"/v1", nil)
	if _, err := c.ListTasks(context.Background(), "openhands"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if seenID == "" {
		t.Fatalf("expected X-Request-Id to be set")
	}
}

var _ = io.Discard
