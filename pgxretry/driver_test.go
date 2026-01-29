package pgxretry_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/hacomono-lib/go-pgkit/pgxretry"

	"github.com/stretchr/testify/suite"
)

type DriverTestSuite struct {
	suite.Suite
	dbHost string
}

func (s *DriverTestSuite) SetupSuite() {
	s.dbHost = os.Getenv("DB_WRITER_HOST")
	if s.dbHost == "" {
		s.dbHost = "127.0.0.1"
	}
}

func TestDriverSuite(t *testing.T) {
	suite.Run(t, new(DriverTestSuite))
}

func (s *DriverTestSuite) TestRegister() {
	// Note: Since Register uses a package-level variable, we can't truly test
	// multiple registrations in parallel. This is a limitation of the design.

	s.Run("register with nil config", func() {
		// This should not panic
		pgxretry.Register(nil)

		// Should be able to open a connection (will fail without real DB)
		_, err := sql.Open(pgxretry.DriverName, "fake-dsn")
		// We expect no error from Open itself, only when we try to use it
		s.NoError(err)
	})

	s.Run("register with custom config", func() {
		config := &pgxretry.Config{
			MaxRetries:     5,
			InitialBackoff: 50 * time.Millisecond,
		}

		pgxretry.Register(config)

		// Should be registered
		_, err := sql.Open(pgxretry.DriverName, "fake-dsn")
		s.NoError(err)
	})
}

func (s *DriverTestSuite) TestDriver() {
	config := pgxretry.DefaultConfig()
	driver := pgxretry.NewDriver(config)

	s.Run("new driver with nil config", func() {
		d := pgxretry.NewDriver(nil)
		s.NotNil(d)
	})

	s.Run("open connection without real database", func() {
		// This will fail because we don't have a real database
		conn, err := driver.Open("host=" + s.dbHost + " port=5432 user=test dbname=test sslmode=disable")
		s.Error(err) // Expected to fail without real DB
		s.Nil(conn)
	})

	s.Run("open connector", func() {
		connector, err := driver.OpenConnector("fake-dsn")
		s.Require().NoError(err)
		s.NotNil(connector)

		// Test the connector interface
		s.Equal(driver, connector.Driver())

		// Connect will fail without real DB
		_, err = connector.Connect(context.Background())
		s.Error(err)
	})
}

func (s *DriverTestSuite) TestRegisterReturnsDriver() {
	// Register should return the driver
	driver := pgxretry.Register(nil)
	s.NotNil(driver)

	// Calling Register again should return the same driver
	driver2 := pgxretry.Register(nil)
	s.Equal(driver, driver2)
}

func (s *DriverTestSuite) TestDriverNameConstant() {
	s.Equal("pgxretry", pgxretry.DriverName)
}

func (s *DriverTestSuite) TestConnectorContextHandling() {
	driver := pgxretry.NewDriver(pgxretry.DefaultConfig())

	s.Run("connector respects context cancellation", func() {
		// Use invalid host to ensure failure without real network operations
		connector, err := driver.OpenConnector("host=invalid-test-host port=5432 user=test dbname=test sslmode=disable")
		s.Require().NoError(err)

		// Create already cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Connection attempt should fail due to cancelled context
		conn, err := connector.Connect(ctx)
		s.Error(err)
		s.Nil(conn)
		s.ErrorIs(err, context.Canceled)
	})

	s.Run("connector respects context timeout", func() {
		// Use invalid host to ensure failure without real network operations
		connector, err := driver.OpenConnector("host=invalid-test-host port=5432 user=test dbname=test sslmode=disable")
		s.Require().NoError(err)

		// Create context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Wait for timeout to occur
		time.Sleep(1 * time.Millisecond)

		// Connection attempt should fail due to timeout
		conn, err := connector.Connect(ctx)
		s.Error(err)
		s.Nil(conn)

		// The context deadline should cause a timeout error
		// This should either be context.DeadlineExceeded directly or wrapped within the error chain
		s.ErrorIs(err, context.DeadlineExceeded)
	})

	s.Run("connector works with valid context", func() {
		connector, err := driver.OpenConnector("host=" + s.dbHost + " port=5432 user=test dbname=test sslmode=disable")
		s.Require().NoError(err)

		// Create normal context with reasonable timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Connection attempt should fail due to no real database, not context issues
		conn, err := connector.Connect(ctx)
		s.Error(err) // Still expect error due to no real DB
		s.Nil(conn)
		// Error should be database-related, not context-related
		s.False(errors.Is(err, context.Canceled), "Error should not be context cancellation")
		s.False(errors.Is(err, context.DeadlineExceeded), "Error should not be context timeout")
	})
}
