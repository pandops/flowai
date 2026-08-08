// Targeted unit tests for the Client. They live in package openhands
// (not openhands_test) so they can reference the in-package fakeV1
// helper without violating the Go internal-package rule.
package openhands

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeV1Server is a minimal in-package fake of the OpenHands V1
// agent-server sufficient for client-level wire-shape assertions.
type fakeV1Server struct {
	mu           sync.Mutex
	startBody    map[string]any
	startID      string
	startStatus  int
	startErr     string
	startHits    int
	appendBody   map[string]any
	appendStatus int
	appendHits   int
	pauseHits    int
	healthHits   int
	healthCode   int
	srv          *httptest.Server
}

func newFakeV1(t *testing.T) *fakeV1Server {
	t.Helper()
	f := &fakeV1Server{startID: "conv-abc-123", healthCode: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.healthHits++
		w.WriteHeader(f.healthCode)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/conversations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var parsed map[string]any
		if len(body) > 0 {
			_ = json.Unmarshal(body, &parsed)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.startHits++
		f.startBody = parsed
		if f.startStatus == http.StatusUnprocessableEntity {
			http.Error(w, "validation: bad body", f.startStatus)
			return
		}
		if f.startErr != "" {
			http.Error(w, f.startErr, f.startStatus)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": f.startID})
	})
	mux.HandleFunc("/api/conversations/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		action := parts[1]
		f.mu.Lock()
		defer f.mu.Unlock()
		switch action {
		case "pause":
			f.pauseHits++
			w.WriteHeader(http.StatusOK)
		case "events":
			f.appendHits++
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = json.Unmarshal(body, &f.appendBody)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeV1Server) URL() string { return f.srv.URL }

func (f *fakeV1Server) StartBody() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.startBody))
	for k, v := range f.startBody {
		out[k] = v
	}
	return out
}

func (f *fakeV1Server) LastAppendBody() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.appendBody))
	for k, v := range f.appendBody {
		out[k] = v
	}
	return out
}

func newClientWithFake(t *testing.T) (*Client, *fakeV1Server) {
	t.Helper()
	f := newFakeV1(t)
	c := NewClient(f.URL(), "test-api-key", nil)
	return c, f
}

func TestStartConversationSendsV1BodyAndDecodesID(t *testing.T) {
	c, f := newClientWithFake(t)

	cfg := ConversationConfig{
		Workspace: Workspace{
			Kind:       "LocalWorkspace",
			WorkingDir: "/workspace/project",
		},
		InitialMessage: InitialMessage{
			Role: "user",
			Content: []ContentPart{
				{Type: "text", Text: "hello world"},
			},
			Run: true,
		},
		Agent: &Agent{
			Kind: "Agent",
			LLM: LLM{
				Model:   "openai/gpt-4o-mini",
				APIKey:  "test-key",
				UsageID: "flowai-executor",
			},
		},
	}

	res, err := c.StartConversation(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if res.ID != "conv-abc-123" {
		t.Fatalf("unexpected id %q", res.ID)
	}

	body := f.StartBody()
	ws, ok := body["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("workspace missing or wrong type: %+v", body)
	}
	if ws["kind"] != "LocalWorkspace" || ws["working_dir"] != "/workspace/project" {
		t.Fatalf("unexpected workspace fields: %+v", ws)
	}
	im, ok := body["initial_message"].(map[string]any)
	if !ok {
		t.Fatalf("initial_message missing: %+v", body)
	}
	if im["role"] != "user" {
		t.Fatalf("initial_message.role=%v want user", im["role"])
	}
	parts, _ := im["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("initial_message.content expected 1 part, got %d", len(parts))
	}
	p0, _ := parts[0].(map[string]any)
	if p0["type"] != "text" || p0["text"] != "hello world" {
		t.Fatalf("unexpected content part: %+v", p0)
	}
	if im["run"] != true {
		t.Fatalf("initial_message.run expected true, got %v", im["run"])
	}
}

func TestStartConversationAcceptsAgentProfileID(t *testing.T) {
	c, f := newClientWithFake(t)

	cfg := ConversationConfig{
		Workspace: Workspace{Kind: "LocalWorkspace", WorkingDir: "/workspace"},
		InitialMessage: InitialMessage{
			Role:    "user",
			Content: []ContentPart{{Type: "text", Text: "hi"}},
		},
		AgentProfileID: "flowai-default",
	}
	res, err := c.StartConversation(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if res.ID != "conv-abc-123" {
		t.Fatalf("expected id, got %q", res.ID)
	}
	body := f.StartBody()
	if body["agent_profile_id"] != "flowai-default" {
		t.Fatalf("agent_profile_id=%v", body["agent_profile_id"])
	}
	if body["agent"] != nil {
		t.Fatalf("agent should be nil when agent_profile_id is set: %+v", body["agent"])
	}
}

func TestStartConversationRejectsMissingWorkspace(t *testing.T) {
	_, _ = newClientWithFake(t)
	cfg := ConversationConfig{
		InitialMessage: InitialMessage{
			Role:    "user",
			Content: []ContentPart{{Type: "text", Text: "x"}},
		},
		Agent: &Agent{Kind: "Agent", LLM: LLM{Model: "m", APIKey: "k", UsageID: "u"}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected workspace validation error")
	}
}

func TestStartConversationReturns422Error(t *testing.T) {
	c, f := newClientWithFake(t)
	f.startStatus = http.StatusUnprocessableEntity
	cfg := ConversationConfig{
		Workspace: Workspace{Kind: "LocalWorkspace", WorkingDir: "/workspace"},
		InitialMessage: InitialMessage{
			Role:    "user",
			Content: []ContentPart{{Type: "text", Text: "x"}},
		},
		Agent: &Agent{Kind: "Agent", LLM: LLM{Model: "m", APIKey: "k", UsageID: "u"}},
	}
	_, err := c.StartConversation(context.Background(), cfg)
	if err == nil {
		t.Fatalf("expected error from upstream 422")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Fatalf("expected 422 in error, got: %v", err)
	}
}

func TestAppendEventSendsV1StructuredBody(t *testing.T) {
	c, f := newClientWithFake(t)
	cfg := ConversationConfig{
		Workspace: Workspace{Kind: "LocalWorkspace", WorkingDir: "/w"},
		InitialMessage: InitialMessage{
			Role:    "user",
			Content: []ContentPart{{Type: "text", Text: "hi"}},
		},
		Agent: &Agent{Kind: "Agent", LLM: LLM{Model: "m", APIKey: "k", UsageID: "u"}},
	}
	res, err := c.StartConversation(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if err := c.AppendEvent(context.Background(), res.ID, "user", "follow up"); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	body := f.LastAppendBody()
	if body["role"] != "user" {
		t.Fatalf("role=%v want user", body["role"])
	}
	if _, hasOldType := body["type"]; hasOldType {
		t.Fatalf("V1 must not include top-level type field, got: %+v", body)
	}
	parts, ok := body["content"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("content parts missing or wrong length: %+v", body)
	}
	p0, _ := parts[0].(map[string]any)
	if p0["type"] != "text" || p0["text"] != "follow up" {
		t.Fatalf("unexpected content part: %+v", p0)
	}
	if body["run"] != true {
		t.Fatalf("run expected true, got %v", body["run"])
	}
}

func TestWSPathReturnsSocketsEvents(t *testing.T) {
	got := WSPath("abc-123")
	if got != "/sockets/events/abc-123" {
		t.Fatalf("WSPath=%q", got)
	}
}

func TestHealthCheckReturnsErrorOn5xx(t *testing.T) {
	c, f := newClientWithFake(t)
	f.healthCode = http.StatusInternalServerError
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.HealthCheck(ctx, 1*time.Second)
	if err == nil {
		t.Fatalf("expected health-check error on 500")
	}
}

func TestPauseConversationHappyPath(t *testing.T) {
	c, _ := newClientWithFake(t)
	if err := c.PauseConversation(context.Background(), "some-id"); err != nil {
		t.Fatalf("PauseConversation: %v", err)
	}
}

// TestValidateConversationConfig exercises every V1 schema rule in isolation.
func TestValidateConversationConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  ConversationConfig
		ok   bool
	}{
		{
			name: "valid inline",
			cfg: ConversationConfig{
				Workspace:      Workspace{Kind: "LocalWorkspace", WorkingDir: "/x"},
				InitialMessage: InitialMessage{Role: "user", Content: []ContentPart{{Type: "text", Text: "hi"}}},
				Agent: &Agent{Kind: "Agent", LLM: LLM{
					Model: "m", APIKey: "k", UsageID: "u",
				}},
			},
			ok: true,
		},
		{
			name: "valid profile",
			cfg: ConversationConfig{
				Workspace:      Workspace{Kind: "LocalWorkspace", WorkingDir: "/x"},
				InitialMessage: InitialMessage{Role: "user", Content: []ContentPart{{Type: "text", Text: "hi"}}},
				AgentProfileID: "p",
			},
			ok: true,
		},
		{
			name: "missing workspace",
			cfg: ConversationConfig{
				InitialMessage: InitialMessage{Role: "user", Content: []ContentPart{{Type: "text", Text: "hi"}}},
				Agent:          &Agent{Kind: "Agent", LLM: LLM{Model: "m", APIKey: "k", UsageID: "u"}},
			},
			ok: false,
		},
		{
			name: "missing initial message",
			cfg: ConversationConfig{
				Workspace: Workspace{Kind: "LocalWorkspace", WorkingDir: "/x"},
				Agent:     &Agent{Kind: "Agent", LLM: LLM{Model: "m", APIKey: "k", UsageID: "u"}},
			},
			ok: false,
		},
		{
			name: "missing both agent and profile",
			cfg: ConversationConfig{
				Workspace:      Workspace{Kind: "LocalWorkspace", WorkingDir: "/x"},
				InitialMessage: InitialMessage{Role: "user", Content: []ContentPart{{Type: "text", Text: "hi"}}},
			},
			ok: false,
		},
		{
			name: "agent missing llm model",
			cfg: ConversationConfig{
				Workspace:      Workspace{Kind: "LocalWorkspace", WorkingDir: "/x"},
				InitialMessage: InitialMessage{Role: "user", Content: []ContentPart{{Type: "text", Text: "hi"}}},
				Agent:          &Agent{Kind: "Agent", LLM: LLM{APIKey: "k", UsageID: "u"}},
			},
			ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}
