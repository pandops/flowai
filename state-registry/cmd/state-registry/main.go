// Command state-registry runs the State Registry scaffold HTTP
// server. It reads configuration from environment variables, opens a
// PostgreSQL connection, starts the readiness probe goroutine, and
// serves /v1/livez, /v1/readyz, and (when STATE_REGISTRY_TEST_MODE=true)
// /v1/_test/decrypt-ops and the implementation-active business routes.
// Temporary header-derived identities remain confined to test mode.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/flowai/platform/state-registry/internal/config"
	"github.com/flowai/platform/state-registry/internal/health"
	"github.com/flowai/platform/state-registry/internal/httpapi"
	"github.com/flowai/platform/state-registry/internal/logging"
	"github.com/flowai/platform/state-registry/internal/migrations"
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

	logger := logging.New(logging.LevelInfo)

	listener, actualPort, err := bindLoopback(cfg.BindHost, cfg.BindPort)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	cfg.BindPort = actualPort

	logger.Info("starting state-registry",
		"bind", cfg.BindAddress(),
	)

	db, err := sql.Open("pgx", cfg.PostgresURL)
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
	adminStore := store.New(db)
	handler := httpapi.RoutesWithKeyring(serviceName, "", logger, checker, ops, cursorKeyring, isTestMode(), adminStore)

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
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

// isTestMode reports whether the test-only observability surface may
// be mounted. It returns true ONLY when the deployment opted in via
// the STATE_REGISTRY_TEST_MODE env var; production startup never sets
// that variable so /v1/_test/decrypt-ops is not mounted.
func isTestMode() bool {
	v, ok := os.LookupEnv("STATE_REGISTRY_TEST_MODE")
	if !ok {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return b
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
