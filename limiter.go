package loadshedder

import (
	"sync/atomic"
	"time"
)

// Token represents an acquired slot in the loadshedder.
// It must be released by calling Release() when the operation completes.
type Token struct {
	loadshedder *Loadshedder
	start       time.Time
}

// Release releases the token back to the loadshedder.
// This should be called when the operation completes, typically in a defer.
func (t *Token) Release() {
	duration := time.Since(t.start)
	t.loadshedder.current.Add(-1)
	t.loadshedder.durationTracker.record(duration)
}

// Loadshedder is a framework-agnostic concurrency limiter.
// It tracks concurrent operations and determines whether new operations
// should be accepted or rejected based on the configured limit and QoS settings.
type Loadshedder struct {
	limit           int
	current         atomic.Int64
	durationTracker *durationTracker

	// QoS: Projected wait time limiting
	maxWaitTime time.Duration // If > 0, only reject if projected wait exceeds this
}

// New creates a new concurrency limiter with the specified limit.
// The limit must be positive.
func New(limit int, opts ...Option) *Loadshedder {
	if limit <= 0 {
		panic("loadshedder: limit must be positive")
	}

	l := &Loadshedder{
		limit:           limit,
		durationTracker: newDurationTracker(0.1), // Default alpha = 0.1
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

// Acquire attempts to acquire a slot for processing.
// Returns a Token if the slot was acquired, nil if the request should be rejected.
// If a token is returned, the caller MUST call token.Release() when done, typically in a defer.
func (l *Loadshedder) Acquire() *Token {
	current := l.current.Add(1)

	// Check if we exceeded the limit
	if current > int64(l.limit) {
		// QoS: If maxWaitTime is configured, check projected wait time
		if l.maxWaitTime > 0 {
			projectedWait := l.calculateProjectedWaitTime(int(current))

			// Only reject if projected wait exceeds the threshold
			if projectedWait <= l.maxWaitTime {
				// Accept even though we're over the limit
				return &Token{loadshedder: l, start: time.Now()}
			}
		}

		// Release the slot immediately (hard rejection)
		l.current.Add(-1)
		return nil
	}

	// Accepted (under limit)
	return &Token{loadshedder: l, start: time.Now()}
}

// Current returns the current number of concurrent operations.
func (l *Loadshedder) Current() int {
	return int(l.current.Load())
}

// Limit returns the configured concurrency limit.
func (l *Loadshedder) Limit() int {
	return l.limit
}

// calculateProjectedWaitTime estimates how long a request would wait in queue.
// Formula: (current_concurrency - limit) * avg_request_duration
func (l *Loadshedder) calculateProjectedWaitTime(current int) time.Duration {
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

// Option configures a Loadshedder.
type Option func(*Loadshedder)

// WithMaxWaitTime enables QoS-based rejection: only reject requests if the
// projected wait time exceeds the specified duration.
func WithMaxWaitTime(maxWaitTime time.Duration) Option {
	return func(l *Loadshedder) {
		l.maxWaitTime = maxWaitTime
	}
}

// WithEMAAlpha sets the smoothing factor for the exponential moving average
// of request durations. Alpha must be between 0 and 1 (exclusive).
func WithEMAAlpha(alpha float64) Option {
	return func(l *Loadshedder) {
		l.durationTracker = newDurationTracker(alpha)
	}
}
