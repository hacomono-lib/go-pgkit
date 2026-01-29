package rlsconn

import (
	"context"
	"database/sql/driver"
	"fmt"
)

// RLSConnector implements database/sql/driver.Connector interface.
// It wraps a base connector and adds RLS session variable management
// to all connections.
type RLSConnector struct {
	baseConnector driver.Connector
	config        *Config
}

// NewRLSConnector creates a new RLSConnector.
// It validates the configuration and panics if invalid.
// Use NewRLSConnectorWithValidation if you prefer error handling over panic.
func NewRLSConnector(baseConnector driver.Connector, config *Config) *RLSConnector {
	connector, err := NewRLSConnectorWithValidation(baseConnector, config)
	if err != nil {
		panic(fmt.Sprintf("invalid RLS configuration: %v", err))
	}
	return connector
}

// NewRLSConnectorWithValidation creates a new RLSConnector with explicit validation.
// Returns an error if the configuration is invalid.
func NewRLSConnectorWithValidation(baseConnector driver.Connector, config *Config) (*RLSConnector, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return &RLSConnector{
		baseConnector: baseConnector,
		config:        config,
	}, nil
}

// Connect creates a new connection and wraps it with RLSSessionResetter.
// It also calls ResetSession to set initial session variables.
func (c *RLSConnector) Connect(ctx context.Context) (driver.Conn, error) {
	// Create base connection
	baseConn, err := c.baseConnector.Connect(ctx)
	if err != nil {
		return nil, err
	}

	// Wrap with RLSSessionResetter
	rlsConn := NewRLSSessionResetter(baseConn, c.config)

	// Set initial session variables
	// This ensures RLS is properly configured even on new connections
	if err := rlsConn.ResetSession(ctx); err != nil {
		_ = rlsConn.Close() // Clean up on error
		return nil, err
	}

	return rlsConn, nil
}

// Driver returns the underlying driver from the base connector.
func (c *RLSConnector) Driver() driver.Driver {
	return c.baseConnector.Driver()
}
