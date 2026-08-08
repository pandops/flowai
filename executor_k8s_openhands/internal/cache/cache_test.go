package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func openTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store, func() { _ = store.Close() }
}

func TestOpenAndStoreExecutorID(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()

	if got, err := store.ExecutorID(context.Background()); err != nil {
		t.Fatalf("executor_id initial: %v", err)
	} else if got != "" {
		t.Fatalf("initial executor_id = %q, want empty", got)
	}

	id := "exec-" + time.Now().UTC().Format("20060102T150405")
	if err := store.PutExecutorID(context.Background(), id); err != nil {
		t.Fatalf("put executor_id: %v", err)
	}

	got, err := store.ExecutorID(context.Background())
	if err != nil {
		t.Fatalf("executor_id: %v", err)
	}
	if got != id {
		t.Fatalf("executor_id = %q, want %q", got, id)
	}
}

func TestOpenRejectsUnsupportedNewerSchema(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = store.Close()

	dbPath := filepath.Join(dir, "executor.db")
	if err := forceStoredSchemaVersion(dbPath, SchemaVersion+1); err != nil {
		t.Fatalf("force schema version: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatalf("expected unsupported-schema rejection, got nil")
	}
}

func TestAssignmentLifecycle(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()

	rec := AssignmentRecord{
		TaskID:         "task-1",
		TeamID:         "team-a",
		ExecutorID:     "exec-1",
		OwnerCommandID: "cmd-1",
		State:          "claiming",
		LastObserved:   time.Now().UTC(),
	}
	if err := store.PutAssignment(context.Background(), rec); err != nil {
		t.Fatalf("put assignment: %v", err)
	}
	got, err := store.Assignment(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("assignment: %v", err)
	}
	if got.TaskID != rec.TaskID || got.OwnerCommandID != rec.OwnerCommandID {
		t.Fatalf("assignment record mismatch: %+v", got)
	}

	rec.State = "claimed"
	if err := store.PutAssignment(context.Background(), rec); err != nil {
		t.Fatalf("update assignment: %v", err)
	}
	got, _ = store.Assignment(context.Background(), "task-1")
	if got.State != "claimed" {
		t.Fatalf("assignment state = %q, want claimed", got.State)
	}

	if err := store.DeleteAssignment(context.Background(), "task-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := store.Assignment(context.Background(), "task-1"); got.TaskID != "" {
		t.Fatalf("expected task to be gone, got %+v", got)
	}
}

func TestOutboxLifecycle(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()

	rec := EventOutboxRecord{
		EventID:    "evt-1",
		TaskID:     "task-1",
		ExecutorID: "exec-1",
		TeamID:     "team-a",
		EventType:  "running",
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		State:      "pending",
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.PutOutbox(context.Background(), rec); err != nil {
		t.Fatalf("put outbox: %v", err)
	}
	pending, err := store.PendingOutbox(context.Background())
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(pending) != 1 || pending[0].EventID != "evt-1" {
		t.Fatalf("pending list = %+v", pending)
	}

	if err := store.UpdateOutboxState(context.Background(), "evt-1", "accepted"); err != nil {
		t.Fatalf("update outbox state: %v", err)
	}
	pending, _ = store.PendingOutbox(context.Background())
	if len(pending) != 0 {
		t.Fatalf("pending list after accept = %+v", pending)
	}
}

func TestLockFileSecondProcessFails(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()

	first, err := store.LockFile()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer func() { _ = first.Release() }()

	if _, err := store.LockFile(); err == nil {
		t.Fatalf("second lock attempt should fail with contention")
	}
}

func TestLockReleaseAllowsRelock(t *testing.T) {
	store, cleanup := openTestStore(t)
	defer cleanup()

	first, err := store.LockFile()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	second, err := store.LockFile()
	if err != nil {
		t.Fatalf("re-lock: %v", err)
	}
	defer func() { _ = second.Release() }()
	if second.Path() != first.Path() {
		t.Fatalf("lock path mismatch: first=%s second=%s", first.Path(), second.Path())
	}
}

func TestOpenRejectsSymlinkDirectory(t *testing.T) {
	target := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := Open(link); err == nil {
		t.Fatalf("expected symlink rejection")
	}
}

// forceStoredSchemaVersion mutates the recorded schema_version to
// force the support-range guard to fire on reopen.
func forceStoredSchemaVersion(dbPath string, version uint32) error {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("metadata"))
		if b == nil {
			return fmt.Errorf("metadata bucket missing")
		}
		return b.Put([]byte("schema_version"), []byte(fmt.Sprintf("%d", version)))
	})
}
