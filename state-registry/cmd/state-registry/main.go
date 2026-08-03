// Command state-registry runs the State Registry HTTP server. It
// reads configuration from environment variables, opens a PostgreSQL
// connection, starts the readiness probe goroutine, and serves
// /v1/livez, /v1/readyz, and the implementation-active business
// routes. mTLS is mandatory in production startup; the verified
// peer certificate subject drives every X-FlowAI-* identity header
// (see withPeerIdentity) and the header-trusting test-mode opt-in
// (STATE_REGISTRY_TEST_MODE=true) is gated by the
// state_registry_test_harness build tag — a normal production
// binary rejects the env var outright.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/flowai/platform/state-registry/internal/config"
	"github.com/flowai/platform/state-registry/internal/health"
	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/logging"
	"github.com/flowai/platform/state-registry/internal/migrations"
	"github.com/flowai/platform/state-registry/internal/peerauth"
	"github.com/flowai/platform/state-registry/internal/store"
)

const serviceName = "state-registry"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "state-registry failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cursorKeyring, err := cursorKeyringForBoot()
	if err != nil {
		return fmt.Errorf("load cursor keyring: %w", err)
	}
	tlsConfig, err := serverTLSConfig(cfg)
	if err != nil {
		return fmt.Errorf("load server TLS: %w", err)
	}
	postgresURL, err := postgresURLWithTLS(cfg)
	if err != nil {
		return fmt.Errorf("load postgres TLS: %w", err)
	}

	logger := logging.New(logging.LevelInfo)

	listener, actualPort, err := bindLoopback(cfg.BindHost, cfg.BindPort)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	cfg.BindPort = actualPort
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}

	logger.Info("starting state-registry",
		"bind", cfg.BindAddress(),
	)

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	migrationCtx, cancelMigrations := context.WithTimeout(context.Background(), 30*time.Second)
	if err := migrations.ApplyUp(migrationCtx, db); err != nil {
		cancelMigrations()
		_ = listener.Close()
		return fmt.Errorf("apply postgres migrations: %w", err)
	}
	cancelMigrations()

	probe := health.NewPostgresProbe(health.NewPostgresPinger(db))
	checker := health.Compose(probe, cfg.AESKey)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	probeCtx, cancelProbe := context.WithCancel(ctx)
	defer cancelProbe()
	go probe.Run(probeCtx, 2*time.Second)

	ops := httpapi.NewDecryptOps()
	scopeKeyring, err := store.NewScopeTokenKeyring(cfg.ScopeTokenKeyID, scopeTokenKeyHex(&cfg), cfg.ScopeTokenPrevious, scopeTokenAlgorithm(&cfg))
	if err != nil {
		return fmt.Errorf("load scope token keyring: %w", err)
	}
	adminStore := store.NewWithScopeTokenKeyring(db, cfg.AESKey, scopeKeyring)
	handler := httpapi.RoutesWithKeyring(serviceName, "", logger, checker, ops, cursorKeyring, cfg.TestMode, adminStore)
	if tlsConfig != nil {
		handler = withPeerIdentity(handler)
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         tlsConfig,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("state-registry listening", "bind", cfg.BindAddress())
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("state-registry stopped")
	return nil
}

// bindLoopback opens a TCP listener on host. When port is 0 the OS
// assigns an ephemeral port and the actualPort return reflects it.
func bindLoopback(host string, port int) (net.Listener, int, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, 0, err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, 0, fmt.Errorf("listener address is not *net.TCPAddr: %T", listener.Addr())
	}
	return listener, tcpAddr.Port, nil
}

// cursorKeyringForBoot loads the active signing key and any previous
// verification keys. Configuration errors are returned to run so startup
// fails before binding a listener or mounting a partial business API.
func cursorKeyringForBoot() (*config.CursorKeyring, error) {
	return config.LoadRotatingCursorKeyring(
		os.Getenv("STATE_REGISTRY_CURSOR_KEY_ID"),
		os.Getenv("STATE_REGISTRY_CURSOR_KEY_HEX"),
		os.Getenv("STATE_REGISTRY_CURSOR_PREVIOUS_KEYS_JSON"),
	)
}

// scopeTokenKeyHex re-encodes the loaded AES-HMAC key bytes back
// to a hex string so the scope-token keyring can be loaded from the
// same validated bytes the AES path already uses. Returning the
// pointer keeps the keying material in one place.
func scopeTokenKeyHex(cfg *config.Config) string {
	return fmt.Sprintf("%x", cfg.ScopeTokenKey)
}

func scopeTokenAlgorithm(cfg *config.Config) store.ScopeTokenAlgorithm {
	switch cfg.ScopeTokenAlgorithm {
	case "HS384":
		return store.ScopeTokenAlgHS384
	case "HS512":
		return store.ScopeTokenAlgHS512
	default:
		return store.ScopeTokenAlgHS256
	}
}

func serverTLSConfig(cfg config.Config) (*tls.Config, error) {
	if cfg.TLSServerCert == "" && cfg.TLSServerKey == "" && cfg.TLSClientCA == "" {
		return nil, nil
	}
	certificate, err := tls.LoadX509KeyPair(cfg.TLSServerCert, cfg.TLSServerKey)
	if err != nil {
		return nil, fmt.Errorf("load STATE_REGISTRY_TLS_SERVER_CERT/STATE_REGISTRY_TLS_SERVER_KEY: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if cfg.TLSClientCA != "" {
		encoded, err := os.ReadFile(cfg.TLSClientCA)
		if err != nil {
			return nil, fmt.Errorf("read STATE_REGISTRY_TLS_CLIENT_CA: %w", err)
		}
		if !clientCAs.AppendCertsFromPEM(encoded) {
			return nil, errors.New("STATE_REGISTRY_TLS_CLIENT_CA contains no certificates")
		}
	}
	clientAuth := tls.VerifyClientCertIfGiven
	if cfg.TLSRequireClientCert {
		clientAuth = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    clientCAs,
		ClientAuth:   clientAuth,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func postgresURLWithTLS(cfg config.Config) (string, error) {
	parsed, err := validatePostgresNetworkURL(cfg.PostgresURL)
	if err != nil {
		return "", err
	}
	if cfg.PostgresTLSMode == "" {
		return cfg.PostgresURL, nil
	}
	query := parsed.Query()
	query.Set("sslmode", cfg.PostgresTLSMode)
	query.Set("sslrootcert", cfg.PostgresTLSCA)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func validatePostgresNetworkURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("STATE_REGISTRY_POSTGRES_URL must be a valid PostgreSQL network URL")
	}
	if !strings.EqualFold(parsed.Scheme, "postgres") && !strings.EqualFold(parsed.Scheme, "postgresql") {
		return nil, errors.New("STATE_REGISTRY_POSTGRES_URL must use the postgres or postgresql URL scheme")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("STATE_REGISTRY_POSTGRES_URL must include a PostgreSQL network host")
	}
	for key := range parsed.Query() {
		if strings.EqualFold(key, "servicefile") || strings.EqualFold(key, "passfile") {
			return nil, errors.New("STATE_REGISTRY_POSTGRES_URL must not use servicefile or passfile indirection")
		}
	}
	return parsed, nil
}

func withPeerIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Caller-supplied identity headers are NEVER authority.
		// Strip every documented header BEFORE mapping verified
		// peer-cert claims so a copy-pasted header can never widen
		// team authority across a missing / forged certificate.
		for _, name := range []string{
			"X-FlowAI-Role", "X-FlowAI-Team-Id", "X-FlowAI-Executor-Id",
			"X-FlowAI-Listener-Identity", "X-FlowAI-Source-System-Id",
			"X-FlowAI-Operator-Id", "X-FlowAI-Admin-Subject",
		} {
			r.Header.Del(name)
		}
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		peerauth.Parse(r.TLS.PeerCertificates[0]).Apply(r.Header)
		next.ServeHTTP(w, r)
	})
}
