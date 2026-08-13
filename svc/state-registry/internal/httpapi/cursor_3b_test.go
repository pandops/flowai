// Section 3b — opaque cursor integrity, scope binding, and
// reject-before-read. The tests below cover the eleven exact test
// functions required by `openspec/changes/v0002-state-registry/tasks.md`.
package httpapi_test

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/svc/state-registry/internal/cursor"
	"github.com/flowai/platform/svc/state-registry/internal/httpapi"
	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// testCursorKeyring is the in-memory keyring used by the rejection
// tests. The methods match the httpapi.cursorKeyring interface so
// the harness can pass it directly to RegisterAdminList.
type testCursorKeyring struct {
	primary cursor.KeyID
	keys    map[cursor.KeyID][]byte
}

func (k *testCursorKeyring) ActiveKeyID() string { return string(k.primary) }
func (k *testCursorKeyring) Keys() map[string][]byte {
	out := make(map[string][]byte, len(k.keys))
	for id, key := range k.keys {
		out[string(id)] = key
	}
	return out
}

// asCursorKeyring wraps the httpapi-style keyring into a
// cursor.Keyring so the tests can pass the same keyring to both the
// admin list handler and the cursor.Encode/Decode helpers.
func (k *testCursorKeyring) asCursorKeyring() *cursorKeyringAdapter {
	return &cursorKeyringAdapter{inner: k}
}

// cursorKeyringAdapter implements cursor.Keyring on top of the
// httpapi.cursorKeyring interface.
type cursorKeyringAdapter struct {
	inner *testCursorKeyring
}

func (a *cursorKeyringAdapter) ActiveKeyID() cursor.KeyID     { return a.inner.primary }
func (a *cursorKeyringAdapter) Keys() map[cursor.KeyID][]byte { return a.inner.keys }

func newTestCursorKeyring(t *testing.T) *testCursorKeyring {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return &testCursorKeyring{
		primary: cursor.KeyID(configCursorKeyID),
		keys:    map[cursor.KeyID][]byte{cursor.KeyID(configCursorKeyID): key},
	}
}

const configCursorKeyID = "state-registry-cursor-v1"

// cursor3bHarness mounts the admin list handlers on a fresh router
// with a recording list repository so the rejection tests can
// mechanically assert zero protected repository calls.
type cursor3bHarness struct {
	router  *chi.Mux
	repo    *recordingAdminRepo
	keyring *testCursorKeyring
	server  *httptest.Server
}

func newCursor3bHarness(t *testing.T) *cursor3bHarness {
	t.Helper()
	repo := &recordingAdminRepo{}
	keyring := newTestCursorKeyring(t)
	logger := newTestLogger()
	router := chi.NewRouter()
	httpapi.RegisterAdminList(router, logger, repo, keyring)
	httpapi.RegisterGatewayList(router, logger, repo, keyring)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &cursor3bHarness{router: router, repo: repo, keyring: keyring, server: srv}
}

func (h *cursor3bHarness) do(t *testing.T, path string, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.server.URL+path, nil)
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

func adminAuthHeaders() http.Header {
	h := http.Header{}
	h.Set(hdrRole, "admin")
	h.Set(hdrAdminSubject, "admin-cursor-3b")
	h.Set(hdrRequestID, "req-cursor-3b")
	return h
}

func gatewayAuthHeaders(teamID, operatorID string) http.Header {
	h := http.Header{}
	h.Set(hdrRole, "gateway")
	h.Set("X-FlowAI-Team-Id", teamID)
	h.Set("X-FlowAI-Operator-Id", operatorID)
	h.Set(hdrRequestID, "req-gw-cursor-3b")
	return h
}

// errorEnvelope3b decodes the documented flat envelope.
type errorEnvelope3b struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func decodeErrorEnvelope3b(t *testing.T, body []byte) errorEnvelope3b {
	t.Helper()
	var env errorEnvelope3b
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env
}

// signCursorEnvelope signs an envelope with the documented HMAC-SHA-256
// algorithm and returns the cursor wire token. The helper reproduces
// the cursor package's signing pipeline so the wrong-direction test
// can build a structurally valid cursor with an arbitrary envelope.
func signCursorEnvelope(t *testing.T, k *testCursorKeyring, env map[string]any) string {
	t.Helper()
	envBytes, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	keyID := string(k.primary)
	key := k.keys[cursor.KeyID(keyID)]
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("v0002-cursor/v1\x00"))
	mac.Write([]byte(keyID))
	mac.Write([]byte("\x00"))
	mac.Write(envBytes)
	return keyID + "." + base64.RawURLEncoding.EncodeToString(envBytes) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// flipCursorByte flips a single byte in the envelope partition so
// the MAC verifies fail.
func flipCursorByte(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return tok
	}
	if parts[1] == "" {
		parts[1] = "AAA"
	} else if parts[1][0] == 'A' {
		parts[1] = "B" + parts[1][1:]
	} else {
		parts[1] = "A" + parts[1][1:]
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

func equalStringSlices3b(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// TestCursorIntegrityRejectedBeforeQuery covers the OpenSpec scenario
// "Tampered cursor is rejected before any protected query runs".
func TestCursorIntegrityRejectedBeforeQuery(t *testing.T) {
	seed := newCursor3bHarness(t)
	tok, err := cursor.Encode(seed.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:     cursor.EndpointGatewayTasks,
		Identity:     cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:     cursor.OrderingTasksDesc,
		Filters:      map[string]string{},
		TaskPosition: cursor.TaskPositionTuple(1700000000000000000, "task-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}

	cases := []struct {
		name  string
		query string
	}{
		{name: "tampered", query: "cursor=" + url.QueryEscape(flipCursorByte(tok))},
		{name: "empty", query: "cursor="},
		{name: "multiple", query: "cursor=" + url.QueryEscape(tok) + "&cursor=" + url.QueryEscape(tok)},
		{name: "malformed", query: "cursor=not-a-cursor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCursor3bHarness(t)
			resp, body := h.do(t, "/v1/tasks?"+tc.query, gatewayAuthHeaders("team-a", "op-a"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
			}
			env := decodeErrorEnvelope3b(t, body)
			if env.Code != "invalid_pagination" {
				t.Errorf("code=%q, want invalid_pagination", env.Code)
			}
			if got := h.repo.listTaskCalls(); got != 0 {
				t.Errorf("ListTasks calls=%d, want 0", got)
			}
		})
	}
}

// TestCursorCrossEndpointRejected covers the OpenSpec scenario
// "Cross-endpoint cursor reuse is rejected".
func TestCursorCrossEndpointRejected(t *testing.T) {
	h := newCursor3bHarness(t)

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
	resp, body := h.do(t, "/admin/tasks?"+q.Encode(), adminAuthHeaders())
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Errorf("ListTasks calls=%d, want 0 (cross-endpoint cursor must not reach repository)", got)
	}
}

// TestCursorCrossScopeRejected covers the OpenSpec scenario
// "Cross-scope cursor reuse is rejected" for both team-b team
// boundary and Gateway-vs-admin role boundary.
func TestCursorCrossScopeRejected(t *testing.T) {
	h := newCursor3bHarness(t)

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
	resp, body := h.do(t, "/v1/tasks?"+q.Encode(), gatewayAuthHeaders("team-b", "op-b"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-team status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Errorf("ListTasks calls=%d, want 0 (cross-team cursor must not reach repository)", got)
	}

	resp2, body2 := h.do(t, "/admin/tasks?"+q.Encode(), adminAuthHeaders())
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-role status=%d, want 400; body=%s", resp2.StatusCode, body2)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Errorf("ListTasks calls=%d, want 0 (cross-role cursor must not reach repository)", got)
	}
}

// TestCursorChangedFilterRejected covers the OpenSpec scenario
// "Changed-filter cursor reuse is rejected".
func TestCursorChangedFilterRejected(t *testing.T) {
	h := newCursor3bHarness(t)

	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:     cursor.EndpointGatewayTasks,
		Identity:     cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:     cursor.OrderingTasksDesc,
		Filters:      map[string]string{"state": "pending"},
		TaskPosition: cursor.TaskPositionTuple(1700000000000000000, "task-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}

	q := url.Values{}
	q.Set("cursor", tok)
	q.Set("state", "running")
	resp, body := h.do(t, "/v1/tasks?"+q.Encode(), gatewayAuthHeaders("team-a", "op-a"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Errorf("ListTasks calls=%d, want 0 (changed-filter cursor must not reach repository)", got)
	}
}

// TestCursorChangedOrderingRejected covers the OpenSpec scenario
// "Changed-ordering cursor reuse is rejected".
func TestCursorChangedOrderingRejected(t *testing.T) {
	h := newCursor3bHarness(t)

	// Mint a cursor bound to a foreign ordering; the gateway handler
	// pins OrderingTasksDesc and rejects the mismatch.
	tok, err := cursor.Encode(h.keyring.asCursorKeyring(), cursor.EncodeIssue{
		Endpoint:     cursor.EndpointGatewayTasks,
		Identity:     cursor.Identity{Kind: "gateway", TeamID: "team-a", OperatorID: "op-a"},
		Ordering:     cursor.OrderingAdminTags,
		Filters:      map[string]string{},
		TaskPosition: cursor.TaskPositionTuple(1700000000000000000, "task-a"),
	})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}

	q := url.Values{}
	q.Set("cursor", tok)
	resp, body := h.do(t, "/v1/tasks?"+q.Encode(), gatewayAuthHeaders("team-a", "op-a"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Errorf("ListTasks calls=%d, want 0 (changed-ordering cursor must not reach repository)", got)
	}
}

// TestCursorWrongDirectionRejected covers the OpenSpec scenario
// "wrong-direction cursor reuse is rejected". The contract permits
// forward-only cursors.
func TestCursorWrongDirectionRejected(t *testing.T) {
	h := newCursor3bHarness(t)

	env := map[string]any{
		"ep": string(cursor.EndpointGatewayTasks),
		"sc": "gateway|team-a|op-a",
		"or": string(cursor.OrderingTasksDesc),
		"dr": "backward",
		"ft": map[string]string{},
		"kp": map[string]any{"i": 1700000000000000000, "t": "task-a"},
	}
	tamperedTok := signCursorEnvelope(t, h.keyring, env)

	q := url.Values{}
	q.Set("cursor", tamperedTok)
	resp, body := h.do(t, "/v1/tasks?"+q.Encode(), gatewayAuthHeaders("team-a", "op-a"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Errorf("ListTasks calls=%d, want 0 (wrong-direction cursor must not reach repository)", got)
	}
}

// TestLimitOutOfRangeRejectedBeforeQuery covers every out-of-range
// limit value documented in the OpenSpec contract.
func TestLimitOutOfRangeRejectedBeforeQuery(t *testing.T) {
	cases := []string{"0", "201", "-1", "999"}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			h := newCursor3bHarness(t)
			q := url.Values{}
			q.Set("limit", raw)
			resp, body := h.do(t, "/admin/tasks?"+q.Encode(), adminAuthHeaders())
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("limit=%s status=%d, want 400; body=%s", raw, resp.StatusCode, body)
			}
			if got := h.repo.listTaskCalls(); got != 0 {
				t.Errorf("ListTasks calls=%d, want 0 (out-of-range limit must not reach repository)", got)
			}
		})
	}
}

// TestLimitMalformedRejectedBeforeQuery covers every malformed limit
// shape documented in the OpenSpec contract.
func TestLimitMalformedRejectedBeforeQuery(t *testing.T) {
	cases := []string{"", " ", "abc", "01", "+1", "1.5", "50 ", " 50", "0x1"}
	for _, raw := range cases {
		raw := raw
		t.Run("raw="+raw, func(t *testing.T) {
			h := newCursor3bHarness(t)
			q := url.Values{}
			q.Set("limit", raw)
			resp, body := h.do(t, "/admin/tasks?"+q.Encode(), adminAuthHeaders())
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("limit=%q status=%d, want 400; body=%s", raw, resp.StatusCode, body)
			}
			if got := h.repo.listTaskCalls(); got != 0 {
				t.Errorf("ListTasks calls=%d, want 0 (malformed limit must not reach repository)", got)
			}
		})
	}
}

// TestGatewayListTasksOrderingIngestedAtDesc proves the
// deterministic (ingested_at DESC, task_id DESC) ordering for the
// Gateway collection.
func TestGatewayListTasksOrderingIngestedAtDesc(t *testing.T) {
	h := newCursor3bHarness(t)
	entries := []platform.TaskListEntry{
		{TaskID: "task-latest", TeamID: "team-a", TaskTypeID: "tt-a", SourceSystemID: "src-a", SourceID: "src-latest", RequiredTag: "tag-a", CurrentState: "pending"},
		{TaskID: "task-middle", TeamID: "team-a", TaskTypeID: "tt-a", SourceSystemID: "src-a", SourceID: "src-middle", RequiredTag: "tag-a", CurrentState: "pending"},
		{TaskID: "task-earliest", TeamID: "team-a", TaskTypeID: "tt-a", SourceSystemID: "src-a", SourceID: "src-earliest", RequiredTag: "tag-a", CurrentState: "pending"},
	}
	h.repo.setListTasksFn(func(_ platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
		return entries, nil, nil
	})

	resp, body := h.do(t, "/v1/tasks", gatewayAuthHeaders("team-a", "op-a"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	var page platform.GatewayTaskPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode page: %v (body=%s)", err, body)
	}
	want := []string{"task-latest", "task-middle", "task-earliest"}
	got := make([]string, 0, len(page.Items))
	for _, entry := range page.Items {
		got = append(got, entry.TaskID)
	}
	if !equalStringSlices3b(got, want) {
		t.Fatalf("ordering: got=%v want=%v", got, want)
	}
}

// TestAdminTagsOrderingDeterministic proves the deterministic
// (team_id ASC, execution_tag ASC, task_type_id ASC) ordering for
// the admin tag collection.
func TestAdminTagsOrderingDeterministic(t *testing.T) {
	h := newCursor3bHarness(t)
	h.repo.setListTaskTypesFn(func(_ platform.AdminTagFilter) ([]platform.AdminTagEntry, *store.TaskTypeAfter, error) {
		return []platform.AdminTagEntry{
			{TeamID: "team-a", TaskTypeID: "tt-a-cron", ExecutionTag: "cron"},
			{TeamID: "team-a", TaskTypeID: "tt-a-openhands", ExecutionTag: "openhands"},
			{TeamID: "team-b", TaskTypeID: "tt-b-openhands", ExecutionTag: "openhands"},
			{TeamID: "team-b", TaskTypeID: "tt-b-shell", ExecutionTag: "shell"},
		}, nil, nil
	})

	resp, body := h.do(t, "/admin/tags", adminAuthHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	var page platform.AdminTagPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode page: %v (body=%s)", err, body)
	}
	want := []string{
		"team-a|cron|tt-a-cron",
		"team-a|openhands|tt-a-openhands",
		"team-b|openhands|tt-b-openhands",
		"team-b|shell|tt-b-shell",
	}
	got := make([]string, 0, len(page.Items))
	for _, entry := range page.Items {
		got = append(got, entry.TeamID+"|"+entry.ExecutionTag+"|"+entry.TaskTypeID)
	}
	if !equalStringSlices3b(got, want) {
		t.Fatalf("ordering: got=%v want=%v", got, want)
	}
}

// TestAdminTasksOrderingDeterministic proves the deterministic
// (ingested_at DESC, task_id DESC) ordering for the admin task
// collection.
func TestAdminTasksOrderingDeterministic(t *testing.T) {
	h := newCursor3bHarness(t)
	entries := []platform.TaskListEntry{
		{TaskID: "task-latest", TeamID: "team-a", TaskTypeID: "tt-a", SourceSystemID: "src-a", SourceID: "src-latest", RequiredTag: "tag-a", CurrentState: "pending"},
		{TaskID: "task-middle", TeamID: "team-a", TaskTypeID: "tt-a", SourceSystemID: "src-a", SourceID: "src-middle", RequiredTag: "tag-a", CurrentState: "pending"},
		{TaskID: "task-earliest", TeamID: "team-a", TaskTypeID: "tt-a", SourceSystemID: "src-a", SourceID: "src-earliest", RequiredTag: "tag-a", CurrentState: "pending"},
	}
	h.repo.setListTasksFn(func(_ platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
		return entries, nil, nil
	})

	resp, body := h.do(t, "/admin/tasks", adminAuthHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	var page platform.AdminTaskPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode page: %v (body=%s)", err, body)
	}
	want := []string{"task-latest", "task-middle", "task-earliest"}
	got := make([]string, 0, len(page.Items))
	for _, entry := range page.Items {
		got = append(got, entry.TaskID)
	}
	if !equalStringSlices3b(got, want) {
		t.Fatalf("ordering: got=%v want=%v", got, want)
	}
}

// jsonKeysFromObject decodes one JSON object and returns its
// top-level key set in stable order.
func jsonKeysFromObject(t *testing.T, raw []byte) []string {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode json object: %v (body=%s)", err, raw)
	}
	out := make([]string, 0, len(body))
	for key := range body {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// TestGatewayTasksResponseExactKeys proves the wire-level JSON for
// the Gateway collection item exposes every OpenAPI-required Task
// key and no extra keys. The keys are pinned so a future widening
// of the canonical row or a JSON-tag drift breaks the test.
func TestGatewayTasksResponseExactKeys(t *testing.T) {
	h := newCursor3bHarness(t)

	owner := "cmd-a"
	exec := "exec-a"
	project := "proj-a"
	envID := "env-a"
	resolved := platform.ImageReference{
		Repository: "registry.example/agent:stable",
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}
	imageSrc := platform.ImageSourceTeamDefault

	h.repo.setListTasksFn(func(_ platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
		raw := json.RawMessage(`{"hello":"world","n":42}`)
		return []platform.TaskListEntry{{
			TaskID:         "task-1",
			TeamID:         "team-a",
			SourceSystemID: "src-a",
			SourceID:       "src-1",
			TaskTypeID:     "tt-a",
			RequiredTag:    "tag-a",
			Payload:        raw,
			CurrentState:   platform.TaskStateRunning,
			OwnerCommandID: &owner,
			ExecutorID:     &exec,
			ProjectID:      &project,
			EnvironmentID:  &envID,
			ResolvedImage:  &resolved,
			ImageSource:    &imageSrc,
			IngestedAt:     "2024-01-01T00:00:00Z",
			ClaimedAt:      ptrString("2024-01-01T01:02:03Z"),
		}}, nil, nil
	})

	resp, body := h.do(t, "/v1/tasks", gatewayAuthHeaders("team-a", "op-a"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	var envelope struct {
		Items []json.RawMessage `json:"items"`
		Page  map[string]any    `json:"page"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("items count=%d, want 1; body=%s", len(envelope.Items), body)
	}

	got := jsonKeysFromObject(t, envelope.Items[0])
	want := []string{
		"claimed_at",
		"current_state",
		"environment_id",
		"executor_id",
		"image",
		"image_source",
		"ingested_at",
		"owner_command_id",
		"payload",
		"project_id",
		"required_tag",
		"resolved_image",
		"source_id",
		"source_system_id",
		"task_id",
		"task_type_id",
		"team_id",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Gateway task keys drift: got=%v want=%v", got, want)
	}
}

// TestAdminTasksResponseExactKeys pins the wire-level JSON for the
// admin collection to the documented 11-field allowlist.
func TestAdminTasksResponseExactKeys(t *testing.T) {
	h := newCursor3bHarness(t)

	owner := "cmd-a"
	exec := "exec-a"
	h.repo.setListTasksFn(func(_ platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
		raw := json.RawMessage(`{"hello":"world"}`)
		return []platform.TaskListEntry{{
			TaskID:         "task-1",
			TeamID:         "team-a",
			SourceSystemID: "src-a",
			SourceID:       "src-1",
			TaskTypeID:     "tt-a",
			RequiredTag:    "tag-a",
			Payload:        raw, // canonical row carries payload, but admin handler must drop it
			CurrentState:   platform.TaskStateRunning,
			OwnerCommandID: &owner,
			ExecutorID:     &exec,
			IngestedAt:     "2024-01-01T00:00:00Z",
			ClaimedAt:      ptrString("2024-01-01T01:02:03Z"),
		}}, nil, nil
	})

	resp, body := h.do(t, "/admin/tasks", adminAuthHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}

	var envelope struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("items count=%d, want 1; body=%s", len(envelope.Items), body)
	}

	got := jsonKeysFromObject(t, envelope.Items[0])
	want := []string{
		"claimed_at",
		"current_state",
		"executor_id",
		"ingested_at",
		"owner_command_id",
		"required_tag",
		"source_id",
		"source_system_id",
		"task_id",
		"task_type_id",
		"team_id",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Admin task keys drift: got=%v want=%v", got, want)
	}
}

// TestGatewayTasksRejectsTaskTypeIDQuery proves undocumented filters
// are rejected rather than silently ignored or ambiguously forwarded.
func TestGatewayTasksRejectsTaskTypeIDQuery(t *testing.T) {
	h := newCursor3bHarness(t)

	q := url.Values{}
	q.Set("task_type_id", "tt-foreign")
	resp, body := h.do(t, "/v1/tasks?"+q.Encode(), gatewayAuthHeaders("team-a", "op-a"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
	}
	if got := h.repo.listTaskCalls(); got != 0 {
		t.Fatalf("ListTasks calls=%d, want 0", got)
	}
}

func TestListEndpointsRejectDuplicateFiltersBeforeQuery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		headers http.Header
	}{
		{name: "gateway state", path: "/v1/tasks?state=pending&state=running", headers: gatewayAuthHeaders("team-a", "op-a")},
		{name: "gateway tag", path: "/v1/tasks?tag=alpha&tag=beta", headers: gatewayAuthHeaders("team-a", "op-a")},
		{name: "admin team", path: "/admin/tasks?team_id=team-a&team_id=team-b", headers: adminAuthHeaders()},
		{name: "admin task type", path: "/admin/tasks?task_type_id=tt-a&task_type_id=tt-b", headers: adminAuthHeaders()},
		{name: "admin tag", path: "/admin/tags?tag=alpha&tag=beta", headers: adminAuthHeaders()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCursor3bHarness(t)
			resp, body := h.do(t, tc.path, tc.headers)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
			}
			if got := h.repo.listTaskCalls() + h.repo.listTagCalls(); got != 0 {
				t.Fatalf("protected list calls=%d, want 0", got)
			}
		})
	}
}

func TestListEndpointsRejectOversizedInputBeforeQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "cursor", path: "/v1/tasks?cursor=" + strings.Repeat("a", cursor.MaxTokenLength+1)},
		{name: "raw query", path: "/v1/tasks?tag=" + strings.Repeat("a", 2100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCursor3bHarness(t)
			resp, body := h.do(t, tc.path, gatewayAuthHeaders("team-a", "op-a"))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", resp.StatusCode, body)
			}
			if got := h.repo.listTaskCalls(); got != 0 {
				t.Fatalf("ListTasks calls=%d, want 0", got)
			}
		})
	}
}

// TestAdminTasksTaskTypeIDFilterStillWorks proves the admin filter
// retains task_type_id support because GET /admin/tasks documents it
// as an admin-only filter and the change MUST NOT widen or narrow
// the admin projection.
func TestAdminTasksTaskTypeIDFilterStillWorks(t *testing.T) {
	h := newCursor3bHarness(t)
	var received platform.AdminTaskFilter
	h.repo.setListTasksFn(func(f platform.AdminTaskFilter) ([]platform.TaskListEntry, *store.TaskAfter, error) {
		received = f
		return nil, nil, nil
	})

	q := url.Values{}
	q.Set("task_type_id", "tt-admin")
	resp, body := h.do(t, "/admin/tasks?"+q.Encode(), adminAuthHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, body)
	}
	if received.TaskTypeID != "tt-admin" {
		t.Errorf("admin filter task_type_id=%q, want tt-admin", received.TaskTypeID)
	}
}

func ptrString(s string) *string { return &s }
