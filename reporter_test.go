package loadshedder

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogReporter_Accepted(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	reporter := NewLogReporter(logger)

	req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	stats := Stats{Running: 5, Waiting: 2, Limit: 10}

	reporter.Accepted(req, stats)

	output := buf.String()
	if !strings.Contains(output, "Request accepted") {
		t.Errorf("expected 'Request accepted' in output, got: %s", output)
	}
	if !strings.Contains(output, `"method":"GET"`) {
		t.Errorf("expected method in output, got: %s", output)
	}
	if !strings.Contains(output, `"path":"/test"`) {
		t.Errorf("expected path in output, got: %s", output)
	}
	if !strings.Contains(output, `"running":5`) {
		t.Errorf("expected running in output, got: %s", output)
	}
	if !strings.Contains(output, `"waiting":2`) {
		t.Errorf("expected waiting in output, got: %s", output)
	}
	if !strings.Contains(output, `"limit":10`) {
		t.Errorf("expected limit in output, got: %s", output)
	}
	if !strings.Contains(output, `"utilization":0.5`) {
		t.Errorf("expected utilization in output, got: %s", output)
	}
}

func TestLogReporter_Rejected(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	reporter := NewLogReporter(logger)

	req := httptest.NewRequest(http.MethodPost, "/api/data", http.NoBody)
	stats := Stats{Running: 10, Waiting: 5, Limit: 10}

	reporter.Rejected(req, stats)

	output := buf.String()
	if !strings.Contains(output, "Request rejected") {
		t.Errorf("expected 'Request rejected' in output, got: %s", output)
	}
	if !strings.Contains(output, `"level":"WARN"`) {
		t.Errorf("expected WARN level for rejection, got: %s", output)
	}
	if !strings.Contains(output, `"method":"POST"`) {
		t.Errorf("expected method in output, got: %s", output)
	}
	if !strings.Contains(output, `"running":10`) {
		t.Errorf("expected running in output, got: %s", output)
	}
}

func TestLogReporter_Integration(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	limiter := New(Config{Limit: 2})
	mw := NewMiddleware(limiter, NewLogReporter(logger), NewRejectionHandler(5))

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	// Make a request
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.String()

	// Should have accepted log
	if !strings.Contains(output, "Request accepted") {
		t.Errorf("expected 'Request accepted' in output, got: %s", output)
	}
}

func TestLogReporter_NilLogger(t *testing.T) {
	// Should not panic with nil logger (uses default)
	reporter := NewLogReporter(nil)

	req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	stats := Stats{Running: 1, Waiting: 0, Limit: 10}

	// Should not panic
	reporter.Accepted(req, stats)
	reporter.Rejected(req, stats)
}
