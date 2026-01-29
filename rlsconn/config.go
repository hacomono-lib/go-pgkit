// Package rlsconn provides PostgreSQL Row Level Security (RLS) integration
// through database/sql SessionResetter and Connector interfaces.
//
// This package is designed to be completely generic and reusable across projects.
// It does not depend on any project-specific structures or environment variables.
// Configuration is provided through the Config struct with SessionVars.
package rlsconn

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
)

// Ensure interfaces are implemented at compile time
var (
	_ Logger = (*slogAdapter)(nil)
	_ Logger = (*noopLogger)(nil)
)

// sessionVarNamePattern validates PostgreSQL session variable names.
// PostgreSQL custom variables must be in the form "namespace.name".
// Only alphanumeric characters, underscores, and dots are allowed.
// This prevents SQL injection in SET/RESET statements.
var sessionVarNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$`)

// Config holds the RLS configuration.
// It is completely generic and can be used for any session variables.
type Config struct {
	// SessionVars is a list of PostgreSQL session variable rules.
	// Each SessionVar defines how to extract a value from context
	// and which PostgreSQL session variable to set.
	SessionVars []SessionVar

	// Logger is the logger instance.
	// Default: NoopLogger (no logs emitted)
	Logger Logger

	// OnExecQuery is an optional hook called when execQuery is executed.
	// Use this for debugging to log the actual SQL query and arguments.
	// If nil, no hook is called (zero overhead in production).
	OnExecQuery func(ctx context.Context, query string, args []any)
}

// WithSlog configures the RLS connector to use slog for logging.
func (c *Config) WithSlog(logger *slog.Logger) *Config {
	c.Logger = NewSlogAdapter(logger)
	return c
}

// Validate validates the configuration and sets defaults.
// Returns an error if any session variable name is invalid.
func (c *Config) Validate() error {
	// Set default logger if not provided
	if c.Logger == nil {
		c.Logger = NewNoopLogger()
	}

	for i, sv := range c.SessionVars {
		if err := ValidateSessionVarName(sv.Name); err != nil {
			return fmt.Errorf("invalid session variable at index %d: %w", i, err)
		}
		if sv.ValueGetter == nil {
			return fmt.Errorf("session variable at index %d has nil ValueGetter", i)
		}
	}
	return nil
}

// SessionVar defines a PostgreSQL session variable setting rule.
type SessionVar struct {
	// Name is the PostgreSQL session variable name.
	// Must be in the form "namespace.name" (e.g., "app.current_tenant_id").
	// Only alphanumeric characters and underscores are allowed.
	// This is validated to prevent SQL injection.
	Name string

	// ValueGetter extracts the value from context.
	// If the value exists: returns (value, true) -> SET Name = value
	// If the value doesn't exist: returns ("", false) -> RESET Name
	ValueGetter func(context.Context) (string, bool)
}

// ValidateSessionVarName validates a PostgreSQL session variable name.
// Returns an error if the name is invalid or potentially dangerous.
//
// Valid names must:
// - Be in the form "namespace.name" (custom variables require a namespace)
// - Only contain alphanumeric characters and underscores
// - Start with a letter or underscore
//
// This validation prevents SQL injection when the name is used in SET/RESET statements.
func ValidateSessionVarName(name string) error {
	if name == "" {
		return fmt.Errorf("session variable name cannot be empty")
	}

	if !sessionVarNamePattern.MatchString(name) {
		return fmt.Errorf("invalid session variable name: %q (must match pattern 'namespace.name' with only alphanumeric and underscore characters)", name)
	}

	return nil
}
