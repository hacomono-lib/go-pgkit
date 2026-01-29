package conn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
)

// sessionForTx defines the methods required from a session for transaction management.
// This internal interface enables testing with mocks.
type sessionForTx interface {
	Transaction(ctx context.Context, fn func(txCtx context.Context) error) error
	ResetConnection(ctx context.Context) error
}

// TransactionManager manages transaction execution with automatic retry
// for retryable errors (pgxretry.ErrRetryableInTx).
//
// It is the application-level counterpart to pgxretry's driver-level retry:
// pgxretry retries individual queries outside transactions, while
// TransactionManager retries entire transactions when the driver signals
// ErrRetryableInTx.
type TransactionManager struct {
	session    sessionForTx
	maxRetries int
	retryDelay time.Duration
	logger     *slog.Logger
}

// TransactionManagerOption configures a TransactionManager.
type TransactionManagerOption func(*TransactionManager)

// WithMaxRetries sets the maximum number of retry attempts.
// Default is 3.
func WithMaxRetries(n int) TransactionManagerOption {
	return func(tm *TransactionManager) {
		tm.maxRetries = n
	}
}

// WithRetryDelay sets the delay between retry attempts.
// Default is 1 second.
func WithRetryDelay(d time.Duration) TransactionManagerOption {
	return func(tm *TransactionManager) {
		tm.retryDelay = d
	}
}

// WithLogger sets the logger for the TransactionManager.
// Default is the WriterSession's logger.
func WithLogger(logger *slog.Logger) TransactionManagerOption {
	return func(tm *TransactionManager) {
		tm.logger = logger
	}
}

// NewTransactionManager creates a new TransactionManager for the given WriterSession.
func NewTransactionManager(session *WriterSession, opts ...TransactionManagerOption) *TransactionManager {
	tm := &TransactionManager{
		session:    session,
		maxRetries: 3,
		retryDelay: 1 * time.Second,
		logger:     session.Logger(),
	}

	for _, opt := range opts {
		opt(tm)
	}

	return tm
}

// Do executes fn within a transaction. If the transaction fails with
// pgxretry.ErrRetryableInTx, it resets the connection and retries up to
// maxRetries times. Non-retryable errors are returned immediately.
func (tm *TransactionManager) Do(ctx context.Context, fn func(txCtx context.Context) error) error {
	var lastErr error

	for attempt := 0; attempt <= tm.maxRetries; attempt++ {
		err := tm.session.Transaction(ctx, fn)

		if err == nil {
			if attempt > 0 {
				tm.logger.InfoContext(ctx, fmt.Sprintf("transaction succeeded after retry (%d/%d attempts)", attempt+1, tm.maxRetries+1))
			}
			return nil
		}

		lastErr = err

		if !errors.Is(err, pgxretry.ErrRetryableInTx) {
			return err
		}

		if attempt >= tm.maxRetries {
			break
		}

		tm.logger.WarnContext(ctx, fmt.Sprintf("retryable error detected, retrying transaction (%d/%d)", attempt+1, tm.maxRetries+1),
			"error", err,
		)

		if resetErr := tm.session.ResetConnection(ctx); resetErr != nil {
			xerr := ErrTransaction.WithReason("failed to reset connection on attempt %d, continuing with retry", attempt+1).WithCause(resetErr)
			tm.logger.ErrorContext(ctx, "failed to reset connection",
				"error", xerr,
				"attempt", attempt+1,
			)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tm.retryDelay):
		}
	}

	xerr := ErrTransaction.WithReason("transaction failed after %d retries", tm.maxRetries).WithCause(lastErr)
	tm.logger.ErrorContext(ctx, "transaction failed on final attempt",
		"error", xerr,
		"attempt", tm.maxRetries+1,
	)

	return xerr
}
