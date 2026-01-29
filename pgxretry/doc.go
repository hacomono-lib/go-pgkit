// Package pgxretry provides a PostgreSQL driver wrapper that automatically retries
// queries on specific transient errors, particularly useful for handling online DDL
// operations and cached plan errors.
//
// The driver wraps the standard pgx driver and intercepts query execution to detect
// and retry on specific PostgreSQL errors such as:
//   - cached plan must not change result type (SQLSTATE 0A000)
//   - cached plan changes during online DDL operations
//
// Basic usage:
//
//	import (
//		"database/sql"
//		"github.com/hacomono-lib/go-pgkit/pgxretry"
//	)
//
//	func main() {
//		// Register the driver with default configuration
//		pgxretry.Register(nil)
//
//		// Use the driver as normal
//		db, err := sql.Open("pgxretry", "postgres://user:pass@localhost/db")
//		if err != nil {
//			log.Fatal(err)
//		}
//		defer db.Close()
//	}
//
// Advanced usage with custom configuration:
//
//	config := &pgxretry.Config{
//		MaxRetries:     5,
//		InitialBackoff: 100 * time.Millisecond,
//		MaxBackoff:     1 * time.Second,
//		BackoffFactor:  2.0,
//	}
//	pgxretry.Register(config)
//
// The driver is safe for concurrent use and can be registered once at application
// startup. It seamlessly integrates with connection pools and supports all standard
// database/sql operations.
package pgxretry
