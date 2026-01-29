package pgxretry

import (
	"log/slog"
	"time"
)

// Config holds the configuration for the retryable driver
type Config struct {
	// MaxRetries is the maximum number of retry attempts
	// Default: 3
	MaxRetries int

	// InitialBackoff is the initial backoff duration
	// Default: 100ms
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration
	// Default: 5s
	MaxBackoff time.Duration

	// BackoffFactor is the exponential backoff multiplier
	// Default: 2.0
	BackoffFactor float64

	// ErrorDetector is the custom error detector
	// Default: DefaultErrorDetector
	ErrorDetector ErrorDetector

	// RetryStrategy is the custom retry strategy
	// Default: ExponentialBackoff strategy
	RetryStrategy RetryStrategy

	// Logger is the logger instance
	// Default: NoopLogger
	Logger Logger

	// ConnectionResetFunc is an optional callback for resetting connections
	// This is useful when integrating with connection pools
	ConnectionResetFunc ConnectionResetFunc

	// ConnectorWrapper wraps the base connector with additional functionality.
	// This is the preferred way to add RLS session variable management.
	// The wrapper (e.g., rlsconn.RLSConnector) is responsible for calling ResetSession
	// on new connections.
	// Example: rlsconn.NewRLSConnector(baseConnector, rlsConfig)
	ConnectorWrapper ConnectorWrapper
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		BackoffFactor:  2.0,
		Logger:         NewNoopLogger(),
	}
}

// WithDefaults fills in default values for unset fields
func (c *Config) WithDefaults() *Config {
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = 100 * time.Millisecond
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 5 * time.Second
	}
	if c.BackoffFactor < 1.0 {
		c.BackoffFactor = 2.0
	}
	if c.ErrorDetector == nil {
		c.ErrorDetector = NewDefaultErrorDetector()
	}
	if c.RetryStrategy == nil {
		c.RetryStrategy = NewExponentialBackoff(c.MaxRetries, c.InitialBackoff, c.MaxBackoff, c.BackoffFactor)
	}
	if c.Logger == nil {
		c.Logger = NewNoopLogger()
	}

	return c
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.MaxRetries < 0 {
		return WrapDriverError(ErrInvalidConfig, "MaxRetries must be non-negative")
	}
	if c.InitialBackoff < 0 {
		return WrapDriverError(ErrInvalidConfig, "InitialBackoff must be non-negative")
	}
	if c.MaxBackoff < 0 {
		return WrapDriverError(ErrInvalidConfig, "MaxBackoff must be non-negative")
	}
	if c.BackoffFactor < 1.0 {
		return WrapDriverError(ErrInvalidConfig, "BackoffFactor must be >= 1.0")
	}
	return nil
}

// WithSlog configures the driver to use slog for logging
func (c *Config) WithSlog(logger *slog.Logger) *Config {
	c.Logger = NewSlogAdapter(logger)
	return c
}

// WithErrorDetector sets a custom error detector
func (c *Config) WithErrorDetector(detector ErrorDetector) *Config {
	c.ErrorDetector = detector
	return c
}

// WithRetryStrategy sets a custom retry strategy
func (c *Config) WithRetryStrategy(strategy RetryStrategy) *Config {
	c.RetryStrategy = strategy
	return c
}

// WithConnectionReset sets the connection reset function
func (c *Config) WithConnectionReset(fn ConnectionResetFunc) *Config {
	c.ConnectionResetFunc = fn
	return c
}

// WithConnectorWrapper sets the connector wrapper function.
// The wrapper is applied to the base connector before Connect() is called.
// This is the preferred way to add RLS session variable management.
//
//	Example: config.WithConnectorWrapper(func(base driver.Connector) driver.Connector {
//	    return rlsconn.NewRLSConnector(base, rlsConfig)
//	})
func (c *Config) WithConnectorWrapper(wrapper ConnectorWrapper) *Config {
	c.ConnectorWrapper = wrapper
	return c
}
