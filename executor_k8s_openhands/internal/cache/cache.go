// Package cache supplies the v0005 persistent recovery cache and the
// exclusive cache-volume lock for one concrete Executor service.
// Each concrete Executor owns its own copy under its own internal/
// tree; the platform rule "no shared code between services" rules out
// importing this package from any sibling Executor.
//
// The cache is the single bbolt database at <CacheDir>/executor.db
// carrying three versioned buckets:
//
//   - metadata        holds the canonical schema version and the
//     State Registry-generated executor_id
//   - assignments     holds per-task recovery records keyed by
//     task_id; each record carries the immutable
//     owner_command_id, the platform and runtime
//     identity needed to reconnect to the task,
//     and the conversation identity required to
//     re-attach to the agent after restart
//   - event_outbox    holds every event before send; a record
//     transitions from pending to accepted only
//     after the State Registry returns 202
//
// The lock on <CacheDir>/executor.lock is a non-blocking exclusive
// POSIX file lock. A second process attempting to acquire the same
// lock becomes unhealthy without mutating Registry or runtime state.
// The kernel releases the lock when the process exits or crashes; a
// Registry-side lease or fencing protocol is intentionally not used.
package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	bolt "go.etcd.io/bbolt"
)

// CacheDirDefault is the default cache directory used when an
// Executor is started without an explicit override.
const CacheDirDefault = "/var/lib/flowai/executor"

// SchemaVersion pins the bbolt schema that the Executor recognises.
// An unknown newer version fails the cache open call so operators
// do not silently reset or replace the database.
const SchemaVersion uint32 = 1

// knownBuckets enumerates the buckets the cache opens. Keep the
// order stable; new buckets may only be appended with a new
// SchemaVersion.
var knownBuckets = [][]byte{
	[]byte("metadata"),
	[]byte("assignments"),
	[]byte("event_outbox"),
}

// Store is the bbolt-backed persistent recovery cache. It is safe to
// call any Store method on the zero value once Open has returned a
// successful handle.
type Store struct {
	dir string
	db  *bolt.DB

	mu       sync.Mutex
	cacheDir string
}

// MetadataRecord is the per-store key/value pair stored in the
// metadata bucket.
type MetadataRecord struct {
	ExecutorID   string
	RegisteredAt time.Time
}

// AssignmentRecord is the per-task recovery record stored in the
// assignments bucket. The body is opaque JSON to keep the cache
// schema versioned; readers decode via the platform wire types.
type AssignmentRecord struct {
	TaskID         string
	TeamID         string
	ExecutorID     string
	OwnerCommandID string
	ResolvedImage  string
	ImageSource    string
	EnvironmentID  string
	ScopeToken     string
	RuntimeKind    string // "docker" | "k8s"
	RuntimeRef     string // container id | pod name@uid
	ConversationID string
	LastObserved   time.Time
	State          string // "claiming" | "claimed" | "running" | "finished" | "failed"
}

// EventOutboxRecord is one durable event delivery state. Pending
// records are retried with the original event_id after a Registry
// restart; accepted records are never re-sent.
type EventOutboxRecord struct {
	EventID    string
	TeamID     string
	TaskID     string
	ExecutorID string
	EventType  string
	OccurredAt string
	Payload    []byte
	State      string // "pending" | "accepted"
	UpdatedAt  time.Time
}

// OpenDir resolves the cache directory and ensures the directory
// exists with owner-only permissions. A symlink is rejected because
// the cache lives on a host-backed volume (K8s PVC or Docker
// host-volume); an unsupported copy is the operator's responsibility.
func OpenDir(path string) (string, error) {
	if path == "" {
		path = CacheDirDefault
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("cache: ensure dir %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("cache: resolve symlinks: %w", err)
	}
	if resolved != path {
		return "", fmt.Errorf("cache: directory %s is a symlink; expected owner-only directory", path)
	}
	return path, nil
}

// Open returns an opened cache store. The store must be Closed when
// the owning process exits; LockFile() acquires the OS-level advisory
// file lock that protects the cache volume against a second process.
func Open(path string) (*Store, error) {
	dir, err := OpenDir(path)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "executor.db")
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{
		ReadOnly: false,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("cache: open bbolt %s: %w", dbPath, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range knownBuckets {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("create bucket %s: %w", string(b), err)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cache: init buckets: %w", err)
	}
	if err := verifySchemaVersion(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := writeSchemaVersion(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{dir: dir, db: db, cacheDir: dir}, nil
}

// Dir returns the configured cache directory.
func (s *Store) Dir() string { return s.dir }

// Close releases the bbolt file handle. The OS lock acquired via
// LockFile() is owned by the caller and must be released separately
// to keep the documented lifecycle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

const metadataSchemaVersionKey = "schema_version"

// verifySchemaVersion asserts the recorded schema is recognised.
// A cache stored with an unknown newer schema fails closed.
func verifySchemaVersion(db *bolt.DB) error {
	return db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("metadata"))
		if b == nil {
			return errors.New("cache: metadata bucket missing")
		}
		raw := b.Get([]byte(metadataSchemaVersionKey))
		if raw == nil {
			return nil
		}
		var stored uint32
		if _, err := fmt.Sscanf(string(raw), "%d", &stored); err != nil {
			return fmt.Errorf("cache: parse schema_version: %w", err)
		}
		if stored > SchemaVersion {
			return fmt.Errorf("cache: stored schema_version %d is newer than supported %d", stored, SchemaVersion)
		}
		return nil
	})
}

func writeSchemaVersion(db *bolt.DB) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("metadata"))
		if b == nil {
			return errors.New("cache: metadata bucket missing")
		}
		return b.Put([]byte(metadataSchemaVersionKey), []byte(fmt.Sprintf("%d", SchemaVersion)))
	})
}

// ExecutorID returns the cached executor_id or "" when none is set.
func (s *Store) ExecutorID(ctx context.Context) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("cache: store is not open")
	}
	var out string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("metadata"))
		if b == nil {
			return errors.New("cache: metadata bucket missing")
		}
		raw := b.Get([]byte("executor_id"))
		if raw == nil {
			return nil
		}
		out = string(raw)
		return nil
	})
	return out, err
}

// PutExecutorID stores the State Registry-generated executor_id.
// Callers MUST persist the value before any discovery, claim, or
// event emission so a process crash before first registration does
// not reach the State Registry on the next start.
func (s *Store) PutExecutorID(ctx context.Context, executorID string) error {
	if s == nil || s.db == nil {
		return errors.New("cache: store is not open")
	}
	if executorID == "" {
		return errors.New("cache: executor_id is required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("metadata"))
		if b == nil {
			return errors.New("cache: metadata bucket missing")
		}
		return b.Put([]byte("executor_id"), []byte(executorID))
	})
}

// PutAssignment persists the durable assignment record. The
// write-tx-before-side-effect rule keeps the State Registry in step
// with the cache on every commit.
func (s *Store) PutAssignment(ctx context.Context, rec AssignmentRecord) error {
	if s == nil || s.db == nil {
		return errors.New("cache: store is not open")
	}
	if rec.TaskID == "" {
		return errors.New("cache: assignment task_id is required")
	}
	payload, err := encodeAssignment(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("assignments"))
		return b.Put([]byte(rec.TaskID), payload)
	})
}

// Assignment returns the assignment record for the given task_id.
// A missing record returns (AssignmentRecord{}, nil).
func (s *Store) Assignment(ctx context.Context, taskID string) (AssignmentRecord, error) {
	if s == nil || s.db == nil {
		return AssignmentRecord{}, errors.New("cache: store is not open")
	}
	var out AssignmentRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("assignments"))
		if b == nil {
			return errors.New("cache: assignments bucket missing")
		}
		raw := b.Get([]byte(taskID))
		if raw == nil {
			return nil
		}
		return decodeAssignment(raw, &out)
	})
	return out, err
}

// ListAssignments returns every cached assignment ordered by task_id.
func (s *Store) ListAssignments(ctx context.Context) ([]AssignmentRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("cache: store is not open")
	}
	var out []AssignmentRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("assignments"))
		if b == nil {
			return errors.New("cache: assignments bucket missing")
		}
		return b.ForEach(func(_, v []byte) error {
			var rec AssignmentRecord
			if err := decodeAssignment(v, &rec); err != nil {
				return err
			}
			out = append(out, rec)
			return nil
		})
	})
	return out, err
}

// DeleteAssignment removes a terminal assignment record so the
// restart reconciliation loop ignores already-finished work.
func (s *Store) DeleteAssignment(ctx context.Context, taskID string) error {
	if s == nil || s.db == nil {
		return errors.New("cache: store is not open")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("assignments"))
		return b.Delete([]byte(taskID))
	})
}

// PutOutbox persists a delivery-state record before send. State
// begins at "pending" and transitions to "accepted" only after the
// State Registry returns 202.
func (s *Store) PutOutbox(ctx context.Context, rec EventOutboxRecord) error {
	if s == nil || s.db == nil {
		return errors.New("cache: store is not open")
	}
	if rec.EventID == "" {
		return errors.New("cache: outbox event_id is required")
	}
	payload, err := encodeOutbox(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("event_outbox"))
		return b.Put([]byte(rec.EventID), payload)
	})
}

// UpdateOutboxState moves the delivery state for an existing record.
// Callers MUST use the same EventID on retry so the Registry's
// (task_id, event_id) idempotency handler deduplicates the delivery.
func (s *Store) UpdateOutboxState(ctx context.Context, eventID, state string) error {
	if s == nil || s.db == nil {
		return errors.New("cache: store is not open")
	}
	if eventID == "" {
		return errors.New("cache: outbox event_id is required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("event_outbox"))
		raw := b.Get([]byte(eventID))
		if raw == nil {
			return errors.New("cache: outbox record missing")
		}
		var rec EventOutboxRecord
		if err := decodeOutbox(raw, &rec); err != nil {
			return err
		}
		rec.State = state
		rec.UpdatedAt = time.Now().UTC()
		encoded, err := encodeOutbox(rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(eventID), encoded)
	})
}

// PendingOutbox returns every still-pending outbox record.
func (s *Store) PendingOutbox(ctx context.Context) ([]EventOutboxRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("cache: store is not open")
	}
	var out []EventOutboxRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("event_outbox"))
		if b == nil {
			return errors.New("cache: event_outbox bucket missing")
		}
		return b.ForEach(func(_, v []byte) error {
			var rec EventOutboxRecord
			if err := decodeOutbox(v, &rec); err != nil {
				return err
			}
			if rec.State == "pending" {
				out = append(out, rec)
			}
			return nil
		})
	})
	return out, err
}

// LockPath returns the absolute path to the cache-volume lock file.
// The caller is responsible for taking the OS advisory lock via
// LockFile before any registration, discovery, claim, or runtime
// mutation, and for keeping the file descriptor open for the full
// process lifetime.
func (s *Store) LockPath() string { return filepath.Join(s.dir, "executor.lock") }

// LockFile holds the non-blocking exclusive POSIX file lock
// protecting the cache volume against a second process.
type LockFile struct {
	path string
	fd   *os.File
}

// LockFile attempts the non-blocking POSIX exclusive lock on the
// cache volume. Lock contention fails closed; the caller reports the
// second process as unhealthy with no Registry or runtime mutation.
// The kernel releases the lock after a process crash or normal exit;
// the cache store does not implement a Registry-side lease.
func (s *Store) LockFile() (*LockFile, error) {
	if s == nil {
		return nil, errors.New("cache: store is not open")
	}
	path := s.LockPath()
	fd, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cache: open lock file: %w", err)
	}
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = fd.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("cache: another process holds the lock on %s", path)
		}
		return nil, fmt.Errorf("cache: lock %s: %w", path, err)
	}
	return &LockFile{path: path, fd: fd}, nil
}

// Release drops the OS file lock and closes the file descriptor.
// Safe to call on a zero-value LockFile.
func (lf *LockFile) Release() error {
	if lf == nil || lf.fd == nil {
		return nil
	}
	err := syscall.Flock(int(lf.fd.Fd()), syscall.LOCK_UN)
	if cErr := lf.fd.Close(); cErr != nil && err == nil {
		err = cErr
	}
	lf.fd = nil
	return err
}

// Path returns the absolute path of the lock file. Useful for
// diagnostics captured before deletion.
func (lf *LockFile) Path() string {
	if lf == nil {
		return ""
	}
	return lf.path
}
