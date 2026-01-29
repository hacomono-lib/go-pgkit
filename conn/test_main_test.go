package conn

import (
	"database/sql/driver"
	"os"
	"testing"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
	"github.com/hacomono-lib/go-pgkit/rlsconn"
	"github.com/hacomono-lib/go-pgkit/rlsctx"
)

// TestMain is a special Go test function that runs before and after all tests in the package.
//
// Execution order:
//  1. TestMain is called
//  2. Setup (driver initialization, etc.)
//  3. m.Run() executes all tests
//  4. Teardown (cleanup, etc.)
//  5. os.Exit(code)
//
// The driver is initialized here with RLS enabled.
// This ensures that even if non-RLS tests (e.g., benchmarks) run first,
// the driver has the ConnectorWrapper configured.
func TestMain(m *testing.M) {
	// Set default environment variables for local development (matches compose.yaml)
	setDefaultEnv()

	// Initialize the driver with RLS ConnectorWrapper
	initDriverWithRLS()

	os.Exit(m.Run())
}

// setDefaultEnv sets default environment variables when not already set.
// Values correspond to compose.yaml configuration.
// Does not overwrite existing values (e.g., in CI or container environments).
func setDefaultEnv() {
	defaults := map[string]string{
		"DB_HOST":     "localhost",
		"DB_PORT":     "5432",
		"DB_USER":     "app_user",
		"DB_PASSWORD": "app_password",
		"DB_NAME":     "pgkit_test",
	}
	for key, value := range defaults {
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

// initDriverWithRLS initializes the pgxretry driver with RLS support.
func initDriverWithRLS() {
	rlsConfig := &rlsconn.Config{
		SessionVars: []rlsconn.SessionVar{
			{
				Name:        "app.current_tenant_id",
				ValueGetter: rlsctx.GetTenantIDFromContext,
			},
			{
				Name:        "app.current_user_id",
				ValueGetter: rlsctx.GetUserIDFromContext,
			},
		},
	}

	retryConfig := &pgxretry.Config{
		MaxRetries: 0, // No retries needed for test initialization
	}
	// Use RLSConnector so ResetSession is called on new connections
	retryConfig.WithConnectorWrapper(func(baseConnector driver.Connector) driver.Connector {
		return rlsconn.NewRLSConnector(baseConnector, rlsConfig)
	})

	// Register the driver (first registration determines the ConnectorWrapper)
	pgxretry.Register(retryConfig)
}
