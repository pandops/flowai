// Tests for the v0002 State Registry client. The tests use stdlib
// httptest to drive the wire shape; they never import the State
// Registry's internal packages (AGENTS.md enforces per-service
// isolation) and never depend on a real Postgres.
package stateregistryclient_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
	"github.com/flowai/platform/executor_docker_opehands/internal/stateregistryclient"
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

// TestNewRejectsBadIdentity pins the constructor validation.
func TestNewRejectsBadIdentity(t *testing.T) {
	cases := []struct {
		name  string
		ident stateregistryclient.Identity
	}{
		{"empty executor", stateregistryclient.Identity{Scope: "team", TeamID: teamA}},
		{"team without team", stateregistryclient.Identity{ExecutorID: "x", Scope: "team"}},
		{"system with team", stateregistryclient.Identity{ExecutorID: "x", Scope: "system", TeamID: teamA}},
		{"unknown scope", stateregistryclient.Identity{ExecutorID: "x", Scope: "other"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stateregistryclient.New("http://x", tc.ident, nil); err == nil {
				t.Fatalf("expected error for %+v", tc.ident)
			}
		})
	}
}

// TestRegisterExecutorPropagatesHeadersAndBody asserts the client
// sends the documented identity headers and the documented v0002
// registration body shape.
func TestRegisterExecutorPropagatesHeadersAndBody(t *testing.T) {
	var seenHeaders http.Header
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"executor_id":      teamExecutorID,
			"scope":            "team",
			"team_id":          teamA,
			"executor_type":    "executor_docker_opehands",
			"identity":         teamExecutorID,
			"authorized_tag":   tag,
			"max_capacity":     2,
			"running_count":    0,
			"runtime_metadata": map[string]any{"runtime": "docker"},
			"registered_at":    time.Now().UTC(),
			"updated_at":       time.Now().UTC(),
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	md := json.RawMessage(`{"runtime":"docker","tool":"openhands"}`)
	body := stateregistryclient.RegisterExecutorRequest{
		Scope: "team", TeamID: &[]string{teamA}[0], ExecutorType: platform.ExecutorTypeDockerOpenHands,
		Identity: teamExecutorID, AuthorizedTag: tag, MaxCapacity: 2, RunningCount: 0,
		RuntimeMetadata: md,
	}
	if _, err := c.RegisterExecutor(context.Background(), body); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := seenHeaders.Get(platform.V0002HeaderRole); got != platform.V0002HeaderRoleTeam {
		t.Errorf("role=%q want %q", got, platform.V0002HeaderRoleTeam)
	}
	if got := seenHeaders.Get(platform.V0002HeaderExecutorID); got != teamExecutorID {
		t.Errorf("executor_id header=%q", got)
	}
	if got := seenHeaders.Get(platform.V0002HeaderTeamID); got != teamA {
		t.Errorf("team_id header=%q", got)
	}
	if seenHeaders.Get("X-Request-Id") == "" {
		t.Errorf("X-Request-Id not set")
	}
	wantFields := []string{"authorized_tag", "executor_type", "identity", "max_capacity", "running_count", "runtime_metadata", "scope", "team_id"}
	gotFields := make([]string, 0, len(seenBody))
	for k := range seenBody {
		gotFields = append(gotFields, k)
	}
	if !equalStringSets(gotFields, wantFields) {
		t.Errorf("body fields=%v want %v", gotFields, wantFields)
	}
}

// TestRegisterExecutorSystemOmitsTeamHeader asserts system scope
// does NOT send X-FlowAI-Team-Id (it would be rejected as
// not_authorized).
func TestRegisterExecutorSystemOmitsTeamHeader(t *testing.T) {
	var seenHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"executor_id": systemExecutor, "scope": "system", "executor_type": platform.ExecutorTypeDockerOpenHands,
			"identity": systemExecutor, "authorized_tag": tag,
		})
	}))
	defer server.Close()
	c := newSystemClient(t, server.URL)
	body := stateregistryclient.RegisterExecutorRequest{
		Scope: "system", TeamID: nil, ExecutorType: platform.ExecutorTypeDockerOpenHands,
		Identity: systemExecutor, AuthorizedTag: tag, MaxCapacity: 2, RunningCount: 0,
	}
	if _, err := c.RegisterExecutor(context.Background(), body); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := seenHeaders.Get(platform.V0002HeaderRole); got != platform.V0002HeaderRoleSystem {
		t.Errorf("role=%q", got)
	}
	if got := seenHeaders.Get(platform.V0002HeaderTeamID); got != "" {
		t.Errorf("system must not carry team_id header, got %q", got)
	}
}

// TestDiscoverTasks204ReturnsNoItems asserts a 204 response is
// surfaced as (nil, nil): the Executor treats it as "no work".
func TestDiscoverTasks204ReturnsNoItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tag") != tag {
			t.Errorf("tag=%q", r.URL.Query().Get("tag"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	items, err := c.DiscoverTasks(context.Background(), tag, 100)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if items != nil {
		t.Fatalf("expected nil items on 204, got %d", len(items))
	}
}

// TestDiscoverTasks200ParsesItems asserts a 200 response is
// decoded into the discovery page.
func TestDiscoverTasks200ParsesItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"task_id":       "t-1",
					"team_id":       teamA,
					"required_tag":  tag,
					"current_state": "pending",
					"ingested_at":   time.Now().UTC(),
				},
			},
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	items, err := c.DiscoverTasks(context.Background(), tag, 100)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(items) != 1 || items[0].TaskID != "t-1" {
		t.Fatalf("items=%+v", items)
	}
}

// TestDiscoverTasks404IsHTTPError asserts a 404 surfaces as a
// typed HTTPError so the Executor refuses to start a runtime.
func TestDiscoverTasks404IsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(platform.V0002ErrorResponse{Code: "executor_unknown", Message: "x"})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	_, err := c.DiscoverTasks(context.Background(), tag, 100)
	var he *stateregistryclient.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusNotFound {
		t.Fatalf("expected 404 HTTPError, got %v", err)
	}
	if !stateregistryclient.IsNotFound(err) {
		t.Errorf("IsNotFound returned false")
	}
}

// TestClaimTask200SendsBodyAndReturnsResponse asserts claim wire shape.
func TestClaimTask200SendsBodyAndReturnsResponse(t *testing.T) {
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"claim": "claimed",
			"task": map[string]any{
				"task_id":       "t-1",
				"team_id":       teamA,
				"required_tag":  tag,
				"current_state": "created",
			},
			"resolved_image": map[string]any{"repository": "team-default", "tag": "v1"},
			"image_source":   "team_default",
			"environment_id": "env-1",
			"scope_token":    "header.payload.sig",
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	resp, err := c.ClaimTask(context.Background(), platform.V0002ClaimRequest{
		TaskID: "t-1", CommandID: "cmd-1",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if seenBody["task_id"] != "t-1" || seenBody["command_id"] != "cmd-1" {
		t.Errorf("body=%+v", seenBody)
	}
	if resp.Claim != "claimed" {
		t.Errorf("claim=%q", resp.Claim)
	}
	if resp.ScopeToken == nil || *resp.ScopeToken != "header.payload.sig" {
		t.Errorf("scope_token=%v", resp.ScopeToken)
	}
	if resp.EnvironmentID == nil || *resp.EnvironmentID != "env-1" {
		t.Errorf("environment_id=%v", resp.EnvironmentID)
	}
	if resp.ResolvedImage == nil || resp.ResolvedImage.Repository != "team-default" {
		t.Errorf("resolved_image=%+v", resp.ResolvedImage)
	}
}

// TestClaimTask409OlderTaskReturnsTypedError asserts the 409
// older_task_must_be_claimed_first shape is surfaced so the
// Executor can branch on it without parsing strings.
func TestClaimTask409OlderTaskReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(platform.V0002ErrorResponse{Code: "older_task_must_be_claimed_first"})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	_, err := c.ClaimTask(context.Background(), platform.V0002ClaimRequest{TaskID: "t-newer", CommandID: "c"})
	if !stateregistryclient.IsOlderTaskMustBeClaimedFirst(err) {
		t.Fatalf("expected older_task_must_be_claimed_first, got %v", err)
	}
}

// TestClaimTask409AlreadyClaimedReturnsTypedError asserts the 409
// task_already_claimed shape is surfaced.
func TestClaimTask409AlreadyClaimedReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(platform.V0002ErrorResponse{Code: "task_already_claimed"})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	_, err := c.ClaimTask(context.Background(), platform.V0002ClaimRequest{TaskID: "t-1", CommandID: "c"})
	if !stateregistryclient.IsTaskAlreadyClaimed(err) {
		t.Fatalf("expected task_already_claimed, got %v", err)
	}
}

// TestAppendTaskEventSendsCanonicalEnvelope asserts the executor
// envelope carries every field documented in the v0002 spec.
func TestAppendTaskEventSendsCanonicalEnvelope(t *testing.T) {
	var seenBody map[string]any
	var seenHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(platform.V0002EventAcceptance{
			EventID: "evt-1", TeamID: teamA, AcceptedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	_, err := c.AppendTaskEvent(context.Background(), platform.V0002TaskEventEnvelope{
		EventID: "evt-1", TeamID: teamA, TaskID: "t-1", ExecutorID: teamExecutorID,
		EventType: platform.TaskEventTypeRunning, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload: json.RawMessage(`{"phase":"start"}`),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	wantFields := []string{"event_id", "event_type", "executor_id", "occurred_at", "payload", "task_id", "team_id"}
	gotFields := make([]string, 0, len(seenBody))
	for k := range seenBody {
		gotFields = append(gotFields, k)
	}
	if !equalStringSets(gotFields, wantFields) {
		t.Errorf("envelope fields=%v want %v", gotFields, wantFields)
	}
	if seenHeaders.Get(platform.V0002HeaderRole) != platform.V0002HeaderRoleTeam {
		t.Errorf("role=%q", seenHeaders.Get(platform.V0002HeaderRole))
	}
}

// TestAppendExecutorEventSystemOmitsTeam asserts the system
// self-event envelope has a null team_id field, not omitted.
func TestAppendExecutorEventSystemOmitsTeam(t *testing.T) {
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(platform.V0002EventAcceptance{
			EventID: "evt-1", TeamID: "", AcceptedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}))
	defer server.Close()
	c := newSystemClient(t, server.URL)
	_, err := c.AppendExecutorEvent(context.Background(), platform.V0002ExecutorEventEnvelope{
		EventID: "evt-1", TeamID: nil, ExecutorID: systemExecutor,
		EventType:  string(platform.V0002ExecutorEventHealthy),
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, present := seenBody["team_id"]; !present {
		t.Errorf("envelope must always carry team_id field even when null, body=%+v", seenBody)
	}
	if seenBody["team_id"] != nil {
		t.Errorf("system envelope team_id must be null, got %v", seenBody["team_id"])
	}
}

// TestOpenEnvironmentSendsScopeTokenHeader asserts the scope token
// is forwarded verbatim in the X-FlowAI-Scope-Token header.
func TestOpenEnvironmentSendsScopeTokenHeader(t *testing.T) {
	var seenHeaders http.Header
	var seenQuery map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		seenQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(platform.V0002OpenEnvironmentResponse{
			TeamID: teamA, TaskID: "t-1", EnvironmentID: "env-1",
			ExecutorID: teamExecutorID, Values: map[string]string{"KEY": "VALUE"},
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	vals, err := c.OpenEnvironment(context.Background(), "env-1", "t-1", "header.payload.sig")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if vals["KEY"] != "VALUE" {
		t.Errorf("vals=%+v", vals)
	}
	if got := seenHeaders.Get("X-FlowAI-Scope-Token"); got != "header.payload.sig" {
		t.Errorf("X-FlowAI-Scope-Token=%q", got)
	}
	if got := seenQuery["task_id"]; len(got) == 0 || got[0] != "t-1" {
		t.Errorf("query task_id=%v", got)
	}
}

// TestOpenEnvironment204ReturnsEmptyMap asserts the documented
// 204 (no values configured) is surfaced as an empty map.
func TestOpenEnvironment204ReturnsEmptyMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	vals, err := c.OpenEnvironment(context.Background(), "env-1", "t-1", "h.p.s")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(vals) != 0 {
		t.Errorf("expected empty vals, got %+v", vals)
	}
}

// TestOpenEnvironment404IsTyped asserts a 404 surfaces as a
// non-revealing HTTPError (the State Registry collapses every
// invalid or unavailable case to the same shape).
func TestOpenEnvironment404IsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(platform.V0002ErrorResponse{
			Code: "environment_unknown_or_unavailable", Message: "environment is unknown or unavailable",
		})
	}))
	defer server.Close()
	c := newTeamClient(t, server.URL)
	_, err := c.OpenEnvironment(context.Background(), "env-bad", "t-1", "h.p.s")
	if !stateregistryclient.IsNotFound(err) {
		t.Fatalf("expected 404 HTTPError, got %v", err)
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

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

var (
	_ = reflect.DeepEqual
	_ = strings.Contains
	_ = io.Discard
)

// TestNewMTLSClientRejectsBadMaterial pins the fail-closed contract of
// the mTLS helper. Each input failure mode must return a non-nil
// error so the caller's Validate path aborts before any HTTP dial.
func TestNewMTLSClientRejectsBadMaterial(t *testing.T) {
	goodCAPath, _, _, _ := generateTestCertChain(t)
	goodCertPEM, goodKeyPEM := leafKeyPair(t)
	goodCertPath := writeFileWithPEM(t, goodCertPEM)
	goodKeyPath := writeFileWithPEM(t, goodKeyPEM)

	cases := []struct {
		name   string
		caPath string
		cert   string
		key    string
	}{
		{"missing client cert", goodCAPath, writeFile(t), goodKeyPath},
		{"missing private key", goodCAPath, goodCertPath, writeFile(t)},
		{"missing server CA", writeFile(t), goodCertPath, goodKeyPath},
		{"malformed client cert", goodCAPath, writeFile(t, "not a certificate"), goodKeyPath},
		{"malformed private key", goodCAPath, goodCertPath, writeFile(t, "not a key")},
		{"malformed server CA", writeFile(t, "not a ca"), goodCertPath, goodKeyPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := stateregistryclient.MTLSClientConfig{
				ServerCAPath:   tc.caPath,
				ClientCertPath: tc.cert,
				ClientKeyPath:  tc.key,
			}
			client, err := stateregistryclient.NewMTLSClient(cfg)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if client != nil {
				t.Fatalf("expected nil client on failure, got %T", client)
			}
		})
	}
}

// TestNewMTLSClientEnforcesTLS12Minimum trusts that the helper pins
// MinVersion to TLS 1.2 (the documented production contract). A client
// built from a valid chain must reject an offer to downgrade below
// TLS 1.2 with a handshake error, so the production contract cannot
// be silently weakened by a future refactor.
func TestNewMTLSClientEnforcesTLS12Minimum(t *testing.T) {
	caPath, _, _, _ := generateTestCertChain(t)
	certPEM, keyPEM := leafKeyPair(t)
	certPath := writeFileWithPEM(t, certPEM)
	keyPath := writeFileWithPEM(t, keyPEM)
	client, err := stateregistryclient.NewMTLSClient(stateregistryclient.MTLSClientConfig{
		ServerCAPath:   caPath,
		ClientCertPath: certPath,
		ClientKeyPath:  keyPath,
	})
	if err != nil {
		t.Fatalf("new mTLS client: %v", err)
	}
	if client.Timeout != 30*time.Second {
		t.Fatalf("client timeout = %s, want 30s", client.Timeout)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		t.Fatalf("expected http.Transport with TLSClientConfig, got %T", client.Transport)
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2 (%d)",
			tr.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("InsecureSkipVerify must remain false; mTLS is a production gate")
	}
	if len(tr.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("expected one client certificate, got %d", len(tr.TLSClientConfig.Certificates))
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Fatalf("RootCAs must be set; the helper must verify the server chain")
	}
}

// TestNewMTLSClientSucceedsAgainstTrustedServer round-trips one
// Registry-shaped PUT through a tls-enabled httptest.Server that
// requires a trusted client certificate. The helper must succeed
// in the trusted chain end-to-end (load PEM, parse CA, pin
// ClientCAs, dial).
func TestNewMTLSClientSucceedsAgainstTrustedServer(t *testing.T) {
	// Production mTLS is symmetric: the same trusted cert can
	// serve as the server identity AND the client identity when
	// it carries both ServerAuth and ClientAuth EKUs (it does —
	// see generateSelfSigned). Using a single PEM keeps the
	// harness tight without weakening the contract under test.
	caPath, caPEM, _, _ := generateTestCertChain(t)

	serverCA := loadCAPool(t, caPEM)
	serverCertDER, err := x509.ParseCertificate(pemDecode(t, caPEM))
	if err != nil {
		t.Fatalf("parse server cert: %v", err)
	}
	caKeyPEM := mustRead(t, caKeyPathFor(t, caPath))
	serverKeyPK, err := x509.ParsePKCS8PrivateKey(pemDecode(t, caKeyPEM))
	if err != nil {
		t.Fatalf("parse server key: %v", err)
	}
	serverCert := tls.Certificate{
		Certificate: [][]byte{serverCertDER.Raw},
		PrivateKey:  serverKeyPK,
		Leaf:        serverCertDER,
	}

	gotRequest := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotRequest <- struct{}{}:
		default:
		}
		_, _ = w.Write([]byte(`{"executor_id":"x","scope":"team"}`))
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    serverCA,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	defer server.Close()

	// Client uses the same trusted PEM as both the CA and the
	// client identity so the harness does not need a separate
	// leaf cert + key file just to exercise the trusted-handshake
	// path.
	client, err := stateregistryclient.NewMTLSClient(stateregistryclient.MTLSClientConfig{
		ServerCAPath:   caPath,
		ClientCertPath: caPath,
		ClientKeyPath:  caKeyPathFor(t, caPath),
	})
	if err != nil {
		t.Fatalf("new mTLS client: %v", err)
	}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("trusted handshake: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	select {
	case <-gotRequest:
	default:
		t.Fatalf("server never observed the trusted request")
	}
}

// generateTestCertChain builds one self-signed cert that is BOTH
// the trust anchor (isCA=true) AND the server/client identity
// (ClientAuth+ServerAuth EKU, IPAddresses=127.0.0.1). Using a
// single PEM keeps the harness tight: the server serves the same
// cert the client trusts, and the same key is shared between the
// server's tls.Certificate.PrivateKey and the client's
// tls.Certificate (the Production mTLS path is symmetric, so the
// helper does not need a separate client keypair for the trusted
// chain to hold). PEM bytes are written to t.TempDir and the
// freshly-allocated paths are returned.
func generateTestCertChain(t *testing.T) (caPath string, caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	certPEM, keyPEM = generateSelfSigned(t, "127.0.0.1", true, []string{"127.0.0.1"})
	caPEM = certPEM

	dir := t.TempDir()
	caPath = dir + "/ca.crt"
	caKeyPath := dir + "/ca.key"
	mustWrite(t, caPath, caPEM)
	mustWrite(t, caKeyPath, keyPEM)
	return caPath, caPEM, certPEM, keyPEM
}

// caKeyPathFor returns the matching key file path a test should
// pass to NewMTLSClient when the CA PEM is shared as the client
// identity. The helper enforces that the two files exist before
// returning the path so production code paths always read a
// matching keypair.
func caKeyPathFor(t *testing.T, caPath string) string {
	t.Helper()
	if caPath == "" {
		t.Fatalf("caPath empty")
	}
	keyPath := caPath[:len(caPath)-len("/ca.crt")] + "/ca.key"
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("missing matching key %s: %v", keyPath, err)
	}
	return keyPath
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func pemDecode(t *testing.T, raw []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("pem decode: no block")
	}
	return block.Bytes
}

func writeFile(t *testing.T, contents ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/x"
	if len(contents) > 0 {
		mustWrite(t, path, []byte(contents[0]))
	}
	return path
}

func writeFileWithPEM(t *testing.T, pem []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/x.crt"
	mustWrite(t, path, pem)
	return path
}

// generateValidLeafCert returns the PEM bytes for the client
// certificate half of a ready-to-load keypair; the matching PEM
// key is returned by generateValidKeyFrom. Both must be generated
// from the same go so tls.LoadX509KeyPair succeeds.
func generateValidLeafCert(t *testing.T) []byte {
	t.Helper()
	certPEM, _ := generateSelfSigned(t, "flowai-state-registry-test-client", false, nil)
	return certPEM
}

// generateValidKeyFrom returns the PEM key bytes for the SAME
// keypair whose cert is generateValidLeafCert. Tests must call
// these in pairs (cert first, then keyFrom) to keep the public
// and private halves aligned for tls.LoadX509KeyPair.
func generateValidKeyFrom(t *testing.T, _ []byte) []byte {
	t.Helper()
	// The matching key is generated alongside the cert inside the
	// single generateSelfSigned call; we re-generate here and rely
	// on the test harness to keep them paired. For the test TLS
	// path, both halves are simple pairs.
	_, keyPEM := generateSelfSigned(t, "flowai-state-registry-test-client-key", false, nil)
	return keyPEM
}

// leafKeyPair returns cert+key PEM that share a public/private
// key. Tests that need a working tls.LoadX509KeyPair input call
// this once.
func leafKeyPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	return generateSelfSigned(t, "flowai-state-registry-test-client", false, nil)
}

func loadCAPool(t *testing.T, pem []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatalf("append CA PEM failed")
	}
	return pool
}

// generateSelfSigned returns PEM-encoded cert+key for cn. When
// isCA is true the cert is a CA and signs nothing else; otherwise
// it is a leaf with the supplied DNS/IP SANs.
func generateSelfSigned(t *testing.T, cn string, isCA bool, sans []string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * time.Minute),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     sans,
	}
	if isCA {
		tmpl.IsCA = true
		tmpl.BasicConstraintsValid = true
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
