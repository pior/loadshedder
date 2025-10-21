package loadshedder

import (
	"math"
	"net/http"
	"sync/atomic"
	"time"
)

// Loadshedder is an HTTP middleware that limits concurrent request processing.
// When the concurrency limit is reached, additional requests are rejected with 429.
type Loadshedder struct {
	limit            int
	current          atomic.Int64
	reporter         Reporter
	rejectionHandler http.Handler

	// QoS: Projected wait time limiting
	maxWaitTime time.Duration     // If > 0, only reject if projected wait exceeds this
	avgDuration atomic.Uint64     // Exponential moving average of request duration (nanoseconds)
	emaAlpha    float64           // Smoothing factor for EMA (default 0.1)
}

// New creates a new Loadshedder middleware with the specified concurrency limit.
// The limit must be positive.
func New(limit int, opts ...Option) *Loadshedder {
	if limit <= 0 {
		panic("loadshedder: limit must be positive")
	}

	ls := &Loadshedder{
		limit:            limit,
		rejectionHandler: defaultRejectionHandler(),
		emaAlpha:         0.1, // Default smoothing factor
	}

	for _, opt := range opts {
		opt(ls)
	}

	return ls
}

// Middleware returns an HTTP middleware that wraps the given handler.
func (ls *Loadshedder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to acquire a slot
		current := ls.current.Add(1)

		// Check if we exceeded the limit
		if current > int64(ls.limit) {
			// QoS: If maxWaitTime is configured, check projected wait time
			if ls.maxWaitTime > 0 {
				projectedWait := ls.calculateProjectedWaitTime(int(current))

				// Only reject if projected wait exceeds the threshold
				if projectedWait <= ls.maxWaitTime {
					// Accept the request even though we're over the limit
					if ls.reporter != nil {
						ls.reporter.OnAccepted(r, int(current), ls.limit)
					}

					start := time.Now()
					defer func() {
						current := ls.current.Add(-1)
						duration := time.Since(start)
						ls.updateAvgDuration(duration)

						if ls.reporter != nil {
							ls.reporter.OnCompleted(r, int(current), ls.limit, duration)
						}
					}()

					next.ServeHTTP(w, r)
					return
				}
			}

			// Release the slot immediately (hard rejection)
			current = ls.current.Add(-1)

			if ls.reporter != nil {
				ls.reporter.OnRejected(r, int(current), ls.limit)
			}

			ls.rejectionHandler.ServeHTTP(w, r)
			return
		}

		// Request accepted (under limit)
		if ls.reporter != nil {
			ls.reporter.OnAccepted(r, int(current), ls.limit)
		}

		// Track request duration
		start := time.Now()

		// Ensure we release the slot when done
		defer func() {
			current := ls.current.Add(-1)
			duration := time.Since(start)
			ls.updateAvgDuration(duration)

			if ls.reporter != nil {
				ls.reporter.OnCompleted(r, int(current), ls.limit, duration)
			}
		}()

		// Process the request
		next.ServeHTTP(w, r)
	})
}

// ServeHTTP implements http.Handler by wrapping a nil handler.
// This allows Loadshedder to be used directly as middleware.
func (ls *Loadshedder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ls.Middleware(http.DefaultServeMux).ServeHTTP(w, r)
}

// calculateProjectedWaitTime estimates how long a request would wait in queue.
// Formula: (current_concurrency - limit) * avg_request_duration
func (ls *Loadshedder) calculateProjectedWaitTime(current int) time.Duration {
	if current <= ls.limit {
		return 0
	}

	avgNanos := ls.avgDuration.Load()
	if avgNanos == 0 {
		// No historical data yet, assume zero wait
		return 0
	}

	queueDepth := current - ls.limit
	projectedNanos := uint64(queueDepth) * avgNanos
	return time.Duration(projectedNanos)
}

// updateAvgDuration updates the exponential moving average of request duration.
// EMA formula: EMA_new = alpha * current + (1 - alpha) * EMA_old
func (ls *Loadshedder) updateAvgDuration(duration time.Duration) {
	nanos := uint64(duration.Nanoseconds())

	for {
		oldAvg := ls.avgDuration.Load()

		var newAvg uint64
		if oldAvg == 0 {
			// First measurement, use it directly
			newAvg = nanos
		} else {
			// Apply exponential moving average
			// Using integer arithmetic to avoid float precision issues
			alpha := ls.emaAlpha
			newAvgFloat := alpha*float64(nanos) + (1-alpha)*float64(oldAvg)
			newAvg = uint64(math.Round(newAvgFloat))
		}

		if ls.avgDuration.CompareAndSwap(oldAvg, newAvg) {
			break
		}
	}
}

// defaultRejectionHandler returns a simple 429 response with Retry-After header.
func defaultRejectionHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Too Many Requests\n"))
	})
}
