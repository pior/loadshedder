// Package ginloadshedder provides Gin middleware for the loadshedder.
package ginloadshedder

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pior/loadshedder"
)

// Middleware wraps a Gin handler with concurrency limiting.
type Middleware struct {
	loadshedder      *loadshedder.Loadshedder
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

// New creates a new Gin middleware with the given loadshedder.
func New(ls *loadshedder.Loadshedder, opts ...Option) *Middleware {
	m := &Middleware{
		loadshedder:      ls,
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
		token := m.loadshedder.Acquire()
		if token == nil {
			// Request rejected
			if m.reporter != nil {
				m.reporter.OnRejected(c, m.loadshedder.Current(), m.loadshedder.Limit())
			}
			m.rejectionHandler(c)
			c.Abort()
			return
		}

		// Request accepted
		if m.reporter != nil {
			m.reporter.OnAccepted(c, m.loadshedder.Current(), m.loadshedder.Limit())
		}

		defer func() {
			duration := time.Since(token.start)
			token.Release()

			if m.reporter != nil {
				m.reporter.OnCompleted(c, m.loadshedder.Current(), m.loadshedder.Limit(), duration)
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
