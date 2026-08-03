// Package stateregistryclient is the v0002 State Registry HTTP client
// the Docker Executor uses for team/system-scoped registration, FIFO
// discovery, atomic FIFO claim, task and Executor self events,
// assigned controls, and task-bound environment opens.
//
// The package is fully self-contained: it imports the platform wire
// types defined in executor_docker_opehands/internal/platform and the
// standard library only. It NEVER imports the State Registry
// internal packages, mirroring the per-service isolation rule
// documented in AGENTS.md.
//
// Every call carries the test-mode transport identity headers
// (X-FlowAI-Role, X-FlowAI-Executor-Id, X-FlowAI-Team-Id) so the
// State Registry's request-time identity adapter can authorise the
// Executor against its registered scope and team binding. Production
// deployment is expected to keep the same envelope behind mTLS.
package stateregistryclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
)

// RegisterExecutorRequest mirrors the v0002 registration body. The
// State Registry persists the Executor row with this shape; the
// caller's RuntimeMetadata may carry any opaque JSON object.
type RegisterExecutorRequest struct {
	Scope           string          `json:"scope"`
	TeamID          *string         `json:"team_id"`
	ExecutorType    string          `json:"executor_type"`
	Identity        string          `json:"identity"`
	AuthorizedTag   string          `json:"authorized_tag"`
	MaxCapacity     int             `json:"max_capacity"`
	RunningCount    int             `json:"running_count"`
	RuntimeMetadata json.RawMessage `json:"runtime_metadata"`
}

// ExecutorRecord mirrors the documented Executor response row.
type ExecutorRecord struct {
	ExecutorID      string          `json:"executor_id"`
	Scope           string          `json:"scope"`
	TeamID          *string         `json:"team_id"`
	ExecutorType    string          `json:"executor_type"`
	Identity        string          `json:"identity"`
	AuthorizedTag   string          `json:"authorized_tag"`
	MaxCapacity     int             `json:"max_capacity"`
	RunningCount    int             `json:"running_count"`
	RuntimeMetadata json.RawMessage `json:"runtime_metadata"`
	RegisteredAt    time.Time       `json:"registered_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// Identity pins the Executor service identity used by every
// Registry-bound call. Scope=team requires TeamID; scope=system
// requires TeamID == "".
type Identity struct {
	ExecutorID string
	Scope      string // "team" or "system"
	TeamID     string // empty for system scope
}

// validate reports an error when the identity fields are mutually
// inconsistent. The Caller runs validate exactly once at construction.
func (i Identity) validate() error {
	if i.ExecutorID == "" {
		return errors.New("stateregistryclient: ExecutorID is required")
	}
	switch i.Scope {
	case "team":
		if i.TeamID == "" {
			return errors.New("stateregistryclient: team scope requires TeamID")
		}
	case "system":
		if i.TeamID != "" {
			return errors.New("stateregistryclient: system scope must have empty TeamID")
		}
	default:
		return fmt.Errorf("stateregistryclient: scope must be team or system, got %q", i.Scope)
	}
	return nil
}

// HTTPError is a typed error wrapping the State Registry's
// documented error responses and HTTP status. Executor code uses
// errors.Is/As to discriminate 404 (foreign or unknown) from 409
// (FIFO conflicts) from transient 5xx; any non-2xx response MUST
// surface as an HTTPError so the runtime path can refuse to start
// a container on a non-200 claim.
type HTTPError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	URL       string
	Method    string
}

func (e *HTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("state registry %s %s: %d %s: %s", e.Method, e.URL, e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("state registry %s %s: %d: %s", e.Method, e.URL, e.Status, e.Message)
}

// IsNotFound reports whether err is a State Registry 404 (or a 4xx
// that the Registry documents as a non-revealing foreign/unknown
// 404 shape). The Executor treats 404 as "do not start a runtime".
func IsNotFound(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusNotFound
	}
	return false
}

// IsTaskAlreadyClaimed reports whether err is the 409
// task_already_claimed response. The Executor drops the candidate
// and returns to discovery.
func IsTaskAlreadyClaimed(err error) bool {
	return isCode(err, "task_already_claimed")
}

// IsOlderTaskMustBeClaimedFirst reports whether err is the 409
// older_task_must_be_claimed_first response. The Executor returns
// to discovery without starting a runtime.
func IsOlderTaskMustBeClaimedFirst(err error) bool {
	return isCode(err, "older_task_must_be_claimed_first")
}

func isCode(err error, code string) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusConflict && he.Code == code
	}
	return false
}

// Client is the State Registry HTTP client. The zero value is
// unusable; construct through New.
type Client struct {
	baseURL    string
	identity   Identity
	httpClient *http.Client
}

// New constructs a Client rooted at baseURL. The supplied identity
// is included on every request as documented in the v0002
// transport. baseURL is trimmed of trailing slashes and a /v1
// suffix is appended when missing.
func New(baseURL string, identity Identity, httpClient *http.Client) (*Client, error) {
	if err := identity.validate(); err != nil {
		return nil, err
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    baseURL,
		identity:   identity,
		httpClient: httpClient,
	}, nil
}

// Identity returns a copy of the pinned identity.
func (c *Client) Identity() Identity { return c.identity }

// RegisterExecutor PUTs /v1/executors/{executor_id} with the
// v0002 registration body. The Registry is idempotent for repeated
// PUTs: the same scope and team binding round-trip successfully.
func (c *Client) RegisterExecutor(ctx context.Context, body RegisterExecutorRequest) (*ExecutorRecord, error) {
	u := c.baseURL + "/executors/" + url.PathEscape(c.identity.ExecutorID)
	out := &ExecutorRecord{}
	if err := c.doJSON(ctx, http.MethodPut, u, body, out, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// DiscoverTasks GETs /v1/executors/{executor_id}/tasks?tag=... with
// the optional limit query. A 204 response means no eligible
// pending task is visible; the function returns (nil, nil) and the
// caller treats the situation as "no work". A 404 is returned as
// an HTTPError so the Executor can refuse to start a runtime.
func (c *Client) DiscoverTasks(ctx context.Context, tag string, limit int) ([]platform.V0002TaskSummary, error) {
	if tag == "" {
		return nil, errors.New("stateregistryclient: tag is required")
	}
	q := url.Values{}
	q.Set("tag", tag)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	u := c.baseURL + "/executors/" + url.PathEscape(c.identity.ExecutorID) + "/tasks?" + q.Encode()
	var page platform.V0002TaskDiscoveryPage
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &page, ""); err != nil {
		if he := (*HTTPError)(nil); errors.As(err, &he) {
			if he.Status == http.StatusNoContent {
				return nil, nil
			}
		}
		return nil, err
	}
	return page.Items, nil
}

// ClaimTask POSTs /v1/executors/{executor_id}/claim with
// (task_id, command_id). The Registry returns the canonical
// envelope: resolved_image, image_source, environment_id, and the
// scope_token required for the v0002 environment.open path.
func (c *Client) ClaimTask(ctx context.Context, req platform.V0002ClaimRequest) (*platform.V0002ClaimResponse, error) {
	u := c.baseURL + "/executors/" + url.PathEscape(c.identity.ExecutorID) + "/claim"
	out := &platform.V0002ClaimResponse{}
	if err := c.doJSON(ctx, http.MethodPost, u, req, out, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// AppendTaskEvent POSTs /v1/tasks/{task_id}/events with the
// documented envelope. The State Registry returns 202 on accept
// and 202 on an identical (task_id, event_id) retry; non-2xx
// responses are surfaced as HTTPError.
func (c *Client) AppendTaskEvent(ctx context.Context, ev platform.V0002TaskEventEnvelope) (*platform.V0002EventAcceptance, error) {
	u := c.baseURL + "/tasks/" + url.PathEscape(ev.TaskID) + "/events"
	out := &platform.V0002EventAcceptance{}
	if err := c.doJSON(ctx, http.MethodPost, u, ev, out, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// AppendExecutorEvent POSTs /v1/executors/{executor_id}/events
// with the documented envelope. The caller supplies the canonical
// envelope (event_id, team_id-or-null, event_type, occurred_at,
// payload). The State Registry mirrors the latest
// capacity_observed value onto the Executor row in the same
// transaction.
func (c *Client) AppendExecutorEvent(ctx context.Context, ev platform.V0002ExecutorEventEnvelope) (*platform.V0002EventAcceptance, error) {
	u := c.baseURL + "/executors/" + url.PathEscape(c.identity.ExecutorID) + "/events"
	out := &platform.V0002EventAcceptance{}
	if err := c.doJSON(ctx, http.MethodPost, u, ev, out, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// ListAssignedControls GETs /v1/tasks/{task_id}/controls. The
// authenticated Executor MUST be the immutable assignee; otherwise
// the Registry returns 403 not_assigned. An empty assigned list
// is signalled by a 200 with `items: []`.
func (c *Client) ListAssignedControls(ctx context.Context, taskID string) ([]platform.V0002TaskControl, error) {
	u := c.baseURL + "/tasks/" + url.PathEscape(taskID) + "/controls"
	var page platform.V0002TaskControlPage
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &page, ""); err != nil {
		return nil, err
	}
	if page.Items == nil {
		page.Items = []platform.V0002TaskControl{}
	}
	return page.Items, nil
}

// OpenEnvironment GETs /v1/environments/{environment_id}/open with
// the supplied scope_token in the X-FlowAI-Scope-Token header and
// the canonical task_id as a query parameter. A 204 means the
// authorised environment has no values; the function returns
// (empty-map, nil). A 404 is surfaced as HTTPError: the Executor
// MUST treat the environment as unknown and MUST NOT log the
// underlying failure reason.
func (c *Client) OpenEnvironment(ctx context.Context, environmentID, taskID, scopeToken string) (map[string]string, error) {
	if environmentID == "" || taskID == "" || scopeToken == "" {
		return nil, errors.New("stateregistryclient: environment_id, task_id, and scope_token are required")
	}
	q := url.Values{}
	q.Set("task_id", taskID)
	u := c.baseURL + "/environments/" + url.PathEscape(environmentID) + "/open?" + q.Encode()
	out := &platform.V0002OpenEnvironmentResponse{}
	if err := c.doJSON(ctx, http.MethodGet, u, nil, out, scopeToken); err != nil {
		if he := (*HTTPError)(nil); errors.As(err, &he) {
			if he.Status == http.StatusNoContent {
				return map[string]string{}, nil
			}
		}
		return nil, err
	}
	if out.Values == nil {
		out.Values = map[string]string{}
	}
	return out.Values, nil
}

// doJSON executes the request, attaching the documented test-mode
// identity headers (X-FlowAI-Role, X-FlowAI-Executor-Id,
// X-FlowAI-Team-Id) plus an X-Request-Id correlation header. A
// non-2xx response is wrapped in *HTTPError. The optional
// scopeToken is forwarded verbatim as X-FlowAI-Scope-Token; callers
// never log it.
func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any, scopeToken string) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("stateregistryclient: marshal request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("stateregistryclient: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.applyIdentityHeaders(req)
	if scopeToken != "" {
		req.Header.Set("X-FlowAI-Scope-Token", scopeToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stateregistryclient: http %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		// The Registry documents ErrorResponse as a single shape
		// carrying code + message. We surface the parsed code so
		// callers can use errors.As for the conflict taxonomy.
		var errBody platform.V0002ErrorResponse
		_ = json.Unmarshal(raw, &errBody)
		// The Executor MUST treat any 4xx with an unparseable
		// body as 404-equivalent (non-revealing foreign/unknown)
		// so the contract rule "404 hides the failure reason"
		// round-trips on the client side too.
		if errBody.Code == "" && resp.StatusCode == http.StatusNotFound {
			errBody.Code = "executor_unknown"
			errBody.Message = "executor is unknown or unavailable"
		}
		return &HTTPError{
			Status:    resp.StatusCode,
			Code:      errBody.Code,
			Message:   errBody.Message,
			RequestID: errBody.RequestID,
			URL:       url,
			Method:    method,
		}
	}
	if resp.StatusCode == http.StatusNoContent {
		return &HTTPError{Status: resp.StatusCode, URL: url, Method: method}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, bytes.NewReader(raw))
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("stateregistryclient: decode %s %s: %w", method, url, err)
	}
	return nil
}

func (c *Client) applyIdentityHeaders(req *http.Request) {
	req.Header.Set(platform.V0002HeaderExecutorID, c.identity.ExecutorID)
	req.Header.Set(platform.V0002HeaderRole, roleForScope(c.identity.Scope))
	if c.identity.TeamID != "" {
		req.Header.Set(platform.V0002HeaderTeamID, c.identity.TeamID)
	}
	if req.Header.Get("X-Request-Id") == "" {
		req.Header.Set("X-Request-Id", uuid.NewString())
	}
}

func roleForScope(scope string) string {
	if scope == "system" {
		return platform.V0002HeaderRoleSystem
	}
	return platform.V0002HeaderRoleTeam
}
