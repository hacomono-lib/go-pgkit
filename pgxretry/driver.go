package pgxretry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/stdlib"
)

const DriverName = "pgxretry"

var (
	driverRegistered bool
	driverMu         sync.Mutex
	registeredDriver *Driver
)

// Register registers the retryable driver with the given configuration
// If config is nil, default configuration will be used
// Returns the registered driver instance
//
// Note: The driver can only be registered once per process. The first caller's
// configuration (including ConnectorWrapper) will be used for all subsequent connections.
// For RLS, ensure the first session to connect has RLS enabled.
func Register(config *Config) *Driver {
	driverMu.Lock()
	defer driverMu.Unlock()

	if driverRegistered {
		return registeredDriver
	}

	registeredDriver = NewDriver(config)

	sql.Register(DriverName, registeredDriver)
	driverRegistered = true

	return registeredDriver
}

// Driver is the retryable PostgreSQL driver
type Driver struct {
	config *Config
	mu     sync.RWMutex
}

// NewDriver creates a new retryable driver
func NewDriver(config *Config) *Driver {
	if config == nil {
		config = DefaultConfig()
	}
	config = config.WithDefaults()

	// Validate final configuration
	if err := config.Validate(); err != nil {
		panic(fmt.Sprintf("invalid configuration: %v", err))
	}

	return &Driver{
		config: config,
	}
}

// Open opens a new database connection
// This method is kept for backward compatibility with the driver.Driver interface.
// Internally, it uses OpenConnector and Connect to avoid code duplication.
func (d *Driver) Open(name string) (driver.Conn, error) {
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

// OpenConnector implements the DriverContext interface
func (d *Driver) OpenConnector(name string) (driver.Connector, error) {
	return &connector{
		driver: d,
		dsn:    name,
	}, nil
}

// connector implements driver.Connector
type connector struct {
	driver *Driver
	dsn    string
}

// Connect implements driver.Connector
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	// Create base pgx connector to honor context
	pgxDriver := &stdlib.Driver{}
	pgxConnector, err := pgxDriver.OpenConnector(c.dsn)
	if err != nil {
		return nil, WrapDriverError(err, "failed to create pgx connector")
	}

	// Get current config with read lock
	c.driver.mu.RLock()
	config := c.driver.config
	c.driver.mu.RUnlock()

	var baseConn driver.Conn

	// Preferred: Use ConnectorWrapper (e.g., RLSConnector)
	// RLSConnector.Connect() calls ResetSession() internally for new connections.
	if config.ConnectorWrapper != nil {
		wrappedConnector := config.ConnectorWrapper(pgxConnector)
		baseConn, err = wrappedConnector.Connect(ctx)
		if err != nil {
			return nil, WrapDriverError(err, "failed to connect with wrapped connector")
		}
	} else {
		// No wrapper: direct connection
		baseConn, err = pgxConnector.Connect(ctx)
		if err != nil {
			return nil, WrapDriverError(err, "failed to connect with context")
		}
	}

	// Create retryable connection wrapper
	conn := &retryableConn{
		baseConn:  baseConn,
		dsn:       c.dsn,
		config:    config,
		driver:    c.driver,
		logger:    config.Logger,
		detector:  config.ErrorDetector,
		strategy:  config.RetryStrategy,
		resetFunc: config.ConnectionResetFunc,
	}

	return conn, nil
}

// Driver implements driver.Connector
func (c *connector) Driver() driver.Driver {
	return c.driver
}
