package conn

import (
	"context"
	"log/slog"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// LogLevel represents the log level for the database logger.
// Re-exported from gorm/logger for convenience.
type LogLevel = logger.LogLevel

// Log level constants from gorm/logger
const (
	LogLevelSilent = logger.Silent
	LogLevelError  = logger.Error
	LogLevelWarn   = logger.Warn
	LogLevelInfo   = logger.Info
)

// GormOpener is a function that opens a GORM database connection.
// This allows for custom initialization logic (e.g., DataDog APM tracing).
type GormOpener func(dialector gorm.Dialector, config *gorm.Config) (*gorm.DB, error)

// DefaultGormOpener is the default GORM opener that uses gorm.Open directly.
func DefaultGormOpener(dialector gorm.Dialector, config *gorm.Config) (*gorm.DB, error) {
	return gorm.Open(dialector, config)
}

// GormCallbackRegistrar is a function that registers GORM callbacks.
// This allows for custom callback registration (e.g., DataDog APM resource naming).
type GormCallbackRegistrar func(db *gorm.DB, logger *slog.Logger)

// Sessioner is the common interface for ReaderSession and WriterSession.
// Used as a type parameter in generic reader implementations.
type Sessioner interface {
	GetDBOrTx(ctx context.Context) *gorm.DB
}
