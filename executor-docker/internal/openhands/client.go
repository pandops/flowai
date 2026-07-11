// Package openhands implements the HTTP client the Executor uses to talk to
// a running OpenHands container.
//
// The Executor's ADR-0003 pins the integration surface to three endpoint
// families, all under /api/ (or /health for liveness).
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

// Client is the OpenHands REST client used by the Executor.
type Client struct {
	baseURL    string // e.g. http://127.0.0.1:8123
	apiKey     string
	httpClient *http.Client
}

// NewClient returns a Client. baseURL must include scheme+host+port (no trailing slash).
// apiKey is optional in v0001 (matches the dev-mode OpenHands server).
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

// HealthCheck polls GET /health on the OpenHands server until it returns 200
// or until timeout elapses. Returns nil on success, ctx error on timeout.
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

// ConversationStartResult is returned by StartConversation.
type ConversationStartResult struct {
	ConversationID string `json:"conversation_id"`
}

// StartConversation submits the prompt to OpenHands and returns the OpenHands
// conversation identifier. The exact endpoint shape mirrors the OpenHands V1
// software-agent-sdk.
func (c *Client) StartConversation(ctx context.Context, prompt string) (*ConversationStartResult, error) {
	body := map[string]any{
		"initial_user_msg": prompt,
	}
	url := c.baseURL + "/api/conversations"
	var out ConversationStartResult
	if err := c.doJSON(ctx, http.MethodPost, url, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PauseConversation calls POST /api/conversations/{conversation_id}/pause.
// Returns nil on 2xx, otherwise an error.
func (c *Client) PauseConversation(ctx context.Context, conversationID string) error {
	url := c.baseURL + "/api/conversations/" + conversationID + "/pause"
	return c.doJSON(ctx, http.MethodPost, url, nil, nil)
}

// AppendEvent posts a user message to /api/conversations/{conversation_id}/events.
// eventType and role are passed in so callers can match the documented contract.
func (c *Client) AppendEvent(ctx context.Context, conversationID, eventType, role, content string) error {
	body := map[string]any{
		"type":    eventType,
		"role":    role,
		"content": content,
	}
	url := c.baseURL + "/api/conversations/" + conversationID + "/events"
	return c.doJSON(ctx, http.MethodPost, url, body, nil)
}

// doJSON performs an HTTP request with optional JSON body and decodes the response.
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
		return fmt.Errorf("openhands %s %s: %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrNotReady is returned when health-check polling exhausts the budget.
var ErrNotReady = errors.New("openhands not ready")