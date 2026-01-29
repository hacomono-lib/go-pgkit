package pgxretry

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors
var (
	// ErrDriver is the base error for driver-related errors
	ErrDriver = errors.New("pgxretry driver error")

	// ErrRetryableInTx indicates a retryable error occurred within a transaction
	ErrRetryableInTx = errors.New("retryable error in transaction")

	// ErrMaxRetriesExceeded indicates the maximum number of retries was exceeded
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")

	// ErrInvalidConfig indicates invalid configuration
	ErrInvalidConfig = errors.New("invalid configuration")
)

// DriverError wraps an error with driver-specific context
type DriverError struct {
	Err    error
	Reason string
}

func (e *DriverError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s: %s: %v", ErrDriver, e.Reason, e.Err)
	}
	return fmt.Sprintf("%s: %v", ErrDriver, e.Err)
}

func (e *DriverError) Unwrap() error {
	return e.Err
}

// WrapDriverError creates a new driver error with reason
func WrapDriverError(err error, reason string) error {
	if err == nil {
		return nil
	}
	return &DriverError{
		Err:    err,
		Reason: reason,
	}
}

// RetryableInTxError wraps an error that occurred in a transaction but is retryable
type RetryableInTxError struct {
	Cause error
}

func (e *RetryableInTxError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrRetryableInTx.Error()
	}
	causeStr := e.Cause.Error()
	// Remove leading ": " from PgError if present
	causeStr = strings.TrimPrefix(causeStr, ": ")
	return fmt.Sprintf("%s: %s", ErrRetryableInTx.Error(), causeStr)
}

func (e *RetryableInTxError) Unwrap() error {
	return e.Cause
}

func (e *RetryableInTxError) Is(target error) bool {
	return target == ErrRetryableInTx
}

// WrapRetryableInTx wraps an error as a retryable error in transaction
func WrapRetryableInTx(err error) error {
	if err == nil {
		return nil
	}
	return &RetryableInTxError{Cause: err}
}
