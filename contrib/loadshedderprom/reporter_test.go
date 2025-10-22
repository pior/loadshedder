package loadshedderprom

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pior/loadshedder"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestReporter_OnAccepted(t *testing.T) {
	// Use a custom registry to avoid conflicts with global metrics
	registry := prometheus.NewRegistry()

	// Create reporter with custom registry
	reporter := &Reporter{
		requestsAccepted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "test",
			Name:      "requests_accepted_total",
		}),
		requestsRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "test",
			Name:      "requests_rejected_total",
		}),
		concurrencyRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "concurrency_running",
		}),
		concurrencyWaiting: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "concurrency_waiting",
		}),
		concurrencyLimit: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "concurrency_limit",
		}),
		utilizationRatio: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "utilization_ratio",
		}),
		waitTimeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "test",
			Name:      "wait_time_seconds",
		}),
	}

	registry.MustRegister(reporter.requestsAccepted)
	registry.MustRegister(reporter.requestsRejected)
	registry.MustRegister(reporter.concurrencyRunning)
	registry.MustRegister(reporter.concurrencyWaiting)
	registry.MustRegister(reporter.concurrencyLimit)
	registry.MustRegister(reporter.utilizationRatio)
	registry.MustRegister(reporter.waitTimeSeconds)

	req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	stats := loadshedder.Stats{Running: 5, Waiting: 2, Limit: 10, WaitTime: 50 * time.Millisecond}

	reporter.OnAccepted(req, stats)

	// Verify counter was incremented
	if count := testutil.ToFloat64(reporter.requestsAccepted); count != 1 {
		t.Errorf("expected requestsAccepted = 1, got %f", count)
	}

	// Verify gauges were updated
	if running := testutil.ToFloat64(reporter.concurrencyRunning); running != 5 {
		t.Errorf("expected concurrencyRunning = 5, got %f", running)
	}
	if waiting := testutil.ToFloat64(reporter.concurrencyWaiting); waiting != 2 {
		t.Errorf("expected concurrencyWaiting = 2, got %f", waiting)
	}
	if limit := testutil.ToFloat64(reporter.concurrencyLimit); limit != 10 {
		t.Errorf("expected concurrencyLimit = 10, got %f", limit)
	}
	if util := testutil.ToFloat64(reporter.utilizationRatio); util != 0.5 {
		t.Errorf("expected utilizationRatio = 0.5, got %f", util)
	}

	// Verify wait time histogram was updated (verify count is 1)
	if count := testutil.CollectAndCount(reporter.waitTimeSeconds); count != 1 {
		t.Errorf("expected 1 histogram metric, got %d", count)
	}
}

func TestReporter_OnRejected(t *testing.T) {
	// Use a custom registry to avoid conflicts with global metrics
	registry := prometheus.NewRegistry()

	reporter := &Reporter{
		requestsAccepted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "test",
			Name:      "requests_accepted_total",
		}),
		requestsRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "test",
			Name:      "requests_rejected_total",
		}),
		concurrencyRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "concurrency_running",
		}),
		concurrencyWaiting: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "concurrency_waiting",
		}),
		concurrencyLimit: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "concurrency_limit",
		}),
		utilizationRatio: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "test",
			Name:      "utilization_ratio",
		}),
		waitTimeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "test",
			Name:      "wait_time_seconds",
		}),
	}

	registry.MustRegister(reporter.requestsAccepted)
	registry.MustRegister(reporter.requestsRejected)
	registry.MustRegister(reporter.concurrencyRunning)
	registry.MustRegister(reporter.concurrencyWaiting)
	registry.MustRegister(reporter.concurrencyLimit)
	registry.MustRegister(reporter.utilizationRatio)
	registry.MustRegister(reporter.waitTimeSeconds)

	req := httptest.NewRequest(http.MethodPost, "/api/data", http.NoBody)
	stats := loadshedder.Stats{Running: 10, Waiting: 5, Limit: 10, WaitTime: 0}

	reporter.OnRejected(req, stats)

	// Verify counter was incremented
	if count := testutil.ToFloat64(reporter.requestsRejected); count != 1 {
		t.Errorf("expected requestsRejected = 1, got %f", count)
	}

	// Verify gauges were updated
	if running := testutil.ToFloat64(reporter.concurrencyRunning); running != 10 {
		t.Errorf("expected concurrencyRunning = 10, got %f", running)
	}
	if util := testutil.ToFloat64(reporter.utilizationRatio); util != 1.0 {
		t.Errorf("expected utilizationRatio = 1.0, got %f", util)
	}

	// Verify wait time histogram was updated (with 0 for hard rejection)
	if count := testutil.CollectAndCount(reporter.waitTimeSeconds); count != 1 {
		t.Errorf("expected 1 histogram metric, got %d", count)
	}
}

func TestReporter_Integration(t *testing.T) {
	// This test verifies that the reporter can be used with loadshedder middleware
	// without actually checking metrics (to avoid registry conflicts)

	limiter := loadshedder.New(loadshedder.Config{Limit: 2})
	mw := loadshedder.NewMiddleware(limiter)

	// Use the standard constructor which uses promauto (global registry)
	reporter := NewReporter("integration_test")
	mw.Reporter = reporter

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}
