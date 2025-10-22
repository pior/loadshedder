// Package loadshedderprom provides Prometheus metrics integration for loadshedder.
package loadshedderprom

import (
	"net/http"

	"github.com/pior/loadshedder"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Reporter implements the loadshedder.Reporter interface
// to export loadshedder-specific metrics to Prometheus.
type Reporter struct {
	// Counter metrics
	requestsAccepted prometheus.Counter
	requestsRejected prometheus.Counter

	// Gauge for current state
	concurrencyRunning prometheus.Gauge
	concurrencyWaiting prometheus.Gauge
	concurrencyLimit   prometheus.Gauge
	utilizationRatio   prometheus.Gauge
}

// NewReporter creates a new Prometheus-based reporter with loadshedder metrics.
// The namespace parameter is used to prefix all metric names (e.g., "myapp" -> "myapp_requests_accepted_total").
func NewReporter(namespace string) *Reporter {
	r := &Reporter{
		requestsAccepted: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_accepted_total",
			Help:      "Total number of requests accepted by the loadshedder",
		}),
		requestsRejected: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_rejected_total",
			Help:      "Total number of requests rejected by the loadshedder due to capacity",
		}),
		concurrencyRunning: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "concurrency_running",
			Help:      "Current number of running requests",
		}),
		concurrencyWaiting: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "concurrency_waiting",
			Help:      "Current number of requests waiting for a slot",
		}),
		concurrencyLimit: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "concurrency_limit",
			Help:      "Configured concurrency limit",
		}),
		utilizationRatio: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "utilization_ratio",
			Help:      "Current utilization ratio (running / limit)",
		}),
	}

	return r
}

// OnAccepted is called when a request is accepted.
func (r *Reporter) OnAccepted(req *http.Request, stats loadshedder.Stats) {
	r.requestsAccepted.Inc()
	r.updateGauges(stats)
}

// OnRejected is called when a request is rejected.
func (r *Reporter) OnRejected(req *http.Request, stats loadshedder.Stats) {
	r.requestsRejected.Inc()
	r.updateGauges(stats)
}

func (r *Reporter) updateGauges(stats loadshedder.Stats) {
	r.concurrencyRunning.Set(float64(stats.Running))
	r.concurrencyWaiting.Set(float64(stats.Waiting))
	r.concurrencyLimit.Set(float64(stats.Limit))

	if stats.Limit > 0 {
		r.utilizationRatio.Set(float64(stats.Running) / float64(stats.Limit))
	}
}
