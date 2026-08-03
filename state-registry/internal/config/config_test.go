package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testModeEnv = "STATE_REGISTRY_TEST_MODE"
)

func randomHexKey(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf)
}

// baseProductionTLSDir stages a complete mutually-authenticated TLS
// bundle in a temp dir. The server key is written with mode 0o600
// so the restrictive-permission check accepts the baseline.
func baseProductionTLSDir(t *testing.T) (cert, key, ca string) {
	t.Helper()
	dir := t.TempDir()
	cert = writeConfigFile(t, dir, "server.crt")
	key = writeConfigFile(t, dir, "server.key")
	ca = writeConfigFile(t, dir, "ca.crt")
	return cert, key, ca
}

func TestLoadRequiresAESKeyHex(t *testing.T) {
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", "")
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AES_KEY_HEX") {
		t.Fatalf("expected AES_KEY_HEX required error, got %v", err)
	}
}

func TestLoadRejectsShortKey(t *testing.T) {
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", hex.EncodeToString([]byte{1, 2, 3}))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "exactly 32 bytes") {
		t.Fatalf("expected exactly-32-bytes error, got %v", err)
	}
}

func TestLoadRejectsBadHex(t *testing.T) {
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", "zz")
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLoadRequiresPostgresURL(t *testing.T) {
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "POSTGRES_URL") {
		t.Fatalf("expected POSTGRES_URL required error, got %v", err)
	}
}

func TestLoadAcceptsValidConfig(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	keyHex := randomHexKey(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", keyHex)
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://u:p@host:5432/db?sslmode=disable")
	t.Setenv("STATE_REGISTRY_BIND_PORT", "18999")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHexKey(t))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AESKey) != 32 {
		t.Fatalf("AESKey length=%d want 32", len(cfg.AESKey))
	}
	if cfg.PostgresURL == "" {
		t.Fatalf("expected postgres url")
	}
	if cfg.BindPort != 18999 {
		t.Fatalf("BindPort=%d want 18999", cfg.BindPort)
	}
	if cfg.TestMode {
		t.Fatal("TestMode=true, want false in un-tagged production build")
	}
}

func TestBindAddress(t *testing.T) {
	cfg := Config{BindHost: "127.0.0.1", BindPort: 20001}
	if cfg.BindAddress() != "127.0.0.1:20001" {
		t.Fatalf("unexpected bind address %q", cfg.BindAddress())
	}
}

func TestBindAddressAllowsZero(t *testing.T) {
	cfg := Config{BindHost: "127.0.0.1", BindPort: 0}
	if cfg.BindAddress() != "127.0.0.1:0" {
		t.Fatalf("expected 127.0.0.1:0 to be preserved for OS-assigned port, got %q", cfg.BindAddress())
	}
}

func TestLoadRejectsInvalidBindPort(t *testing.T) {
	cases := map[string]string{
		"negative":  "-1",
		"too_large": "70000",
	}
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("STATE_REGISTRY_BIND_PORT", value)
			_, err := Load()
			if err == nil {
				t.Fatalf("expected invalid bind port %s to error", value)
			}
		})
	}
}

func TestLoadAcceptsBindPortZero(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_BIND_PORT", "0")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BindPort != 0 {
		t.Fatalf("BindPort=%d want 0 (OS-assigned)", cfg.BindPort)
	}
}

func TestLoadAcceptsCompleteTLSConfig(t *testing.T) {
	dir := t.TempDir()
	cert := writeConfigFile(t, dir, "server.crt")
	key := writeConfigFile(t, dir, "server.key")
	ca := writeConfigFile(t, dir, "ca.crt")
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHexKey(t))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TLSServerCert != cert || cfg.TLSServerKey != key || cfg.TLSClientCA != ca {
		t.Fatalf("unexpected server TLS config: %+v", cfg)
	}
	if !cfg.TLSRequireClientCert {
		t.Fatal("TLSRequireClientCert=false, want true")
	}
	if cfg.PostgresTLSCA != ca || cfg.PostgresTLSMode != "verify-full" {
		t.Fatalf("unexpected postgres TLS config: %+v", cfg)
	}
}

func TestLoadRejectsIncompleteTLSConfig(t *testing.T) {
	dir := t.TempDir()
	cert := writeConfigFile(t, dir, "server.crt")
	key := writeConfigFile(t, dir, "server.key")
	ca := writeConfigFile(t, dir, "ca.crt")
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "server key missing",
			env: map[string]string{
				"STATE_REGISTRY_TLS_SERVER_CERT":         cert,
				"STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT": "true",
				"STATE_REGISTRY_TLS_CLIENT_CA":           ca,
				"STATE_REGISTRY_POSTGRES_TLS_CA":         ca,
				"STATE_REGISTRY_POSTGRES_TLS_MODE":       "verify-full",
			},
			want: "TLS_SERVER_KEY",
		},
		{
			name: "required client cert without CA",
			env: map[string]string{
				"STATE_REGISTRY_TLS_SERVER_CERT":         cert,
				"STATE_REGISTRY_TLS_SERVER_KEY":          key,
				"STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT": "true",
				"STATE_REGISTRY_POSTGRES_TLS_CA":         ca,
				"STATE_REGISTRY_POSTGRES_TLS_MODE":       "verify-full",
			},
			want: "TLS_CLIENT_CA",
		},
		{
			name: "verify full without postgres CA",
			env: map[string]string{
				"STATE_REGISTRY_TLS_SERVER_CERT":         cert,
				"STATE_REGISTRY_TLS_SERVER_KEY":          key,
				"STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT": "true",
				"STATE_REGISTRY_TLS_CLIENT_CA":           ca,
				"STATE_REGISTRY_POSTGRES_TLS_MODE":       "verify-full",
			},
			want: "POSTGRES_TLS_CA",
		},
		{
			name: "unknown postgres TLS mode",
			env: map[string]string{
				"STATE_REGISTRY_TLS_SERVER_CERT":         cert,
				"STATE_REGISTRY_TLS_SERVER_KEY":          key,
				"STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT": "true",
				"STATE_REGISTRY_TLS_CLIENT_CA":           ca,
				"STATE_REGISTRY_POSTGRES_TLS_CA":         ca,
				"STATE_REGISTRY_POSTGRES_TLS_MODE":       "trust-me",
			},
			want: "POSTGRES_TLS_MODE",
		},
		{
			name: "missing configured CA file",
			env: map[string]string{
				"STATE_REGISTRY_TLS_SERVER_CERT":         cert,
				"STATE_REGISTRY_TLS_SERVER_KEY":          key,
				"STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT": "true",
				"STATE_REGISTRY_TLS_CLIENT_CA":           ca,
				"STATE_REGISTRY_POSTGRES_TLS_CA":         filepath.Join(dir, "missing.crt"),
				"STATE_REGISTRY_POSTGRES_TLS_MODE":       "verify-full",
			},
			want: "POSTGRES_TLS_CA",
		},
		{
			name: "valid CA baseline",
			env: map[string]string{
				"STATE_REGISTRY_TLS_SERVER_CERT":         cert,
				"STATE_REGISTRY_TLS_SERVER_KEY":          key,
				"STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT": "true",
				"STATE_REGISTRY_TLS_CLIENT_CA":           ca,
				"STATE_REGISTRY_POSTGRES_TLS_CA":         ca,
				"STATE_REGISTRY_POSTGRES_TLS_MODE":       "verify-full",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
			t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
			t.Setenv(testModeEnv, "")
			for _, key := range []string{
				"STATE_REGISTRY_TLS_SERVER_CERT", "STATE_REGISTRY_TLS_SERVER_KEY",
				"STATE_REGISTRY_TLS_CLIENT_CA", "STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT",
				"STATE_REGISTRY_POSTGRES_TLS_CA", "STATE_REGISTRY_POSTGRES_TLS_MODE",
			} {
				t.Setenv(key, "")
			}
			t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHexKey(t))
			t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_ID", "")
			t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_PREVIOUS_KEYS_JSON", "")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			_, err := Load()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want containing %q", err, tc.want)
			}
		})
	}
}

func writeConfigFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// writeConfigFileAs writes a regular file and then enforces the
// requested mode via chmod so the test does not depend on the
// process umask. Used by the private-key permission tests to stage
// a server key with deliberately loose permissions.
func writeConfigFileAs(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("test"), mode); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
	return path
}

// setProductionEnv clears every TLS env var so each
// production-transport test starts from a known blank slate.
func setProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv(testModeEnv, "")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", "")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", "")
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", "")
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", "")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_ID", "")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_PREVIOUS_KEYS_JSON", "")
}

// The un-tagged-only STATE_REGISTRY_TEST_MODE rejection tests
// (TestLoadRejectsNonEmptyTestModeEnv, TestLoadLeavesTestModeFalseWhenEnvUnset,
// TestLoadRejectsInvalidTestModeValueInProduction) live in
// config_test_unharness.go and run only when
// state_registry_test_harness is not set. The harness-only
// counterparts that exercise STATE_REGISTRY_TEST_MODE=true live in
// config_test_harness.go.

func TestLoadRejectsPlainHTTPInProduction(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted plain HTTP in production, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "TLS_SERVER_CERT") && !strings.Contains(msg, "mutually-authenticated") {
		t.Fatalf("error=%v, want mention of TLS_SERVER_CERT or mutually-authenticated", err)
	}
}

func TestLoadRequiresServerCertInProduction(t *testing.T) {
	_, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TLS_SERVER_CERT") {
		t.Fatalf("error=%v, want mention of TLS_SERVER_CERT", err)
	}
}

func TestLoadRequiresServerKeyInProduction(t *testing.T) {
	cert, _, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TLS_SERVER_KEY") {
		t.Fatalf("error=%v, want mention of TLS_SERVER_KEY", err)
	}
}

func TestLoadRequiresClientCAInProduction(t *testing.T) {
	cert, key, _ := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", writeConfigFile(t, t.TempDir(), "ca.crt"))
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TLS_CLIENT_CA") {
		t.Fatalf("error=%v, want mention of TLS_CLIENT_CA", err)
	}
}

func TestLoadRequiresClientCertInProduction(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "false")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted TLS_REQUIRE_CLIENT_CERT=false in production, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "TLS_REQUIRE_CLIENT_CERT") && !strings.Contains(msg, "mutually-authenticated") {
		t.Fatalf("error=%v, want mention of TLS_REQUIRE_CLIENT_CERT or mutually-authenticated", err)
	}
}

func TestLoadRejectsPostgresPlaintextInProduction(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted plaintext postgres in production, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "POSTGRES_TLS_CA") && !strings.Contains(msg, "POSTGRES_TLS_MODE") && !strings.Contains(msg, "verify-full") && !strings.Contains(msg, "PostgreSQL TLS") {
		t.Fatalf("error=%v, want mention of POSTGRES_TLS_CA / POSTGRES_TLS_MODE / verify-full / PostgreSQL TLS", err)
	}
}

func TestLoadRequiresPostgresCAInProduction(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "POSTGRES_TLS_CA") {
		t.Fatalf("error=%v, want mention of POSTGRES_TLS_CA", err)
	}
}

func TestLoadRequiresPostgresModeInProduction(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted missing POSTGRES_TLS_MODE in production, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "POSTGRES_TLS_MODE") && !strings.Contains(msg, "verify-full") {
		t.Fatalf("error=%v, want mention of POSTGRES_TLS_MODE / verify-full", err)
	}
}

func TestLoadRejectsVerifyCAInProduction(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-ca")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "verify-ca") {
		t.Fatalf("error=%v, want mention of verify-ca", err)
	}
}

// TestLoadRejectsVerifyCAInTestMode, TestLoadRejectsPartialServerTLSEvenInTestMode,
// and TestLoadRejectsPartialPostgresTLSEvenInTestMode live in the
// harness-tagged config_test_harness.go because they require
// STATE_REGISTRY_TEST_MODE=true, which the un-tagged build rejects.

func TestLoadAcceptsCompleteTLSInProduction(t *testing.T) {
	cert, key, ca := baseProductionTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TestMode {
		t.Fatal("TestMode=true, want false in production")
	}
	if !cfg.TLSRequireClientCert {
		t.Fatal("TLSRequireClientCert=false, want true")
	}
	if cfg.PostgresTLSMode != "verify-full" {
		t.Fatalf("PostgresTLSMode=%q, want verify-full", cfg.PostgresTLSMode)
	}
}

func TestLoadRejectsWorldReadableServerKey(t *testing.T) {
	cert := writeConfigFile(t, t.TempDir(), "server.crt")
	ca := writeConfigFile(t, t.TempDir(), "ca.crt")
	dir := t.TempDir()
	looseKey := writeConfigFileAs(t, dir, "server.key", 0o644)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", looseKey)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted world-readable server key, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "TLS_SERVER_KEY") && !strings.Contains(msg, "permission") {
		t.Fatalf("error=%v, want mention of TLS_SERVER_KEY or permission", err)
	}
}

func TestLoadRejectsGroupReadableServerKey(t *testing.T) {
	cert := writeConfigFile(t, t.TempDir(), "server.crt")
	ca := writeConfigFile(t, t.TempDir(), "ca.crt")
	dir := t.TempDir()
	looseKey := writeConfigFileAs(t, dir, "server.key", 0o640)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", looseKey)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted group-readable server key, want error")
	}
	if !strings.Contains(err.Error(), "TLS_SERVER_KEY") {
		t.Fatalf("error=%v, want mention of TLS_SERVER_KEY", err)
	}
}

func TestLoadAcceptsRestrictiveServerKey(t *testing.T) {
	cert := writeConfigFile(t, t.TempDir(), "server.crt")
	key := writeConfigFileAs(t, t.TempDir(), "server.key", 0o600)
	ca := writeConfigFile(t, t.TempDir(), "ca.crt")
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
