// Tests that exercise the un-tagged production contract: any
// non-empty STATE_REGISTRY_TEST_MODE value MUST be rejected with the
// build-tag message. The harness-only counterpart lives in
// config_test_harness.go.

//go:build !state_registry_test_harness

package config

import (
	"strings"
	"testing"
)

// TestLoadRejectsNonEmptyTestModeEnv pins the un-tagged build
// contract: a normal production binary refuses to opt into test
// mode via STATE_REGISTRY_TEST_MODE alone. The tag-gated
// alternative is exercised in config_test_harness.go.
func TestLoadRejectsNonEmptyTestModeEnv(t *testing.T) {
	cases := []string{"true", "1", "false", "0", "yes", "no", "not-a-bool", " TrUe ", " 0 "}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			setProductionEnv(t)
			t.Setenv(testModeEnv, value)
			t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
			t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
			_, err := Load()
			if err == nil {
				t.Fatalf("STATE_REGISTRY_TEST_MODE=%q accepted in un-tagged build; want error", value)
			}
			if !strings.Contains(err.Error(), "state_registry_test_harness") {
				t.Fatalf("error=%v, want mention of the build tag", err)
			}
		})
	}
}

// TestLoadLeavesTestModeFalseWhenEnvUnset asserts the canonical
// production default: STATE_REGISTRY_TEST_MODE unset yields
// TestMode=false and a normal Load() succeeds only when the
// production Postgres TLS contract is satisfied.
func TestLoadLeavesTestModeFalseWhenEnvUnset(t *testing.T) {
	ca := baseProductionPostgresTLSDir(t)
	setProductionEnv(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TestMode {
		t.Fatalf("TestMode=true in un-tagged production build, want false")
	}
}

// TestLoadRejectsInvalidTestModeValueInProduction asserts the
// un-tagged equivalent of the harness's ParseBool rejection:
// every non-empty value is rejected with the build-tag message.
func TestLoadRejectsInvalidTestModeValueInProduction(t *testing.T) {
	t.Setenv(testModeEnv, "not-a-bool")
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHexKey(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	_, err := Load()
	if err == nil {
		t.Fatalf("Load accepted non-empty TEST_MODE, want error")
	}
	if !strings.Contains(err.Error(), testModeEnv) {
		t.Fatalf("error=%v, want containing %q", err, testModeEnv)
	}
	if !strings.Contains(err.Error(), "state_registry_test_harness") {
		t.Fatalf("error=%v, want mention of the build tag", err)
	}
}
