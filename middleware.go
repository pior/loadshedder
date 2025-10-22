package loadshedder

import (
	"net/http"
	"strconv"
)

// RejectionHandler is a function that receives Stats and returns an http.HandlerFunc
// to handle rejected requests. This allows customizing the rejection response based
// on current concurrency state.
type RejectionHandler func(Stats) http.HandlerFunc

// Middleware wraps an http.Handler with concurrency limiting.
type Middleware struct {
	loadshedder      *Loadshedder
	reporter         Reporter
	rejectionHandler RejectionHandler
}

// Reporter provides hooks for observability into the middleware's behavior.
type Reporter interface {
	// Accepted is called when a request is accepted and will be processed.
	Accepted(*http.Request, Stats)

	// Rejected is called when a request is rejected due to concurrency limit.
	Rejected(*http.Request, Stats)
}

// NewMiddleware creates a new HTTP middleware with the given loadshedder, reporter, and rejection handler.
// If reporter is nil, a NullReporter is used (no observability).
// If rejectionHandler is nil, a default handler responding with HTTP 429, and a Retry-After header set to 5s is used.
func NewMiddleware(loadshedder *Loadshedder, reporter Reporter, rejectionHandler RejectionHandler) *Middleware {
	if reporter == nil {
		reporter = NewNullReporter()
	}
	if rejectionHandler == nil {
		retryAfter := 5
		rejectionHandler = NewRejectionHandler(retryAfter)
	}

	return &Middleware{
		loadshedder:      loadshedder,
		reporter:         reporter,
		rejectionHandler: rejectionHandler,
	}
}

// Handler wraps the given http.Handler with concurrency limiting.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats, token := m.loadshedder.Acquire(r.Context())
		if !token.Accepted() {
			m.reporter.Rejected(r, stats)
			m.rejectionHandler(stats).ServeHTTP(w, r)
			return
		}

		defer m.loadshedder.Release(token)

		m.reporter.Accepted(r, stats)
		next.ServeHTTP(w, r)
	})
}

// NewRejectionHandler creates a rejection handler function that responds with HTTP 429
// and a Retry-After header. The handler receives Stats which can be used to customize
// the response.
func NewRejectionHandler(retryAfterSeconds int) RejectionHandler {
	retryAfter := strconv.Itoa(retryAfterSeconds)
	return func(_ Stats) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", retryAfter)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("Too Many Requests\n"))
		}
	}
}
