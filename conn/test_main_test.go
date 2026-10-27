package conn

import (
	"os"
	"testing"
)

// TestMain is a special Go test function that runs before and after all tests in the package.
//
// Execution order:
//  1. TestMain is called
//  2. Setup (environment variables, etc.)
//  3. m.Run() executes all tests
//  4. Teardown (cleanup, etc.)
//  5. os.Exit(code)
func TestMain(m *testing.M) {
	// Set default environment variables for local development (matches compose.yaml)
	setDefaultEnv()

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
