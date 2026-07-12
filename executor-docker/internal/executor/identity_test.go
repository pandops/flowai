package executor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempDirEnv unsets FLOWAI_CLEANUP_ID_PATH and pins FLOWAI_CLEANUP_ID_DIR
// to the supplied dir for the duration of the test.
func withTempDirEnv(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(EnvCleanupIDPath, "")
	t.Setenv(EnvCleanupIDDir, dir)
}

func TestCleanupIDResolvePrefersDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCleanupIDPath, "")
	t.Setenv(EnvCleanupIDDir, dir)
	p, err := resolveCleanupIDPath()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(dir, CleanupIDFileName)
	if p != want {
		t.Fatalf("got %q want %q", p, want)
	}
}

func TestCleanupIDResolveFallsBackToCacheDir(t *testing.T) {
	t.Setenv(EnvCleanupIDPath, "")
	t.Setenv(EnvCleanupIDDir, "")
	p, err := resolveCleanupIDPath()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Contains(p, DefaultCleanupIDPath) {
		t.Fatalf("unexpected fallback to %s: %q", DefaultCleanupIDPath, p)
	}
	// Should derive from UserCacheDir when available.
	if cache, _ := os.UserCacheDir(); cache != "" && !strings.HasPrefix(p, cache) {
		t.Fatalf("expected path under cache %q, got %q", cache, p)
	}
}

func TestLoadOrCreateCleanupIDPersistsStable(t *testing.T) {
	dir := t.TempDir()
	withTempDirEnv(t, dir)

	first, err := loadOrCreateCleanupID(filepath.Join(dir, CleanupIDFileName))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !strings.HasPrefix(first, "cleanup-") {
		t.Fatalf("unexpected id %q", first)
	}

	// Second call must reuse the same id from the persisted file.
	second, err := loadOrCreateCleanupID(filepath.Join(dir, CleanupIDFileName))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Fatalf("expected stable reread, got %q != %q", first, second)
	}

	// Verify on-disk mode is owner-only.
	info, lerr := os.Lstat(filepath.Join(dir, CleanupIDFileName))
	if lerr != nil {
		t.Fatalf("lstat: %v", lerr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("cleanup_id file is a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("file mode is world/group accessible: %v", info.Mode().Perm())
	}
}

func TestLoadOrCreateCleanupIDRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	withTempDirEnv(t, dir)
	target := filepath.Join(dir, "real-id")
	if err := os.WriteFile(target, []byte("cleanup-real"), SecureFilePerm); err != nil {
		t.Fatalf("seed: %v", err)
	}
	link := filepath.Join(dir, CleanupIDFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported on this fs: %v", err)
	}

	// readCleanupIDFile must surface an error and refuse to follow
	// the symlink (id must NOT be the target's identity).
	if _, err := readCleanupIDFile(link); err == nil {
		t.Fatalf("expected error reading symlink cleanup_id")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}

	// loadOrCreateCleanupID tolerates the symlink path (legacy /tmp
	// may not be safe) by emitting an in-memory identity and emitting
	// no error so startup does not hard-fail.
	id, err := loadOrCreateCleanupID(link)
	if err != nil {
		t.Fatalf("must tolerate legacy symlink: %v", err)
	}
	if id == "cleanup-real" {
		t.Fatalf("symlink target identity must NOT leak: %q", id)
	}
	if !strings.HasPrefix(id, "cleanup-") {
		t.Fatalf("unexpected id %q", id)
	}
}

func TestLoadOrCreateCleanupIDRejectsWorldReadable(t *testing.T) {
	dir := t.TempDir()
	withTempDirEnv(t, dir)
	path := filepath.Join(dir, CleanupIDFileName)
	// Write directly with a permissive mask to simulate a hostile env.
	if err := os.WriteFile(path, []byte("cleanup-x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := readCleanupIDFile(path)
	if err == nil {
		t.Fatalf("expected error on world-readable cleanup_id")
	}
	if !strings.Contains(err.Error(), "world") && !strings.Contains(err.Error(), "group") {
		t.Fatalf("expected perm error, got: %v", err)
	}
}

func TestWriteCleanupIDFileAtomicCreatesRegular(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CleanupIDFileName)
	if err := writeCleanupIDFile(path, "cleanup-test-id"); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("file became a symlink")
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("not a regular file: %v", info.Mode())
	}
	if info.Mode().Perm() != SecureFilePerm {
		t.Fatalf("perm: got %v want %v", info.Mode().Perm(), SecureFilePerm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "cleanup-test-id" {
		t.Fatalf("content: %q", data)
	}
}

func TestLoadOrCreateCleanupIDDoesNotClobberExisting(t *testing.T) {
	dir := t.TempDir()
	withTempDirEnv(t, dir)
	path := filepath.Join(dir, CleanupIDFileName)
	if err := writeCleanupIDFile(path, "cleanup-existing"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// loadOrCreateCleanupID must reuse the existing value if load
	// succeeds; it must not write a new one.
	id, err := loadOrCreateCleanupID(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id != "cleanup-existing" {
		t.Fatalf("expected existing id, got %q", id)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "cleanup-existing" {
		t.Fatalf("file clobbered: %q", data)
	}
}

func TestLoadOrCreateCleanupIDSymlinkMitigated(t *testing.T) {
	dir := t.TempDir()
	withTempDirEnv(t, dir)
	path := filepath.Join(dir, CleanupIDFileName)
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("cleanup-real"), SecureFilePerm); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// Even though the open fails, the cleanup path must surface an
	// in-memory id so the executor startup does not hard-fail.
	id, err := loadOrCreateCleanupID(path)
	if err != nil {
		t.Fatalf("must tolerate symlink on legacy path: %v", err)
	}
	if !strings.HasPrefix(id, "cleanup-") {
		t.Fatalf("unexpected id %q", id)
	}
}

func TestOpenNoFollowRejectsUnsupportedFlag(t *testing.T) {
	// isUnsupported is exercised by passing an ENOTSUP-style error.
	if !isUnsupported(errors.New("operation not supported")) {
		t.Fatalf("expected isUnsupported to match 'operation not supported'")
	}
	if isUnsupported(nil) {
		t.Fatalf("isUnsupported(nil) must be false")
	}
}
