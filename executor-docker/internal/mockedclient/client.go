// Package mockedclient implements the HTTP client the Executor uses to talk
// to the mocked task server. It covers all three surfaces:
//   - Router surface  : GET /v1/tasks?filter=<routing_target>
//   - State Registry surface: PUT /v1/executors/{id}, POST /v1/executors/{id}/events,
//     POST /v1/tasks/{id}/events
//   - Env Registry surface: GET /v1/env
package mockedclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor-docker/internal/platform"
)

// Client is the HTTP client the Executor uses to talk to the mocked task server.
type Client struct {
	baseURL    string // e.g. http://127.0.0.1:8080/v1
	httpClient *http.Client
}

// New returns a Client. baseURL must include /v1.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// ListTasks calls GET /v1/tasks?filter=<routing_target>. If filter is empty,
// the call is GET /v1/tasks (which returns all tasks per the Router spec).
func (c *Client) ListTasks(ctx context.Context, routingTarget string) ([]platform.RouterTask, error) {
	u := c.baseURL + "/tasks"
	if routingTarget != "" {
		q := url.Values{}
		q.Set("filter", routingTarget)
		u += "?" + q.Encode()
	}
	var resp platform.TaskListResponse
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// RegisterExecutor calls PUT /v1/executors/{executor_id}.
func (c *Client) RegisterExecutor(ctx context.Context, rec *platform.ExecutorRecord) (*platform.ExecutorRegistrationResponse, error) {
	u := fmt.Sprintf("%s/executors/%s", c.baseURL, rec.ExecutorID)
	var out platform.ExecutorRegistrationResponse
	if err := c.doJSON(ctx, http.MethodPut, u, rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AppendExecutorEvent calls POST /v1/executors/{executor_id}/events.
func (c *Client) AppendExecutorEvent(ctx context.Context, ev *platform.ExecutorEvent) error {
	if ev.EventID == "" {
		ev.EventID = uuid.NewString()
	}
	u := fmt.Sprintf("%s/executors/%s/events", c.baseURL, ev.ExecutorID)
	var out platform.EventAppendResponse
	return c.doJSON(ctx, http.MethodPost, u, ev, &out)
}

// AppendTaskEvent calls POST /v1/tasks/{task_id}/events.
func (c *Client) AppendTaskEvent(ctx context.Context, ev *platform.TaskEvent) error {
	if ev.EventID == "" {
		ev.EventID = uuid.NewString()
	}
	u := fmt.Sprintf("%s/tasks/%s/events", c.baseURL, ev.TaskID)
	var out platform.EventAppendResponse
	return c.doJSON(ctx, http.MethodPost, u, ev, &out)
}

// OpenEnv calls GET /v1/env with the supplied identifiers. A 204 response
// means no values are configured; that case is signalled by an empty Values
// map and a nil error.
func (c *Client) OpenEnv(ctx context.Context, executorID, routingTarget, taskID, scopeToken string) (map[string]string, error) {
	q := url.Values{}
	q.Set("executor_id", executorID)
	q.Set("routing_target", routingTarget)
	q.Set("scope_token", scopeToken)
	if taskID != "" {
		q.Set("task_id", taskID)
	}
	u := c.baseURL + "/env?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return map[string]string{}, nil
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mocked env %s: %d: %s", u, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out platform.OpenEnvResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Values, nil
}

func (c *Client) doJSON(ctx context.Context, method, url string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Generate a fresh request id per outbound call so log correlation is easy.
	req.Header.Set("X-Request-Id", uuid.NewString())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mocked %s %s: %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// IsTransientNetworkError reports whether err is likely transient (worth retrying).
func IsTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "i/o timeout")
}

// ErrEmptyTaskList is returned by ListTasks when no tasks match.
var ErrEmptyTaskList = errors.New("empty task list")

// Helpers to build typed event payloads for the State Registry.

func NewExecutorEvent(executorID string, t platform.ExecutorEventType, payload map[string]interface{}) *platform.ExecutorEvent {
	return &platform.ExecutorEvent{
		EventID:    uuid.NewString(),
		ExecutorID: executorID,
		Type:       t,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}

func NewTaskEvent(taskID, executorID string, source platform.TaskEventSource, eventType string, payload map[string]interface{}) *platform.TaskEvent {
	return &platform.TaskEvent{
		EventID:    uuid.NewString(),
		TaskID:     taskID,
		ExecutorID: executorID,
		Source:     source,
		Type:       eventType,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}