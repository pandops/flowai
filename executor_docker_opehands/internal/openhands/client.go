// Package openhands implements the HTTP client the Executor uses to talk to
// a running OpenHands container.
//
// The integration surface is the OpenHands V1 agent-server (commit
// 2eff609 of OpenHands/software-agent-sdk v1.34.0). All V1 wire shapes
// (POST /api/conversations with the documented workspace + agent +
// initial_message body, response field `id`, WS path
// /sockets/events/{id}, POST /api/conversations/{id}/events with the
// V1 structured content shape) are encoded here.
package openhands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the OpenHands V1 REST client used by the Executor. The
// X-Session-API-Key header is sent on every call when non-empty; the
// upstream V1 server accepts first-frame auth as well but we keep the
// header for compatibility with prior deployments.
type Client struct {
	baseURL    string // e.g. http://127.0.0.1:8123
	apiKey     string
	httpClient *http.Client
}

// NewClient returns a Client. baseURL must include scheme+host+port (no
// trailing slash). apiKey is the V1 server session API key and is sent in
// the X-Session-API-Key header.
func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: httpClient,
	}
}

// HealthCheck polls GET /health on the OpenHands server until it returns
// 200 or until timeout elapses. Returns nil on success.
func (c *Client) HealthCheck(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	backoff := 100 * time.Millisecond
	const maxBackoff = 2 * time.Second
	url := c.baseURL + "/health"
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("openhands health-check timeout after %s", timeout)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if c.apiKey != "" {
			req.Header.Set("X-Session-API-Key", c.apiKey)
		}
		resp, err := c.httpClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// ConversationConfig is the V1 request body for POST /api/conversations.
// Exactly one of Agent (inline) and AgentProfileID (server-side profile)
// must be set; AgentProfileID is verified by Validate().
type ConversationConfig struct {
	// Workspace is required. The V1 schema requires a typed
	// {kind:"LocalWorkspace"} object.
	Workspace Workspace `json:"workspace"`
	// InitialMessage is required.
	InitialMessage InitialMessage `json:"initial_message"`
	// Agent (inline) is the V1 inline Agent definition with at minimum
	// llm.model, llm.api_key, llm.usage_id. Tools is optional but when
	// non-empty must contain known V1 tool names like TerminalTool,
	// FileEditorTool, TaskTrackerTool.
	Agent *Agent `json:"agent,omitempty"`
	// AgentProfileID is the alternative to inline Agent. The V1 server
	// resolves the profile name to an Agent server-side.
	AgentProfileID string `json:"agent_profile_id,omitempty"`
}

// Workspace is the V1 workspace block.
type Workspace struct {
	Kind       string `json:"kind"` // "LocalWorkspace"
	WorkingDir string `json:"working_dir"`
}

// InitialMessage is the V1 initial_message block. Role is "user" and
// content is a list of typed parts; the first text part carries the
// prompt. Run controls whether the agent loops immediately; tests that
// only need a created conversation set it to false.
type InitialMessage struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
	Run     bool          `json:"run"`
}

// ContentPart is a single typed content entry.
type ContentPart struct {
	Type string `json:"type"` // "text" | "image_url" | ...
	Text string `json:"text,omitempty"`
}

// Agent is the V1 inline Agent definition. Tools is optional; the V1
// server accepts an empty list when the executor doesn't need tools.
type Agent struct {
	Kind  string `json:"kind"` // "Agent"
	LLM   LLM    `json:"llm"`
	Tools []Tool `json:"tools,omitempty"`
}

// LLM is the V1 LLM block; UsageID is mandatory in V1.
type LLM struct {
	Model   string `json:"model"`
	APIKey  string `json:"api_key"`
	UsageID string `json:"usage_id"`
	BaseURL string `json:"base_url,omitempty"`
}

// Tool is a single V1 tool entry referenced by name.
type Tool struct {
	Name string `json:"name"`
}

// Validate enforces the V1 schema invariants.
func (c ConversationConfig) Validate() error {
	if c.Workspace.Kind == "" || c.Workspace.WorkingDir == "" {
		return errors.New("conversation config requires a non-empty workspace")
	}
	if c.InitialMessage.Role == "" {
		return errors.New("conversation config requires initial_message.role")
	}
	if len(c.InitialMessage.Content) == 0 {
		return errors.New("conversation config requires at least one content part")
	}
	if c.Agent == nil && c.AgentProfileID == "" {
		return errors.New("conversation config requires either an inline Agent or AgentProfileID")
	}
	if c.Agent != nil && c.Agent.LLM.Model == "" {
		return errors.New("inline Agent requires llm.model")
	}
	if c.Agent != nil && c.Agent.LLM.UsageID == "" {
		return errors.New("inline Agent requires llm.usage_id")
	}
	return nil
}

// ConversationStartResult mirrors the V1 response shape (id + body). Only
// ID is consumed by the Executor; the full body is retained for tests.
type ConversationStartResult struct {
	ID  string `json:"id"`
	Raw []byte `json:"-"`
}

// StartConversation submits the conversation request and returns the
// conversation id assigned by the V1 server. The exact request body and
// response decoding follow the OpenHands/software-agent-sdk v1.34.0
// agent-server contract.
func (c *Client) StartConversation(ctx context.Context, cfg ConversationConfig) (*ConversationStartResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	url := c.baseURL + "/api/conversations"
	buf, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal conversation config: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-Session-API-Key", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openhands POST %s: %d: %s",
			url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	out := struct {
		ID string `json:"id"`
	}{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode conversation response: %w (body=%s)",
			err, strings.TrimSpace(string(body)))
	}
	if out.ID == "" {
		return nil, fmt.Errorf("openhands returned no conversation id: %s", strings.TrimSpace(string(body)))
	}
	return &ConversationStartResult{ID: out.ID, Raw: body}, nil
}

// PauseConversation calls POST /api/conversations/{conversation_id}/pause.
func (c *Client) PauseConversation(ctx context.Context, conversationID string) error {
	url := c.baseURL + "/api/conversations/" + conversationID + "/pause"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("X-Session-API-Key", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("openhands POST %s: %d: %s",
			url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// AppendEvent posts a user message to /api/conversations/{id}/events
// using the V1 structured-content shape. The top-level `type` field is
// intentionally NOT sent (it was a V0 affordance removed in V1). Role
// is always "user"; Run is true so the agent picks the message up
// immediately.
func (c *Client) AppendEvent(ctx context.Context, conversationID, role, content string) error {
	body := map[string]any{
		"role": role,
		"content": []ContentPart{{
			Type: "text",
			Text: content,
		}},
		"run": true,
	}
	url := c.baseURL + "/api/conversations/" + conversationID + "/events"
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal append event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-Session-API-Key", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("openhands POST %s: %d: %s",
			url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// WSPath is the V1 websocket events-socket path used by the executor's
// streaming goroutine.
func WSPath(conversationID string) string {
	return "/sockets/events/" + conversationID
}

// doJSON is retained for callers that need a generic JSON round-trip;
// most callers use StartConversation / PauseConversation / AppendEvent
// directly because their wire shape is fixed by V1.
func (c *Client) doJSON(ctx context.Context, method, url string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-Session-API-Key", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("openhands %s %s: %d: %s",
			method, url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrNotReady is returned when health-check polling exhausts the budget.
var ErrNotReady = errors.New("openhands not ready")
