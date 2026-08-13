//go:build integration

package test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/flowai/platform/svc/state-registry/internal/migrations"
)

const (
	dockerImage    = "postgres:16"
	containerLabel = "flowai.state-registry.test"
	readyTimeout   = 60 * time.Second
	dialTimeout    = 2 * time.Second
)

// fixture holds the single shared PostgreSQL container that backs
// every integration test in this package. The container is started
// once in TestMain and torn down via runtime cleanup so each
// individual test only pays for one fresh database (random name)
// plus the per-test schema apply.
type fixture struct {
	binary    string // absolute path of docker or podman
	container string // container name
	host      string // loopback address (127.0.0.1)
	port      int    // mapped host port (loopback-only)
	username  string // superuser
	password  string // superuser password
}

// shared is the package-shared container fixture. It is allocated in
// TestMain, used as a template database by every test, and torn down
// when the package finishes. Tests MUST NOT mutate it; each test
// creates its own fresh database on the same container.
var shared *fixture

// TestMain is the only place the shared PostgreSQL container is
// brought up and torn down. Each individual test runs migrations on
// a fresh randomly-named database so the migration metadata table
// (goose_db_version) never collides between tests.
func TestMain(m *testing.M) {
	// Detect the container runtime eagerly so a missing daemon
	// fails fast and the failure message clearly identifies the
	// "container runtime" setup phase (rather than some test
	// internal error).
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 10*time.Second)
	binary, err := pickContainerBinary(probeCtx)
	cancelProbe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "container runtime detection failed: %v\n", err)
		os.Exit(1)
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 2*time.Minute)
	f, err := startContainer(startCtx, binary)
	cancelStart()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres fixture start failed: %v\n", err)
		os.Exit(1)
	}
	shared = f

	code := m.Run()

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	if stopErr := stopContainer(stopCtx, f); stopErr != nil {
		fmt.Fprintf(os.Stderr, "container teardown: %v\n", stopErr)
	}
	cancelStop()
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Container lifecycle helpers
// ---------------------------------------------------------------------------

// startContainer picks Docker first then Podman, verifies the actual
// daemon with an innocuous command, and brings up a single PostgreSQL
// 16 container with random credentials and a loopback-only random
// host port. The returned fixture is the only shared resource;
// tests create their own databases on it.
func startContainer(ctx context.Context, binary string) (*fixture, error) {
	port, err := pickLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("pick loopback port: %w", err)
	}

	container, err := randomToken(12)
	if err != nil {
		return nil, fmt.Errorf("container name: %w", err)
	}
	container = "flowai-sr-" + container

	password, err := randomToken(24)
	if err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	username := "flowai"

	// Create the container: detach, remove-on-exit, loopback-only
	// port mapping, random container name, random password.
	createArgs := []string{
		"run",
		"--detach",
		"--rm",
		"--name", container,
		"--label", containerLabel,
		"--publish", fmt.Sprintf("127.0.0.1:%d:5432", port),
		"--tmpfs", "/var/lib/postgresql/data:rw",
		"--env", "POSTGRES_USER=" + username,
		"--env", "POSTGRES_PASSWORD=" + password,
		"--env", "POSTGRES_DB=postgres",
		dockerImage,
	}
	if err := runCmd(ctx, binary, createArgs); err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	f := &fixture{
		binary:    binary,
		container: container,
		host:      "127.0.0.1",
		port:      port,
		username:  username,
		password:  password,
	}

	if err := waitReady(ctx, f); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = stopContainer(stopCtx, f)
		cancel()
		return nil, fmt.Errorf("wait for postgres ready: %w", err)
	}
	return f, nil
}

// stopContainer best-effort kills and removes the test container.
// Errors are returned for the caller to log; the fixture is best
// cleaned up even on error.
func stopContainer(ctx context.Context, f *fixture) error {
	if f == nil {
		return nil
	}
	// `docker rm --force` removes the container even if it is
	// running. Detach + rm is the documented Podman equivalent.
	return runCmd(ctx, f.binary, []string{"rm", "--force", f.container})
}

// pickContainerBinary checks Docker first, then Podman. It verifies
// the actual daemon with `info` (not just `--version`) so a present
// but unreachable CLI does not silently waste seconds per test.
func pickContainerBinary(ctx context.Context) (string, error) {
	for _, candidate := range []string{"docker", "podman"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		infoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = runCmdSilent(infoCtx, path, []string{"info"})
		cancel()
		if err == nil {
			return path, nil
		}
	}
	return "", errors.New("no usable docker or podman CLI found")
}

// pickLoopbackPort asks the OS for a free TCP port by opening a
// listener on 127.0.0.1:0, reading the assigned port, and closing
// the listener immediately. The port is loopback-only so other
// processes on the host cannot reach the test database.
func pickLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listener addr is %T", listener.Addr())
	}
	return addr.Port, nil
}

// waitReady polls PostgreSQL until it accepts a real connection or
// the bounded timeout expires. The probe combines a TCP dial check
// with a `SELECT 1` over pgx so readiness implies that the database
// is ready to accept real client connections.
func waitReady(ctx context.Context, f *fixture) error {
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", f.host, f.port), dialTimeout)
		if err == nil {
			_ = conn.Close()
		}
		db, err := sql.Open("pgx", f.adminDSN("postgres"))
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, dialTimeout)
			pingErr := db.PingContext(pingCtx)
			cancel()
			if pingErr == nil {
				_ = db.Close()
				return nil
			}
			_ = db.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("postgres not ready within %s on %s:%d", readyTimeout, f.host, f.port)
}

// ---------------------------------------------------------------------------
// Per-test database helpers
// ---------------------------------------------------------------------------

// freshDB returns a *sql.DB connected to a brand-new randomly named
// PostgreSQL database. The database is owned by the calling test and
// dropped via t.Cleanup so no test leaves schema residue behind. The
// returned *sql.DB is closed by t.Cleanup.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	if shared == nil {
		t.Fatalf("postgres fixture unavailable; ensure docker or podman is reachable")
	}

	dbName, err := randomToken(16)
	if err != nil {
		t.Fatalf("generate random database name: %v", err)
	}
	dbName = "sr_" + strings.ToLower(dbName)

	adminDB, err := sql.Open("pgx", shared.adminDSN("postgres"))
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()

	createCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := adminDB.ExecContext(createCtx, createDatabaseSQL(dbName)); err != nil {
		t.Fatalf("create fresh database %s: %v", dbName, err)
	}

	db, err := sql.Open("pgx", shared.adminDSN(dbName))
	if err != nil {
		t.Fatalf("open fresh database %s: %v", dbName, err)
	}

	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		cleanupDB, openErr := sql.Open("pgx", shared.adminDSN("postgres"))
		if openErr != nil {
			t.Logf("cleanup: open admin db: %v", openErr)
			return
		}
		defer cleanupDB.Close()
		// pgx stdlib wraps multi-statement ExecContext in a single
		// transaction, which Postgres rejects for DROP DATABASE.
		// Issue the terminate-sessions and DROP as two separate
		// ExecContext calls so each runs outside a transaction.
		if _, termErr := cleanupDB.ExecContext(dropCtx, terminateDatabaseSessionsSQL(dbName)); termErr != nil {
			t.Logf("cleanup: terminate sessions on %s: %v", dbName, termErr)
		}
		if _, dropErr := cleanupDB.ExecContext(dropCtx, dropDatabaseSQL(dbName)); dropErr != nil {
			t.Logf("cleanup: drop database %s: %v", dbName, dropErr)
		}
	})

	return db
}

// createDatabaseSQL returns a CREATE DATABASE statement whose
// identifier has been validated to contain only [a-z0-9_]. The
// identifier is wrapped in double quotes so PostgreSQL accepts it
// verbatim; the validation guarantees no injection is possible.
func createDatabaseSQL(name string) string {
	if !isSafeIdentifier(name) {
		panic(fmt.Sprintf("createDatabaseSQL: unsafe identifier %q", name))
	}
	return fmt.Sprintf(`CREATE DATABASE "%s"`, name)
}

// dropDatabaseSQL mirrors createDatabaseSQL and is intended to run
// AFTER terminateDatabaseSessionsSQL has killed every back-end
// still attached to the target database.
func dropDatabaseSQL(name string) string {
	if !isSafeIdentifier(name) {
		panic(fmt.Sprintf("dropDatabaseSQL: unsafe identifier %q", name))
	}
	return fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, name)
}

// terminateDatabaseSessionsSQL returns a SELECT that asks Postgres
// to terminate every back-end currently connected to the named
// database. The identifier is sanitized via isSafeIdentifier so the
// datname literal cannot inject SQL.
func terminateDatabaseSessionsSQL(name string) string {
	if !isSafeIdentifier(name) {
		panic(fmt.Sprintf("terminateDatabaseSessionsSQL: unsafe identifier %q", name))
	}
	return fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`,
		strings.ReplaceAll(name, "'", "''"),
	)
}

// isSafeIdentifier accepts only lower-case alphanumeric plus
// underscore, length 1..63. PostgreSQL's identifier limit is 63
// bytes; we stay well under that for random tokens.
func isSafeIdentifier(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// applyMigrations runs the service-local Goose migrations against the
// supplied *sql.DB. It is a thin wrapper that surfaces errors with
// enough context to identify the failure phase.
func applyMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrations.ApplyUp(ctx, db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

// preflight checks the connection plus a `SELECT 1` plus the
// Goose-version-table presence so schema assertions cannot mask fixture problems.
func preflight(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("preflight SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("preflight SELECT 1 returned %d, want 1", one)
	}
	version, err := migrations.CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("preflight goose current version: %v", err)
	}
	if version < 1 {
		t.Fatalf("preflight goose version: %v, want >= 1", version)
	}
}

// ---------------------------------------------------------------------------
// Random token helper
// ---------------------------------------------------------------------------

// randomToken returns a hex-encoded random token of the requested
// byte length. Uses crypto/rand so the output is unpredictable; the
// password, container name, and database name all derive from this
// helper.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

// ---------------------------------------------------------------------------
// Command runner (separate argv, sanitized errors)
// ---------------------------------------------------------------------------

// runCmd executes the binary with the supplied argv. CombinedOutput
// is bound to a single buffer so failures are visible without
// exposing secrets; the supplied context is propagated.
func runCmd(ctx context.Context, binary string, argv []string) error {
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s",
			binary,
			redactArgv(argv),
			err,
			sanitizeOutput(string(out)),
		)
	}
	return nil
}

// runCmdSilent is like runCmd but discards the output. Used by the
// container-binary probe to avoid polluting test output with `info`
// noise.
func runCmdSilent(ctx context.Context, binary string, argv []string) error {
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Env = os.Environ()
	return cmd.Run()
}

// redactArgv replaces argv values that look like a password or DSN
// with `<redacted>` so error messages cannot leak credentials. The
// structure of the command line is preserved so debugging stays
// useful.
func redactArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if isSecretArg(argv, i) {
			parts[i] = "<redacted>"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// isSecretArg reports whether argv[i] is a value following a
// password-flavored flag (POSTGRES_PASSWORD=, --env POSTGRES_PASSWORD,
// or the password field of a libpq DSN).
func isSecretArg(argv []string, i int) bool {
	if i == 0 {
		return false
	}
	prev := strings.ToUpper(argv[i-1])
	cur := argv[i]
	// `KEY=value` style secrets: redact only the value portion.
	if strings.HasPrefix(cur, "POSTGRES_PASSWORD=") {
		return true
	}
	// Direct positional value after the env-var name.
	if prev == "POSTGRES_PASSWORD" {
		return true
	}
	// libpq DSN: "postgres://user:password@host/db"
	if strings.HasPrefix(cur, "postgres://") && strings.Contains(cur, "@") {
		return true
	}
	return false
}

// sanitizeOutput strips anything that looks like a DSN or the shared
// container password from a captured command output. PostgreSQL
// startup logs do not normally include the password but the helper
// is defensive.
func sanitizeOutput(s string) string {
	out := s
	if idx := strings.Index(out, "postgres://"); idx >= 0 {
		end := strings.Index(out[idx:], "@")
		if end >= 0 {
			out = out[:idx] + "<redacted-dsn>" + out[idx+end:]
		}
	}
	if shared != nil && shared.password != "" {
		out = strings.ReplaceAll(out, shared.password, "<redacted>")
	}
	return out
}

// adminDSN returns the libpq DSN for connecting as the superuser to
// the named database. The DSN is never printed because every error
// path routes through redactArgv / sanitizeOutput.
func (f *fixture) adminDSN(database string) string {
	if !isSafeIdentifier(database) {
		panic(fmt.Sprintf("adminDSN: unsafe database identifier %q", database))
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		f.username, f.password, f.host, f.port, database,
	)
}
