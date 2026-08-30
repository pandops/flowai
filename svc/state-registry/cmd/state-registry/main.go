// Command state-registry runs the State Registry HTTP server. It
// reads configuration from environment variables, opens a PostgreSQL
// connection, starts the readiness probe goroutine, and serves
// /v1/livez, /v1/readyz, and the implementation-active business
// routes. The HTTP listener is plaintext; external HTTPS terminates
// at the Ingress. The State Registry does NOT terminate backend
// service-to-service mTLS, derive identity from peer certificates,
// or load any HTTP certificate, key, or CA file. PostgreSQL
// transport remains secure with server-certificate verification.
//
// The test-mode opt-in (STATE_REGISTRY_TEST_MODE=true) is gated by
// the state_registry_test_harness build tag — a normal production
// binary rejects the env var outright.
package main

import (
	"context"
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

	"github.com/flowai/platform/svc/state-registry/internal/adminauth"
	"github.com/flowai/platform/svc/state-registry/internal/config"
	"github.com/flowai/platform/svc/state-registry/internal/health"
	"github.com/flowai/platform/svc/state-registry/internal/httpapi"
	"github.com/flowai/platform/svc/state-registry/internal/logging"
	"github.com/flowai/platform/svc/state-registry/internal/migrations"
	"github.com/flowai/platform/svc/state-registry/internal/store"
	"github.com/flowai/platform/svc/state-registry/internal/testcontrol"
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
	if !cfg.TestMode && cfg.AdminIssuer == "" {
		return errors.New("load administrator JWT trust: STATE_REGISTRY_ADMIN_ISSUER and complete admin trust configuration are required")
	}
	cursorKeyring, err := cursorKeyringForBoot()
	if err != nil {
		return fmt.Errorf("load cursor keyring: %w", err)
	}
	postgresURL, err := postgresURLWithTLS(cfg)
	if err != nil {
		return fmt.Errorf("load postgres TLS: %w", err)
	}

	logger := logging.New(logging.LevelInfo)
	if len(cfg.LegacyTLSIgnoredKeys) > 0 {
		// Emit at most one deprecation warning naming only the
		// keys that were supplied. Never log the values; never
		// read the referenced paths.
		logger.Warn(
			"state-registry: legacy backend HTTP TLS configuration keys are ignored; backend transport is plaintext and external HTTPS terminates at Ingress",
			"ignored_keys", cfg.LegacyTLSIgnoredKeys,
		)
	}

	listener, actualPort, err := bindLoopback(cfg.BindHost, cfg.BindPort)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	cfg.BindPort = actualPort

	logger.Info("starting state-registry",
		"bind", cfg.BindAddress(),
	)

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	migrationURL := cfg.MigrationPostgresURL
	if migrationURL == cfg.PostgresURL {
		migrationURL = postgresURL
	}
	migrationDB, err := sql.Open("pgx", migrationURL)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("open postgres migration connection: %w", err)
	}
	defer migrationDB.Close()
	migrationCtx, cancelMigrations := context.WithTimeout(context.Background(), 30*time.Second)
	if err := migrations.ApplyUp(migrationCtx, migrationDB); err != nil {
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
	var coordinator *testcontrol.Coordinator
	if cfg.TestControlEnabled {
		coordinator = testcontrol.New(cfg.BarrierTimeout)
		adminStore.WithTransactionBarriers(coordinator)
		defer coordinator.Close()
	}
	var administratorAuth httpapi.AdminAuthenticator
	if cfg.AdminIssuer != "" {
		administratorAuth, err = adminauth.New(adminauth.Config{Issuer: cfg.AdminIssuer, Audience: cfg.AdminAudience, JWKSURL: cfg.AdminJWKSURL, RolePointer: cfg.AdminRolePointer, RequiredRole: "flowai-system-admin", Algorithms: cfg.AdminAlgorithms, ClockSkew: cfg.AdminClockSkew, TokenMaxAge: cfg.AdminTokenMaxAge, HTTPTimeout: cfg.AdminJWKSTimeout})
		if err != nil {
			return fmt.Errorf("load administrator JWT trust: %w", err)
		}
	}
	handler := httpapi.RoutesWithKeyringAndAdminAuth(serviceName, "", logger, checker, ops, cursorKeyring, cfg.TestMode, administratorAuth, adminStore)

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	var controlServer *http.Server
	var controlListener net.Listener
	if coordinator != nil {
		controlListener, err = net.Listen("tcp", cfg.TestControlAddress)
		if err != nil {
			return fmt.Errorf("bind test control: %w", err)
		}
		defer controlListener.Close()
		controlServer = &http.Server{Handler: testcontrol.Handler(coordinator, cfg.TestControlToken), ReadHeaderTimeout: 5 * time.Second}
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("state-registry listening", "bind", cfg.BindAddress())
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	if controlServer != nil {
		go func() {
			err := controlServer.Serve(controlListener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("test control server: %w", err)
				return
			}
			errCh <- nil
		}()
	}

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
	if controlServer != nil {
		if err := controlServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("test control shutdown: %w", err)
		}
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
