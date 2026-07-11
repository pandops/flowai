package mocks

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// FakeOpenHands is a tiny in-process OpenHands server for testing the
// Executor's openhands.Client. It records every received request and exposes
// knobs to control health, conversation start, pause, and event responses.
type FakeOpenHands struct {
	mu sync.Mutex

	// Health responses. Default = 200 OK.
	HealthStatus int
	HealthDelay  time.Duration

	// ConversationStart body. If nil, a default is used.
	ConversationID string

	// ConversationStart returns this error if non-nil.
	StartErr error

	// Pause returns this error if non-nil.
	PauseErr error

	// EventAppend returns this error if non-nil.
	AppendErr error

	// Captured requests.
	HealthCalls   int
	StartCalls    int
	PauseCalls    int
	AppendCalls   int
	LastAppendBody map[string]any

	// HTTP server.
	srv     *http.Server
	listener net.Listener
	stopped chan struct{}
}

// Start launches the fake server on a random localhost port.
func (f *FakeOpenHands) Start() {
	f.startOn("127.0.0.1:0")
}

// StartOn launches the fake server on the supplied address (host:port).
// Useful when the executor expects a specific port.
func (f *FakeOpenHands) StartOn(addr string) {
	f.startOn(addr)
}

func (f *FakeOpenHands) startOn(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", f.handleHealth)
	mux.HandleFunc("/api/conversations", f.handleStart)
	mux.HandleFunc("/api/conversations/", f.handleSubpath)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	f.listener = ln
	f.stopped = make(chan struct{})
	f.srv = &http.Server{Handler: mux}
	go func() {
		_ = f.srv.Serve(ln)
		close(f.stopped)
	}()
}

// Stop shuts the server down.
func (f *FakeOpenHands) Stop() {
	if f.srv != nil {
		_ = f.srv.Close()
	}
	if f.listener != nil {
		_ = f.listener.Close()
	}
}

// URL returns the server's base URL.
func (f *FakeOpenHands) URL() string {
	return "http://" + f.listener.Addr().String()
}

// Port returns the bound TCP port (useful when bound to :0).
func (f *FakeOpenHands) Port() int {
	return f.listener.Addr().(*net.TCPAddr).Port
}

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

func (f *FakeOpenHands) handleStart(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.StartCalls++
	if f.StartErr != nil {
		errVal := f.StartErr
		f.mu.Unlock()
		http.Error(w, errVal.Error(), http.StatusInternalServerError)
		return
	}
	cid := f.ConversationID
	if cid == "" {
		cid = "conv-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"conversation_id": cid})
}

func (f *FakeOpenHands) handleSubpath(w http.ResponseWriter, r *http.Request) {
	// /api/conversations/{id}/{action}
	path := r.URL.Path[len("/api/conversations/"):]
	if len(path) == 0 {
		http.NotFound(w, r)
		return
	}
	// path is "id/pause" or "id/events"
	for i, c := range path {
		if c == '/' {
			id := path[:i]
			action := path[i+1:]
			switch action {
			case "pause":
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
				return
			case "events":
				f.mu.Lock()
				f.AppendCalls++
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				f.LastAppendBody = body
				errVal := f.AppendErr
				f.mu.Unlock()
				if errVal != nil {
					http.Error(w, errVal.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
				return
			}
		}
	}
	http.NotFound(w, r)
}