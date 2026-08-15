// Command web-ui serves the embedded FlowAI operator interface.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flowai/platform/svc/web-ui/internal/config"
	"github.com/flowai/platform/svc/web-ui/internal/httpapi"
	webassets "github.com/flowai/platform/svc/web-ui/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "web-ui failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.BindAddress(),
		Handler:           httpapi.New(webassets.Files),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("web-ui listening", "bind", cfg.BindAddress())
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
