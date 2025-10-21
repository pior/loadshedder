// Package httpware provides net/http middleware for the loadshedder.
package httpware

import (
	"net/http"
	"time"

	"github.com/pior/loadshedder"
)

// Middleware wraps an http.Handler with concurrency limiting.
type Middleware struct {
	limiter          *loadshedder.Limiter
	reporter         Reporter
	rejectionHandler http.Handler
}

// Reporter provides hooks for observability into the middleware's behavior.
type Reporter interface {
	// OnAccepted is called when a request is accepted and will be processed.
	OnAccepted(r *http.Request, current, limit int)

	// OnRejected is called when a request is rejected due to concurrency limit.
	OnRejected(r *http.Request, current, limit int)

	// OnCompleted is called when a request finishes processing.
	OnCompleted(r *http.Request, current, limit int, duration time.Duration)
}

// Option configures the HTTP middleware.
type Option func(*Middleware)

// WithReporter sets a reporter for observability.
func WithReporter(r Reporter) Option {
	return func(m *Middleware) {
		m.reporter = r
	}
}

// WithRejectionHandler sets a custom handler for rejected requests.
// Default returns HTTP 429 with a Retry-After header.
func WithRejectionHandler(h http.Handler) Option {
	return func(m *Middleware) {
		m.rejectionHandler = h
	}
}

// New creates a new HTTP middleware with the given limiter.
func New(limiter *loadshedder.Limiter, opts ...Option) *Middleware {
	m := &Middleware{
		limiter:          limiter,
		rejectionHandler: defaultRejectionHandler(),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Handler wraps the given http.Handler with concurrency limiting.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if !m.limiter.Acquire() {
			// Request rejected
			if m.reporter != nil {
				m.reporter.OnRejected(r, m.limiter.Current(), m.limiter.Limit())
			}
			m.rejectionHandler.ServeHTTP(w, r)
			return
		}

		// Request accepted
		if m.reporter != nil {
			m.reporter.OnAccepted(r, m.limiter.Current(), m.limiter.Limit())
		}

		defer func() {
			duration := time.Since(start)
			m.limiter.Release(duration)

			if m.reporter != nil {
				m.reporter.OnCompleted(r, m.limiter.Current(), m.limiter.Limit(), duration)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// defaultRejectionHandler returns a simple 429 response with Retry-After header.
func defaultRejectionHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Too Many Requests\n"))
	})
}
