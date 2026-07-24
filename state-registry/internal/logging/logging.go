// Package logging configures the JSON slog logger for the State Registry
// service. The logger writes to stdout and never includes secret
// material, AES key bytes, nonce bytes, ciphertext bytes, authentication
// tag bytes, key material, or derived key bytes.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Level represents the supported log levels.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// New returns a JSON slog logger at the requested level.
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
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}

// FromContext returns the logger stored on ctx, falling back to the
// default JSON logger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithContext stores the logger on the returned context.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

type loggerKey struct{}
