package conn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
)

// mockSession is a simple mock implementation of sessionForTx for testing.
type mockSession struct {
	transactionFunc      func(ctx context.Context, fn func(txCtx context.Context) error) error
	resetConnectionFunc  func(ctx context.Context) error
	callCount            int
	resetConnectionCount int
}

func (m *mockSession) Transaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	m.callCount++
	if m.transactionFunc != nil {
		return m.transactionFunc(ctx, fn)
	}
	return fn(ctx)
}

func (m *mockSession) ResetConnection(ctx context.Context) error {
	m.resetConnectionCount++
	if m.resetConnectionFunc != nil {
		return m.resetConnectionFunc(ctx)
	}
	return nil
}

type TransactionManagerTestSuite struct {
	suite.Suite
}

func TestTransactionManagerTestSuite(t *testing.T) {
	suite.Run(t, new(TransactionManagerTestSuite))
}

func (s *TransactionManagerTestSuite) newTM(mock *mockSession, opts ...func(*TransactionManager)) *TransactionManager {
	tm := &TransactionManager{
		session:    mock,
		maxRetries: 3,
		retryDelay: 10 * time.Millisecond,
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(tm)
	}
	return tm
}

func (s *TransactionManagerTestSuite) TestDoWithRetryableError() {
	ctx := context.Background()
	attempts := 0

	testFn := func(txCtx context.Context) error {
		attempts++
		if attempts < 3 {
			return pgxretry.ErrRetryableInTx
		}
		return nil
	}

	mock := &mockSession{
		transactionFunc: func(ctx context.Context, fn func(txCtx context.Context) error) error {
			return fn(ctx)
		},
	}

	tm := s.newTM(mock)
	err := tm.Do(ctx, testFn)

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), 3, attempts, "should retry 3 times")
}

func (s *TransactionManagerTestSuite) TestDoWithNonRetryableError() {
	ctx := context.Background()
	attempts := 0

	testFn := func(txCtx context.Context) error {
		attempts++
		return errors.New("syntax error at or near 'INVALID'")
	}

	mock := &mockSession{
		transactionFunc: func(ctx context.Context, fn func(txCtx context.Context) error) error {
			return fn(ctx)
		},
	}

	tm := s.newTM(mock)
	err := tm.Do(ctx, testFn)

	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "syntax error")
	assert.Equal(s.T(), 1, attempts, "should not retry for non-retryable errors")
}

func (s *TransactionManagerTestSuite) TestDoWithContextCancellation() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0

	testFn := func(txCtx context.Context) error {
		attempts++
		if attempts == 2 {
			cancel()
		}
		return pgxretry.ErrRetryableInTx
	}

	mock := &mockSession{
		transactionFunc: func(ctx context.Context, fn func(txCtx context.Context) error) error {
			return fn(ctx)
		},
	}

	tm := s.newTM(mock)
	err := tm.Do(ctx, testFn)

	assert.Error(s.T(), err)
	assert.Equal(s.T(), context.Canceled, err)
	assert.Equal(s.T(), 2, attempts, "should stop retrying when context is cancelled")
}

func (s *TransactionManagerTestSuite) TestDoWithErrRetryableInTx() {
	ctx := context.Background()
	attempts := 0

	testFn := func(txCtx context.Context) error {
		attempts++
		if attempts < 3 {
			return pgxretry.ErrRetryableInTx
		}
		return nil
	}

	mock := &mockSession{
		transactionFunc: func(ctx context.Context, fn func(txCtx context.Context) error) error {
			return fn(ctx)
		},
	}

	var logBuffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	tm := s.newTM(mock, func(tm *TransactionManager) {
		tm.logger = logger
	})
	err := tm.Do(ctx, testFn)

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), 3, attempts, "should retry 3 times")
	assert.Equal(s.T(), 2, mock.resetConnectionCount, "should reset connection for each retry")

	logs := logBuffer.String()
	assert.Contains(s.T(), logs, "retryable error detected, retrying transaction", "should log retry attempts")
	assert.Contains(s.T(), logs, "transaction succeeded after retry", "should log successful recovery")
	assert.Contains(s.T(), logs, "(3/4 attempts)", "should show final attempt number in message")
}

func (s *TransactionManagerTestSuite) TestResetConnectionError() {
	ctx := context.Background()
	attempts := 0

	testFn := func(txCtx context.Context) error {
		attempts++
		if attempts < 3 {
			return pgxretry.ErrRetryableInTx
		}
		return nil
	}

	mock := &mockSession{
		transactionFunc: func(ctx context.Context, fn func(txCtx context.Context) error) error {
			return fn(ctx)
		},
		resetConnectionFunc: func(ctx context.Context) error {
			return errors.New("reset connection failed")
		},
	}

	tm := s.newTM(mock)
	err := tm.Do(ctx, testFn)

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), 3, attempts, "should retry 3 times")
	assert.Equal(s.T(), 2, mock.resetConnectionCount, "should attempt to reset connection for each retry")
}

func (s *TransactionManagerTestSuite) TestPgxretryErrRetryableInTxCompatibility() {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Direct pgxretry.ErrRetryableInTx with errors.Is",
			err:      pgxretry.ErrRetryableInTx,
			expected: true,
		},
		{
			name:     "Wrapped pgxretry.ErrRetryableInTx with fmt.Errorf",
			err:      fmt.Errorf("wrapped: %w", pgxretry.ErrRetryableInTx),
			expected: true,
		},
		{
			name:     "Double wrapped pgxretry.ErrRetryableInTx",
			err:      fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", pgxretry.ErrRetryableInTx)),
			expected: true,
		},
		{
			name:     "WrapRetryableInTx wraps correctly",
			err:      pgxretry.WrapRetryableInTx(errors.New("connection reset by peer")),
			expected: true,
		},
		{
			name:     "Different error",
			err:      errors.New("different error"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := errors.Is(tt.err, pgxretry.ErrRetryableInTx)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *TransactionManagerTestSuite) TestTransactionManagerErrorHandling() {
	tests := []struct {
		name        string
		err         error
		shouldRetry bool
	}{
		{
			name:        "Direct ErrRetryableInTx",
			err:         pgxretry.ErrRetryableInTx,
			shouldRetry: true,
		},
		{
			name:        "Wrapped ErrRetryableInTx with fmt.Errorf",
			err:         fmt.Errorf("wrapped: %w", pgxretry.ErrRetryableInTx),
			shouldRetry: true,
		},
		{
			name:        "Cached plan error (not retryable at transaction level)",
			err:         errors.New("ERROR: cached plan must not change result type (SQLSTATE 0A000)"),
			shouldRetry: false,
		},
		{
			name:        "Non-retryable error",
			err:         errors.New("syntax error"),
			shouldRetry: false,
		},
		{
			name:        "Nil error",
			err:         nil,
			shouldRetry: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := errors.Is(tt.err, pgxretry.ErrRetryableInTx)
			s.Equal(tt.shouldRetry, result)
		})
	}
}
