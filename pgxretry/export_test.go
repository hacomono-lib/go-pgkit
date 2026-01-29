package pgxretry

import (
	"database/sql/driver"
)

// Exported types and functions for testing

// RetryableConn exports retryableConn for testing
type RetryableConn = retryableConn

// NewRetryableConnForTest creates a new retryableConn for testing
func NewRetryableConnForTest(baseConn driver.Conn, dsn string, config *Config, d *Driver) *RetryableConn {
	// Handle nil config by starting with empty Config
	if config == nil {
		config = DefaultConfig()
	}

	// Apply defaults to ensure all required fields are populated
	cfg := config.WithDefaults()

	return &retryableConn{
		baseConn:      baseConn,
		dsn:           dsn,
		config:        cfg,
		driver:        d,
		logger:        cfg.Logger,
		detector:      cfg.ErrorDetector,
		strategy:      cfg.RetryStrategy,
		resetFunc:     cfg.ConnectionResetFunc,
		closed:        false,
		inTransaction: false,
	}
}

// Exported methods for testing

// GetInTransaction returns the inTransaction state for testing
func (c *RetryableConn) GetInTransaction() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inTransaction
}

// GetClosed returns the closed state for testing
func (c *RetryableConn) GetClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// SetInTransaction sets the inTransaction state for testing
func (c *RetryableConn) SetInTransaction(inTx bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inTransaction = inTx
}

// RetryableTx exports retryableTx for testing
type RetryableTx = retryableTx

// IsCachedPlanError exports isCachedPlanError for testing
func (c *RetryableConn) IsCachedPlanError(err error) bool {
	return c.isCachedPlanError(err)
}
