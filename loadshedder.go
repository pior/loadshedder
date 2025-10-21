package loadshedder

import (
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
			// Release the slot immediately
			current = ls.current.Add(-1)

			if ls.reporter != nil {
				ls.reporter.OnRejected(r, int(current), ls.limit)
			}

			ls.rejectionHandler.ServeHTTP(w, r)
			return
		}

		// Request accepted
		if ls.reporter != nil {
			ls.reporter.OnAccepted(r, int(current), ls.limit)
		}

		// Track request duration for future QoS improvements
		start := time.Now()

		// Ensure we release the slot when done
		defer func() {
			current := ls.current.Add(-1)
			duration := time.Since(start)

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

// defaultRejectionHandler returns a simple 429 response with Retry-After header.
func defaultRejectionHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Too Many Requests\n"))
	})
}
