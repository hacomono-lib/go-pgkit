package pgxretry

import (
	"context"
	"database/sql/driver"
	"log/slog"
	"time"
)

// ErrorDetector defines the interface for detecting retryable errors
type ErrorDetector interface {
	// IsRetryableError checks if an error should trigger a retry
	IsRetryableError(err error) bool
}

// RetryStrategy defines the interface for retry behavior
type RetryStrategy interface {
	// NextBackoff returns the duration to wait before the next retry
	// Returns -1 when no more retries should be attempted
	NextBackoff(attempt int) time.Duration
}

// Logger defines the interface for logging
type Logger interface {
	// DebugContext logs a debug message with context
	DebugContext(ctx context.Context, msg string, args ...any)

	// InfoContext logs an info message with context
	InfoContext(ctx context.Context, msg string, args ...any)

	// WarnContext logs a warning message with context
	WarnContext(ctx context.Context, msg string, args ...any)

	// ErrorContext logs an error message with context
	ErrorContext(ctx context.Context, msg string, args ...any)
}

// ConnectionResetFunc is a callback for resetting database connections
type ConnectionResetFunc func(ctx context.Context) error

// ConnectorWrapper wraps a driver.Connector with additional functionality.
// This is the preferred way to add RLS session variable management.
// The wrapper (e.g., rlsconn.RLSConnector) is responsible for calling ResetSession
// on new connections.
type ConnectorWrapper func(connector driver.Connector) driver.Connector

// slogAdapter adapts slog.Logger to the Logger interface
type slogAdapter struct {
	logger *slog.Logger
}

func (a *slogAdapter) DebugContext(ctx context.Context, msg string, args ...any) {
	a.logger.DebugContext(ctx, msg, args...)
}

func (a *slogAdapter) InfoContext(ctx context.Context, msg string, args ...any) {
	a.logger.InfoContext(ctx, msg, args...)
}

func (a *slogAdapter) WarnContext(ctx context.Context, msg string, args ...any) {
	a.logger.WarnContext(ctx, msg, args...)
}

func (a *slogAdapter) ErrorContext(ctx context.Context, msg string, args ...any) {
	a.logger.ErrorContext(ctx, msg, args...)
}

// NewSlogAdapter creates a Logger from an slog.Logger
func NewSlogAdapter(logger *slog.Logger) Logger {
	return &slogAdapter{logger: logger}
}

// noopLogger is a logger that does nothing
type noopLogger struct{}

func (n *noopLogger) DebugContext(ctx context.Context, msg string, args ...any) {}
func (n *noopLogger) InfoContext(ctx context.Context, msg string, args ...any)  {}
func (n *noopLogger) WarnContext(ctx context.Context, msg string, args ...any)  {}
func (n *noopLogger) ErrorContext(ctx context.Context, msg string, args ...any) {}

// NewNoopLogger creates a logger that does nothing
func NewNoopLogger() Logger {
	return &noopLogger{}
}
