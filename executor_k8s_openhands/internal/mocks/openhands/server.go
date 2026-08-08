package mocks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var _ = fmt.Sprintf

// FakeOpenHands is a tiny in-process OpenHands V1 agent-server for testing
// the Executor's openhands.Client and event-streaming path. It records
// every received request and exposes knobs to control health, conversation
// start, append-message, pause, and event-stream responses.
//
// V1 wire shape (matches OpenHands/software-agent-sdk v1.34.0):
//   - POST /api/conversations accepts {workspace, agent?, agent_profile_id?, initial_message}
//     and returns {id, ...}. The body is validated for V1 structure.
//   - POST /api/conversations/{id}/events accepts {role, content:[{type,text}], run}
//     and is validated for the V1 structured-content shape (no top-level
//     `type` field).
//   - WS path is /sockets/events/{id} (not the V0 /api/conversations/{id}/events/socket).
//
// The fake is implemented on top of httptest.NewServer so the wire-level
// hijacker used by gorilla/websocket has the canonical Go server config.
type FakeOpenHands struct {
	mu sync.Mutex

	HealthStatus int
	HealthDelay  time.Duration

	ConversationID string
	StartErr       error
	PauseErr       error
	AppendErr      error
	// StartStatusCode overrides the response status for POST
	// /api/conversations (use 422 to exercise the executor's error path).
	StartStatusCode int

	HealthCalls int
	StartCalls  int
	PauseCalls  int
	AppendCalls int

	LastStartBody  map[string]any
	LastAppendBody map[string]any

	streamMu    sync.Mutex
	streamConns map[string]*websocket.Conn
	streamHooks []StreamHook

	srv *httptest.Server
}

// StreamHook is the callback signature for WS-connection customisations.
type StreamHook func(conn *websocket.Conn, conversationID string)

// RegisterStreamHook registers a callback invoked once per WS connection.
func (f *FakeOpenHands) RegisterStreamHook(h StreamHook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamHooks = append(f.streamHooks, h)
}

// PushEvent pushes a JSON event frame to all currently open WS connections.
func (f *FakeOpenHands) PushEvent(conversationID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	f.streamMu.Lock()
	defer f.streamMu.Unlock()
	for _, c := range f.streamConns {
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			return err
		}
	}
	return nil
}

// PushTerminal pushes a terminal conversation-status frame.
func (f *FakeOpenHands) PushTerminal(conversationID, status string) error {
	return f.PushEvent(conversationID, map[string]any{
		"type":             "conversation.status",
		"execution_status": status,
		"kind":             status,
		"status":           status,
	})
}

// Start launches the fake server on a random localhost port.
func (f *FakeOpenHands) Start() {
	f.streamConns = map[string]*websocket.Conn{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", f.handleHealth)
	mux.HandleFunc("/api/conversations", f.handleStart)
	mux.HandleFunc("/api/conversations/", f.handleSubpath)
	mux.HandleFunc("/sockets/events/", f.handleSockets)
	f.srv = httptest.NewServer(mux)
}

// StartOn is retained for API compatibility. It ignores the requested
// address and always binds to a free port; FakeOpenHands tests should use
// URL() (not a hard-coded port) to discover the bound address.
func (f *FakeOpenHands) StartOn(addr string) {
	f.Start()
}

// Stop shuts the server down and closes any open WS connections.
func (f *FakeOpenHands) Stop() {
	f.streamMu.Lock()
	for _, c := range f.streamConns {
		_ = c.Close()
	}
	f.streamConns = map[string]*websocket.Conn{}
	f.streamMu.Unlock()
	if f.srv != nil {
		f.srv.Close()
	}
}

// URL returns the server's base URL.
func (f *FakeOpenHands) URL() string {
	if f.srv == nil {
		return ""
	}
	return f.srv.URL
}

// PauseCallCount returns how many pause requests the fake received.
func (f *FakeOpenHands) PauseCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PauseCalls
}

// AppendSnapshot returns the append call count and a copy of the last body.
func (f *FakeOpenHands) AppendSnapshot() (int, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := make(map[string]any, len(f.LastAppendBody))
	for key, value := range f.LastAppendBody {
		body[key] = value
	}
	return f.AppendCalls, body
}

// StartBodySnapshot returns the last POST /api/conversations body.
func (f *FakeOpenHands) StartBodySnapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := make(map[string]any, len(f.LastStartBody))
	for key, value := range f.LastStartBody {
		body[key] = value
	}
	return body
}

// Port returns the bound TCP port (useful when bound to :0).
func (f *FakeOpenHands) Port() int {
	if f.srv == nil || f.srv.Listener == nil {
		return 0
	}
	return f.srv.Listener.Addr().(*net.TCPAddr).Port
}

// StreamConnFor returns the WebSocket connection stored for the given
// conversation ID. Tests use it to verify that the executor's streaming
// routine is live on the wire.
func (f *FakeOpenHands) StreamConnFor(conversationID string) *websocket.Conn {
	f.streamMu.Lock()
	defer f.streamMu.Unlock()
	return f.streamConns[conversationID]
}

// handleHealth answers GET /health.
func (f *FakeOpenHands) handleHealth(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.HealthCalls++
	delay := f.HealthDelay
	status := f.HealthStatus
	if status == 0 {
		status = http.StatusOK
	}
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleStart answers POST /api/conversations with the V1 schema.
func (f *FakeOpenHands) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var parsed map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := validateV1StartBody(parsed); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	f.mu.Lock()
	f.StartCalls++
	f.LastStartBody = parsed
	if f.StartErr != nil {
		errVal := f.StartErr
		status := f.StartStatusCode
		f.mu.Unlock()
		if status == 0 {
			status = http.StatusInternalServerError
		}
		http.Error(w, errVal.Error(), status)
		return
	}
	cid := f.ConversationID
	if cid == "" {
		cid = "conv-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":               cid,
		"workspace":        parsed["workspace"],
		"execution_status": "idle",
	})
}

// validateV1StartBody enforces the V1 schema for POST /api/conversations.
// Returns the standard 422 message used by the real V1 agent-server.
func validateV1StartBody(body map[string]any) error {
	if body == nil {
		return errors.New("body is required")
	}
	ws, ok := body["workspace"].(map[string]any)
	if !ok || ws["kind"] == nil || ws["working_dir"] == nil {
		return errors.New("workspace.kind and workspace.working_dir are required")
	}
	im, ok := body["initial_message"].(map[string]any)
	if !ok || im["role"] == nil {
		return errors.New("initial_message.role is required")
	}
	parts, ok := im["content"].([]any)
	if !ok || len(parts) == 0 {
		return errors.New("initial_message.content must contain at least one part")
	}
	if body["agent"] == nil && body["agent_profile_id"] == nil {
		return errors.New("either agent or agent_profile_id is required")
	}
	if body["agent"] != nil {
		agent, _ := body["agent"].(map[string]any)
		llm, _ := agent["llm"].(map[string]any)
		if llm == nil || llm["model"] == nil || llm["api_key"] == nil || llm["usage_id"] == nil {
			return errors.New("agent.llm requires model, api_key and usage_id")
		}
	}
	return nil
}

// handleSubpath dispatches /api/conversations/{id}/... subpaths.
func (f *FakeOpenHands) handleSubpath(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/conversations/"):]
	if len(path) == 0 {
		http.NotFound(w, r)
		return
	}
	for i, c := range path {
		if c == '/' {
			id := path[:i]
			action := path[i+1:]
			switch action {
			case "pause":
				f.handlePause(w, r, id)
				return
			case "events":
				if r.Method != http.MethodPost {
					http.Error(w, "method", http.StatusMethodNotAllowed)
					return
				}
				f.handleAppend(w, r, id)
				return
			}
		}
	}
	http.NotFound(w, r)
}

// handlePause answers POST /api/conversations/{id}/pause.
func (f *FakeOpenHands) handlePause(w http.ResponseWriter, r *http.Request, id string) {
	f.mu.Lock()
	f.PauseCalls++
	errVal := f.PauseErr
	f.mu.Unlock()
	if errVal != nil {
		http.Error(w, errVal.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"paused"}`))
	_ = id
}

// handleAppend answers POST /api/conversations/{id}/events with the V1
// structured-content shape.
func (f *FakeOpenHands) handleAppend(w http.ResponseWriter, r *http.Request, id string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var parsed map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := validateV1AppendBody(parsed); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	f.mu.Lock()
	f.AppendCalls++
	f.LastAppendBody = parsed
	errVal := f.AppendErr
	f.mu.Unlock()
	if errVal != nil {
		http.Error(w, errVal.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
	_ = id
}

// validateV1AppendBody enforces the V1 structured-content shape.
func validateV1AppendBody(body map[string]any) error {
	if body == nil {
		return errors.New("body is required")
	}
	if body["role"] == nil {
		return errors.New("role is required")
	}
	// V1 removed the top-level `type` field. Reject it explicitly so
	// the executor cannot regress to V0.
	if _, hasOldType := body["type"]; hasOldType {
		return errors.New("top-level type field is no longer accepted in V1")
	}
	parts, ok := body["content"].([]any)
	if !ok || len(parts) == 0 {
		return errors.New("content must be a non-empty array of typed parts")
	}
	for _, raw := range parts {
		part, _ := raw.(map[string]any)
		if part == nil || part["type"] == nil {
			return errors.New("each content part must declare its type")
		}
	}
	return nil
}

// handleSockets upgrades WS requests at /sockets/events/{id}.
func (f *FakeOpenHands) handleSockets(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/sockets/events/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.streamMu.Lock()
	f.streamConns[id] = conn
	f.streamMu.Unlock()

	f.mu.Lock()
	hooks := append([]StreamHook(nil), f.streamHooks...)
	f.mu.Unlock()
	for _, h := range hooks {
		h(conn, id)
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	f.streamMu.Lock()
	delete(f.streamConns, id)
	f.streamMu.Unlock()
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}
