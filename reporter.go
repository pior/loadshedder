package loadshedder

import (
	"net/http"
	"time"
)

// Reporter provides hooks for observability into the load shedder's behavior.
// All methods receive the original request for context-aware logging/metrics.
type Reporter interface {
	// OnAccepted is called when a request is accepted and will be processed.
	// current is the number of concurrent requests after accepting this one.
	OnAccepted(r *http.Request, current, limit int)

	// OnRejected is called when a request is rejected due to concurrency limit.
	// current is the number of concurrent requests at the time of rejection.
	OnRejected(r *http.Request, current, limit int)

	// OnCompleted is called when a request finishes processing.
	// current is the number of concurrent requests after this one completes.
	// duration is how long the request took to process.
	OnCompleted(r *http.Request, current, limit int, duration time.Duration)
}
