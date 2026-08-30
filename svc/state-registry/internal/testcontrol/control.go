// Package testcontrol provides an isolated, disabled-by-default E2E transaction barrier server.
package testcontrol

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var allowed = map[string]struct{}{"team_archive_after_lock": {}, "task_ingest_after_team_lock": {}, "task_claim_after_team_lock": {}, "team_default_image_update_after_lock": {}, "team_name_write_after_constraint_before_commit": {}}

type Barrier struct {
	ID        string        `json:"barrier_id"`
	Name      string        `json:"name"`
	State     string        `json:"state"`
	ExpiresAt time.Time     `json:"expires_at"`
	release   chan struct{} `json:"-"`
	changed   chan struct{} `json:"-"`
}
type Coordinator struct {
	mu      sync.Mutex
	timeout time.Duration
	byID    map[string]*Barrier
	active  map[string]string
	closed  bool
}

func New(timeout time.Duration) *Coordinator {
	return &Coordinator{timeout: timeout, byID: map[string]*Barrier{}, active: map[string]string{}}
}
func (c *Coordinator) Arm(name string) (Barrier, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := allowed[name]; !ok {
		return Barrier{}, errors.New("invalid")
	}
	if _, ok := c.active[name]; ok {
		return Barrier{}, errors.New("conflict")
	}
	b := &Barrier{ID: uuid.NewString(), Name: name, State: "armed", ExpiresAt: time.Now().Add(c.timeout), release: make(chan struct{}), changed: make(chan struct{})}
	c.byID[b.ID] = b
	c.active[name] = b.ID
	return copyBarrier(b), nil
}
func (c *Coordinator) Wait(ctx context.Context, name string) {
	c.mu.Lock()
	id, ok := c.active[name]
	if !ok || c.closed {
		c.mu.Unlock()
		return
	}
	b := c.byID[id]
	if b.State != "armed" {
		c.mu.Unlock()
		return
	}
	b.State = "reached"
	signal(b)
	release := b.release
	deadline := time.Until(b.ExpiresAt)
	c.mu.Unlock()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	state := "cancelled"
	select {
	case <-release:
		state = "released"
	case <-timer.C:
		state = "timed_out"
	case <-ctx.Done():
	}
	c.terminal(b.ID, state)
}
func (c *Coordinator) terminal(id, state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.byID[id]
	if b == nil || isTerminal(b.State) {
		return
	}
	b.State = state
	delete(c.active, b.Name)
	signal(b)
}
func (c *Coordinator) Get(ctx context.Context, id string, wait time.Duration) (Barrier, bool) {
	c.mu.Lock()
	b := c.byID[id]
	if b == nil {
		c.mu.Unlock()
		return Barrier{}, false
	}
	initial := b.State
	changed := b.changed
	c.mu.Unlock()
	if wait > 0 && !isTerminal(initial) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-changed:
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b = c.byID[id]
	if b == nil {
		return Barrier{}, false
	}
	return copyBarrier(b), true
}
func (c *Coordinator) Release(id string) (Barrier, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.byID[id]
	if b == nil {
		return Barrier{}, "not_found"
	}
	if b.State != "reached" {
		return copyBarrier(b), "not_reached"
	}
	b.State = "released"
	delete(c.active, b.Name)
	signal(b)
	close(b.release)
	return copyBarrier(b), ""
}
func (c *Coordinator) Delete(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.byID[id]
	if b == nil {
		return false
	}
	if !isTerminal(b.State) {
		b.State = "cancelled"
		delete(c.active, b.Name)
		close(b.release)
	}
	delete(c.byID, id)
	return true
}
func (c *Coordinator) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, b := range c.byID {
		if !isTerminal(b.State) {
			b.State = "cancelled"
			close(b.release)
		}
		delete(c.byID, id)
	}
	c.active = map[string]string{}
}
func signal(b *Barrier)        { close(b.changed); b.changed = make(chan struct{}) }
func isTerminal(s string) bool { return s == "released" || s == "timed_out" || s == "cancelled" }
func copyBarrier(b *Barrier) Barrier {
	return Barrier{ID: b.ID, Name: b.Name, State: b.State, ExpiresAt: b.ExpiresAt}
}

func Handler(c *Coordinator, token string) http.Handler {
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				writeError(w, 401, "invalid_control_token")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /test-control/v1/barriers", auth(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name string `json:"name"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil {
			writeError(w, 400, "invalid_control_request")
			return
		}
		b, err := c.Arm(input.Name)
		if err != nil {
			if err.Error() == "conflict" {
				writeError(w, 409, "barrier_conflict")
			} else {
				writeError(w, 400, "invalid_control_request")
			}
			return
		}
		writeJSON(w, 201, b)
	}))
	mux.HandleFunc("GET /test-control/v1/barriers/{barrier_id}", auth(func(w http.ResponseWriter, r *http.Request) {
		wait := time.Duration(0)
		if raw := r.URL.Query().Get("wait_ms"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || value > 30000 {
				writeError(w, 400, "invalid_control_request")
				return
			}
			wait = time.Duration(value) * time.Millisecond
		}
		b, ok := c.Get(r.Context(), r.PathValue("barrier_id"), wait)
		if !ok {
			writeError(w, 404, "barrier_not_found")
			return
		}
		writeJSON(w, 200, b)
	}))
	mux.HandleFunc("POST /test-control/v1/barriers/{barrier_id}/release", auth(func(w http.ResponseWriter, r *http.Request) {
		b, status := c.Release(r.PathValue("barrier_id"))
		if status == "not_found" {
			writeError(w, 404, "barrier_not_found")
			return
		}
		if status == "not_reached" {
			writeError(w, 409, "barrier_not_reached")
			return
		}
		writeJSON(w, 200, b)
	}))
	mux.HandleFunc("DELETE /test-control/v1/barriers/{barrier_id}", auth(func(w http.ResponseWriter, r *http.Request) {
		if !c.Delete(r.PathValue("barrier_id")) {
			writeError(w, 404, "barrier_not_found")
			return
		}
		w.WriteHeader(204)
	}))
	return mux
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code, "message": strings.ReplaceAll(code, "_", " ")})
}
