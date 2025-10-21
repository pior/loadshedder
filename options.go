package loadshedder

import (
	"net/http"
	"time"
)

// Option configures a Loadshedder.
type Option func(*Loadshedder)

// WithReporter sets a reporter for observability into the load shedder's behavior.
func WithReporter(r Reporter) Option {
	return func(ls *Loadshedder) {
		ls.reporter = r
	}
}

// WithRejectionHandler sets a custom handler for rejected requests.
// If not set, a default handler returns 429 with a Retry-After header.
func WithRejectionHandler(h http.Handler) Option {
	return func(ls *Loadshedder) {
		ls.rejectionHandler = h
	}
}

// WithMaxWaitTime enables QoS-based rejection: only reject requests if the
// projected wait time exceeds the specified duration. This reduces unnecessary
// 429 responses when requests complete quickly.
//
// The projected wait time is calculated as:
//   (current_concurrency - limit) * exponential_moving_average(request_duration)
//
// If maxWaitTime is 0 (default), all requests exceeding the limit are rejected
// immediately (Phase 1 behavior).
func WithMaxWaitTime(maxWaitTime time.Duration) Option {
	return func(ls *Loadshedder) {
		ls.maxWaitTime = maxWaitTime
	}
}

// WithEMAAlpha sets the smoothing factor for the exponential moving average
// of request durations. Alpha must be between 0 and 1 (exclusive).
// Higher values give more weight to recent observations.
// Default is 0.1.
//
// This option is typically used for fine-tuning the QoS behavior.
func WithEMAAlpha(alpha float64) Option {
	return func(ls *Loadshedder) {
		// Replace the duration tracker with a new one using the specified alpha
		ls.durationTracker = newDurationTracker(alpha)
	}
}
