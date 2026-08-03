// mTLS helper for the v0002 State Registry client.
//
// Production Executor connections to State Registry use mutually
// authenticated TLS identities (AGENTS.md + v0002 OpenSpec). NewMTLSClient
// builds the *http.Client that the Executor injects into
// stateregistryclient.New; it loads the operator-supplied PEM
// artefacts at registration time, then fails closed on any
// parse-or-load error so the registration transaction aborts before
// the first Registry call.
//
// The helper is narrowly scoped:
//   - only stdlib crypto/tls (per the task contract);
//   - pins MinVersion to TLS 1.2 (the production floor);
//   - leaves InsecureSkipVerify = false so the server chain is
//     always verified against the supplied CA bundle;
//   - keeps the documented 30s call timeout so wire behaviour is
//     unchanged from the legacy plaintext fallback (which was the
//     standard library default in New).
//
// Exposed only because the Executor needs to construct one at startup;
// this file is NOT a generic TLS helper. Callers MUST validate
// state_registry_url is https and the three material paths are
// non-empty before invoking this helper. The Executor::Validate
// helper enforces that gate so production code paths cannot reach
// NewMTLSClient with empty inputs.

package stateregistryclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// mtlsClientTimeout matches the timeout the legacy plaintext
// httpClient default applied inside New when httpClient == nil. The
// production wire contract documented the 30s call budget before
// the mTLS slice landed; the mTLS helper preserves it so the v0002
// discovery/claim envelopes never get a longer budget than the
// legacy code path.
const mtlsClientTimeout = 30 * time.Second

// MTLSClientConfig carries the three operator-supplied PEM material
// paths the Executor reads at registration. Each path MUST point to
// a PEM-encoded file on disk; the helper reads each file once and
// returns an error on any read or parse failure.
type MTLSClientConfig struct {
	ServerCAPath   string
	ClientCertPath string
	ClientKeyPath  string
}

// NewMTLSClient builds a production-ready mTLS *http.Client rooted
// at the operator's State Registry identity. The returned client
// always verifies the server chain (InsecureSkipVerify is
// permanently false) and pins TLS 1.2 as the minimum negotiated
// version. Any failure to read or parse the operator-supplied
// material surfaces as a non-nil error and a nil client so the
// caller's Validate path can refuse to start the runtime before any
// HTTP dial.
func NewMTLSClient(cfg MTLSClientConfig) (*http.Client, error) {
	if cfg.ServerCAPath == "" || cfg.ClientCertPath == "" || cfg.ClientKeyPath == "" {
		return nil, errors.New("stateregistryclient: mTLS material paths are required")
	}
	clientCert, err := tls.LoadX509KeyPair(cfg.ClientCertPath, cfg.ClientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("stateregistryclient: load client keypair: %w", err)
	}
	if err := validateClientCert(clientCert); err != nil {
		return nil, fmt.Errorf("stateregistryclient: invalid client keypair: %w", err)
	}
	caPool, err := loadServerCAPool(cfg.ServerCAPath)
	if err != nil {
		return nil, fmt.Errorf("stateregistryclient: load server CA: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}
	return &http.Client{
		Timeout: mtlsClientTimeout,
		Transport: &http.Transport{
			TLSClientConfig:       tlsCfg,
			ForceAttemptHTTP2:     true,
			DisableCompression:    true,
			ResponseHeaderTimeout: mtlsClientTimeout,
		},
	}, nil
}

// validateClientCert rejects a parsed keypair that does not match
// the production contract: the leaf must be parseable as X.509 and
// must advertise the clientAuth extended key usage. Without the
// clientAuth EKU, the State Registry's mTLS handshake fails closed
// at the server side; surfacing the same check here lets the
// Executor fail fast at registration.
func validateClientCert(cert tls.Certificate) error {
	if cert.Leaf == nil {
		// tls.LoadX509KeyPair does not always populate Leaf for
		// raw-keypair files; force a parse so the operator gets a
		// precise error before the first dial.
		if len(cert.Certificate) == 0 {
			return errors.New("client certificate chain is empty")
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse client certificate: %w", err)
		}
		cert.Leaf = leaf
	}
	hasClientAuth := false
	for _, eku := range cert.Leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
			break
		}
	}
	if !hasClientAuth {
		if len(cert.Leaf.ExtKeyUsage) == 0 {
			return errors.New("client certificate is missing the clientAuth EKU")
		}
		return errors.New("client certificate is missing the clientAuth EKU")
	}
	return nil
}

// loadServerCAPool reads the operator-supplied PEM bundle and
// appends every certificate inside it to a fresh x509.CertPool.
// One PEM-encoded CA bundle per file is the documented contract;
// multi-file bundles are out of scope and rejected so operators
// do not silently trust an unintended issuer.
func loadServerCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("no certificates parsed from CA bundle")
	}
	return pool, nil
}
