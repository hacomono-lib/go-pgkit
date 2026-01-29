package pgxretry_test

import (
	"testing"
	"time"

	"github.com/hacomono-lib/go-pgkit/pgxretry"

	"github.com/stretchr/testify/suite"
)

type StrategyTestSuite struct {
	suite.Suite
}

func TestStrategySuite(t *testing.T) {
	suite.Run(t, new(StrategyTestSuite))
}

func (s *StrategyTestSuite) TestExponentialBackoff() {
	s.Run("basic exponential backoff", func() {
		strategy := pgxretry.NewExponentialBackoff(3, 100*time.Millisecond, 1*time.Second, 2.0)

		// First attempt
		backoff := strategy.NextBackoff(0)
		s.Equal(100*time.Millisecond, backoff)

		// Second attempt
		backoff = strategy.NextBackoff(1)
		s.Equal(200*time.Millisecond, backoff)

		// Third attempt
		backoff = strategy.NextBackoff(2)
		s.Equal(400*time.Millisecond, backoff)

		// Exceeds max attempts
		backoff = strategy.NextBackoff(3)
		s.Equal(time.Duration(-1), backoff)
	})

	s.Run("respects max backoff", func() {
		strategy := pgxretry.NewExponentialBackoff(5, 100*time.Millisecond, 300*time.Millisecond, 2.0)

		// Should hit max at 300ms
		s.Equal(100*time.Millisecond, strategy.NextBackoff(0))
		s.Equal(200*time.Millisecond, strategy.NextBackoff(1))
		s.Equal(300*time.Millisecond, strategy.NextBackoff(2)) // capped at max
		s.Equal(300*time.Millisecond, strategy.NextBackoff(3)) // still capped
	})

	s.Run("different factor", func() {
		strategy := pgxretry.NewExponentialBackoff(4, 100*time.Millisecond, 10*time.Second, 1.5)

		s.Equal(100*time.Millisecond, strategy.NextBackoff(0))
		s.Equal(150*time.Millisecond, strategy.NextBackoff(1))
		s.Equal(225*time.Millisecond, strategy.NextBackoff(2))
		s.Equal(337500*time.Microsecond, strategy.NextBackoff(3)) // 337.5ms
	})
}

func (s *StrategyTestSuite) TestFixedBackoff() {
	s.Run("basic fixed backoff", func() {
		strategy := pgxretry.NewFixedBackoff(3, 200*time.Millisecond)

		// All attempts should return same backoff
		s.Equal(200*time.Millisecond, strategy.NextBackoff(0))
		s.Equal(200*time.Millisecond, strategy.NextBackoff(1))
		s.Equal(200*time.Millisecond, strategy.NextBackoff(2))

		// Exceeds max attempts
		s.Equal(time.Duration(-1), strategy.NextBackoff(3))
	})

	s.Run("zero backoff", func() {
		strategy := pgxretry.NewFixedBackoff(3, 0)
		s.Equal(time.Duration(0), strategy.NextBackoff(0))
		s.Equal(time.Duration(0), strategy.NextBackoff(1))
	})
}

func (s *StrategyTestSuite) TestNoRetry() {
	strategy := pgxretry.NewNoRetry()

	s.Run("always returns -1", func() {
		s.Equal(time.Duration(-1), strategy.NextBackoff(0))
		s.Equal(time.Duration(-1), strategy.NextBackoff(1))
		s.Equal(time.Duration(-1), strategy.NextBackoff(100))
	})
}

func (s *StrategyTestSuite) TestStrategyEdgeCases() {
	s.Run("exponential with factor 1.0", func() {
		strategy := pgxretry.NewExponentialBackoff(3, 100*time.Millisecond, 1*time.Second, 1.0)

		// Should always return initial backoff
		s.Equal(100*time.Millisecond, strategy.NextBackoff(0))
		s.Equal(100*time.Millisecond, strategy.NextBackoff(1))
		s.Equal(100*time.Millisecond, strategy.NextBackoff(2))
	})

	s.Run("exponential with very large factor", func() {
		strategy := pgxretry.NewExponentialBackoff(3, 1*time.Millisecond, 100*time.Millisecond, 100.0)

		s.Equal(1*time.Millisecond, strategy.NextBackoff(0))
		s.Equal(100*time.Millisecond, strategy.NextBackoff(1)) // hits max immediately
		s.Equal(100*time.Millisecond, strategy.NextBackoff(2)) // stays at max
	})

	s.Run("negative attempt number", func() {
		strategy := pgxretry.NewExponentialBackoff(3, 100*time.Millisecond, 1*time.Second, 2.0)

		// Should handle negative attempts gracefully
		s.Equal(100*time.Millisecond, strategy.NextBackoff(-1))
		s.Equal(100*time.Millisecond, strategy.NextBackoff(-10))
	})
}

// Custom strategy for testing
type customStrategy struct {
	attempts []time.Duration
}

func (s *customStrategy) NextBackoff(attempt int) time.Duration {
	if attempt >= len(s.attempts) {
		return -1
	}
	return s.attempts[attempt]
}

func (s *StrategyTestSuite) TestCustomStrategy() {
	strategy := &customStrategy{
		attempts: []time.Duration{
			10 * time.Millisecond,
			50 * time.Millisecond,
			200 * time.Millisecond,
		},
	}

	s.Run("custom backoff sequence", func() {
		s.Equal(10*time.Millisecond, strategy.NextBackoff(0))
		s.Equal(50*time.Millisecond, strategy.NextBackoff(1))
		s.Equal(200*time.Millisecond, strategy.NextBackoff(2))
		s.Equal(time.Duration(-1), strategy.NextBackoff(3))
	})
}
