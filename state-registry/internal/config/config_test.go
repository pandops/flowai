package config

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func randomHexKey(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf)
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
	key := randomHexKey(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", key)
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://u:p@host:5432/db?sslmode=disable")
	t.Setenv("STATE_REGISTRY_BIND_PORT", "18999")
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
