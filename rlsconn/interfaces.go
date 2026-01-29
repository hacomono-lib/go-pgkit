package rlsconn

import (
	"context"
	"log/slog"
)

// Logger defines the interface for logging
type Logger interface {
	// WarnContext logs a warning message with context
	WarnContext(ctx context.Context, msg string, args ...any)
}

// slogAdapter adapts slog.Logger to the Logger interface
type slogAdapter struct {
	logger *slog.Logger
}

func (a *slogAdapter) WarnContext(ctx context.Context, msg string, args ...any) {
	a.logger.WarnContext(ctx, msg, args...)
}

// NewSlogAdapter creates a Logger from an slog.Logger
func NewSlogAdapter(logger *slog.Logger) Logger {
	return &slogAdapter{logger: logger}
}

// noopLogger is a logger that does nothing
type noopLogger struct{}

func (n *noopLogger) WarnContext(ctx context.Context, msg string, args ...any) {}

// NewNoopLogger creates a logger that does nothing
func NewNoopLogger() Logger {
	return &noopLogger{}
}
