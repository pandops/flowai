// Command executor_k8s_openhands runs the v0005 K8s Executor. It
// reads configuration from environment variables (with optional
// YAML override), opens the bbolt recovery cache, takes the
// exclusive OS file lock, registers with the State Registry using
// the cached or freshly-issued executor_id, and runs the v0005
// state machine. The HTTP listener is plaintext; external HTTPS
// terminates at the Ingress.
//
// Per-service conventions (no shared code with other concrete
// Executors; one binary per service) are documented in
// AGENTS.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/executor_k8s_openhands/internal/cache"
	"github.com/flowai/platform/executor_k8s_openhands/internal/config"
	"github.com/flowai/platform/executor_k8s_openhands/internal/executor"
	"github.com/flowai/platform/executor_k8s_openhands/internal/httpapi"
	"github.com/flowai/platform/executor_k8s_openhands/internal/k8sclient"
	"github.com/flowai/platform/executor_k8s_openhands/internal/logging"
	"github.com/flowai/platform/executor_k8s_openhands/internal/platform"
	"github.com/flowai/platform/executor_k8s_openhands/internal/stateregistryclient"
)

const serviceName = "executor_k8s_openhands"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, serviceName+" failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := executor.LoadConfig(envOr("EXECUTOR_CONFIG", ""))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(logging.Level(cfg.LogLevel)).With("service", serviceName)

	store, err := cache.Open(cfg.CacheDir)
	if err != nil {
		return fmt.Errorf("open recovery cache: %w", err)
	}
	defer store.Close()

	lock, err := store.LockFile()
	if err != nil {
		return fmt.Errorf("acquire cache lock: %w", err)
	}
	defer func() { _ = lock.Release() }()
	cachedExecutorID, err := store.ExecutorID(context.Background())
	if err != nil {
		return fmt.Errorf("read cached executor identity: %w", err)
	}
	cfg.ExecutorID = cachedExecutorID

	registry, err := stateregistryclient.New(cfg.StateRegistryURL,
		stateregistryclient.Identity{
			ExecutorID: cfg.ExecutorID,
			Scope:      cfg.Scope,
			TeamID:     cfg.TeamID,
		}, nil)
	if err != nil {
		return fmt.Errorf("registry client: %w", err)
	}

	kube, err := k8sclientFromEnvOrHealthy(cfg)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		logger.Info("shutdown", "signal", s.String())
		cancel()
	}()

	exec := executor.New(cfg, registry, kube, store, lock, func(format string, args ...any) {
		logger.Info(fmt.Sprintf(format, args...))
	})

	r := chi.NewRouter()
	httpapi.RegisterProbesWithExecutorID(r, serviceName, exec.ExecutorID, httpapi.ReadinessFuncWithDeps(func() (ready, registered, healthy bool) {
		ready = exec.State() != executor.StateFailed
		healthy = kube != nil && kube.Healthy(ctx)
		registered = registry.Identity().ExecutorID != ""
		return
	}))

	srv := &http.Server{
		Addr:              cfg.ExecutorAPIBind,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http", "err", err)
		}
	}()
	logger.Info("started", "bind", cfg.ExecutorAPIBind, "executor_id", cfg.ExecutorID,
		"executor_type", platform.ExecutorTypeK8sOpenHands,
		"scope", cfg.Scope, "team_id", cfg.TeamID, "tag", cfg.AuthorizedTag)

	if err := exec.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("executor", "err", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}

// k8sclientFromEnvOrHealthy resolves the production in-cluster client from
// the mounted ServiceAccount token and CA. An explicit KUBE_API_URL is
// rejected until an equally explicit credential source is defined.
func k8sclientFromEnvOrHealthy(cfg *executor.Config) (k8sclient.Client, error) {
	_ = cfg
	if os.Getenv("KUBE_API_URL") == "" {
		return k8sclient.NewInClusterClient()
	}
	return nil, errors.New("out-of-cluster KUBE_API_URL mode is unsupported")
}

func envOr(k, fb string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fb
}

// silence unused symbol warnings when the harness omits some hooks
var (
	_ = net.Listen
	_ = config.Load
	_ = logging.Level("")
)
