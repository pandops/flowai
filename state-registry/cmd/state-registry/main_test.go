package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flowai/platform/state-registry/internal/config"
)

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

func TestServerTLSConfigRequiresAndVerifiesClientCertificates(t *testing.T) {
	certPath, keyPath, caPath := writeTestTLSMaterial(t)
	tlsConfig, err := serverTLSConfig(config.Config{
		TLSServerCert: certPath, TLSServerKey: keyPath, TLSClientCA: caPath,
		TLSRequireClientCert: true,
	})
	if err != nil {
		t.Fatalf("serverTLSConfig: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("serverTLSConfig returned nil")
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth=%v, want RequireAndVerifyClientCert", tlsConfig.ClientAuth)
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion=%d, want TLS 1.2", tlsConfig.MinVersion)
	}
	if len(tlsConfig.Certificates) != 1 || tlsConfig.ClientCAs == nil {
		t.Fatal("server certificate or client CA pool missing")
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

func writeTestTLSMaterial(t *testing.T) (string, string, string) {
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

// TestConfigLoadTestModeDefaultIsFalse asserts the canonical
// production default. The TestMode=true harness cases live in
// main_harness_test.go behind the state_registry_test_harness tag.
func TestConfigLoadTestModeDefaultIsFalse(t *testing.T) {
	cert, key, ca := writeTestTLSMaterial(t)
	t.Setenv("STATE_REGISTRY_AES_KEY_HEX", randomAESKeyHex(t))
	t.Setenv("STATE_REGISTRY_POSTGRES_URL", "postgresql://example/db")
	t.Setenv("STATE_REGISTRY_TLS_SERVER_CERT", cert)
	t.Setenv("STATE_REGISTRY_TLS_SERVER_KEY", key)
	t.Setenv("STATE_REGISTRY_TLS_CLIENT_CA", ca)
	t.Setenv("STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT", "true")
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

// TestWithPeerIdentityStripsCallerHeadersBeforeMapping exercises
// the security-critical invariant: caller-supplied identity
// headers MUST never widen a request's authority. The middleware
// strips every X-FlowAI-* identity header before mapping the
// verified peer certificate subject; any header that survives the
// middleware must come from the verified cert subject, not the
// caller's pre-middleware value.
func TestWithPeerIdentityStripsCallerHeadersBeforeMapping(t *testing.T) {
	forged := "forged-value"
	all := []string{
		"X-FlowAI-Role", "X-FlowAI-Team-Id", "X-FlowAI-Executor-Id",
		"X-FlowAI-Listener-Identity", "X-FlowAI-Source-System-Id",
		"X-FlowAI-Operator-Id", "X-FlowAI-Admin-Subject",
	}
	var observed http.Header
	handler := withPeerIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = r.Header.Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/livez", nil)
	for _, k := range all {
		req.Header.Set(k, forged)
	}
	cert := &x509.Certificate{
		Subject: pkix.Name{
			OrganizationalUnit: []string{"admin"},
			CommonName:         "admin-cn",
		},
	}
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Cert-derived values must be present (parser wins over caller).
	if got := observed.Get("X-FlowAI-Role"); got != "admin" {
		t.Errorf("X-FlowAI-Role=%q, want admin (parser-derived)", got)
	}
	if got := observed.Get("X-FlowAI-Admin-Subject"); got != "admin-cn" {
		t.Errorf("X-FlowAI-Admin-Subject=%q, want admin-cn", got)
	}
	// Every other header must have been stripped (no team, no
	// executor, no listener identity, no source-system, no
	// operator — the admin role is team-less).
	for _, k := range []string{
		"X-FlowAI-Team-Id", "X-FlowAI-Executor-Id",
		"X-FlowAI-Listener-Identity", "X-FlowAI-Source-System-Id",
		"X-FlowAI-Operator-Id",
	} {
		if got := observed.Get(k); got != "" {
			t.Errorf("header %q=%q, want empty (admin role is team-less)", k, got)
		}
	}
	// The forged caller value MUST NOT survive on any header the
	// parser doesn't claim.
	for _, k := range all {
		if observed.Get(k) == forged {
			t.Errorf("header %q still equals the forged caller value; caller headers must be stripped before cert mapping", k)
		}
	}
}

// TestWithPeerIdentityRejectsAmbiguousCertShape asserts that the
// strict peerauth parser drops ambiguous cert shapes without
// promoting any X-FlowAI-* identity header. Each fixture targets
// one documented reject rule.
func TestWithPeerIdentityRejectsAmbiguousCertShape(t *testing.T) {
	cases := []struct {
		name string
		subj pkix.Name
	}{
		{
			name: "no OU",
			subj: pkix.Name{CommonName: "cn"},
		},
		{
			name: "multiple OUs",
			subj: pkix.Name{
				OrganizationalUnit: []string{"listener", "admin"},
				CommonName:         "cn",
				Organization:       []string{"team-a"},
				SerialNumber:       "src-a",
			},
		},
		{
			name: "unknown OU role",
			subj: pkix.Name{
				OrganizationalUnit: []string{"wrong-role"},
				CommonName:         "cn",
				Organization:       []string{"team-a"},
				SerialNumber:       "src-a",
			},
		},
		{
			name: "blank CN",
			subj: pkix.Name{
				OrganizationalUnit: []string{"listener"},
				Organization:       []string{"team-a"},
				SerialNumber:       "src-a",
			},
		},
		{
			name: "multiple Organizations",
			subj: pkix.Name{
				OrganizationalUnit: []string{"listener"},
				Organization:       []string{"team-a", "team-b"},
				CommonName:         "cn",
				SerialNumber:       "src-a",
			},
		},
		{
			name: "listener missing team",
			subj: pkix.Name{
				OrganizationalUnit: []string{"listener"},
				CommonName:         "cn",
				SerialNumber:       "src-a",
			},
		},
		{
			name: "listener missing serial",
			subj: pkix.Name{
				OrganizationalUnit: []string{"listener"},
				Organization:       []string{"team-a"},
				CommonName:         "cn",
			},
		},
		{
			name: "gateway carries source-system serial",
			subj: pkix.Name{
				OrganizationalUnit: []string{"gateway"},
				Organization:       []string{"team-a"},
				CommonName:         "cn",
				SerialNumber:       "src-a",
			},
		},
		{
			name: "admin carries team",
			subj: pkix.Name{
				OrganizationalUnit: []string{"admin"},
				Organization:       []string{"team-a"},
				CommonName:         "cn",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var observed http.Header
			handler := withPeerIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				observed = r.Header.Clone()
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/livez", nil)
			cert := &x509.Certificate{Subject: tc.subj}
			req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			for _, k := range []string{
				"X-FlowAI-Role", "X-FlowAI-Team-Id", "X-FlowAI-Executor-Id",
				"X-FlowAI-Listener-Identity", "X-FlowAI-Source-System-Id",
				"X-FlowAI-Operator-Id", "X-FlowAI-Admin-Subject",
			} {
				if got := observed.Get(k); got != "" {
					t.Fatalf("header %q=%q after ambiguous-cert middleware, want empty (strict parser must drop)", k, got)
				}
			}
		})
	}
}

// TestWithPeerIdentityMapsListenerWithSourceSystem exercises the
// listener path: the cert's Organization becomes X-FlowAI-Team-Id,
// the CommonName becomes X-FlowAI-Listener-Identity, and the
// Subject.SerialNumber becomes X-FlowAI-Source-System-Id so the
// downstream listener authorization boundary receives the
// source-system claim.
func TestWithPeerIdentityMapsListenerWithSourceSystem(t *testing.T) {
	var observed http.Header
	handler := withPeerIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = r.Header.Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks", nil)
	cert := &x509.Certificate{
		Subject: pkix.Name{
			OrganizationalUnit: []string{"listener"},
			Organization:       []string{"team-a"},
			CommonName:         "listener-team-a",
			SerialNumber:       "src-a",
		},
	}
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := observed.Get("X-FlowAI-Role"); got != "listener" {
		t.Errorf("X-FlowAI-Role=%q, want listener", got)
	}
	if got := observed.Get("X-FlowAI-Team-Id"); got != "team-a" {
		t.Errorf("X-FlowAI-Team-Id=%q, want team-a", got)
	}
	if got := observed.Get("X-FlowAI-Listener-Identity"); got != "listener-team-a" {
		t.Errorf("X-FlowAI-Listener-Identity=%q, want listener-team-a", got)
	}
	if got := observed.Get("X-FlowAI-Source-System-Id"); got != "src-a" {
		t.Errorf("X-FlowAI-Source-System-Id=%q, want src-a (subject.SerialNumber must map to source-system)", got)
	}
}

// TestWithPeerIdentityWithoutTLSDropsAllHeaders asserts that with
// no TLS connection the middleware never synthesizes identity
// headers — production security requires verified identity before
// any X-FlowAI-* header is set.
func TestWithPeerIdentityWithoutTLSDropsAllHeaders(t *testing.T) {
	var observed http.Header
	handler := withPeerIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = r.Header.Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/livez", nil)
	req.Header.Set("X-FlowAI-Role", "forged")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	for _, k := range []string{
		"X-FlowAI-Role", "X-FlowAI-Team-Id", "X-FlowAI-Executor-Id",
		"X-FlowAI-Listener-Identity", "X-FlowAI-Source-System-Id",
		"X-FlowAI-Operator-Id", "X-FlowAI-Admin-Subject",
	} {
		if got := observed.Get(k); got != "" {
			t.Errorf("header %q=%q with no TLS, want empty", k, got)
		}
	}
}

func randomAESKeyHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(buf)
}
