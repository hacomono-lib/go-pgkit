package pgxretry

import (
	"math"
	"time"
)

// ExponentialBackoff implements an exponential backoff retry strategy
type ExponentialBackoff struct {
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	factor         float64
}

// NewExponentialBackoff creates a new exponential backoff strategy
func NewExponentialBackoff(maxAttempts int, initial, maxBackoff time.Duration, factor float64) RetryStrategy {
	return &ExponentialBackoff{
		maxAttempts:    maxAttempts,
		initialBackoff: initial,
		maxBackoff:     maxBackoff,
		factor:         factor,
	}
}

// NextBackoff returns the next backoff duration
func (e *ExponentialBackoff) NextBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= e.maxAttempts {
		return -1
	}

	backoff := float64(e.initialBackoff) * math.Pow(e.factor, float64(attempt))
	if backoff > float64(e.maxBackoff) {
		backoff = float64(e.maxBackoff)
	}

	return time.Duration(backoff)
}

// FixedBackoff implements a fixed backoff retry strategy
type FixedBackoff struct {
	maxAttempts int
	backoff     time.Duration
}

// NewFixedBackoff creates a new fixed backoff strategy
func NewFixedBackoff(maxAttempts int, backoff time.Duration) RetryStrategy {
	return &FixedBackoff{
		maxAttempts: maxAttempts,
		backoff:     backoff,
	}
}

// NextBackoff returns the next backoff duration
func (f *FixedBackoff) NextBackoff(attempt int) time.Duration {
	if attempt >= f.maxAttempts {
		return -1
	}
	return f.backoff
}

// NoRetry implements a no-retry strategy
type NoRetry struct{}

// NewNoRetry creates a strategy that never retries
func NewNoRetry() RetryStrategy {
	return &NoRetry{}
}

// NextBackoff always returns -1 (no retry)
func (n *NoRetry) NextBackoff(attempt int) time.Duration {
	return -1
}
