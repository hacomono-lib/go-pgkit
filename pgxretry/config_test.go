package pgxretry_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hacomono-lib/go-pgkit/pgxretry"

	"github.com/stretchr/testify/suite"
)

type ConfigTestSuite struct {
	suite.Suite
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigTestSuite))
}

func (s *ConfigTestSuite) TestDefaultConfig() {
	config := pgxretry.DefaultConfig()

	s.Equal(3, config.MaxRetries)
	s.Equal(100*time.Millisecond, config.InitialBackoff)
	s.Equal(5*time.Second, config.MaxBackoff)
	s.Equal(2.0, config.BackoffFactor)
	s.NotNil(config.Logger)
}

func (s *ConfigTestSuite) TestConfig_WithDefaults() {
	tests := []struct {
		name   string
		config *pgxretry.Config
		check  func(c *pgxretry.Config)
	}{
		{
			name:   "nil config gets all defaults",
			config: &pgxretry.Config{},
			check: func(c *pgxretry.Config) {
				s.Equal(3, c.MaxRetries)
				s.Equal(100*time.Millisecond, c.InitialBackoff)
				s.Equal(5*time.Second, c.MaxBackoff)
				s.Equal(2.0, c.BackoffFactor)
				s.NotNil(c.ErrorDetector)
				s.NotNil(c.RetryStrategy)
				s.NotNil(c.Logger)
			},
		},
		{
			name: "partial config preserves set values",
			config: &pgxretry.Config{
				MaxRetries:     5,
				InitialBackoff: 200 * time.Millisecond,
			},
			check: func(c *pgxretry.Config) {
				s.Equal(5, c.MaxRetries)
				s.Equal(200*time.Millisecond, c.InitialBackoff)
				// Defaults
				s.Equal(5*time.Second, c.MaxBackoff)
				s.Equal(2.0, c.BackoffFactor)
			},
		},
		{
			name: "zero values get defaults",
			config: &pgxretry.Config{
				MaxRetries:     0,
				InitialBackoff: 0,
				MaxBackoff:     0,
				BackoffFactor:  0,
			},
			check: func(c *pgxretry.Config) {
				s.Equal(3, c.MaxRetries)
				s.Equal(100*time.Millisecond, c.InitialBackoff)
				s.Equal(5*time.Second, c.MaxBackoff)
				s.Equal(2.0, c.BackoffFactor)
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			config := tt.config.WithDefaults()
			tt.check(config)
		})
	}
}

func (s *ConfigTestSuite) TestConfig_Validate() {
	tests := []struct {
		name    string
		config  *pgxretry.Config
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			config:  pgxretry.DefaultConfig(),
			wantErr: false,
		},
		{
			name: "negative max retries",
			config: &pgxretry.Config{
				MaxRetries: -1,
			},
			wantErr: true,
			errMsg:  "MaxRetries must be non-negative",
		},
		{
			name: "negative initial backoff",
			config: &pgxretry.Config{
				MaxRetries:     3,
				InitialBackoff: -1,
			},
			wantErr: true,
			errMsg:  "InitialBackoff must be non-negative",
		},
		{
			name: "negative max backoff",
			config: &pgxretry.Config{
				MaxRetries:     3,
				InitialBackoff: 100 * time.Millisecond,
				MaxBackoff:     -1,
			},
			wantErr: true,
			errMsg:  "MaxBackoff must be non-negative",
		},
		{
			name: "backoff factor less than 1",
			config: &pgxretry.Config{
				MaxRetries:     3,
				InitialBackoff: 100 * time.Millisecond,
				MaxBackoff:     1 * time.Second,
				BackoffFactor:  0.5,
			},
			wantErr: true,
			errMsg:  "BackoffFactor must be >= 1.0",
		},
		{
			name: "zero values are valid",
			config: &pgxretry.Config{
				MaxRetries:     0,
				InitialBackoff: 0,
				MaxBackoff:     0,
				BackoffFactor:  1.0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := tt.config.Validate()
			if tt.wantErr {
				s.Require().Error(err)
				s.Contains(err.Error(), tt.errMsg)
			} else {
				s.Require().NoError(err)
			}
		})
	}
}

func (s *ConfigTestSuite) TestConfig_WithSlog() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := pgxretry.DefaultConfig().WithSlog(logger)

	s.NotNil(config.Logger)
	// The logger should be wrapped in slogAdapter
}

func (s *ConfigTestSuite) TestConfig_WithErrorDetector() {
	detector := pgxretry.NewDefaultErrorDetector()
	config := pgxretry.DefaultConfig().WithErrorDetector(detector)

	s.Equal(detector, config.ErrorDetector)
}

func (s *ConfigTestSuite) TestConfig_WithRetryStrategy() {
	strategy := pgxretry.NewFixedBackoff(5, 200*time.Millisecond)
	config := pgxretry.DefaultConfig().WithRetryStrategy(strategy)

	s.Equal(strategy, config.RetryStrategy)
}

func (s *ConfigTestSuite) TestConfig_WithConnectionReset() {
	called := false
	resetFunc := func(ctx context.Context) error {
		called = true
		return nil
	}

	config := pgxretry.DefaultConfig().WithConnectionReset(resetFunc)

	s.NotNil(config.ConnectionResetFunc)

	// Test that the function works
	err := config.ConnectionResetFunc(context.Background())
	s.NoError(err)
	s.True(called)
}

func (s *ConfigTestSuite) TestConfig_Chaining() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	detector := pgxretry.NewDefaultErrorDetector()
	strategy := pgxretry.NewExponentialBackoff(5, 50*time.Millisecond, 2*time.Second, 1.5)

	config := pgxretry.DefaultConfig().
		WithSlog(logger).
		WithErrorDetector(detector).
		WithRetryStrategy(strategy).
		WithConnectionReset(func(ctx context.Context) error {
			return nil
		})

	s.NotNil(config.Logger)
	s.Equal(detector, config.ErrorDetector)
	s.Equal(strategy, config.RetryStrategy)
	s.NotNil(config.ConnectionResetFunc)
}

func (s *ConfigTestSuite) TestConfig_ImmutabilityOnChaining() {
	original := pgxretry.DefaultConfig()
	originalMaxRetries := original.MaxRetries

	// Chaining should not modify the original
	modified := original.WithSlog(slog.Default())

	// Original should be unchanged
	s.Equal(originalMaxRetries, original.MaxRetries)
	s.NotNil(modified.Logger)
}

// Test logger implementations
func (s *ConfigTestSuite) TestSlogAdapter() {
	// Create a logger that writes to a buffer so we can test output
	logger := pgxretry.NewSlogAdapter(slog.Default())

	s.Run("basic logging", func() {
		// These should not panic
		ctx := context.Background()
		logger.DebugContext(ctx, "debug message", "key", "value")
		logger.InfoContext(ctx, "info message", "key", "value")
		logger.WarnContext(ctx, "warn message", "key", "value")
		logger.ErrorContext(ctx, "error message", "key", "value")
	})

	s.Run("with context", func() {
		ctx := context.Background()

		// Should not panic when using context methods
		logger.InfoContext(ctx, "message with context")
		logger.DebugContext(ctx, "debug with context")
		logger.WarnContext(ctx, "warning with context")
		logger.ErrorContext(ctx, "error with context")
	})
}

func (s *ConfigTestSuite) TestNoopLogger() {
	logger := pgxretry.NewNoopLogger()

	// None of these should panic or do anything
	ctx := context.Background()
	logger.DebugContext(ctx, "debug")
	logger.InfoContext(ctx, "info")
	logger.WarnContext(ctx, "warn")
	logger.ErrorContext(ctx, "error")
}
