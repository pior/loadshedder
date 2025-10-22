package loadshedder

import (
	"log/slog"
	"net/http"
)

// LogReporter is a Reporter implementation that logs events using slog.
// It tracks request latency by recording start times for accepted requests.
type LogReporter struct {
	logger *slog.Logger
}

// NewLogReporter creates a new slog-based reporter.
// If logger is nil, slog.Default() is used.
func NewLogReporter(logger *slog.Logger) *LogReporter {
	if logger == nil {
		logger = slog.Default()
	}

	return &LogReporter{
		logger: logger,
	}
}

func (r *LogReporter) Accepted(req *http.Request, stats Stats) {
	r.logger.InfoContext(
		req.Context(),
		"Request accepted",
		slog.String("method", req.Method),
		slog.String("path", req.URL.Path),
		slog.String("remote_addr", req.RemoteAddr),
		slog.Int64("running", stats.Running),
		slog.Int64("waiting", stats.Waiting),
		slog.Int64("limit", stats.Limit),
		slog.Float64("utilization", float64(stats.Running)/float64(stats.Limit)),
		slog.Duration("wait_time", stats.WaitTime),
	)
}

func (r *LogReporter) Rejected(req *http.Request, stats Stats) {
	r.logger.WarnContext(
		req.Context(),
		"Request rejected",
		slog.String("method", req.Method),
		slog.String("path", req.URL.Path),
		slog.String("remote_addr", req.RemoteAddr),
		slog.Int64("running", stats.Running),
		slog.Int64("waiting", stats.Waiting),
		slog.Int64("limit", stats.Limit),
		slog.Float64("utilization", float64(stats.Running)/float64(stats.Limit)),
		slog.Duration("wait_time", stats.WaitTime),
	)
}
