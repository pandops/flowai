// Package config loads configuration for the State Registry service.
// The State Registry is intentionally minimal at this scaffold step:
// it owns only the liveness/readiness contract and AES-256-GCM key
// presence check needed by the autotest harness. Real business
// configuration lands under the protected behavior slices.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const MaxValidPort = 65535

// Config is the resolved runtime configuration for the State Registry
// scaffold. Values are loaded from environment variables with the
// documented prefix. The ServiceName is fixed by package and used for
// logging, probe responses, and the bind host.
type Config struct {
	ServiceName string
	BindHost    string
	BindPort    int
	PostgresURL string
	AESKey      []byte
}

// EnvPrefix is the prefix every environment variable must use.
const EnvPrefix = "STATE_REGISTRY_"

// Load reads configuration from environment variables and validates it.
// AESKey must decode to exactly 32 bytes (AES-256-GCM). The 32-byte
// material is never logged and is wrapped behind the AESKey accessor
// so callers cannot accidentally pass it to a logging field.
func Load() (Config, error) {
	cfg := Config{
		ServiceName: "state-registry",
		BindHost:    getEnv(EnvPrefix+"BIND_HOST", "127.0.0.1"),
		BindPort:    getEnvInt(EnvPrefix+"BIND_PORT", 18443),
		PostgresURL: getEnv(EnvPrefix+"POSTGRES_URL", ""),
	}

	if cfg.BindPort < 0 || cfg.BindPort > MaxValidPort {
		return cfg, fmt.Errorf("STATE_REGISTRY_BIND_PORT=%d is invalid; expected 0 (OS-assigned) or 1..%d", cfg.BindPort, MaxValidPort)
	}

	keyHex := getEnv(EnvPrefix+"AES_KEY_HEX", "")
	if keyHex == "" {
		return cfg, errors.New("STATE_REGISTRY_AES_KEY_HEX is required (32-byte hex-encoded AES-256-GCM key)")
	}
	key, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil {
		return cfg, fmt.Errorf("decode STATE_REGISTRY_AES_KEY_HEX: %w", err)
	}
	if len(key) != 32 {
		return cfg, fmt.Errorf("STATE_REGISTRY_AES_KEY_HEX must decode to exactly 32 bytes, got %d", len(key))
	}
	cfg.AESKey = key

	if cfg.PostgresURL == "" {
		return cfg, errors.New("STATE_REGISTRY_POSTGRES_URL is required (postgresql:// connection string)")
	}
	return cfg, nil
}

// BindAddress returns the host:port string used by the HTTP server.
func (c Config) BindAddress() string {
	return fmt.Sprintf("%s:%d", c.BindHost, c.BindPort)
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}
