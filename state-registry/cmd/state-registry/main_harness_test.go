// Harness-only main_test.go cases. The state_registry_test_harness
// build tag is the only path that opts into
// STATE_REGISTRY_TEST_MODE=true; this file is only compiled when
// that tag is set.
//
//go:build state_registry_test_harness

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flowai/platform/state-registry/internal/config"
)

func TestServerTLSConfigHarnessReturnsNilForPlaintext(t *testing.T) {
	cfg := config.Config{TestMode: true}
	tlsConfig, err := serverTLSConfig(cfg)
	if err != nil {
		t.Fatalf("serverTLSConfig: %v", err)
	}
	if tlsConfig != nil {
		t.Fatalf("serverTLSConfig returned non-nil for plaintext test mode, want nil")
	}
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
// is honoured end-to-end.
func TestConfigLoadHarnessPropagatesTestMode(t *testing.T) {
	cert, key, ca := writeHarnessTLSMaterial(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomHarnessAESKeyHex(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_CA", ca)
	t.Setenv("STATE_REGISTRY_POSTGRES_TLS_MODE", "verify-full")
	t.Setenv("STATE_REGISTRY_TEST_MODE", "true")
	t.Setenv("STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX", randomHarnessAESKeyHex(t))
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.TestMode {
		t.Fatal("cfg.TestMode=false under harness tag, want true")
	}
}

func writeHarnessTLSMaterial(t *testing.T) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:        true, BasicConstraintsValid: true,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, certPath
}

func randomHarnessAESKeyHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf)
}
