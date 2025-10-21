// Package ginware provides Gin middleware for the loadshedder.
package ginware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pior/loadshedder"
)

// Middleware wraps a Gin handler with concurrency limiting.
type Middleware struct {
	limiter          *loadshedder.Limiter
	reporter         Reporter
	rejectionHandler gin.HandlerFunc
}

// Reporter provides hooks for observability into the middleware's behavior.
type Reporter interface {
	// OnAccepted is called when a request is accepted and will be processed.
	OnAccepted(c *gin.Context, current, limit int)

	// OnRejected is called when a request is rejected due to concurrency limit.
	OnRejected(c *gin.Context, current, limit int)

	// OnCompleted is called when a request finishes processing.
	OnCompleted(c *gin.Context, current, limit int, duration time.Duration)
}

// Option configures the Gin middleware.
type Option func(*Middleware)

// WithReporter sets a reporter for observability.
func WithReporter(r Reporter) Option {
	return func(m *Middleware) {
		m.reporter = r
	}
}

// WithRejectionHandler sets a custom handler for rejected requests.
// Default returns HTTP 429 with a Retry-After header.
func WithRejectionHandler(h gin.HandlerFunc) Option {
	return func(m *Middleware) {
		m.rejectionHandler = h
	}
}

// New creates a new Gin middleware with the given limiter.
func New(limiter *loadshedder.Limiter, opts ...Option) *Middleware {
	m := &Middleware{
		limiter:          limiter,
		rejectionHandler: defaultRejectionHandler,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Handler returns a Gin middleware handler function.
func (m *Middleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		if !m.limiter.Acquire() {
			// Request rejected
			if m.reporter != nil {
				m.reporter.OnRejected(c, m.limiter.Current(), m.limiter.Limit())
			}
			m.rejectionHandler(c)
			c.Abort()
			return
		}

		// Request accepted
		if m.reporter != nil {
			m.reporter.OnAccepted(c, m.limiter.Current(), m.limiter.Limit())
		}

		defer func() {
			duration := time.Since(start)
			m.limiter.Release(duration)

			if m.reporter != nil {
				m.reporter.OnCompleted(c, m.limiter.Current(), m.limiter.Limit(), duration)
			}
		}()

		c.Next()
	}
}

// defaultRejectionHandler returns a simple 429 response with Retry-After header.
func defaultRejectionHandler(c *gin.Context) {
	c.Header("Retry-After", "1")
	c.String(http.StatusTooManyRequests, "Too Many Requests\n")
}
