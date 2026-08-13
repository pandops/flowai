// Tests for the v0002 State Registry client. The tests use stdlib
// httptest to drive the wire shape; they never import the State
// Registry's internal packages (AGENTS.md enforces per-service
// isolation) and never depend on a real Postgres.
package stateregistryclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/flowai/platform/executor/k8s-openhands/internal/platform"
	"github.com/flowai/platform/executor/k8s-openhands/internal/stateregistryclient"
)

const (
	teamExecutorID = "exec-team-1"
	systemExecutor = "exec-system-1"
	teamA          = "team-a"
	tag            = "openhands"
)

func newTeamClient(t *testing.T, baseURL string) *stateregistryclient.Client {
	t.Helper()
	c, err := stateregistryclient.New(baseURL, stateregistryclient.Identity{
		ExecutorID: teamExecutorID, Scope: "team", TeamID: teamA,
	}, nil)
	if err != nil {
		t.Fatalf("new team client: %v", err)
	}
	return c
}

func newSystemClient(t *testing.T, baseURL string) *stateregistryclient.Client {
	t.Helper()
	c, err := stateregistryclient.New(baseURL, stateregistryclient.Identity{
		ExecutorID: systemExecutor, Scope: "system",
	}, nil)
	if err != nil {
		t.Fatalf("new system client: %v", err)
	}
	return c
}

// TestClientAllowsEmptyExecutorIDForFirstRegistration pins the v0005
// first-start lifecycle; refresh remains unavailable before POST.
func TestClientAllowsEmptyExecutorIDForFirstRegistration(t *testing.T) {
	c, err := stateregistryclient.New("http://example.com", stateregistryclient.Identity{
		Scope: "system",
	}, nil)
	if err != nil {
		t.Fatalf("new first-start client: %v", err)
	}
	_, err = c.RegisterExecutor(context.Background(), stateregistryclient.RegisterExecutorRequest{})
	if err == nil {
		t.Fatal("expected refresh without executor_id to fail")
	}
}

// TestClientRejectsInvalidScope asserts the v0009 contract:
// identity validation rejects an unknown scope.
func TestClientRejectsInvalidScope(t *testing.T) {
	_, err := stateregistryclient.New("http://example.com", stateregistryclient.Identity{
		ExecutorID: "exec-1", Scope: "alien",
	}, nil)
	if err == nil {
		t.Fatal("expected unknown scope to be rejected")
	}
}

// TestClientRejectsTeamScopeWithoutTeamID asserts the v0009
// contract: team scope requires a team_id.
func TestClientRejectsTeamScopeWithoutTeamID(t *testing.T) {
	_, err := stateregistryclient.New("http://example.com", stateregistryclient.Identity{
		ExecutorID: "exec-1", Scope: "team",
	}, nil)
	if err == nil {
		t.Fatal("expected team scope to require team_id")
	}
}

// TestClientRejectsSystemScopeWithTeamID asserts the v0009
// contract: system scope must not carry a team_id.
func TestClientRejectsSystemScopeWithTeamID(t *testing.T) {
	tid := "team-a"
	_, err := stateregistryclient.New("http://example.com", stateregistryclient.Identity{
		ExecutorID: "exec-1", Scope: "system", TeamID: tid,
	}, nil)
	if err == nil {
		t.Fatal("expected system scope to reject team_id")
	}
}

// TestClientAcceptsHTTPAndHTTPSURLs asserts the v0009 contract:
// the State Registry transport is plaintext; legacy mTLS-only
// https:// URLs are still accepted for staged configuration
// cleanup.
func TestClientAcceptsHTTPAndHTTPSURLs(t *testing.T) {
	for _, u := range []string{
		"http://state-registry.example.com",
		"https://state-registry.example.com:8443",
	} {
		_, err := stateregistryclient.New(u, stateregistryclient.Identity{
			ExecutorID: "exec-1", Scope: "system",
		}, nil)
		if err != nil {
			t.Fatalf("url %q: %v", u, err)
		}
	}
}

// TestRegisterExecutorRoundTrips asserts the wire shape of a
// successful Executor registration PUT.
func TestRegisterExecutorRoundTrips(t *testing.T) {
	var gotBody stateregistryclient.RegisterExecutorRequest
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stateregistryclient.ExecutorRecord{
			ExecutorID:    teamExecutorID,
			Scope:         gotBody.Scope,
			TeamID:        gotBody.TeamID,
			ExecutorType:  gotBody.ExecutorType,
			Identity:      teamExecutorID,
			AuthorizedTag: gotBody.AuthorizedTag,
			MaxCapacity:   gotBody.MaxCapacity,
			RunningCount:  gotBody.RunningCount,
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	tid := teamA
	out, err := c.RegisterExecutor(context.Background(), stateregistryclient.RegisterExecutorRequest{
		Scope:           "team",
		TeamID:          &tid,
		ExecutorType:    "executor_docker_openhands",
		AuthorizedTag:   tag,
		MaxCapacity:     4,
		RunningCount:    0,
		RuntimeMetadata: json.RawMessage(`{"runtime":"docker","tool":"openhands"}`),
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if gotPath != "/v1/executors/"+teamExecutorID {
		t.Fatalf("path=%q", gotPath)
	}
	if out.ExecutorID != teamExecutorID {
		t.Fatalf("executor_id=%q", out.ExecutorID)
	}
}

func TestCreateExecutorUsesPOSTAndPinsGeneratedID(t *testing.T) {
	var raw map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/executors" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		_ = json.NewEncoder(w).Encode(stateregistryclient.ExecutorRecord{
			ExecutorID: "generated-id", Scope: "system", ExecutorType: "executor_k8s_openhands",
			AuthorizedTag: tag,
		})
	}))
	defer server.Close()
	c, err := stateregistryclient.New(server.URL, stateregistryclient.Identity{Scope: "system"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := c.CreateExecutor(context.Background(), stateregistryclient.RegisterExecutorRequest{
		Scope: "system", ExecutorType: "executor_k8s_openhands", AuthorizedTag: tag,
		RuntimeMetadata: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateExecutor: %v", err)
	}
	if record.ExecutorID != "generated-id" || c.Identity().ExecutorID != "generated-id" {
		t.Fatalf("generated identity not pinned: record=%q client=%q", record.ExecutorID, c.Identity().ExecutorID)
	}
	if _, ok := raw["identity"]; ok {
		t.Fatal("request serialized forbidden identity")
	}
	if _, ok := raw["team_id"]; ok {
		t.Fatal("system request serialized team_id")
	}
}

// Test5xxSurfacesAsTypedError asserts non-2xx/4xx responses do
// not silently succeed.
func Test5xxSurfacesAsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(platform.V0002ErrorResponse{Code: "internal_error"})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	_, err := c.DiscoverTasks(context.Background(), tag, 100)
	var he *stateregistryclient.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusInternalServerError {
		t.Fatalf("expected 500 HTTPError, got %v", err)
	}
	if he.Code != "internal_error" {
		t.Errorf("code=%q", he.Code)
	}
}

// TestListAssignedControlsDecodesItems asserts the controls read
// returns the canonical page and empty list when the assigned
// Executor has no pending controls.
func TestListAssignedControlsDecodesItems(t *testing.T) {
	calls := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	items, err := c.ListAssignedControls(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Errorf("items=%+v", items)
	}
	if calls.Load() != 1 {
		t.Errorf("calls=%d", calls.Load())
	}
}
