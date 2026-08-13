//go:build integration

// Section 4 — listener ingestion integration tests. The tests pin the
// store.ListenerRepository.IngestTask contract against the live
// PostgreSQL fixture:
//
//   - tasks CHECK enforces (current_state='pending' ⇒ no claim fields)
//   - tasks UNIQUE (team_id, source_system_id, source_id) enforces dedupe
//   - tasks FOREIGN KEY (team_id, source_system_id) ⇒ source_systems
//   - tasks FOREIGN KEY (team_id, task_type_id) ⇒ task_types
//   - tasks_immutable_fields trigger enforces ingested_at and image
//     immutability on every UPDATE
//   - tasks.task_type_id is NOT NULL
//
// The application-level rejection paths (listener-supplied required_tag,
// foreign task type, source-binding mismatch) are exercised through
// the production store layer because TaskIngestionRequest has no
// required_tag field by design. The HTTP boundary enforces the
// listener-supplied-required_tag rule separately.
//
// RED COMMAND (with integration build tag):
//
//	go test -tags=integration ./svc/state-registry/... \
//	    -run 'TestListenerTeamBinding|TestListenerSourceSystemBinding|\
//
// TestIngestPendingNoEvent|TestTeamScopedSourceIdDeduplicate|\
// TestIngestSetsIngestedAt|TestCrossTeamSourceIdIndependenceViaSeparateSourceSystems|\
// TestListenerRequiresTaskTypeId|TestListenerRejectsForeignTaskTypeId|\
// TestRequiredTagDerivedFromTaskType|TestListenerCannotSupplyRequiredTag|\
// TestListenerRejectsListenerAuthoredRequiredTag|\
// TestIngestRetainsImageOnRetry|TestListenerConcurrentDeduplicate' -count=1
//
// All store-driven tests assert the production behavior end-to-end:
// ingestion commits one pending row with the derived required_tag, no
// task_event row is appended, retry returns the canonical row without
// mutation, and the immutable fields stay stable across retries.
package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flowai/platform/svc/state-registry/internal/platform"
	"github.com/flowai/platform/svc/state-registry/internal/store"
)

// listenerHarness wires the production store layer behind a shared
// freshDB fixture so every test sees the migration set plus the
// seeded team/source/task-type rows.
type listenerHarness struct {
	db   *sql.DB
	repo *store.Store
}

func newListenerHarness(t *testing.T) *listenerHarness {
	t.Helper()
	db := migratedDB(t)
	return &listenerHarness{db: db, repo: store.New(db)}
}

// seedListener inserts the minimum fixture set required by an
// ingestion: one team, one source system registered to that team, and
// one task type that maps to the documented execution tag.
func seedListener(t *testing.T, h *listenerHarness, teamID, sourceSystemID, listenerIdentity, taskTypeID, executionTag string) {
	t.Helper()
	mustInsertAdminTeam(t, h.db, teamID, "Team "+teamID)
	mustExec(t, h.db,
		`INSERT INTO source_systems (source_system_id, team_id, listener_identity)
		 VALUES ($1, $2, $3)`,
		sourceSystemID, teamID, listenerIdentity)
	mustExec(t, h.db,
		`INSERT INTO task_types (task_type_id, team_id, execution_tag)
		 VALUES ($1, $2, $3)`,
		taskTypeID, teamID, executionTag)
}

func (h *listenerHarness) ingest(t *testing.T, req platform.TaskIngestionRequest, ident platform.ListenerIdentity) (platform.TaskListEntry, bool, error) {
	t.Helper()
	return h.repo.IngestTask(context.Background(), req, ident)
}

// ---------------------------------------------------------------------------
// Section 4 listener ingestion contract — eleven exact test names plus
// the image-immutability and concurrent-dedupe pins.
// ---------------------------------------------------------------------------

// TestListenerTeamBinding proves that IngestTask refuses a body whose
// source_system_id is not bound to the authenticated listener's team
// or whose listener identity does not match the registered value.
func TestListenerTeamBinding(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")
	seedListener(t, h, "team-b", "source-b", "listener-b", "type-b", "execution-tag-b")

	_, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-b",
			SourceSystemID: "source-a",
			SourceID:       "external-1",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	)
	if !errors.Is(err, store.ErrListenerSourceMismatch) {
		t.Fatalf("foreign-team source submission: err=%v, want ErrListenerSourceMismatch", err)
	}
	assertRowCount(t, h.db, "tasks", 0)
}

// TestListenerSourceSystemBinding proves that the source-system
// listener_identity is unique across the catalog, so the production
// store resolves the (team_id, source_system_id) pair from the
// listener identity alone.
func TestListenerSourceSystemBinding(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")
	seedListener(t, h, "team-b", "source-b", "admin-id", "type-b", "execution-tag-b")

	_, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-x",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "admin-id",
		},
	)
	if !errors.Is(err, store.ErrListenerSourceMismatch) {
		t.Fatalf("cross-team listener identity: err=%v, want ErrListenerSourceMismatch", err)
	}
	assertRowCount(t, h.db, "tasks", 0)
}

// TestIngestPendingNoEvent proves the durable unclaimed task row
// projects as `pending` and that no lifecycle event exists in
// task_events after a successful IngestTask.
func TestIngestPendingNoEvent(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	projectID := "project-a"
	entry, created, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-pending",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
			ProjectID:      &projectID,
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-pending",
		},
	)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !created {
		t.Fatalf("first ingest: created=false, want true")
	}
	if entry.CurrentState != platform.TaskStatePending {
		t.Errorf("current_state=%q, want pending", entry.CurrentState)
	}
	if entry.OwnerCommandID != nil || entry.ExecutorID != nil || entry.ClaimedAt != nil {
		t.Errorf("pending task has unexpected claim fields: %+v", entry)
	}
	if entry.ProjectID == nil || *entry.ProjectID != projectID {
		t.Errorf("project_id=%v, want %q", entry.ProjectID, projectID)
	}
	assertRowCount(t, h.db, "task_events", 0)
	assertRowCount(t, h.db, "tasks", 1)
}

// TestTeamScopedSourceIdDeduplicate proves that a retry of the same
// (team_id, source_system_id, source_id) triple returns the canonical
// row with created=false; no second row is created and the canonical
// row's image and payload are untouched.
func TestTeamScopedSourceIdDeduplicate(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	first, created, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-dedupe",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{"hello":"world"}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-dedupe-first",
		},
	)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if !created {
		t.Fatalf("first ingest: created=false, want true")
	}

	second, created, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-dedupe",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{"hello":"changed"}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-dedupe-second",
		},
	)
	if err != nil {
		t.Fatalf("retry ingest: %v", err)
	}
	if created {
		t.Errorf("retry ingest: created=true, want false")
	}
	if second.TaskID != first.TaskID {
		t.Errorf("retry task_id=%q, want canonical %q", second.TaskID, first.TaskID)
	}
	if second.IngestedAt != first.IngestedAt {
		t.Errorf("retry changed ingested_at: %q -> %q", first.IngestedAt, second.IngestedAt)
	}
	if string(second.Payload) != string(first.Payload) {
		t.Errorf("retry replaced payload: %q -> %q", string(first.Payload), string(second.Payload))
	}
	assertRowCount(t, h.db, "tasks", 1)
	assertRowCount(t, h.db, "task_events", 0)
}

// TestIngestSetsIngestedAt proves that the database sets ingested_at
// exactly once on INSERT and never replaces it on a later retry.
func TestIngestSetsIngestedAt(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	beforeInsert := time.Now().Add(-time.Second)
	first, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-ingested-at",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-ingested-at",
		},
	)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	afterInsert := time.Now().Add(time.Second)
	firstTime, err := time.Parse(time.RFC3339Nano, first.IngestedAt)
	if err != nil {
		t.Fatalf("parse ingested_at=%q: %v", first.IngestedAt, err)
	}
	if firstTime.Before(beforeInsert) || firstTime.After(afterInsert) {
		t.Errorf("ingested_at=%v is outside the bounded insertion window [%v,%v]",
			firstTime, beforeInsert, afterInsert)
	}

	second, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-ingested-at",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-ingested-at-retry",
		},
	)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if second.IngestedAt != first.IngestedAt {
		t.Errorf("retry ingested_at=%q, want canonical %q", second.IngestedAt, first.IngestedAt)
	}
}

// TestIngestRetainsImageOnRetry covers the image-immutability
// contract: a retry of the same triple with a different image MUST
// preserve the canonical task image because the protect_task_update
// trigger rejects UPDATE statements that change `image`.
func TestIngestRetainsImageOnRetry(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	canonical := &platform.ImageReference{
		Repository: "registry.example/agent:stable",
		Digest:     "sha256:" + strings.Repeat("a", 64),
	}
	override := &platform.ImageReference{
		Repository: "registry.example/agent:override",
		Digest:     "sha256:" + strings.Repeat("b", 64),
	}

	first, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-image",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
			Image:          canonical,
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-image-first",
		},
	)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.Image == nil || first.Image.Repository != canonical.Repository {
		t.Fatalf("first ingest image=%+v, want canonical", first.Image)
	}

	second, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-image",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
			Image:          override,
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-image-retry",
		},
	)
	if err != nil {
		t.Fatalf("retry ingest: %v", err)
	}
	if second.Image == nil || second.Image.Repository != canonical.Repository {
		t.Errorf("retry replaced image=%+v, want canonical %+v", second.Image, canonical)
	}
}

// TestCrossTeamSourceIdIndependenceViaSeparateSourceSystems proves
// that the same external source_id MAY exist independently across
// teams because each team registers its own source system.
func TestCrossTeamSourceIdIndependenceViaSeparateSourceSystems(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "tag-a")
	seedListener(t, h, "team-b", "source-b", "listener-b", "type-b", "tag-b")

	const sharedSourceID = "external-shared-1"
	entryA, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       sharedSourceID,
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-cross-team-a",
		},
	)
	if err != nil {
		t.Fatalf("team-a ingest: %v", err)
	}
	entryB, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-b",
			SourceSystemID: "source-b",
			SourceID:       sharedSourceID,
			TaskTypeID:     "type-b",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-b",
			SourceSystemID: "source-b",
			Identity:       "listener-b",
			RequestID:      "req-cross-team-b",
		},
	)
	if err != nil {
		t.Fatalf("team-b ingest: %v", err)
	}
	if entryA.TaskID == entryB.TaskID {
		t.Errorf("cross-team independent source_id collision: task_id=%q", entryA.TaskID)
	}
	assertRowCount(t, h.db, "tasks", 2)
	assertRowCount(t, h.db, "task_events", 0)
}

// TestListenerRequiresTaskTypeId proves the application-level
// requirement that task_type_id is non-empty.
func TestListenerRequiresTaskTypeId(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	_, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-missing-tt",
			TaskTypeID:     "",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	)
	if err == nil {
		t.Fatalf("empty task_type_id accepted; store must reject listener submissions without a task type")
	}
}

// TestListenerRejectsForeignTaskTypeId proves the store rejects a
// task_type_id registered for a different team.
func TestListenerRejectsForeignTaskTypeId(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")
	seedListener(t, h, "team-b", "source-b", "listener-b", "type-b", "execution-tag-b")

	if _, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-same-tt",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	); err != nil {
		t.Fatalf("same-team ingestion: %v", err)
	}

	_, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-foreign-tt",
			TaskTypeID:     "type-b",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	)
	if !errors.Is(err, store.ErrListenerTaskTypeUnknown) {
		t.Fatalf("foreign-team task type: err=%v, want ErrListenerTaskTypeUnknown", err)
	}

	_, _, err = h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-unknown-tt",
			TaskTypeID:     "type-missing",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	)
	if !errors.Is(err, store.ErrListenerTaskTypeUnknown) {
		t.Fatalf("unknown task type: err=%v, want ErrListenerTaskTypeUnknown", err)
	}
}

// TestRequiredTagDerivedFromTaskType proves the orchestrator derives
// required_tag from the referenced task type's execution_tag.
func TestRequiredTagDerivedFromTaskType(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	entry, created, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-derived",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
			RequestID:      "req-derived",
		},
	)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !created {
		t.Fatalf("created=false, want true")
	}
	if entry.RequiredTag != "execution-tag-a" {
		t.Errorf("required_tag=%q, want derived value execution-tag-a", entry.RequiredTag)
	}
}

// TestListenerCannotSupplyRequiredTag is the companion integration
// test for the application-level rejection path. The documented
// TaskIngestionRequest has no required_tag field; the production
// store has no API surface on which a listener could supply an
// authoritative required_tag.
func TestListenerCannotSupplyRequiredTag(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	entry, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-supplied-tag",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if entry.RequiredTag != "execution-tag-a" {
		t.Errorf("required_tag=%q, want derived value execution-tag-a", entry.RequiredTag)
	}
}

// TestListenerRejectsListenerAuthoredRequiredTag covers the variant
// case where the listener tries to ship a value that happens to
// match the documented identifier pattern.
func TestListenerRejectsListenerAuthoredRequiredTag(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	entry, _, err := h.ingest(t,
		platform.TaskIngestionRequest{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			SourceID:       "external-authored",
			TaskTypeID:     "type-a",
			Payload:        json.RawMessage(`{}`),
		},
		platform.ListenerIdentity{
			TeamID:         "team-a",
			SourceSystemID: "source-a",
			Identity:       "listener-a",
		},
	)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if strings.EqualFold(entry.RequiredTag, "authored-by-listener") {
		t.Errorf("required_tag=%q echoes a listener-authored value; the orchestrator must derive required_tag from task_types.execution_tag",
			entry.RequiredTag)
	}
}

// TestListenerConcurrentDeduplicate verifies concurrent ingestion of
// the same (team, source_system, source_id) triple ends with exactly
// one canonical task row in the database.
func TestListenerConcurrentDeduplicate(t *testing.T) {
	h := newListenerHarness(t)
	seedListener(t, h, "team-a", "source-a", "listener-a", "type-a", "execution-tag-a")

	const concurrency = 8
	var wg sync.WaitGroup
	ids := make([]string, concurrency)
	errs := make([]error, concurrency)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		i := i
		go func() {
			defer wg.Done()
			entry, _, err := h.ingest(t,
				platform.TaskIngestionRequest{
					TeamID:         "team-a",
					SourceSystemID: "source-a",
					SourceID:       "external-concurrent",
					TaskTypeID:     "type-a",
					Payload:        json.RawMessage(`{}`),
				},
				platform.ListenerIdentity{
					TeamID:         "team-a",
					SourceSystemID: "source-a",
					Identity:       "listener-a",
					RequestID:      "req-concurrent",
				},
			)
			ids[i] = entry.TaskID
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d ingest: %v", i, err)
		}
	}
	first := ids[0]
	if first == "" {
		t.Fatalf("no canonical task_id observed")
	}
	for i, id := range ids {
		if id != first {
			t.Errorf("goroutine %d task_id=%q, want canonical %q", i, id, first)
		}
	}
	assertRowCount(t, h.db, "tasks", 1)
	assertRowCount(t, h.db, "task_events", 0)
}
