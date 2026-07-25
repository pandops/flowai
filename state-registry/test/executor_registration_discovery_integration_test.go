//go:build integration

// Section 5 — Executor registration and read-only FIFO discovery
// PostgreSQL-backed RED integration tests. The tests drive the
// production httpapi.Routes surface against the live PostgreSQL
// fixture, exercise the executors / tasks / source_systems /
// task_types schema already shipped by Section 2, and assert the
// Section 5 contract end-to-end.
//
// RED COMMAND (with integration build tag):
//
//	go test -tags=integration ./state-registry/... \
//	    -run 'TestExecutorTeamRegistrationPersistsRow|\
//
// TestSystemExecutorRegistrationPersistsNullTeam|\
// TestSameScopeReregistrationPreservesScopeAndTeam|\
// TestScopeChangeReregistrationRejected|\
// TestTeamChangeReregistrationRejected|\
// TestTeamDiscoveryPredicateTeamAndTag|\
// TestTeamDiscoveryForeignOnlyReturnsEmpty|\
// TestSystemDiscoveryMatchesTagAcrossTeams|\
// TestDiscoveryFifoOrderingIngestedAtAsctaskIdAsc|\
// TestDiscoveryFifoSameTimestampTieBreakByTaskId|\
// TestDiscoveryShapeContainsOnlyTaskSummaryFields|\
// TestDiscoveryIgnoresCapacityObservations|\
// TestUnregisteredExecutorDiscoveryRejectedWithoutMutation|\
// TestWrongTagDiscoveryRejectedWithoutMutation' -count=1
//
// RED EVIDENCE (current): the live Routes constructor does not
// mount the discovery handler so GET /v1/executors/{id}/tasks
// returns 404; the registration handler returns 501 on every
// team-owned PUT (the test-mode guard) and 403 on every
// system-owned PUT (the role check). Every test below asserts
// the documented Section 5 contract; the failures are
// behavior-specific and reproduce deterministically against the
// live fixture.
package test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/flowai/platform/state-registry/internal/health"
	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/migrations"
	"github.com/flowai/platform/state-registry/internal/platform"
	"github.com/flowai/platform/state-registry/internal/store"
)

// ---------------------------------------------------------------------------
// Fixture — migrated PostgreSQL database + production Routes router.
// ---------------------------------------------------------------------------

// execHarness wires the production httpapi.Routes constructor against
// the live store.Store so every assertion exercises the real handler
// and the real repository. The harness starts with the temporary
// test-mode registration guard already mounted; Section 5 GREEN will
// swap the guard for the production registration / discovery handlers
// without changing the harness surface.
type execHarness struct {
	db   *sql.DB
	repo *store.Store
	rtr  http.Handler
	srv  *httptest.Server
}

func newExecHarness(t *testing.T) *execHarness {
	t.Helper()
	db := migratedDB(t)
	repo := store.New(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	checker := health.Compose(health.NewPostgresProbe(health.NewPostgresPinger(db)), make([]byte, 32))
	router := httpapi.RoutesWithKeyring(
		"state-registry",
		"",
		logger,
		checker,
		httpapi.NewDecryptOps(),
		nil, // no cursor keyring — discovery routes use the guard surface only
		true,
		repo,
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &execHarness{db: db, repo: repo, rtr: router, srv: srv}
}

// ---------------------------------------------------------------------------
// Identity helpers (mirror the test-mode identity contract the
// executor_registration_guard.go mount already understands).
// ---------------------------------------------------------------------------

const (
	execHdrRole       = "X-FlowAI-Role"
	execHdrTeamID     = "X-FlowAI-Team-Id"
	execHdrExecutorID = "X-FlowAI-Executor-Id"
	execHdrRequestID  = "X-FlowAI-Request-Id"
	execRoleTeam      = "team-executor"
	execRoleSystem    = "system-executor"
)

func execPutHeaders(role, teamID, execID, reqID string) http.Header {
	h := http.Header{}
	h.Set(execHdrRole, role)
	h.Set(execHdrExecutorID, execID)
	h.Set(execHdrRequestID, reqID)
	if teamID != "" {
		h.Set(execHdrTeamID, teamID)
	}
	return h
}

type execPutBody struct {
	Scope           string         `json:"scope"`
	TeamID          *string        `json:"team_id"`
	ExecutorType    string         `json:"executor_type"`
	Identity        string         `json:"identity"`
	AuthorizedTag   string         `json:"authorized_tag"`
	MaxCapacity     int            `json:"max_capacity"`
	RunningCount    int            `json:"running_count"`
	RuntimeMetadata map[string]any `json:"runtime_metadata"`
}

func teamBody(team, tag string) []byte {
	t := team
	raw, _ := json.Marshal(execPutBody{
		Scope:           "team",
		TeamID:          &t,
		ExecutorType:    "executor_docker_opehands",
		Identity:        "identity-" + team,
		AuthorizedTag:   tag,
		MaxCapacity:     4,
		RunningCount:    0,
		RuntimeMetadata: map[string]any{"runtime": "docker", "tool": "openhands"},
	})
	return raw
}

func systemBody(tag string) []byte {
	raw, _ := json.Marshal(execPutBody{
		Scope:           "system",
		TeamID:          nil,
		ExecutorType:    "executor_docker_opehands",
		Identity:        "identity-system-" + tag,
		AuthorizedTag:   tag,
		MaxCapacity:     4,
		RunningCount:    0,
		RuntimeMetadata: map[string]any{"runtime": "docker", "tool": "openhands"},
	})
	return raw
}

func (h *execHarness) put(t *testing.T, execID string, headers http.Header, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, h.srv.URL+"/v1/executors/"+execID, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send PUT: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read PUT body: %v", err)
	}
	return resp, raw
}

func (h *execHarness) discover(t *testing.T, execID, tag string, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	path := "/v1/executors/" + execID + "/tasks"
	if tag != "" {
		path += "?tag=" + tag
	}
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send GET: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET body: %v", err)
	}
	return resp, raw
}

// ---------------------------------------------------------------------------
// Seeding helpers — use raw SQL so the tests stay focused on the
// database-level invariants the Section 5 contract pins. Section 5
// GREEN will replace these with the production registration handler;
// today the integration tests stand on raw SQL because the production
// handler is intentionally absent.
// ---------------------------------------------------------------------------

func mustInsertExec(t *testing.T, db *sql.DB, executorID, scope, teamID, tag string) {
	t.Helper()
	if scope == "team" {
		mustExec(t, db,
			`INSERT INTO executors (executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
			 VALUES ($1, $2, $3, 'executor_docker_opehands', $4, $5, 4, 0)`,
			executorID, scope, teamID, "identity-"+executorID, tag)
		return
	}
	mustExec(t, db,
		`INSERT INTO executors (executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
		 VALUES ($1, $2, NULL, 'executor_docker_opehands', $3, $4, 4, 0)`,
		executorID, scope, "identity-"+executorID, tag)
}

// mustInsertPendingTask inserts one pending task row with an explicit
// ingested_at so the FIFO ordering tests can pin the primary sort key
// independent of clock drift. The supplied required_tag determines
// which Executor observes the task. The helper pre-seeds one
// source_systems and one task_types row per team so the FK constraint
// on tasks is satisfied even when the test does not call the seeding
// helpers explicitly.
func mustInsertPendingTask(t *testing.T, db *sql.DB, taskID, teamID, tag, sourceID string, ingestedAt time.Time) {
	t.Helper()
	mustInsertSourceSystem(t, db, teamID, "src-"+teamID, "listener-"+teamID)
	mustInsertTaskType(t, db, teamID, "tt-"+teamID, tag)
	mustExec(t, db,
		`INSERT INTO tasks (task_id, team_id, source_system_id, source_id, task_type_id, required_tag, payload, ingested_at)
		 VALUES ($1, $2, $3, $4, $5, $6, '{}'::jsonb, $7)`,
		taskID, teamID, "src-"+teamID, sourceID, "tt-"+teamID, tag, ingestedAt)
}

func mustInsertSourceSystem(t *testing.T, db *sql.DB, teamID, sourceID, listenerID string) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (source_system_id) DO NOTHING`,
		sourceID, teamID, listenerID)
}

func mustInsertTaskType(t *testing.T, db *sql.DB, teamID, taskTypeID, tag string) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO task_types (task_type_id, team_id, execution_tag) VALUES ($1, $2, $3)
		 ON CONFLICT (task_type_id) DO NOTHING`,
		taskTypeID, teamID, tag)
}

// executorRow reads the canonical (scope, team_id, authorized_tag,
// registered_at) tuple of an Executor row so the integration tests
// can verify immutability and discovery predicate predicates against
// the live database.

// errorEnvelope decodes the documented flat {code,message,request_id}
// envelope shape. Tests assert the three documented fields exist;
// they do NOT pin any other field so future refactors can extend the
// envelope without invalidating RED tests.
type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func decodeErrorEnvelope(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env
}
func executorRow(t *testing.T, db *sql.DB, executorID string) (scope string, teamID sql.NullString, tag string, registeredAt time.Time) {
	t.Helper()
	err := db.QueryRowContext(context.Background(),
		`SELECT scope, team_id, authorized_tag, registered_at FROM executors WHERE executor_id = $1`,
		executorID,
	).Scan(&scope, &teamID, &tag, &registeredAt)
	if err != nil {
		t.Fatalf("read executor row %s: %v", executorID, err)
	}
	return
}

// ---------------------------------------------------------------------------
// Section 5 — Executor registration persistence + immutability
// ---------------------------------------------------------------------------

// TestExecutorTeamRegistrationPersistsRow verifies that the production
// handler persists a team-owned Executor with exactly one
// authorized_tag and an immutable scope + team_id binding. The
// database-level trigger protect_team_owner_update already enforces
// the immutability invariant; the Section 5 handler just needs to
// reach the row.
func TestExecutorTeamRegistrationPersistsRow(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")

	const execID = "exec-section5-team-persist"
	resp, raw := h.put(t, execID,
		execPutHeaders(execRoleTeam, "team-a", execID, "req-section5-team-persist"),
		teamBody("team-a", "openhands"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	scope, teamID, tag, registeredAt := executorRow(t, h.db, execID)
	if scope != "team" {
		t.Errorf("scope=%q, want \"team\"", scope)
	}
	if !teamID.Valid || teamID.String != "team-a" {
		t.Errorf("team_id=%v, want \"team-a\"", teamID)
	}
	if tag != "openhands" {
		t.Errorf("authorized_tag=%q, want \"openhands\"", tag)
	}
	if registeredAt.IsZero() {
		t.Errorf("registered_at is zero; Section 5 contract pins the immutable registration timestamp")
	}
}

// TestSystemExecutorRegistrationPersistsNullTeam verifies that the
// production handler persists a system-owned Executor with
// team_id = NULL.
func TestSystemExecutorRegistrationPersistsNullTeam(t *testing.T) {
	h := newExecHarness(t)
	const execID = "exec-section5-system-persist"
	resp, raw := h.put(t, execID,
		execPutHeaders(execRoleSystem, "", execID, "req-section5-system-persist"),
		systemBody("openhands"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	scope, teamID, tag, _ := executorRow(t, h.db, execID)
	if scope != "system" {
		t.Errorf("scope=%q, want \"system\"", scope)
	}
	if teamID.Valid {
		t.Errorf("system scope team_id=%q, want NULL", teamID.String)
	}
	if tag != "openhands" {
		t.Errorf("authorized_tag=%q, want \"openhands\"", tag)
	}
}

// TestSameScopeReregistrationPreservesScopeAndTeam pins the
// re-registration contract: a second PUT against the same
// executor_id with the SAME scope and team SHALL refresh the
// observational fields (max_capacity, running_count,
// runtime_metadata, updated_at) while preserving scope, team_id,
// and the original registered_at timestamp.
func TestSameScopeReregistrationPreservesScopeAndTeam(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertExec(t, h.db, "exec-reregister", "team", "team-a", "openhands")

	// Read the original registered_at.
	_, _, _, originalRegisteredAt := executorRow(t, h.db, "exec-reregister")
	time.Sleep(5 * time.Millisecond)

	// Re-register with the same scope + team + tag but new observations.
	body := teamBody("team-a", "openhands")
	// Mutate observation values directly because teamBody reuses fixed defaults.
	var parsed execPutBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal teamBody: %v", err)
	}
	parsed.MaxCapacity = 16
	parsed.RunningCount = 3
	parsed.RuntimeMetadata = map[string]any{"runtime": "docker", "tool": "openhands", "region": "us-east"}
	mod, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("marshal reregister body: %v", err)
	}
	resp, raw := h.put(t, "exec-reregister",
		execPutHeaders(execRoleTeam, "team-a", "exec-reregister", "req-section5-reregister"),
		mod,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-registration status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	scope, teamID, tag, registeredAt := executorRow(t, h.db, "exec-reregister")
	if scope != "team" {
		t.Errorf("scope mutated on re-registration: got %q, want \"team\"", scope)
	}
	if !teamID.Valid || teamID.String != "team-a" {
		t.Errorf("team_id mutated on re-registration: got %v, want \"team-a\"", teamID)
	}
	if tag != "openhands" {
		t.Errorf("authorized_tag mutated on re-registration: got %q, want \"openhands\"", tag)
	}
	if !registeredAt.Equal(originalRegisteredAt) {
		t.Errorf("registered_at changed on re-registration: %v -> %v", originalRegisteredAt, registeredAt)
	}
	// updated_at must advance.
	var updatedAt time.Time
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT updated_at FROM executors WHERE executor_id = 'exec-reregister'`,
	).Scan(&updatedAt); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if !updatedAt.After(originalRegisteredAt) {
		t.Errorf("updated_at=%v did not advance past registered_at=%v", updatedAt, originalRegisteredAt)
	}
}

func TestConcurrentFirstRegistrationConverges(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustExec(t, h.db, `
		CREATE FUNCTION test_delay_executor_insert() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_sleep(0.1);
			RETURN NEW;
		END;
		$$`)
	mustExec(t, h.db, `
		CREATE TRIGGER test_executor_insert_delay
		BEFORE INSERT ON executors
		FOR EACH ROW EXECUTE FUNCTION test_delay_executor_insert()`)
	teamID := "team-a"
	req := platform.ExecutorRegistrationRequest{
		Scope: platform.ExecutorScopeTeam, TeamID: &teamID,
		ExecutorType: "executor_docker_opehands", Identity: "identity-concurrent",
		AuthorizedTag: "openhands", MaxCapacity: 4, RunningCount: 0,
		RuntimeMetadata: json.RawMessage(`{"runtime":"docker"}`),
	}
	identity := platform.ExecutorIdentity{
		ExecutorID: "exec-concurrent-first", Scope: platform.ExecutorScopeTeam, TeamID: &teamID,
	}

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := h.repo.RegisterExecutor(context.Background(), identity.ExecutorID, req, identity)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent first registration: %v", err)
		}
	}
	assertRowCount(t, h.db, "executors", 1)
}

// TestScopeChangeReregistrationRejected asserts the documented
// scope-change rejection. Without the production handler the second
// PUT fails with 501; once GREEN lands it MUST return 400
// scope_change_forbidden without mutating the row.
func TestScopeChangeReregistrationRejected(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertExec(t, h.db, "exec-scope-change", "team", "team-a", "openhands")

	resp, raw := h.put(t, "exec-scope-change",
		execPutHeaders(execRoleTeam, "team-a", "exec-scope-change", "req-section5-scope-change"),
		systemBody("openhands"),
	)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 scope_change_forbidden; body=%s", resp.StatusCode, raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, raw)
	}
	if got, _ := env["code"].(string); got != "scope_change_forbidden" {
		t.Errorf("error code=%q, want \"scope_change_forbidden\"; body=%s", got, raw)
	}
	// Row must remain unchanged (scope=team, team_id=team-a).
	scope, teamID, _, _ := executorRow(t, h.db, "exec-scope-change")
	if scope != "team" {
		t.Errorf("scope mutated: got %q, want \"team\"", scope)
	}
	if !teamID.Valid || teamID.String != "team-a" {
		t.Errorf("team_id mutated: got %v, want \"team-a\"", teamID)
	}
}

// TestTeamChangeReregistrationRejected asserts that a team-owned
// Executor cannot be re-registered with a different team. The
// database-level trigger protect_team_owner_update enforces the
// immutability invariant at the persistence layer; the Section 5
// handler must surface 400 team_binding_mismatch before reaching the
// UPDATE.
func TestTeamChangeReregistrationRejected(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertAdminTeam(t, h.db, "team-b", "Team B")
	mustInsertExec(t, h.db, "exec-team-change", "team", "team-a", "openhands")

	resp, raw := h.put(t, "exec-team-change",
		execPutHeaders(execRoleTeam, "team-b", "exec-team-change", "req-section5-team-change"),
		teamBody("team-b", "openhands"),
	)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 team_binding_mismatch; body=%s", resp.StatusCode, raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, raw)
	}
	if got, _ := env["code"].(string); got != "team_binding_mismatch" {
		t.Errorf("error code=%q, want \"team_binding_mismatch\"; body=%s", got, raw)
	}
	scope, teamID, _, _ := executorRow(t, h.db, "exec-team-change")
	if scope != "team" {
		t.Errorf("scope mutated: got %q, want \"team\"", scope)
	}
	if !teamID.Valid || teamID.String != "team-a" {
		t.Errorf("team_id mutated: got %v, want \"team-a\" (immutable)", teamID)
	}
}

// ---------------------------------------------------------------------------
// Section 5 — read-only FIFO discovery (HTTP integration)
// ---------------------------------------------------------------------------

// TestTeamDiscoveryPredicateTeamAndTag verifies the eligibility
// predicate: a team-owned Executor observes ONLY its same-team +
// same-tag tasks. Foreign team or foreign tag rows are invisible.
func TestTeamDiscoveryPredicateTeamAndTag(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertAdminTeam(t, h.db, "team-b", "Team B")
	mustInsertExec(t, h.db, "exec-team-discovery", "team", "team-a", "openhands")
	mustInsertExec(t, h.db, "exec-team-discovery-other-tag", "team", "team-a", "shell")

	now := time.Now()
	mustInsertPendingTask(t, h.db, "task-team-a-tag-a", "team-a", "openhands", "ext-a-1", now)
	mustInsertPendingTask(t, h.db, "task-team-b-tag-a", "team-b", "openhands", "ext-b-1", now.Add(time.Millisecond))
	mustInsertPendingTask(t, h.db, "task-team-a-tag-shell", "team-a", "shell", "ext-a-2", now.Add(2*time.Millisecond))

	resp, raw := h.discover(t, "exec-team-discovery", "openhands",
		execPutHeaders(execRoleTeam, "team-a", "exec-team-discovery", "req-section5-team-discovery"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	ids := map[string]bool{}
	for _, item := range page.Items {
		ids[item["task_id"].(string)] = true
	}
	if !ids["task-team-a-tag-a"] {
		t.Errorf("team-a / openhands task not visible to team-a Executor (got ids=%v)", ids)
	}
	if ids["task-team-b-tag-a"] {
		t.Errorf("foreign-team (team-b) task leaked to team-a Executor: ids=%v", ids)
	}
	if ids["task-team-a-tag-shell"] {
		t.Errorf("foreign-tag (shell) task leaked to team-a Executor: ids=%v", ids)
	}
}

// TestTeamDiscoveryForeignOnlyReturnsEmpty asserts that when ONLY
// foreign tasks match the tag, the team-owned discovery response is
// 204 No Content or an empty 200 page — never a foreign count or
// cursor. Today the route is unmounted so the test asserts 200 with
// an empty items array.
func TestTeamDiscoveryForeignOnlyReturnsEmpty(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertAdminTeam(t, h.db, "team-b", "Team B")
	mustInsertExec(t, h.db, "exec-team-empty", "team", "team-a", "k8s")

	// Only a foreign-team task matches the tag.
	mustInsertPendingTask(t, h.db, "task-team-b-k8s", "team-b", "k8s", "ext-b-k8s", time.Now())

	resp, raw := h.discover(t, "exec-team-empty", "k8s",
		execPutHeaders(execRoleTeam, "team-a", "exec-team-empty", "req-section5-team-empty"),
	)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d, want 200 or 204; body=%s", resp.StatusCode, raw)
	}
	if resp.StatusCode == http.StatusOK {
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
		}
		if len(page.Items) != 0 {
			t.Errorf("team-a Executor received %d items on foreign-only discovery; want 0", len(page.Items))
		}
	}
}

// TestSystemDiscoveryMatchesTagAcrossTeams pins the system-owned
// eligibility predicate: required_tag match alone, no team filter.
// Every team that owns a task with the registered tag is observable.
func TestSystemDiscoveryMatchesTagAcrossTeams(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertAdminTeam(t, h.db, "team-b", "Team B")
	mustInsertExec(t, h.db, "exec-system-discovery", "system", "", "openhands")

	now := time.Now()
	mustInsertPendingTask(t, h.db, "task-team-a-sys", "team-a", "openhands", "ext-a-sys", now)
	mustInsertPendingTask(t, h.db, "task-team-b-sys", "team-b", "openhands", "ext-b-sys", now.Add(time.Millisecond))
	mustInsertPendingTask(t, h.db, "task-team-a-shell-sys", "team-a", "shell", "ext-a-shell", now.Add(2*time.Millisecond))

	resp, raw := h.discover(t, "exec-system-discovery", "openhands",
		execPutHeaders(execRoleSystem, "", "exec-system-discovery", "req-section5-system-discovery"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	teamIDs := map[string]bool{}
	taskIDs := map[string]bool{}
	for _, item := range page.Items {
		teamIDs[item["team_id"].(string)] = true
		taskIDs[item["task_id"].(string)] = true
	}
	if !teamIDs["team-a"] || !teamIDs["team-b"] {
		t.Errorf("system Executor missing a team match: teams=%v", teamIDs)
	}
	if !taskIDs["task-team-a-sys"] || !taskIDs["task-team-b-sys"] {
		t.Errorf("system Executor missing a cross-team task: tasks=%v", taskIDs)
	}
	if taskIDs["task-team-a-shell-sys"] {
		t.Errorf("foreign-tag task leaked to system Executor: tasks=%v", taskIDs)
	}
}

// TestDiscoveryFifoOrderingIngestedAtAsctaskIdAsc proves the
// documented FIFO ordering (ingested_at ASC, task_id ASC) holds for
// both team-owned and system-owned discovery. The fixture seeds three
// tasks with strictly increasing ingested_at so the assertion is
// unambiguous.
func TestDiscoveryFifoOrderingIngestedAtAsctaskIdAsc(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertExec(t, h.db, "exec-team-fifo", "team", "team-a", "openhands")

	base := time.Now()
	mustInsertPendingTask(t, h.db, "task-2", "team-a", "openhands", "ext-2", base.Add(2*time.Second))
	mustInsertPendingTask(t, h.db, "task-1", "team-a", "openhands", "ext-1", base.Add(1*time.Second))
	mustInsertPendingTask(t, h.db, "task-3", "team-a", "openhands", "ext-3", base.Add(3*time.Second))

	resp, raw := h.discover(t, "exec-team-fifo", "openhands",
		execPutHeaders(execRoleTeam, "team-a", "exec-team-fifo", "req-section5-fifo"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	want := []string{"task-1", "task-2", "task-3"}
	got := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		got = append(got, item["task_id"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("discovery ordering: got=%v want=%v", got, want)
	}
}

// TestDiscoveryFifoSameTimestampTieBreakByTaskId proves the
// (ingested_at, task_id) tie-break rule: when two tasks share the
// exact same ingested_at the deterministic tie-break is task_id ASC.
func TestDiscoveryFifoSameTimestampTieBreakByTaskId(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertExec(t, h.db, "exec-team-tie", "team", "team-a", "openhands")

	now := time.Now()
	// Same ingested_at; task_id ASC determines order.
	mustInsertPendingTask(t, h.db, "task-c", "team-a", "openhands", "ext-c", now)
	mustInsertPendingTask(t, h.db, "task-a", "team-a", "openhands", "ext-a", now)
	mustInsertPendingTask(t, h.db, "task-b", "team-a", "openhands", "ext-b", now)

	resp, raw := h.discover(t, "exec-team-tie", "openhands",
		execPutHeaders(execRoleTeam, "team-a", "exec-team-tie", "req-section5-fifo-tie"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	want := []string{"task-a", "task-b", "task-c"}
	got := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		got = append(got, item["task_id"].(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tie-break ordering: got=%v want=%v", got, want)
	}
}

// TestDiscoveryShapeContainsOnlyTaskSummaryFields pins the exact
// TaskSummary allowlist (5 documented fields) and asserts that
// payload / image / claim / event / project / environment /
// source-system / source-id / task_type_id / resolved_image /
// image_source / owner_command_id / executor_id / claimed_at never
// leak into the discovery response.
func TestDiscoveryShapeContainsOnlyTaskSummaryFields(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertExec(t, h.db, "exec-team-shape", "team", "team-a", "openhands")
	mustInsertPendingTask(t, h.db, "task-shape", "team-a", "openhands", "ext-shape", time.Now())

	resp, raw := h.discover(t, "exec-team-shape", "openhands",
		execPutHeaders(execRoleTeam, "team-a", "exec-team-shape", "req-section5-shape"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	if len(page.Items) == 0 {
		t.Fatalf("discovery response has no items; cannot assert TaskSummary shape (body=%s)", raw)
	}
	forbidden := []string{
		"payload", "image", "owner_command_id", "executor_id", "project_id",
		"environment_id", "source_system_id", "source_id", "task_type_id",
		"resolved_image", "image_source", "claimed_at",
	}
	allowed := map[string]bool{
		"task_id": true, "team_id": true, "required_tag": true,
		"current_state": true, "ingested_at": true,
	}
	for i, item := range page.Items {
		for _, banned := range forbidden {
			if _, ok := item[banned]; ok {
				t.Errorf("items[%d] leaks forbidden field %q (item=%+v)", i, banned, item)
			}
		}
		for k := range item {
			if !allowed[k] {
				t.Errorf("items[%d] carries undocumented field %q (allowed=%v)", i, k, allowed)
			}
		}
	}
}

// TestDiscoveryIgnoresCapacityObservations proves the read-only
// capacity-ignoring contract: max_capacity and running_count are
// observational; discovery never reads or evaluates them. The
// fixture saturates the Executor (max_capacity=0, running_count=10)
// and asserts the registered tasks remain visible.
func TestDiscoveryIgnoresCapacityObservations(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	// Seed an Executor with saturated observations.
	mustExec(t, h.db,
		`INSERT INTO executors (executor_id, scope, team_id, executor_type, identity, authorized_tag, max_capacity, running_count)
		 VALUES ('exec-capacity', 'team', 'team-a', 'executor_docker_opehands', 'identity-capacity', 'openhands', 0, 10)`,
	)
	mustInsertPendingTask(t, h.db, "task-capacity", "team-a", "openhands", "ext-capacity", time.Now())

	resp, raw := h.discover(t, "exec-capacity", "openhands",
		execPutHeaders(execRoleTeam, "team-a", "exec-capacity", "req-section5-capacity"),
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 even when Executor is capacity-saturated; body=%s", resp.StatusCode, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode discovery page: %v (body=%s)", err, raw)
	}
	visible := false
	for _, item := range page.Items {
		if item["task_id"] == "task-capacity" {
			visible = true
		}
	}
	if !visible {
		t.Errorf("capacity-saturated Executor did not return its pending task; items=%+v", page.Items)
	}
}

// TestUnregisteredExecutorDiscoveryRejectedWithoutMutation asserts
// that discovery against an unregistered executor_id returns the
// non-revealing not-found envelope and never mutates the tasks table.
// Today the route is unmounted so the response is the chi default 404
// plain-text body, which fails the documented envelope assertion
// (RED).
func TestUnregisteredExecutorDiscoveryRejectedWithoutMutation(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertPendingTask(t, h.db, "task-unregistered", "team-a", "openhands", "ext-unregistered", time.Now())

	resp, raw := h.discover(t, "exec-does-not-exist", "openhands",
		execPutHeaders(execRoleTeam, "team-a", "exec-does-not-exist", "req-section5-unregistered"),
	)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unregistered discovery status=%d, want non-revealing 4xx; body=%s", resp.StatusCode, raw)
	}
	env := decodeErrorEnvelope(t, raw)
	if env.Code == "" {
		t.Errorf("response body is not the documented envelope (raw=%s)", raw)
	}
	if env.RequestID == "" {
		t.Errorf("response envelope missing request_id (raw=%s)", raw)
	}
	assertRowCount(t, h.db, "tasks", 1)
}

// TestWrongTagDiscoveryRejectedWithoutMutation asserts that
// discovery against a registered Executor with a non-matching tag is
// rejected without mutating the task row. Today the unmounted route
// returns the chi default 404 plain-text body, which fails the
// documented envelope assertion (RED).
func TestWrongTagDiscoveryRejectedWithoutMutation(t *testing.T) {
	h := newExecHarness(t)
	mustInsertAdminTeam(t, h.db, "team-a", "Team A")
	mustInsertExec(t, h.db, "exec-tag-mismatch", "team", "team-a", "openhands")
	mustInsertPendingTask(t, h.db, "task-tag-mismatch", "team-a", "k8s", "ext-tag-mismatch", time.Now())

	resp, raw := h.discover(t, "exec-tag-mismatch", "k8s",
		execPutHeaders(execRoleTeam, "team-a", "exec-tag-mismatch", "req-section5-tag-mismatch"),
	)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("wrong-tag discovery returned 200; want rejection (body=%s)", raw)
	}
	env := decodeErrorEnvelope(t, raw)
	if env.Code == "" {
		t.Errorf("response body is not the documented envelope (raw=%s)", raw)
	}
	// Task row must remain pending and un-mutated.
	assertRowCount(t, h.db, "tasks", 1)
	var state string
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT current_state FROM tasks WHERE task_id = 'task-tag-mismatch'`,
	).Scan(&state); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if state != platform.TaskStatePending {
		t.Errorf("task current_state=%q, want \"pending\"", state)
	}
	_ = migrations.CurrentVersion // keep the migrations import alive
}
