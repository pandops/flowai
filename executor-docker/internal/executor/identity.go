package executor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

// DefaultCleanupIDPath is the legacy /tmp fallback used only when the
// per-user CacheDir is unavailable (sandboxed / restricted FS). Real
// deployments MUST set FLOWAI_CLEANUP_ID_DIR to a user-owned directory.
//
// Cleanup identity differs from executor_id: executor_id is unique per
// process instance, while cleanup_id is stable across restarts so the
// executor can find and remove containers from a previous run on the
// same host.
const DefaultCleanupIDPath = "/tmp/flowai-cleanup-id"

// CleanupIDFileName is the canonical name of the cleanup_id file
// under the per-user cache directory.
const CleanupIDFileName = "cleanup-id"

// EnvCleanupIDDir overrides the parent directory; the file name within
// that directory is fixed to CleanupIDFileName. Tests use this to pin
// the path under t.TempDir().
const EnvCleanupIDDir = "FLOWAI_CLEANUP_ID_DIR"

// EnvCleanupIDPath retains the legacy var-name semantics for callers
// that set the full file path. New code should prefer EnvCleanupIDDir.
const EnvCleanupIDPath = "FLOWAI_CLEANUP_ID_PATH"

// SecureDirPerm is the mode for the cleanup identity parent directory:
// owner read/write/execute only (no group/other access).
const SecureDirPerm os.FileMode = 0o700

// SecureFilePerm is the mode for the cleanup identity file:
// owner read/write only (no group/other access).
const SecureFilePerm os.FileMode = 0o600

// resolveCleanupIDPath returns the absolute path the executor uses for
// the cleanup identity file. The order of precedence is:
//
//  1. FLOWAI_CLEANUP_ID_PATH (full file path; legacy)
//  2. FLOWAI_CLEANUP_ID_DIR  + CleanupIDFileName
//  3. <UserCacheDir>/flowai/cleanup-id
//  4. /tmp/flowai-cleanup-id (last-resort fallback)
func resolveCleanupIDPath() (string, error) {
	if p := os.Getenv(EnvCleanupIDPath); p != "" && p != DefaultCleanupIDPath {
		return p, nil
	}
	if d := os.Getenv(EnvCleanupIDDir); d != "" {
		return filepath.Join(d, CleanupIDFileName), nil
	}
	cache, err := os.UserCacheDir()
	if err == nil && cache != "" {
		return filepath.Join(cache, "flowai", CleanupIDFileName), nil
	}
	return DefaultCleanupIDPath, nil
}

// CleanupIDPath exposes the path used to persist the stable cleanup
// identity. It returns the legacy /tmp path when no UserCacheDir is
// available; loadOrCreateCleanupID refuses to write to a non-user-
// owned filesystem by falling back to an in-memory identity instead.
func CleanupIDPath() string {
	p, err := resolveCleanupIDPath()
	if err != nil || p == "" {
		return DefaultCleanupIDPath
	}
	return p
}

// loadOrCreateCleanupID returns the stable cleanup identity, creating
// one on disk if no file already exists. On secure-open failure
// (symlink, bad perms, etc.) it falls back to a transient in-memory
// identity so the Executor process can still start.
func loadOrCreateCleanupID(path string) (string, error) {
	if path == "" {
		path = DefaultCleanupIDPath
	}
	if id, err := readCleanupIDFile(path); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "cleanup-" + uuid.NewString(), nil
	}
	id := "cleanup-" + uuid.NewString()
	if err := writeCleanupIDFile(path, id); err != nil {
		return id, nil
	}
	return id, nil
}

// readCleanupIDFile returns the contents of the cleanup_id file when
// the file exists, is a regular file, is not a symlink, and is not
// world/group accessible. Anything else is treated as "no identity
// present" so the caller can rewrite.
func readCleanupIDFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cleanup_id file is a symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("cleanup_id path is not a regular file: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("cleanup_id file is world/group accessible: %s", path)
	}
	f, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info2, lerr := os.Lstat(path); lerr == nil && info2.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("cleanup_id file became a symlink during read: %s", path)
	}
	if info.Size() > 256 {
		return "", fmt.Errorf("cleanup_id file too large: %d bytes", info.Size())
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("cleanup_id file is empty")
	}
	return id, nil
}

// writeCleanupIDFile atomically creates a non-world-readable
// cleanup_id file with the supplied identity. It rejects symlinks in
// the parent (lstat on the final path before open), refuses to
// clobber an existing non-empty file, and uses O_EXCL where the
// platform allows it.
func writeCleanupIDFile(path string, id string) error {
	if err := os.MkdirAll(filepath.Dir(path), SecureDirPerm); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), SecureDirPerm); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return writeAtomicViaOExcl(path, id)
	}
	return writePortable(path, id)
}

func writeAtomicViaOExcl(path string, id string) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL | syscall_O_NOFOLLOW_if_safe()
	f, err := os.OpenFile(path, flags, SecureFilePerm)
	if err == nil {
		defer f.Close()
		n, werr := f.WriteString(id)
		if werr != nil {
			return werr
		}
		if n != len(id) {
			return fmt.Errorf("short write to %s: %d/%d", path, n, len(id))
		}
		_ = f.Sync()
		if info, lerr := os.Lstat(path); lerr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(path)
				return fmt.Errorf("cleanup_id file became a symlink during write: %s", path)
			}
			if info.Mode().Perm()&0o077 != 0 {
				_ = os.Chmod(path, SecureFilePerm)
			}
		}
		return nil
	}
	return err
}

func writePortable(path string, id string) error {
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cleanup_id path is a symlink: %s", path)
	}
	f, err := openNoFollow(path, os.O_WRONLY, SecureFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	if info2, lerr := os.Lstat(path); lerr == nil && info2.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cleanup_id path became a symlink during open: %s", path)
	}
	if err := os.Chmod(path, SecureFilePerm); err != nil {
		return err
	}
	if _, err := f.WriteString(id); err != nil {
		return err
	}
	return f.Sync()
}

// openNoFollow opens path applying the O_NOFOLLOW bit where the
// platform supports it (POSIX). On platforms without O_NOFOLLOW the
// caller relies on a post-open lstat to detect races.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall_O_NOFOLLOW_if_safe(), perm)
	if err == nil {
		return f, nil
	}
	if isUnsupported(err) {
		return os.OpenFile(path, flag, perm)
	}
	return nil, err
}

func isUnsupported(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "operation not supported") || strings.Contains(s, "not supported")
}
