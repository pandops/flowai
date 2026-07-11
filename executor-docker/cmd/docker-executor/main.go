// docker-executor is the FlowAI v0001 Docker Executor service.
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/executor-docker/internal/dockerclient"
	"github.com/flowai/platform/executor-docker/internal/executor"
	"github.com/flowai/platform/executor-docker/internal/httpapi"
	"github.com/flowai/platform/executor-docker/internal/logging"
	"github.com/flowai/platform/executor-docker/internal/mockedclient"
	"github.com/flowai/platform/executor-docker/internal/openhands"
	"github.com/flowai/platform/executor-docker/internal/platform"
)

func main() {
	var (
		cfgPath = flag.String("config", "executor-docker/configs/docker-executor.yaml", "YAML config path")
		bind    = flag.String("bind", "", "API bind (overrides YAML/env)")
	)
	flag.Parse()

	cfg, err := executor.LoadConfig(*cfgPath)
	if err != nil {
		// Log to stderr and exit.
		os.Stderr.WriteString("config load error: " + err.Error() + "\n")
		os.Exit(2)
	}

	logger := logging.New(logging.Level(cfg.LogLevel))

	docker, err := dockerclient.NewHTTPClient(cfg.DockerSocketPath)
	if err != nil {
		logger.Error("docker client init failed", "err", err.Error())
		os.Exit(1)
	}
	mocked := mockedclient.New(cfg.MockedServerURL, nil)

	exec := executor.New(cfg, docker, mocked, logger)

	// Local platform health API.
	mux := httpapi.NewRouter("docker-executor", logger)
	httpapi.RegisterProbes(mux, "docker-executor", cfg.ExecutorID, httpapi.ReadinessFunc(func() bool {
		st := exec.State()
		return st == executor.StateReady || st == executor.StateBusy
	}))
	// Register the probes under /v1 (e.g. /v1/livez) so they match the
	// platform-wide convention for versioned APIs. The mux is wrapped
	// in a chi router that strips the /v1 prefix before forwarding.
	v1 := chi.NewRouter()
	v1.Mount("/v1", mux)
	httpServer := &http.Server{
		Addr:              cfg.ExecutorAPIBind,
		Handler:           v1,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Honour -bind override (handy for tests).
	if *bind != "" {
		httpServer.Addr = *bind
		cfg.ExecutorAPIBind = *bind
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Append initial executor events.
	exec.Logger().Info("docker-executor starting", "executor_id", cfg.ExecutorID, "image", cfg.OpenHandsImage)

	// Run the HTTP server in the background.
	go func() {
		exec.Logger().Info("platform health API listening", "bind", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			exec.Logger().Error("http server exited with error", "err", err.Error())
			stop()
		}
	}()

	// Inject config + executor into context for downstream packages.
	ctx = executor.WithContext(ctx, exec)
	ctx = logging.WithContext(ctx, logger)

	// Run the executor main loop (blocks).
	if err := exec.Run(ctx); err != nil {
		logger.Error("executor run error", "err", err.Error())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown error", "err", err.Error())
	}
	logger.Info("docker-executor stopped")
}

// Compile-time references to silence "imported and not used" if some
// platforms strip code via build tags.
var (
	_ = openhands.ErrNotReady
	_ = platform.ExecutorTypeDockerOpenHands
)