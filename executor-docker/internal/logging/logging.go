// Package logging sets up the slog JSON logger used by all FlowAI backend services.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Level represents the platform-supported log levels.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// New returns a JSON slog.Logger at the requested level.
// Unknown levels fall back to info.
func New(level Level) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(string(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})
	return slog.New(handler)
}

// FromContext returns the logger from ctx, falling back to the default JSON logger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithContext returns a copy of ctx that carries the supplied logger.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

type loggerKey struct{}
