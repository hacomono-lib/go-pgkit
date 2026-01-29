package pgxretry

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgconn"
)

const (
	// PostgreSQL error codes
	SQLStateFeatureNotSupported          = "0A000" // feature_not_supported
	SQLStatePreparedStatementNotExist    = "26000" // invalid sql statement name
	SQLStateAdminShutdown                = "57P01" // admin_shutdown
	SQLStateInFailedSQLTransaction       = "25P02" // in_failed_sql_transaction
	SQLStateSerializationFailure         = "40001" // serialization_failure
	SQLStateDeadlockDetected             = "40P01" // deadlock_detected
	SQLStateConnectionException          = "08000" // connection_exception (class code)
	SQLStateConnectionFailure            = "08006" // connection_failure
	SQLStateSQLClientError               = "08001" // sqlclient_unable_to_establish_sqlconnection
	SQLStateConnectionDoesNotExist       = "08003" // connection_does_not_exist
	SQLStateTransactionResolutionUnknown = "08007" // transaction_resolution_unknown

	// PostgreSQL error messages
	CachedPlanErrorMessage = "cached plan must not change result type"
)

// DefaultErrorDetector detects retryable PostgreSQL errors
type DefaultErrorDetector struct{}

// NewDefaultErrorDetector creates a new default error detector
func NewDefaultErrorDetector() ErrorDetector {
	return &DefaultErrorDetector{}
}

// IsRetryableError checks if the error is retryable
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context errors first - these should not be retried
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for ErrRetryableInTx - should not be retried
	if errors.Is(err, ErrRetryableInTx) {
		return false
	}

	// Extract PostgreSQL error info
	sqlState, errMsg := extractPostgreSQLError(err)

	// Check cached plan error (0A000 with matching message)
	if sqlState == SQLStateFeatureNotSupported && strings.Contains(errMsg, CachedPlanErrorMessage) {
		return true
	}

	// Check connection errors (08xxx)
	if strings.HasPrefix(sqlState, "08") {
		return true
	}

	// Serialization failure / Deadlock (retryable)
	if sqlState == SQLStateSerializationFailure || sqlState == SQLStateDeadlockDetected {
		return true
	}

	// Prepared statement does not exist
	if sqlState == SQLStatePreparedStatementNotExist {
		return true
	}

	// Terminating connection
	if sqlState == SQLStateAdminShutdown {
		return true
	}

	// Connection errors (EOF, connection reset by peer)
	if isConnectionError(err) {
		return true
	}

	// "database is closed" error
	errStr := err.Error()
	if strings.Contains(errStr, "database is closed") || strings.Contains(errMsg, "database is closed") {
		return true
	}

	return false
}

// isConnectionError checks for connection errors (EOF, connection reset by peer, etc.) (package-private)
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for true EOF sentinel error
	if errors.Is(err, io.EOF) || err.Error() == "EOF" {
		return true
	}

	// Check for closed network connection
	if errors.Is(err, net.ErrClosed) {
		return true
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "connection reset by peer") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "database is closed")
}

// extractPostgreSQLError extracts SQLSTATE and message from PostgreSQL errors (package-private)
func extractPostgreSQLError(err error) (sqlState, message string) {
	if err == nil {
		return "", ""
	}

	// Default message
	message = err.Error()

	// Check pgconn.PgError (pgx v4/v5)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		sqlState = pgErr.SQLState()
		message = pgErr.Message
		return sqlState, message
	}

	// Try to parse SQLSTATE from message if not found
	if sqlState == "" && strings.Contains(message, "SQLSTATE") {
		// Parse from messages like "(SQLSTATE 0A000)"
		if idx := strings.Index(message, "SQLSTATE "); idx != -1 {
			// Boundary check to prevent panic
			if len(message) >= idx+14 {
				sqlState = message[idx+9 : idx+14]
			}
		}
	}

	return sqlState, message
}

// IsRetryableError checks if the error is retryable
func (d *DefaultErrorDetector) IsRetryableError(err error) bool {
	return IsRetryableError(err)
}
