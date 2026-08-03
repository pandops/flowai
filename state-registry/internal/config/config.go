// Package config loads configuration for the State Registry. The
// STATE_REGISTRY_TEST_MODE env var is gated by the
// state_registry_test_harness build tag: a normal production binary
// rejects it; only a build with that tag opts into test mode.
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
// service. Values are loaded from environment variables with the
// documented prefix. The ServiceName is fixed by package and used for
// logging, probe responses, and the bind host.
//
// TestMode is true when STATE_REGISTRY_TEST_MODE=true is supplied.
// Test mode is the only path that allows plaintext HTTP and plaintext
// Postgres for the existing earlier-slice Playwright harness. In test
// mode, partial TLS still fails closed so a typo cannot silently drop
// one half of the transport.
type Config struct {
	ServiceName          string
	BindHost             string
	BindPort             int
	PostgresURL          string
	AESKey               []byte
	ScopeTokenKeyID      string
	ScopeTokenKey        []byte
	ScopeTokenAlgorithm  string
	ScopeTokenPrevious   string
	TestMode             bool
	TLSServerCert        string
	TLSServerKey         string
	TLSClientCA          string
	TLSRequireClientCert bool
	PostgresTLSCA        string
	PostgresTLSMode      string
}

// EnvPrefix is the prefix every environment variable must use.
const EnvPrefix = "STATE_REGISTRY_"

// Load reads configuration from environment variables and validates
// it. AESKey must decode to exactly 32 bytes (AES-256-GCM). The
// 32-byte material is never logged and is wrapped behind the AESKey
// accessor so callers cannot accidentally pass it to a logging field.
//
// In production (STATE_REGISTRY_TEST_MODE unset or false) the State
// Registry requires:
//
//   - STATE_REGISTRY_TLS_SERVER_CERT, STATE_REGISTRY_TLS_SERVER_KEY,
//     STATE_REGISTRY_TLS_CLIENT_CA, and
//     STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true for the HTTP
//     listener (mutually-authenticated TLS);
//   - STATE_REGISTRY_POSTGRES_TLS_CA and
//     STATE_REGISTRY_POSTGRES_TLS_MODE=verify-full for the Postgres
//     client (server-identity authentication).
//
// In test mode (STATE_REGISTRY_TEST_MODE=true) TLS is optional so the
// existing earlier-slice Playwright harness can run, but partial TLS
// still fails closed. POSTGRES_TLS_MODE=verify-ca is rejected in
// every mode because it only validates the certificate chain and
// accepts any cert the CA signed, which does not authenticate the
// server identity to the client process.
func Load() (Config, error) {
	cfg := Config{
		ServiceName: "state-registry",
		BindHost:    getEnv(EnvPrefix+"BIND_HOST", "127.0.0.1"),
		BindPort:    getEnvInt(EnvPrefix+"BIND_PORT", 18443),
		PostgresURL: getEnv(EnvPrefix+"POSTGRES_URL", ""),
	}
	// Test-mode opt-in is gated by the state_registry_test_harness
	// build tag. The un-tagged build of readTestModeFromEnv rejects
	// any non-empty value (after trim); the tagged build honours it.
	testModeRaw := os.Getenv(EnvPrefix + "TEST_MODE")
	testMode, err := readTestModeFromEnv(strings.TrimSpace(testModeRaw))
	if err != nil {
		return cfg, err
	}
	cfg.TestMode = testMode

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
	cfg.ScopeTokenKeyID = getEnv(EnvPrefix+"SCOPE_TOKEN_KEY_ID", "")
	cfg.ScopeTokenAlgorithm = strings.ToUpper(strings.TrimSpace(getEnv(EnvPrefix+"SCOPE_TOKEN_ALGORITHM", "HS256")))
	scopeKeyHex := getEnv(EnvPrefix+"SCOPE_TOKEN_KEY_HEX", "")
	if scopeKeyHex == "" {
		return cfg, errors.New("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX is required (HS256/HS384/HS512 HMAC key, hex-encoded)")
	}
	decodedKey, err := hex.DecodeString(strings.TrimSpace(scopeKeyHex))
	if err != nil {
		return cfg, fmt.Errorf("decode STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX: %w", err)
	}
	if len(decodedKey) < 32 {
		return cfg, fmt.Errorf("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX must decode to at least 32 bytes for HS256/HS384/HS512, got %d", len(decodedKey))
	}
	switch cfg.ScopeTokenAlgorithm {
	case "HS256", "HS384", "HS512":
	default:
		return cfg, fmt.Errorf("STATE_REGISTRY_SCOPE_TOKEN_ALGORITHM=%q is not in the HS256/HS384/HS512 allow-list", cfg.ScopeTokenAlgorithm)
	}
	cfg.ScopeTokenKey = decodedKey
	cfg.ScopeTokenPrevious = getEnv(EnvPrefix+"SCOPE_TOKEN_PREVIOUS_KEYS_JSON", "")
	if err := loadTLSConfig(&cfg, testMode); err != nil {
		return cfg, err
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

// loadTLSConfig enforces the State Registry transport contract:
//   - Production requires mutually-authenticated TLS for the HTTP
//     listener (server cert, server key, client CA,
//     TLS_REQUIRE_CLIENT_CERT=true) and verify-full for Postgres.
//   - Test mode allows the plaintext path the existing earlier-slice
//     Playwright harness relies on, but partial TLS still fails
//     closed.
//   - verify-ca is rejected in every mode; only verify-full is
//     permitted because verify-ca only checks the chain and accepts
//     any cert the CA signed, which does not authenticate the server
//     identity to the client process.
//   - The server private-key file must not have group or other
//     permission bits set so a leaked key is not readable by any
//     process outside the State Registry's owning user. Public
//     certificates and CA bundles are public material and only need
//     to be a regular file.
func loadTLSConfig(cfg *Config, testMode bool) error {
	cfg.TLSServerCert = getEnv(EnvPrefix+"TLS_SERVER_CERT", "")
	cfg.TLSServerKey = getEnv(EnvPrefix+"TLS_SERVER_KEY", "")
	cfg.TLSClientCA = getEnv(EnvPrefix+"TLS_CLIENT_CA", "")
	requireClientCert, err := getEnvBool(EnvPrefix+"TLS_REQUIRE_CLIENT_CERT", false)
	if err != nil {
		return err
	}
	cfg.TLSRequireClientCert = requireClientCert

	cfg.PostgresTLSCA = getEnv(EnvPrefix+"POSTGRES_TLS_CA", "")
	cfg.PostgresTLSMode = getEnv(EnvPrefix+"POSTGRES_TLS_MODE", "")

	serverTLSConfigured := cfg.TLSServerCert != "" ||
		cfg.TLSServerKey != "" ||
		cfg.TLSClientCA != "" ||
		cfg.TLSRequireClientCert
	postgresTLSConfigured := cfg.PostgresTLSCA != "" ||
		cfg.PostgresTLSMode != ""

	if testMode {
		// Test mode: plaintext is allowed (the existing earlier-slice
		// Playwright harness relies on it). Partial TLS still fails
		// closed so a typo cannot silently drop one half of the
		// transport.
		if serverTLSConfigured {
			if cfg.TLSServerCert == "" {
				return errors.New("STATE_REGISTRY_TLS_SERVER_CERT is required when TLS is configured")
			}
			if cfg.TLSServerKey == "" {
				return errors.New("STATE_REGISTRY_TLS_SERVER_KEY is required when TLS is configured")
			}
			if cfg.TLSRequireClientCert && cfg.TLSClientCA == "" {
				return errors.New("STATE_REGISTRY_TLS_CLIENT_CA is required when client certificates are required")
			}
		}
		if postgresTLSConfigured {
			if cfg.PostgresTLSMode == "" {
				return errors.New("STATE_REGISTRY_POSTGRES_TLS_MODE is required when PostgreSQL TLS is configured")
			}
			if cfg.PostgresTLSCA == "" {
				return errors.New("STATE_REGISTRY_POSTGRES_TLS_CA is required when PostgreSQL TLS verification is configured")
			}
		}
	} else {
		// Production: the State Registry MUST serve mutually
		// authenticated TLS and verify-full Postgres. There is no
		// fallback path; the bootstrap is fail-closed.
		if !serverTLSConfigured {
			return errors.New("production requires mutually-authenticated TLS: STATE_REGISTRY_TLS_SERVER_CERT, STATE_REGISTRY_TLS_SERVER_KEY, STATE_REGISTRY_TLS_CLIENT_CA, and STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true")
		}
		if cfg.TLSServerCert == "" {
			return errors.New("STATE_REGISTRY_TLS_SERVER_CERT is required for production")
		}
		if cfg.TLSServerKey == "" {
			return errors.New("STATE_REGISTRY_TLS_SERVER_KEY is required for production")
		}
		if cfg.TLSClientCA == "" {
			return errors.New("STATE_REGISTRY_TLS_CLIENT_CA is required for production")
		}
		if !cfg.TLSRequireClientCert {
			return errors.New("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true is required for production (mutually-authenticated TLS)")
		}
		if !postgresTLSConfigured {
			return errors.New("production requires PostgreSQL TLS: STATE_REGISTRY_POSTGRES_TLS_CA and STATE_REGISTRY_POSTGRES_TLS_MODE=verify-full")
		}
		if cfg.PostgresTLSCA == "" {
			return errors.New("STATE_REGISTRY_POSTGRES_TLS_CA is required for production")
		}
		if cfg.PostgresTLSMode != "verify-full" {
			return fmt.Errorf("STATE_REGISTRY_POSTGRES_TLS_MODE=%q is invalid for production; expected verify-full", cfg.PostgresTLSMode)
		}
	}

	// verify-ca is rejected in every mode.
	if cfg.PostgresTLSMode == "verify-ca" {
		return errors.New("STATE_REGISTRY_POSTGRES_TLS_MODE=verify-ca is not accepted; verify-ca only validates the certificate chain and does not authenticate the Postgres server identity, use verify-full")
	}
	if cfg.PostgresTLSMode != "" && cfg.PostgresTLSMode != "verify-full" {
		return fmt.Errorf("STATE_REGISTRY_POSTGRES_TLS_MODE=%q is invalid; expected verify-full", cfg.PostgresTLSMode)
	}

	// File-presence and permission checks. The private-key file must
	// not be readable by group or other. Public certificates and CA
	// bundles are public material and not subject to the
	// restrictive-permission check.
	if serverTLSConfigured {
		if err := requireRegularFile("STATE_REGISTRY_TLS_SERVER_CERT", cfg.TLSServerCert); err != nil {
			return err
		}
		if err := requireRestrictivePrivateKey("STATE_REGISTRY_TLS_SERVER_KEY", cfg.TLSServerKey); err != nil {
			return err
		}
		if cfg.TLSClientCA != "" {
			if err := requireRegularFile("STATE_REGISTRY_TLS_CLIENT_CA", cfg.TLSClientCA); err != nil {
				return err
			}
		}
	}
	if postgresTLSConfigured {
		if err := requireRegularFile("STATE_REGISTRY_POSTGRES_TLS_CA", cfg.PostgresTLSCA); err != nil {
			return err
		}
	}
	return nil
}

func getEnvBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func requireRegularFile(name, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s=%q is unavailable: %w", name, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s=%q is not a regular file", name, path)
	}
	return nil
}

// requireRestrictivePrivateKey rejects a private-key file whose
// group or other permission bits are set. The 0o077 mask covers
// group + other read, write, and execute; any of those bits leak
// the key to a process outside the State Registry's owning user.
// Public certificates and CA bundles are public material and are
// not subject to this check.
func requireRestrictivePrivateKey(name, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s=%q is unavailable: %w", name, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s=%q is not a regular file", name, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s=%q has group or other permission bits set (mode=%04o); private-key files MUST be readable only by the owning user", name, path, info.Mode().Perm())
	}
	return nil
}
