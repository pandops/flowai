// executor_docker_opehands is the FlowAI concrete Executor for the Docker
// runtime + OpenHands agent toolchain. Concrete wire names follow
// executor_<runtime>_<tool>; "opehands" is the deliberate service
// identifier (the external product spelling "OpenHands" stays unchanged).
package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/flowai/platform/executor_docker_opehands/internal/dockerclient"
	"github.com/flowai/platform/executor_docker_opehands/internal/executor"
	"github.com/flowai/platform/executor_docker_opehands/internal/httpapi"
	"github.com/flowai/platform/executor_docker_opehands/internal/logging"
	"github.com/flowai/platform/executor_docker_opehands/internal/openhands"
	"github.com/flowai/platform/executor_docker_opehands/internal/platform"
)

func main() {
	var (
		cfgPath = flag.String("config", "executor_docker_opehands/configs/executor_docker_opehands.yaml", "YAML config path")
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

	docker, err := dockerclient.NewSDKClient(cfg.DockerSocketPath)
	if err != nil {
		logger.Error("docker client init failed", "err", err.Error())
		os.Exit(1)
	}
	exec := executor.New(cfg, docker, logger)

	// Local platform health API.
	mux := httpapi.NewRouter("executor_docker_opehands", logger)
	httpapi.RegisterProbes(mux, "executor_docker_opehands", cfg.ExecutorID, httpapi.ReadinessFuncWithDeps(func() (bool, bool, bool) {
		st := exec.State()
		ready := st == executor.StateReady || st == executor.StateBusy
		return ready, exec.IsStateRegistryRegistered(), exec.IsOpenHandsReachable()
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
	exec.Logger().Info("executor_docker_opehands starting", "executor_id", cfg.ExecutorID, "image", cfg.OpenHandsImage)

	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		logger.Error("platform health API listen failed", "err", err.Error())
		os.Exit(1)
	}
	httpServer.Addr = listener.Addr().String()

	// Run the HTTP server in the background.
	go func() {
		exec.Logger().Info("platform health API listening", "bind", httpServer.Addr)
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	logger.Info("executor_docker_opehands stopped")
}

// Compile-time references to silence "imported and not used" if some
// platforms strip code via build tags.
var (
	_ = openhands.ErrNotReady
	_ = platform.ExecutorTypeDockerOpenHands
)
