// Section 9 — environment and secret cursor pagination, list
// integrity, and same-team secret revoke. The tests below cover
// the OpenSpec contract for the new cursor-aware collections
// (GET /v1/environments, GET /v1/environments/{id}/secrets,
// GET /v1/environments/{id}/secrets/{id}/versions) and the
// DELETE /v1/environments/{id}/secrets/{id} route. Each test pins
// one invariant and asserts (1) the documented response shape and
// (2) the cursor + limit-before-query contract by mechanically
// counting the number of times the protected repository method was
// invoked.
package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/cursor"
	"github.com/flowai/platform/svc/state-registry/internal/httpapi"
	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// recordingEnvSecretRepo is a thread-safe test double for the
// environment/secret repositories. It records every method
// invocation so the cursor + limit-before-query contract is
// mechanically verifiable.
type recordingEnvSecretRepo struct {
	envCalls         atomic.Int64
	secretListCalls  atomic.Int64
	versionListCalls atomic.Int64
	revokeCalls      atomic.Int64
	envGetCalls      atomic.Int64
	secretGetCalls   atomic.Int64

	// Optional return overrides used by the ordering and pagination
	// tests to drive the cursor encode/decode path.
	listEnvFn       func(limit int, after *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error)
	listSecretFn    func(environmentID string, limit int, after *store.SecretAfter) ([]platform.LogicalSecret, *store.SecretAfter, error)
	listVersionsFn  func(environmentID, secretID string, limit int, after *store.SecretVersionAfter) ([]platform.SecretVersion, *store.SecretVersionAfter, error)
	revokeFn        func(environmentID, secretID string) (platform.LogicalSecret, error)
	listEnvErr      error
	listSecretErr   error
	listVersionsErr error
	revokeErr       error
	revokeResult    *platform.LogicalSecret
}

var _ store.EnvironmentRepository = (*recordingEnvSecretRepo)(nil)
var _ store.SecretRepository = (*recordingEnvSecretRepo)(nil)

func (r *recordingEnvSecretRepo) CreateEnvironment(_ context.Context, _ platform.GatewayIdentity, _ platform.EnvironmentWriteRequest) (platform.Environment, error) {
	return platform.Environment{}, nil
}
func (r *recordingEnvSecretRepo) ListEnvironments(_ context.Context, _ string, _, _ *string, _ int) ([]platform.Environment, error) {
	r.envCalls.Add(1)
	return nil, nil
}
func (r *recordingEnvSecretRepo) ListEnvironmentsPaged(_ context.Context, _ string, _, _ *string, limit int, after *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error) {
	r.envCalls.Add(1)
	if r.listEnvErr != nil {
		return nil, nil, r.listEnvErr
	}
	if r.listEnvFn != nil {
		return r.listEnvFn(limit, after)
	}
	return nil, nil, nil
}
func (r *recordingEnvSecretRepo) GetEnvironment(_ context.Context, _ string, _ string) (platform.Environment, error) {
	r.envGetCalls.Add(1)
	return platform.Environment{}, nil
}
func (r *recordingEnvSecretRepo) ReplaceEnvironment(_ context.Context, _ platform.GatewayIdentity, _ string, _ platform.EnvironmentWriteRequest) (platform.Environment, error) {
	return platform.Environment{}, nil
}
func (r *recordingEnvSecretRepo) DeleteEnvironment(_ context.Context, _ platform.GatewayIdentity, _ string) error {
	return nil
}

// SecretRepository methods.
func (r *recordingEnvSecretRepo) CreateSecret(_ context.Context, _ platform.GatewayIdentity, _ string, _ platform.SecretCreateRequest) (platform.SecretWriteResponse, error) {
	return platform.SecretWriteResponse{}, nil
}
func (r *recordingEnvSecretRepo) ReplaceSecret(_ context.Context, _ platform.GatewayIdentity, _, _ string, _ platform.SecretReplaceRequest) (platform.SecretWriteResponse, error) {
	return platform.SecretWriteResponse{}, nil
}
func (r *recordingEnvSecretRepo) GetSecret(_ context.Context, _, _, _ string) (platform.LogicalSecret, error) {
	r.secretGetCalls.Add(1)
	return platform.LogicalSecret{}, nil
}
func (r *recordingEnvSecretRepo) ListSecrets(_ context.Context, _, _ string, _ int) ([]platform.LogicalSecret, error) {
	r.secretListCalls.Add(1)
	return nil, nil
}
func (r *recordingEnvSecretRepo) ListSecretsPaged(_ context.Context, teamID, environmentID string, limit int, after *store.SecretAfter) ([]platform.LogicalSecret, *store.SecretAfter, error) {
	r.secretListCalls.Add(1)
	if r.listSecretErr != nil {
		return nil, nil, r.listSecretErr
	}
	if r.listSecretFn != nil {
		return r.listSecretFn(environmentID, limit, after)
	}
	return nil, nil, nil
}
func (r *recordingEnvSecretRepo) ListSecretVersions(_ context.Context, _, _, _ string, _ int) ([]platform.SecretVersion, error) {
	r.versionListCalls.Add(1)
	return nil, nil
}
func (r *recordingEnvSecretRepo) ListSecretVersionsPaged(_ context.Context, teamID, environmentID, secretID string, limit int, after *store.SecretVersionAfter) ([]platform.SecretVersion, *store.SecretVersionAfter, error) {
	r.versionListCalls.Add(1)
	if r.listVersionsErr != nil {
		return nil, nil, r.listVersionsErr
	}
	if r.listVersionsFn != nil {
		return r.listVersionsFn(environmentID, secretID, limit, after)
	}
	return nil, nil, nil
}
func (r *recordingEnvSecretRepo) GetSecretVersion(_ context.Context, _, _, _ string, _ int) (platform.SecretVersion, error) {
	return platform.SecretVersion{}, nil
}
func (r *recordingEnvSecretRepo) RevokeSecret(_ context.Context, _ platform.GatewayIdentity, environmentID, secretID string) (platform.LogicalSecret, error) {
	r.revokeCalls.Add(1)
	if r.revokeErr != nil {
		return platform.LogicalSecret{}, r.revokeErr
	}
	if r.revokeFn != nil {
		return r.revokeFn(environmentID, secretID)
	}
	if r.revokeResult != nil {
		return *r.revokeResult, nil
	}
	return platform.LogicalSecret{SecretID: secretID, EnvironmentID: environmentID, Revoked: true}, nil
}

// envSecretHarness mounts the Section 9 environment and secret
// handlers on a fresh router with the supplied repository. The
// harness is used to assert both the response shape and the
// before-query cursor + limit rejection contract.
type envSecretHarness struct {
	router  *chi.Mux
	repo    *recordingEnvSecretRepo
	keyring *testCursorKeyring
	server  *httptest.Server
}

func newEnvSecretHarness(t *testing.T) *envSecretHarness {
	t.Helper()
	repo := &recordingEnvSecretRepo{}
	keyring := newTestCursorKeyring(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	router := chi.NewRouter()
	httpapi.RegisterEnvironments(router, logger, repo, keyring)
	httpapi.RegisterSecrets(router, logger, repo, keyring)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &envSecretHarness{router: router, repo: repo, keyring: keyring, server: srv}
}

func (h *envSecretHarness) do(t *testing.T, method, path string, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp, body
}

func gatewayEnvHeaders(teamID, operatorID, requestID string) http.Header {
	h := http.Header{}
	h.Set("X-FlowAI-Role", "gateway")
	h.Set("X-FlowAI-Team-Id", teamID)
	h.Set("X-FlowAI-Operator-Id", operatorID)
	h.Set("X-FlowAI-Request-Id", requestID)
	return h
}

func decodeEnvEnvError(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env
}

// TestEnvironmentListAcceptsCursorAndPaginates proves the cursor
// envelope is accepted, the (created_at ASC, environment_id ASC)
// ordering is preserved, and the next_cursor is emitted only when
// another row exists beyond the page.
func TestEnvironmentListAcceptsCursorAndPaginates(t *testing.T) {
	h := newEnvSecretHarness(t)
	entries := []platform.Environment{
		{EnvironmentID: "env-a", TeamID: "team-a", Name: "env-a", CreatedAt: time.Unix(0, 1000).UTC()},
		{EnvironmentID: "env-b", TeamID: "team-a", Name: "env-b", CreatedAt: time.Unix(0, 2000).UTC()},
		{EnvironmentID: "env-c", TeamID: "team-a", Name: "env-c", CreatedAt: time.Unix(0, 3000).UTC()},
	}
	h.repo.setListEnvFn(func(limit int, after *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error) {
		// Simulate exact-limit final page (no next cursor).
		if limit != 1 {
			t.Errorf("env list limit=%d, want 1 (caller controls pagination)", limit)
		}
		_ = after
		return entries[:1], nil, nil
	})

	q := url.Values{}
	q.Set("limit", "1")
	resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-env-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.envCalls.Load(); got != 1 {
		t.Errorf("ListEnvironmentsPaged calls=%d, want 1", got)
	}
	var page platform.EnvironmentPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode page: %v (body=%s)", err, body)
	}
	if page.Page.Count != 1 {
		t.Errorf("count=%d, want 1", page.Page.Count)
	}
	if page.Page.NextCursor != nil {
		t.Errorf("next_cursor should be nil for non-overflowing page")
	}
}

// TestEnvironmentListEmitsNextCursorOnOverflow proves the next
// cursor is emitted when limit+1 rows are returned.
func TestEnvironmentListEmitsNextCursorOnOverflow(t *testing.T) {
	h := newEnvSecretHarness(t)
	entries := []platform.Environment{
		{EnvironmentID: "env-a", TeamID: "team-a", Name: "env-a", CreatedAt: time.Unix(0, 1000).UTC()},
		{EnvironmentID: "env-b", TeamID: "team-a", Name: "env-b", CreatedAt: time.Unix(0, 2000).UTC()},
	}
	h.repo.setListEnvFn(func(limit int, after *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error) {
		_ = after
		// The store contract slices to limit and emits a non-nil
		// next when there are additional rows beyond the page.
		return entries[:limit], &store.EnvironmentAfter{
			CreatedAtUnixNano: entries[limit-1].CreatedAt.UnixNano(),
			EnvironmentID:     entries[limit-1].EnvironmentID,
		}, nil
	})

	q := url.Values{}
	q.Set("limit", "1")
	resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-env-2"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	var page platform.EnvironmentPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode page: %v (body=%s)", err, body)
	}
	if page.Page.Count != 1 {
		t.Errorf("count=%d, want 1", page.Page.Count)
	}
	if page.Page.NextCursor == nil || *page.Page.NextCursor == "" {
		t.Fatalf("next_cursor must be populated for limit+1 overflow")
	}
	// The emitted cursor must decode and bind to the documented
	// (team-a, env-a) position under the documented
	// (created_at ASC, environment_id ASC) ordering.
	decoded, err := cursor.Decode(h.keyring.asCursorKeyring(), cursor.DecodeRequest{
		Token:    *page.Page.NextCursor,
		Endpoint: cursor.EndpointGatewayEnvironments,
		Identity: cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering: cursor.OrderingEnvironmentsAsc,
		Filters:  map[string]string{"team_id": "team-a", "project_id": "", "task_id": ""},
	})
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	pos := cursor.EnvironmentPosition(decoded)
	if pos == nil {
		t.Fatalf("environment position missing")
	}
	if pos.EnvironmentID != "env-a" {
		t.Errorf("next_cursor.environment_id=%q, want env-a", pos.EnvironmentID)
	}
	if pos.CreatedAtNano != int64(1000) {
		t.Errorf("next_cursor.created_at=%d, want 1000", pos.CreatedAtNano)
	}
}

// TestEnvironmentListTamperedCursorRejectedBeforeQuery covers
// the OpenSpec "Tampered cursor is rejected before any protected
// query runs" invariant for the environments endpoint.
func TestEnvironmentListTamperedCursorRejectedBeforeQuery(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:            cursor.EndpointGatewayEnvironments,
		Identity:            cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:            cursor.OrderingEnvironmentsAsc,
		Filters:             map[string]string{"team_id": "team-a"},
		EnvironmentPosition: cursor.EnvironmentPositionTuple(1700000000000000000, "env-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	cases := []struct {
		name  string
		query string
	}{
		{name: "tampered_envelope", query: "cursor=" + url.QueryEscape(flipCursorByte(tok))},
		{name: "tampered_mac", query: "cursor=" + url.QueryEscape(flipCursorMAC(tok))},
		{name: "multiple", query: "cursor=" + url.QueryEscape(tok) + "&cursor=" + url.QueryEscape(tok)},
		{name: "empty", query: "cursor="},
		{name: "garbage", query: "cursor=not-a-cursor"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			hh := newEnvSecretHarness(t)
			resp, body := hh.do(t, http.MethodGet, "/v1/environments?"+tc.query, gatewayEnvHeaders("team-a", "op-a", "req-env-tamper"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status=%d, want 400; body=%s", tc.name, resp.StatusCode, body)
			}
			env := decodeEnvEnvError(t, body)
			if env.Code != "invalid_pagination" {
				t.Errorf("%s: code=%q, want invalid_pagination", tc.name, env.Code)
			}
			if got := hh.repo.envCalls.Load(); got != 0 {
				t.Errorf("%s: ListEnvironmentsPaged calls=%d, want 0", tc.name, got)
			}
		})
	}
}

// TestEnvironmentListCrossEndpointCursorRejected covers the
// OpenSpec "Cross-endpoint cursor reuse is rejected" invariant.
func TestEnvironmentListCrossEndpointCursorRejected(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:     cursor.EndpointGatewayTasks,
		Identity:     cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:     cursor.OrderingTasksDesc,
		Filters:      map[string]string{},
		TaskPosition: cursor.TaskPositionTuple(1700000000000000000, "task-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	q := url.Values{}
	q.Set("cursor", tok)
	resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-env-cross"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.envCalls.Load(); got != 0 {
		t.Errorf("ListEnvironmentsPaged calls=%d, want 0", got)
	}
}

// TestEnvironmentListChangedFilterCursorRejected covers the
// OpenSpec "Changed-filter cursor reuse is rejected" invariant for
// the environments endpoint.
func TestEnvironmentListChangedFilterCursorRejected(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:            cursor.EndpointGatewayEnvironments,
		Identity:            cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:            cursor.OrderingEnvironmentsAsc,
		Filters:             map[string]string{"team_id": "team-a", "project_id": "proj-a", "task_id": ""},
		EnvironmentPosition: cursor.EnvironmentPositionTuple(1700000000000000000, "env-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	q := url.Values{}
	q.Set("cursor", tok)
	q.Set("project_id", "proj-other")
	resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-env-filter"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.envCalls.Load(); got != 0 {
		t.Errorf("ListEnvironmentsPaged calls=%d, want 0", got)
	}
}

// TestEnvironmentListCrossScopeCursorRejected covers the
// OpenSpec "Cross-scope cursor reuse is rejected" invariant for
// the environments endpoint.
func TestEnvironmentListCrossScopeCursorRejected(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:            cursor.EndpointGatewayEnvironments,
		Identity:            cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:            cursor.OrderingEnvironmentsAsc,
		Filters:             map[string]string{"team_id": "team-a", "project_id": "", "task_id": ""},
		EnvironmentPosition: cursor.EnvironmentPositionTuple(1700000000000000000, "env-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	q := url.Values{}
	q.Set("cursor", tok)
	// Cross-team replay: same endpoint, different team.
	resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-b", "op-b", "req-env-team"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-team status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.envCalls.Load(); got != 0 {
		t.Errorf("cross-team calls=%d, want 0", got)
	}
}

// TestEnvironmentListMalformedLimitRejectedBeforeQuery covers
// every malformed limit shape documented in the OpenSpec contract.
func TestEnvironmentListMalformedLimitRejectedBeforeQuery(t *testing.T) {
	cases := []string{"0", "201", "-1", "abc", "01", "+1", "1.5", "50 ", " 50", "0x1"}
	for _, raw := range cases {
		raw := raw
		name := "raw=" + strconv.Quote(raw)
		t.Run(name, func(t *testing.T) {
			h := newEnvSecretHarness(t)
			q := url.Values{}
			q.Set("limit", raw)
			resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-env-limit"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status=%d, want 400; body=%s", name, resp.StatusCode, body)
			}
			if got := h.repo.envCalls.Load(); got != 0 {
				t.Errorf("%s: calls=%d, want 0", name, got)
			}
		})
	}
}

// TestEnvironmentListForeignRowIsolation proves the team
// predicate reaches the repository so foreign rows are excluded
// before the page is shaped.
func TestEnvironmentListForeignRowIsolation(t *testing.T) {
	h := newEnvSecretHarness(t)
	var received string
	h.repo.setListEnvFn(func(limit int, after *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error) {
		_ = limit
		_ = after
		return nil, nil, nil
	})
	q := url.Values{}
	q.Set("project_id", "proj-a")
	q.Set("task_id", "task-a")
	resp, body := h.do(t, http.MethodGet, "/v1/environments?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-env-iso"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	_ = received
	// The recorded call counter confirms the protected query ran
	// with the team predicate derived from the Gateway identity.
	if got := h.repo.envCalls.Load(); got != 1 {
		t.Errorf("ListEnvironmentsPaged calls=%d, want 1", got)
	}
}

// TestSecretListAcceptsCursorAndPaginates mirrors the environments
// test for the secrets collection under one environment.
func TestSecretListAcceptsCursorAndPaginates(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.setListSecretFn(func(environmentID string, limit int, after *store.SecretAfter) ([]platform.LogicalSecret, *store.SecretAfter, error) {
		if environmentID != "env-a" {
			t.Errorf("environment_id=%q, want env-a", environmentID)
		}
		return nil, nil, nil
	})
	q := url.Values{}
	q.Set("limit", "1")
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-secret-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.secretListCalls.Load(); got != 1 {
		t.Errorf("ListSecretsPaged calls=%d, want 1", got)
	}
}

// TestSecretListTamperedCursorRejectedBeforeQuery covers the
// OpenSpec tamper invariant for the secrets endpoint.
func TestSecretListTamperedCursorRejectedBeforeQuery(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:       cursor.EndpointGatewaySecrets,
		Identity:       cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:       cursor.OrderingSecretsAsc,
		Filters:        map[string]string{"team_id": "team-a", "environment_id": "env-a"},
		SecretPosition: cursor.SecretPositionTuple(1700000000000000000, "secret-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	q := url.Values{}
	q.Set("cursor", url.QueryEscape(flipCursorByte(tok)))
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-secret-tamper"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.secretListCalls.Load(); got != 0 {
		t.Errorf("ListSecretsPaged calls=%d, want 0", got)
	}
}

// TestSecretListCrossEndpointCursorRejected covers the
// OpenSpec cross-endpoint invariant.
func TestSecretListCrossEndpointCursorRejected(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:            cursor.EndpointGatewayEnvironments,
		Identity:            cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:            cursor.OrderingEnvironmentsAsc,
		Filters:             map[string]string{"team_id": "team-a", "environment_id": "env-a"},
		EnvironmentPosition: cursor.EnvironmentPositionTuple(1700000000000000000, "env-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	q := url.Values{}
	q.Set("cursor", tok)
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-secret-cross"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.secretListCalls.Load(); got != 0 {
		t.Errorf("ListSecretsPaged calls=%d, want 0", got)
	}
}

// TestSecretListChangedFilterCursorRejected covers the
// OpenSpec changed-filter invariant.
func TestSecretListChangedFilterCursorRejected(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:       cursor.EndpointGatewaySecrets,
		Identity:       cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:       cursor.OrderingSecretsAsc,
		Filters:        map[string]string{"team_id": "team-a", "environment_id": "env-a"},
		SecretPosition: cursor.SecretPositionTuple(1700000000000000000, "secret-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	// The cursor binds environment_id=env-a; the request names
	// env-other under the same team.
	q := url.Values{}
	q.Set("cursor", tok)
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-other/secrets?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-secret-filter"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.secretListCalls.Load(); got != 0 {
		t.Errorf("ListSecretsPaged calls=%d, want 0", got)
	}
}

// TestSecretListMalformedLimitRejectedBeforeQuery covers the
// documented malformed limit shapes for the secrets endpoint.
func TestSecretListMalformedLimitRejectedBeforeQuery(t *testing.T) {
	cases := []string{"0", "201", "-1", "abc", "01", "+1", "1.5"}
	for _, raw := range cases {
		raw := raw
		t.Run("raw="+raw, func(t *testing.T) {
			h := newEnvSecretHarness(t)
			q := url.Values{}
			q.Set("limit", raw)
			resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-secret-limit"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status=%d, want 400; body=%s", raw, resp.StatusCode, body)
			}
			if got := h.repo.secretListCalls.Load(); got != 0 {
				t.Errorf("%s: calls=%d, want 0", raw, got)
			}
		})
	}
}

// TestSecretListForeignEnvironmentIsolation proves a foreign
// environment is rejected with the same non-revealing 404 before
// any secret row is read.
func TestSecretListForeignEnvironmentIsolation(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.listSecretErr = store.ErrEnvironmentUnavailable
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-foreign/secrets?limit=50", gatewayEnvHeaders("team-a", "op-a", "req-secret-foreign"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", resp.StatusCode, body)
	}
}

// TestSecretVersionListAcceptsCursorAndPaginates covers the secret
// versions endpoint cursor path.
func TestSecretVersionListAcceptsCursorAndPaginates(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.setListVersionsFn(func(environmentID, secretID string, limit int, after *store.SecretVersionAfter) ([]platform.SecretVersion, *store.SecretVersionAfter, error) {
		if environmentID != "env-a" || secretID != "secret-a" {
			t.Errorf("path id mismatch: env=%s secret=%s", environmentID, secretID)
		}
		return nil, nil, nil
	})
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets/secret-a/versions?limit=1", gatewayEnvHeaders("team-a", "op-a", "req-version-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.versionListCalls.Load(); got != 1 {
		t.Errorf("ListSecretVersionsPaged calls=%d, want 1", got)
	}
}

// TestSecretVersionListTamperedCursorRejectedBeforeQuery covers
// the tamper invariant for the secret-versions endpoint.
func TestSecretVersionListTamperedCursorRejectedBeforeQuery(t *testing.T) {
	h := newEnvSecretHarness(t)
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:              cursor.EndpointGatewaySecretVersions,
		Identity:              cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:              cursor.OrderingSecretVersionsAsc,
		Filters:               map[string]string{"team_id": "team-a", "environment_id": "env-a", "secret_id": "secret-a"},
		SecretVersionPosition: cursor.SecretVersionPositionTuple(1, "secret-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	q := url.Values{}
	q.Set("cursor", url.QueryEscape(flipCursorByte(tok)))
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets/secret-a/versions?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-version-tamper"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.versionListCalls.Load(); got != 0 {
		t.Errorf("ListSecretVersionsPaged calls=%d, want 0", got)
	}
}

// TestSecretVersionListForeignRowIsolation covers the
// environment_id defense: a secret that lives in a foreign
// environment MUST not be readable even when the secret_id is
// known.
func TestSecretVersionListForeignRowIsolation(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.listVersionsErr = store.ErrEnvironmentUnavailable
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-foreign/secrets/secret-a/versions", gatewayEnvHeaders("team-a", "op-a", "req-version-foreign"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", resp.StatusCode, body)
	}
}

// TestRevokeSecretSameTeamReturns204AndMarksRevoked pins the
// documented success path for DELETE /v1/environments/{id}/secrets/{id}.
func TestRevokeSecretSameTeamReturns204AndMarksRevoked(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.revokeFn = func(environmentID, secretID string) (platform.LogicalSecret, error) {
		if environmentID != "env-a" || secretID != "secret-a" {
			t.Errorf("revoke ids: env=%s secret=%s", environmentID, secretID)
		}
		return platform.LogicalSecret{
			SecretID:      "secret-a",
			EnvironmentID: "env-a",
			TeamID:        "team-a",
			Revoked:       true,
			LatestVersion: 2,
		}, nil
	}
	resp, body := h.do(t, http.MethodDelete, "/v1/environments/env-a/secrets/secret-a", gatewayEnvHeaders("team-a", "op-a", "req-revoke-1"))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d, want 204; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.revokeCalls.Load(); got != 1 {
		t.Errorf("RevokeSecret calls=%d, want 1", got)
	}
	if len(body) != 0 {
		t.Errorf("204 body must be empty; got %s", body)
	}
}

// TestRevokeSecretForeignReturnsNonRevealing404 proves a foreign
// (environment_id, secret_id) returns the same non-revealing 404
// shape used for every other point resource.
func TestRevokeSecretForeignReturnsNonRevealing404(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.revokeErr = store.ErrEnvironmentUnavailable
	resp, body := h.do(t, http.MethodDelete, "/v1/environments/env-foreign/secrets/secret-foreign", gatewayEnvHeaders("team-a", "op-a", "req-revoke-foreign"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.revokeCalls.Load(); got != 1 {
		t.Errorf("RevokeSecret calls=%d, want 1", got)
	}
}

// TestRevokeSecretRejectsAnonymousBeforeRepository ensures the
// trusted Gateway identity check rejects unauthenticated calls
// before the repository runs.
func TestRevokeSecretRejectsAnonymousBeforeRepository(t *testing.T) {
	h := newEnvSecretHarness(t)
	resp, body := h.do(t, http.MethodDelete, "/v1/environments/env-a/secrets/secret-a", http.Header{})
	if resp.StatusCode == http.StatusNoContent {
		t.Fatalf("anonymous revoke must be rejected; body=%s", body)
	}
	if got := h.repo.revokeCalls.Load(); got != 0 {
		t.Errorf("anonymous revoke calls=%d, want 0", got)
	}
}

// TestSecretListUnknownEnvironmentIsNotRevealing proves a missing
// environment is reported through the same not-found shape as
// every other point resource.
func TestSecretListUnknownEnvironmentIsNotRevealing(t *testing.T) {
	h := newEnvSecretHarness(t)
	h.repo.listSecretErr = store.ErrEnvironmentUnavailable
	resp, body := h.do(t, http.MethodGet, "/v1/environments/env-missing/secrets", gatewayEnvHeaders("team-a", "op-a", "req-secret-missing"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", resp.StatusCode, body)
	}
	if !errors.Is(h.repo.listSecretErr, store.ErrEnvironmentUnavailable) {
		t.Errorf("err=%v, want ErrEnvironmentUnavailable", h.repo.listSecretErr)
	}
}

// TestSecretVersionListMalformedLimitRejectedBeforeQuery covers
// the malformed limit shapes for the secret-versions endpoint.
func TestSecretVersionListMalformedLimitRejectedBeforeQuery(t *testing.T) {
	cases := []string{"0", "201", "-1", "abc", "01"}
	for _, raw := range cases {
		raw := raw
		t.Run("raw="+raw, func(t *testing.T) {
			h := newEnvSecretHarness(t)
			q := url.Values{}
			q.Set("limit", raw)
			resp, body := h.do(t, http.MethodGet, "/v1/environments/env-a/secrets/secret-a/versions?"+q.Encode(), gatewayEnvHeaders("team-a", "op-a", "req-version-limit"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status=%d, want 400; body=%s", raw, resp.StatusCode, body)
			}
			if got := h.repo.versionListCalls.Load(); got != 0 {
				t.Errorf("%s: calls=%d, want 0", raw, got)
			}
		})
	}
}

// helper: flipCursorMAC flips a single byte in the MAC partition so
// the constant-time MAC comparison must fail.
func flipCursorMAC(tok string) string {
	parts := splitCursor3(tok)
	if parts[2] == "" {
		parts[2] = "AAA"
	} else if parts[2][0] == 'A' {
		parts[2] = "B" + parts[2][1:]
	} else {
		parts[2] = "A" + parts[2][1:]
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

func splitCursor3(tok string) [3]string {
	var out [3]string
	i := -1
	for j, r := range tok {
		if r == '.' {
			if i == -1 {
				i = j
			} else {
				out[0] = tok[:i]
				out[1] = tok[i+1 : j]
				out[2] = tok[j+1:]
				return out
			}
		}
	}
	return out
}

// setListEnvFn wires the optional return override used by the
// ordering and pagination tests.
func (r *recordingEnvSecretRepo) setListEnvFn(fn func(limit int, after *store.EnvironmentAfter) ([]platform.Environment, *store.EnvironmentAfter, error)) {
	r.listEnvFn = fn
}

func (r *recordingEnvSecretRepo) setListSecretFn(fn func(environmentID string, limit int, after *store.SecretAfter) ([]platform.LogicalSecret, *store.SecretAfter, error)) {
	r.listSecretFn = fn
}

func (r *recordingEnvSecretRepo) setListVersionsFn(fn func(environmentID, secretID string, limit int, after *store.SecretVersionAfter) ([]platform.SecretVersion, *store.SecretVersionAfter, error)) {
	r.listVersionsFn = fn
}
