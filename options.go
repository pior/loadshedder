package loadshedder

import "net/http"

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
