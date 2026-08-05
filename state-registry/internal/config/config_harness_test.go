// Test-mode-only config tests. The state_registry_test_harness
// build tag is the only path that opts into header-trusting test
// mode; this file is only compiled when that tag is set, so the
// STATE_REGISTRY_TEST_MODE env var exercises TestMode=true here.
//
// The un-tagged counterpart (config_test.go) rejects every non-empty
// STATE_REGISTRY_TEST_MODE value to fail-closed in production.

//go:build state_registry_test_harness

package config

import (
	"strings"
	"testing"
)

// setTestModeEnv clears TLS env vars and enables STATE_REGISTRY_TEST_MODE=true.
func setTestModeEnv(t *testing.T) {
	t.Helper()
	t.Setenv(testModeEnv, "true")
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

// TestHarnessParsesTestMode asserts that the
// state_registry_test_harness build honours STATE_REGISTRY_TEST_MODE
// as a boolean; the inverse case lives in TestLoadRejectsNonEmptyTestModeEnv
// in config_test.go.
func TestHarnessParsesTestMode(t *testing.T) {
	cases := map[string]struct {
		value string
		want  bool
	}{
		"unset_is_false": {value: "", want: false},
		"true_is_true":   {value: "true", want: true},
		"false_is_false": {value: "false", want: false},
		"one_is_true":    {value: "1", want: true},
		"zero_is_false":  {value: "0", want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setProductionEnv(t)
			t.Setenv(testModeEnv, tc.value)
			t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
			t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
			ca := baseProductionPostgresTLSDir(t)
			t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
			t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TestMode != tc.want {
				t.Fatalf("TestMode=%v, want %v", cfg.TestMode, tc.want)
			}
		})
	}
}

// TestHarnessAcceptsTestModePlaintext asserts the harness plaintext path.
func TestHarnessAcceptsTestModePlaintext(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TestMode {
		t.Fatal("TestMode=false, want true in harness build")
	}
	if cfg.PostgresTLSCA != "" || cfg.PostgresTLSMode != "" {
		t.Fatalf("expected no postgres TLS in test mode, got %+v", cfg)
	}
	if len(cfg.LegacyTLSIgnoredKeys) != 0 {
		t.Fatalf("expected no legacy TLS keys recorded in test mode, got %+v", cfg.LegacyTLSIgnoredKeys)
	}
}

// TestHarnessRejectsVerifyCA asserts that verify-ca is rejected in
// the harness build (it's rejected in every build for the reason
// stated in loadTLSConfig).
func TestHarnessRejectsVerifyCA(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", writeConfigFile(t, t.TempDir(), "ca.crt"))
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-ca")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "verify-ca") {
		t.Fatalf("error=%v, want mention of verify-ca", err)
	}
}

// TestHarnessRejectsPartialPostgresTLS asserts harness test-mode
// partial postgres TLS fails closed.
func TestHarnessRejectsPartialPostgresTLS(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "POSTGRES_TLS_CA") {
		t.Fatalf("error=%v, want mention of POSTGRES_TLS_CA (partial TLS must fail closed in test mode)", err)
	}
}

// TestHarnessInvalidTestModeValue exercises the strconv.ParseBool
// failure path inside the harness build.
func TestHarnessInvalidTestModeValue(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv(testModeEnv, "not-a-bool")
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), testModeEnv) {
		t.Fatalf("error=%v, want containing %q", err, testModeEnv)
	}
}

// TestHarnessAcceptsValidConfig mirrors the production test in
// config_test.go but enables test mode so the harness surface is
// not blocked by production-mode TLS.
func TestHarnessAcceptsValidConfig(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://u:p@host:5432/db?sslmode=disable")
	t.Setenv("STATE_REGISTRY_BIND_PORT", "18999")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BindPort != 18999 {
		t.Fatalf("BindPort=%d want 18999", cfg.BindPort)
	}
	if !cfg.TestMode {
		t.Fatal("TestMode=false, want true in harness build")
	}
}

// TestHarnessAcceptsBindPortZero mirrors the production test in
// config_test.go but with the harness env contract.
func TestHarnessAcceptsBindPortZero(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_BIND_PORT", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BindPort != 0 {
		t.Fatalf("BindPort=%d want 0 (OS-assigned)", cfg.BindPort)
	}
}

// TestHarnessIgnoresLegacyBackendTLSKeys asserts that legacy
// HTTP TLS/mTLS keys are accepted but ignored in the harness
// build (mirrors the production counterpart).
func TestHarnessIgnoresLegacyBackendTLSKeys(t *testing.T) {
	setTestModeEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", "/var/run/flowai/legacy-server-cert-does-not-exist.pem")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", "/var/run/flowai/legacy-server-key-does-not-exist.pem")
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", "/var/run/flowai/legacy-client-ca-does-not-exist.pem")
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v (legacy keys must be ignored, not validated)", err)
	}
	if len(cfg.LegacyTLSIgnoredKeys) != 4 {
		t.Fatalf("LegacyTLSIgnoredKeys=%v, want 4 entries", cfg.LegacyTLSIgnoredKeys)
	}
}
