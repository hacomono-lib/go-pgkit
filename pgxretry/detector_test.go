package pgxretry_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/hacomono-lib/go-pgkit/pgxretry"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/suite"
)

type DetectorTestSuite struct {
	suite.Suite
}

func TestDetectorSuite(t *testing.T) {
	suite.Run(t, new(DetectorTestSuite))
}

func (s *DetectorTestSuite) TestDefaultErrorDetector_IsRetryableError() {
	detector := pgxretry.NewDefaultErrorDetector()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "cached plan error with pgconn",
			err: &pgconn.PgError{
				Code:    pgxretry.SQLStateFeatureNotSupported,
				Message: "cached plan must not change result type",
			},
			want: true,
		},
		{
			name: "connection error (08xxx)",
			err: &pgconn.PgError{
				Code: pgxretry.SQLStateConnectionFailure,
			},
			want: true,
		},
		{
			name: "prepared statement not exist",
			err: &pgconn.PgError{
				Code: pgxretry.SQLStatePreparedStatementNotExist,
			},
			want: true,
		},
		{
			name: "admin shutdown",
			err: &pgconn.PgError{
				Code: pgxretry.SQLStateAdminShutdown,
			},
			want: true,
		},
		{
			name: "serialization failure with pgconn",
			err: &pgconn.PgError{
				Code: pgxretry.SQLStateSerializationFailure,
			},
			want: true,
		},
		{
			name: "deadlock detected with pgconn",
			err: &pgconn.PgError{
				Code: pgxretry.SQLStateDeadlockDetected,
			},
			want: true,
		},
		{
			name: "EOF error",
			err:  io.EOF,
			want: true,
		},
		{
			name: "closed network connection",
			err:  net.ErrClosed,
			want: true,
		},
		{
			name: "connection reset by peer",
			err:  errors.New("read: connection reset by peer"),
			want: true,
		},
		{
			name: "broken pipe",
			err:  errors.New("write: broken pipe"),
			want: true,
		},
		{
			name: "connection refused",
			err:  errors.New("dial tcp: connection refused"),
			want: true,
		},
		{
			name: "database is closed",
			err:  errors.New("database is closed"),
			want: true,
		},
		{
			name: "non-retryable error",
			err: &pgconn.PgError{
				Code:    "23505", // unique violation
				Message: "duplicate key value",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got := detector.IsRetryableError(tt.err)
			s.Equal(tt.want, got)
		})
	}
}

// Custom error detector for testing
type customErrorDetector struct {
	pgxretry.DefaultErrorDetector
	customErrors []string
}

func (d *customErrorDetector) IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check custom errors first
	errMsg := err.Error()
	for _, customErr := range d.customErrors {
		if errMsg == customErr {
			return true
		}
	}

	// Fall back to default detection
	return d.DefaultErrorDetector.IsRetryableError(err)
}

func (s *DetectorTestSuite) TestIsRetryableError() {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error is not retryable",
			err:  nil,
			want: false,
		},
		{
			name: "regular error is not retryable",
			err:  errors.New("some error"),
			want: false,
		},
		{
			name: "ErrRetryableInTx is not retryable",
			err:  pgxretry.ErrRetryableInTx,
			want: false,
		},
		{
			name: "wrapped ErrRetryableInTx is not retryable",
			err:  errors.Join(errors.New("wrapper"), pgxretry.ErrRetryableInTx),
			want: false,
		},
		{
			name: "context.Canceled is not retryable",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "context.DeadlineExceeded is not retryable",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "wrapped context.Canceled is not retryable",
			err:  fmt.Errorf("operation failed: %w", context.Canceled),
			want: false,
		},
		{
			name: "wrapped context.DeadlineExceeded is not retryable",
			err:  fmt.Errorf("timeout occurred: %w", context.DeadlineExceeded),
			want: false,
		},
		{
			name: "EOF error is retryable",
			err:  io.EOF,
			want: true,
		},
		{
			name: "closed network connection is retryable",
			err:  net.ErrClosed,
			want: true,
		},
		{
			name: "connection reset by peer is retryable",
			err:  errors.New("read: connection reset by peer"),
			want: true,
		},
		{
			name: "connection error (08xxx) is retryable",
			err: &pgconn.PgError{
				Code: pgxretry.SQLStateConnectionFailure,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got := pgxretry.IsRetryableError(tt.err)
			s.Equal(tt.want, got)
		})
	}
}

func (s *DetectorTestSuite) TestCustomErrorDetector() {
	detector := &customErrorDetector{
		customErrors: []string{"my custom error", "another custom error"},
	}

	s.Run("detects custom errors", func() {
		s.True(detector.IsRetryableError(errors.New("my custom error")))
		s.True(detector.IsRetryableError(errors.New("another custom error")))
	})

	s.Run("still detects default errors", func() {
		s.True(detector.IsRetryableError(io.EOF))
	})

	s.Run("does not detect non-retryable errors", func() {
		s.False(detector.IsRetryableError(errors.New("unrelated error")))
	})
}
