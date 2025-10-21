package loadshedder

import (
	"net/http"
	"time"
)

// Middleware wraps an http.Handler with concurrency limiting.
type Middleware struct {
	loadshedder      *Loadshedder
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

// MiddlewareOption configures the HTTP middleware.
type MiddlewareOption func(*Middleware)

// WithReporter sets a reporter for observability.
func WithReporter(r Reporter) MiddlewareOption {
	return func(m *Middleware) {
		m.reporter = r
	}
}

// WithRejectionHandler sets a custom handler for rejected requests.
// Default returns HTTP 429 with a Retry-After header.
func WithRejectionHandler(h http.Handler) MiddlewareOption {
	return func(m *Middleware) {
		m.rejectionHandler = h
	}
}

// NewMiddleware creates a new HTTP middleware with the given loadshedder.
func NewMiddleware(loadshedder *Loadshedder, opts ...MiddlewareOption) *Middleware {
	m := &Middleware{
		loadshedder:      loadshedder,
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
		token := m.loadshedder.Acquire()
		defer token.Release()

		if !token.Accepted() {
			// Request rejected
			if m.reporter != nil {
				m.reporter.OnRejected(r, m.loadshedder.Current(), m.loadshedder.Limit())
			}
			m.rejectionHandler.ServeHTTP(w, r)
			return
		}

		// Request accepted
		if m.reporter != nil {
			m.reporter.OnAccepted(r, m.loadshedder.Current(), m.loadshedder.Limit())
		}

		defer func() {
			// Get duration from start embedded in token
			start := token.start
			duration := time.Since(start)

			if m.reporter != nil {
				m.reporter.OnCompleted(r, m.loadshedder.Current(), m.loadshedder.Limit(), duration)
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
