package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/flowai/platform/svc/state-registry/internal/config"
)

func randomAESKeyHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf)
}

func TestCursorKeyringForBootFailsClosed(t *testing.T) {
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_ID", "")
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_HEX", "")
	t.Setenv("STATE_REGISTRY_CURSOR_PREVIOUS_KEYS_JSON", "")
	if _, err := cursorKeyringForBoot(); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestCursorKeyringForBootLoadsRotationSet(t *testing.T) {
	active := make([]byte, 32)
	previous := make([]byte, 32)
	for i := range active {
		active[i] = 2
		previous[i] = 1
	}
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_ID", "cursor-v2")
	t.Setenv("STATE_REGISTRY_CURSOR_KEY_HEX", hex.EncodeToString(active))
	t.Setenv("STATE_REGISTRY_CURSOR_PREVIOUS_KEYS_JSON", `{"cursor-v1":"`+hex.EncodeToString(previous)+`"}`)

	keyring, err := cursorKeyringForBoot()
	if err != nil {
		t.Fatalf("cursorKeyringForBoot: %v", err)
	}
	if keyring.ActiveKeyID() != "cursor-v2" || len(keyring.Keys()) != 2 {
		t.Fatalf("keyring active=%q keys=%d", keyring.ActiveKeyID(), len(keyring.Keys()))
	}
}

func TestPostgresURLWithTLSRejectsUnsafeURLsBeforeOpen(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "servicefile", url: "postgresql://user:pass@db.internal:5432/registry?servicefile=/tmp/pgservice.ini"},
		{name: "passfile", url: "postgresql://user:pass@db.internal:5432/registry?PaSsFiLe=/tmp/pgpass"},
		{name: "non-postgres scheme", url: "mysql://user:pass@db.internal:3306/registry"},
		{name: "missing host", url: "postgresql:///registry"},
	}
	for _, mode := range []struct {
		name     string
		testMode bool
		tlsMode  string
	}{
		{name: "test mode", testMode: true},
		{name: "production TLS mode", tlsMode: "verify-full"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					_, err := postgresURLWithTLS(config.Config{
						TestMode:        mode.testMode,
						PostgresURL:     tc.url,
						PostgresTLSMode: mode.tlsMode,
						PostgresTLSCA:   "/run/secrets/postgres-ca.pem",
					})
					if err == nil {
						t.Fatalf("postgresURLWithTLS accepted unsafe URL")
					}
				})
			}
		})
	}
}

func TestPostgresURLWithTLSOverridesInsecureMode(t *testing.T) {
	got, err := postgresURLWithTLS(config.Config{
		PostgresURL:     "postgresql://user:pass@db.internal:5432/registry?sslmode=disable&application_name=flowai",
		PostgresTLSCA:   "/run/secrets/postgres-ca.pem",
		PostgresTLSMode: "verify-full",
	})
	if err != nil {
		t.Fatalf("postgresURLWithTLS: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.Query().Get("sslmode") != "verify-full" {
		t.Fatalf("sslmode=%q, want verify-full", parsed.Query().Get("sslmode"))
	}
	if parsed.Query().Get("sslrootcert") != "/run/secrets/postgres-ca.pem" {
		t.Fatalf("sslrootcert=%q", parsed.Query().Get("sslrootcert"))
	}
	if parsed.Query().Get("application_name") != "flowai" {
		t.Fatalf("application_name=%q, want preserved", parsed.Query().Get("application_name"))
	}
}

// TestConfigLoadTestModeDefaultIsFalse asserts the canonical
// production default. The TestMode=true harness cases live in
// main_harness_test.go behind the state_registry_test_harness tag.
func TestConfigLoadTestModeDefaultIsFalse(t *testing.T) {
	ca := writeConfigFile(t, t.TempDir(), "ca.crt")
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomAESKeyHex(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	t.Setenv("STATE_REGISTRY_TEST_MODE", "")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomAESKeyHex(t))
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.TestMode {
		t.Fatal("cfg.TestMode=true, want false when STATE_REGISTRY_TEST_MODE is unset/empty")
	}
}

// TestMode=true harness cases live in main_harness_test.go behind
// the state_registry_test_harness tag.

// TestHTTPListenerHasNoPeerIdentityMiddleware exercises the
// v0009 invariant: the State Registry exposes its backend API
// over plain HTTP with no peer-certificate or service-identity
// middleware. We assert this at the package surface by reading
// the runtime config and confirming the legacy TLS fields, when
// present, do not produce a TLS listener and never fail the boot
// because their paths are unreadable or missing.
func TestHTTPListenerHasNoPeerIdentityMiddleware(t *testing.T) {
	ca := writeConfigFile(t, t.TempDir(), "ca.crt")
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomAESKeyHex(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomAESKeyHex(t))
	// Reference nonexistent paths on purpose; Load() must never
	// validate them.
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", "/nonexistent/legacy-cert.pem")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", "/nonexistent/legacy-key.pem")
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", "/nonexistent/legacy-ca.pem")
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v (legacy keys must be ignored)", err)
	}
	if len(cfg.LegacyTLSIgnoredKeys) == 0 {
		t.Fatalf("expected legacy keys to be recorded as ignored, got none")
	}
}

// writeConfigFile writes a small regular file with the requested
// mode so the Postgres CA existence check accepts it.
func writeConfigFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
