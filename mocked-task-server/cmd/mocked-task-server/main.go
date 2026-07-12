// Mocked task server entrypoint. Serves the three surfaces (Router, State Registry,
// Env Registry) under /v1 on a configurable bind address.
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

	"github.com/flowai/platform/mocked-task-server/internal/config"
	"github.com/flowai/platform/mocked-task-server/internal/logging"
	"github.com/flowai/platform/mocked-task-server/internal/server"
	"github.com/flowai/platform/mocked-task-server/internal/store"
)

func main() {
	var (
		cfgPath = flag.String("config", "configs/mocked-task-server.yaml", "YAML config path")
		bind    = flag.String("bind", "", "Bind address (overrides YAML/env)")
	)
	flag.Parse()

	logger := logging.New(logging.Level(config.GetString("LOG_LEVEL", "info")))

	var cfg struct {
		Bind string `yaml:"bind"`
	}
	if *cfgPath != "" {
		if err := config.Load(*cfgPath, &cfg); err != nil {
			logger.Warn("config load failed, using defaults", "err", err.Error())
		}
	}

	finalBind := *bind
	if finalBind == "" {
		finalBind = config.GetString("MOCKED_SERVER_BIND", cfg.Bind)
	}
	if finalBind == "" {
		finalBind = "127.0.0.1:8080"
	}

	mem := store.NewMemoryStore()
	srv := server.New(mem, logger)

	mux := srv.Routes()

	httpServer := &http.Server{
		Addr:              finalBind,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("mocked task server listening", "bind", finalBind)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server exited with error", "err", err.Error())
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err.Error())
	}
	logger.Info("mocked task server stopped")
}
