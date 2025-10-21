package loadshedder

import (
	"net/http"
)

// Middleware wraps an http.Handler with concurrency limiting.
type Middleware struct {
	loadshedder      *Loadshedder
	Reporter         Reporter
	RejectionHandler http.Handler
}

// Reporter provides hooks for observability into the middleware's behavior.
type Reporter interface {
	// OnAccepted is called when a request is accepted and will be processed.
	OnAccepted(*http.Request, Stats)

	// OnRejected is called when a request is rejected due to concurrency limit.
	OnRejected(*http.Request, Stats)

	// OnCompleted is called when a request finishes processing.
	OnCompleted(*http.Request, Stats)
}

// NewMiddleware creates a new HTTP middleware with the given loadshedder.
func NewMiddleware(loadshedder *Loadshedder) *Middleware {
	m := &Middleware{
		loadshedder:      loadshedder,
		RejectionHandler: defaultRejectionHandler(),
	}

	return m
}

// Handler wraps the given http.Handler with concurrency limiting.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats, token := m.loadshedder.Acquire(r.Context())
		if !token.Accepted() {
			// Request rejected
			if m.Reporter != nil {
				m.Reporter.OnRejected(r, stats)
			}
			m.RejectionHandler.ServeHTTP(w, r)
			return
		}

		// Request accepted

		defer func() {
			stats := m.loadshedder.Release(token)

			if m.Reporter != nil {
				m.Reporter.OnCompleted(r, stats)
			}
		}()

		if m.Reporter != nil {
			m.Reporter.OnAccepted(r, stats)
		}

		next.ServeHTTP(w, r)
	})
}

// defaultRejectionHandler returns a simple 429 response with Retry-After header set to 1s.
func defaultRejectionHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Too Many Requests\n"))
	})
}
