package loadshedder

import (
	"math"
	"sync/atomic"
	"time"
)

// durationTracker tracks the exponential moving average (EMA) of request durations.
// It provides thread-safe updates using atomic operations.
type durationTracker struct {
	avgDuration atomic.Uint64 // Exponential moving average of request duration (nanoseconds)
	alpha       float64       // Smoothing factor for EMA (between 0 and 1, exclusive)
}

// newDurationTracker creates a new duration tracker with the given smoothing factor.
// The alpha parameter controls how much weight is given to recent observations.
// Higher values (closer to 1) react faster to changes but are more volatile.
// Lower values (closer to 0) are more stable but slower to adapt.
func newDurationTracker(alpha float64) *durationTracker {
	if alpha <= 0 || alpha >= 1 {
		panic("loadshedder: alpha must be between 0 and 1 (exclusive)")
	}
	return &durationTracker{
		alpha: alpha,
	}
}

// record updates the exponential moving average with a new request duration.
// Uses the formula: EMA_new = alpha * current + (1 - alpha) * EMA_old
// This method is thread-safe and uses atomic compare-and-swap operations.
func (dt *durationTracker) record(duration time.Duration) {
	nanos := uint64(duration.Nanoseconds())

	for {
		oldAvg := dt.avgDuration.Load()

		var newAvg uint64
		if oldAvg == 0 {
			// First measurement, use it directly
			newAvg = nanos
		} else {
			// Apply exponential moving average
			newAvgFloat := dt.alpha*float64(nanos) + (1-dt.alpha)*float64(oldAvg)
			newAvg = uint64(math.Round(newAvgFloat))
		}

		if dt.avgDuration.CompareAndSwap(oldAvg, newAvg) {
			break
		}
	}
}

// average returns the current average duration.
// Returns 0 if no durations have been recorded yet.
func (dt *durationTracker) average() time.Duration {
	nanos := dt.avgDuration.Load()
	return time.Duration(nanos)
}
