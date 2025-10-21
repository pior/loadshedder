package loadshedder

import (
	"sync/atomic"
	"time"
)

// Limiter is a framework-agnostic concurrency limiter.
// It tracks concurrent operations and determines whether new operations
// should be accepted or rejected based on the configured limit and QoS settings.
type Limiter struct {
	limit           int
	current         atomic.Int64
	durationTracker *durationTracker

	// QoS: Projected wait time limiting
	maxWaitTime time.Duration // If > 0, only reject if projected wait exceeds this
}

// NewLimiter creates a new concurrency limiter with the specified limit.
// The limit must be positive.
func NewLimiter(limit int, opts ...LimiterOption) *Limiter {
	if limit <= 0 {
		panic("loadshedder: limit must be positive")
	}

	l := &Limiter{
		limit:           limit,
		durationTracker: newDurationTracker(0.1), // Default alpha = 0.1
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

// Acquire attempts to acquire a slot for processing.
// Returns true if the slot was acquired, false if the request should be rejected.
// If acquired, the caller MUST call Release() when done, typically in a defer.
func (l *Limiter) Acquire() bool {
	current := l.current.Add(1)

	// Check if we exceeded the limit
	if current > int64(l.limit) {
		// QoS: If maxWaitTime is configured, check projected wait time
		if l.maxWaitTime > 0 {
			projectedWait := l.calculateProjectedWaitTime(int(current))

			// Only reject if projected wait exceeds the threshold
			if projectedWait <= l.maxWaitTime {
				// Accept even though we're over the limit
				return true
			}
		}

		// Release the slot immediately (hard rejection)
		l.current.Add(-1)
		return false
	}

	// Accepted (under limit)
	return true
}

// Release releases a previously acquired slot.
// This should be called when the operation completes, typically in a defer.
func (l *Limiter) Release(duration time.Duration) {
	l.current.Add(-1)
	l.durationTracker.record(duration)
}

// Current returns the current number of concurrent operations.
func (l *Limiter) Current() int {
	return int(l.current.Load())
}

// Limit returns the configured concurrency limit.
func (l *Limiter) Limit() int {
	return l.limit
}

// calculateProjectedWaitTime estimates how long a request would wait in queue.
// Formula: (current_concurrency - limit) * avg_request_duration
func (l *Limiter) calculateProjectedWaitTime(current int) time.Duration {
	if current <= l.limit {
		return 0
	}

	avgDuration := l.durationTracker.average()
	if avgDuration == 0 {
		// No historical data yet, assume zero wait
		return 0
	}

	queueDepth := current - l.limit
	return time.Duration(queueDepth) * avgDuration
}

// LimiterOption configures a Limiter.
type LimiterOption func(*Limiter)

// WithMaxWaitTime enables QoS-based rejection: only reject requests if the
// projected wait time exceeds the specified duration.
func WithMaxWaitTime(maxWaitTime time.Duration) LimiterOption {
	return func(l *Limiter) {
		l.maxWaitTime = maxWaitTime
	}
}

// WithEMAAlpha sets the smoothing factor for the exponential moving average
// of request durations. Alpha must be between 0 and 1 (exclusive).
func WithEMAAlpha(alpha float64) LimiterOption {
	return func(l *Limiter) {
		l.durationTracker = newDurationTracker(alpha)
	}
}
