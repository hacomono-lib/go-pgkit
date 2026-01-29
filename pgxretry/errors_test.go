package pgxretry_test

import (
	"errors"
	"testing"

	"github.com/hacomono-lib/go-pgkit/pgxretry"

	"github.com/stretchr/testify/suite"
)

type ErrorsTestSuite struct {
	suite.Suite
}

func TestErrorsSuite(t *testing.T) {
	suite.Run(t, new(ErrorsTestSuite))
}

func (s *ErrorsTestSuite) TestWrapDriverError() {
	tests := []struct {
		name     string
		err      error
		reason   string
		wantNil  bool
		contains string
	}{
		{
			name:    "nil error returns nil",
			err:     nil,
			reason:  "some reason",
			wantNil: true,
		},
		{
			name:     "wraps error with reason",
			err:      errors.New("original error"),
			reason:   "connection failed",
			wantNil:  false,
			contains: "connection failed",
		},
		{
			name:     "wraps error without reason",
			err:      errors.New("original error"),
			reason:   "",
			wantNil:  false,
			contains: "original error",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got := pgxretry.WrapDriverError(tt.err, tt.reason)
			if tt.wantNil {
				s.Nil(got)
			} else {
				s.NotNil(got)
				s.Contains(got.Error(), tt.contains)
				s.ErrorIs(got, tt.err)
			}
		})
	}
}

func (s *ErrorsTestSuite) TestDriverError() {
	baseErr := errors.New("base error")
	driverErr := &pgxretry.DriverError{
		Err:    baseErr,
		Reason: "test reason",
	}

	s.Run("Error message formatting", func() {
		errMsg := driverErr.Error()
		s.Contains(errMsg, "pgxretry driver error")
		s.Contains(errMsg, "test reason")
		s.Contains(errMsg, "base error")
	})

	s.Run("Unwrap returns base error", func() {
		unwrapped := driverErr.Unwrap()
		s.Equal(baseErr, unwrapped)
	})

	s.Run("Error chain works correctly", func() {
		s.ErrorIs(driverErr, baseErr)
	})
}
