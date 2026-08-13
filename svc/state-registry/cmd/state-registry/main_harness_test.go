// Harness-only main_test.go cases. The state_registry_test_harness
// build tag is the only path that opts into
// STATE_REGISTRY_TEST_MODE=true; this file is only compiled when
// that tag is set.

//go:build state_registry_test_harness

package main

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/flowai/platform/svc/state-registry/internal/config"
)

func randomHarnessAESKeyHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf)
}

func TestPostgresURLHarnessReturnsOriginalForPlaintext(t *testing.T) {
	cfg := config.Config{
		TestMode:    true,
		PostgresURL: "postgresql://u:p@db:5432/r?sslmode=disable",
	}
	got, err := postgresURLWithTLS(cfg)
	if err != nil {
		t.Fatalf("postgresURLWithTLS: %v", err)
	}
	if got != cfg.PostgresURL {
		t.Fatalf("postgresURLWithTLS returned %q, want original %q", got, cfg.PostgresURL)
	}
}

// TestConfigLoadHarnessPropagatesTestMode asserts that with the
// state_registry_test_harness build tag, STATE_REGISTRY_TEST_MODE=true
// is honoured end-to-end. The State Registry still does NOT
// terminate backend HTTP TLS in harness test mode; only the
// header-trusting flag is set.
func TestConfigLoadHarnessPropagatesTestMode(t *testing.T) {
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHarnessAESKeyHex(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TEST_MODE", "true")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHarnessAESKeyHex(t))
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.TestMode {
		t.Fatal("cfg.TestMode=false under harness tag, want true")
	}
	if len(cfg.LegacyTLSIgnoredKeys) != 0 {
		t.Fatalf("legacy TLS keys recorded without being set: %+v", cfg.LegacyTLSIgnoredKeys)
	}
}
