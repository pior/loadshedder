// Package main demonstrates comprehensive Prometheus metrics integration with loadshedder.
package main

import (
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/pior/loadshedder"
	"github.com/pior/loadshedder/contrib/loadshedderprom"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// Create loadshedder with limit and waiting queue
	ls := loadshedder.New(loadshedder.Config{
		Limit:        10,
		WaitingLimit: 5,
	})

	// Create middleware with Prometheus reporter
	mw := loadshedder.NewMiddleware(ls)
	mw.Reporter = loadshedderprom.NewReporter("myapp")

	// Create a simple handler that simulates work
	mux := http.NewServeMux()

	mux.HandleFunc("/api/fast", func(w http.ResponseWriter, r *http.Request) {
		// Simulate fast request (10-50ms)
		time.Sleep(time.Duration(10+rand.IntN(40)) * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Fast request completed\n")
	})

	mux.HandleFunc("/api/slow", func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow request (100-500ms)
		time.Sleep(time.Duration(100+rand.IntN(400)) * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Slow request completed\n")
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK\n")
	})

	// Expose Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Wrap the mux with loadshedder middleware
	handler := mw.Handler(mux)

	log.Println("Server starting on :8080")
	log.Println("Endpoints:")
	log.Println("  http://localhost:8080/api/fast   - Fast endpoint (10-50ms)")
	log.Println("  http://localhost:8080/api/slow   - Slow endpoint (100-500ms)")
	log.Println("  http://localhost:8080/health     - Health check")
	log.Println("  http://localhost:8080/metrics    - Prometheus metrics")
	log.Println()
	log.Println("Try load testing with:")
	log.Println("  hey -n 1000 -c 20 http://localhost:8080/api/slow")
	log.Println()
	log.Println("Available Prometheus metrics:")
	log.Println("  myapp_requests_accepted_total    - Total accepted requests")
	log.Println("  myapp_requests_rejected_total    - Total rejected requests")
	log.Println("  myapp_concurrency_running        - Current running requests")
	log.Println("  myapp_concurrency_waiting        - Current waiting requests")
	log.Println("  myapp_concurrency_limit          - Configured concurrency limit")
	log.Println("  myapp_utilization_ratio          - Current utilization (running/limit)")

	log.Fatal(http.ListenAndServe(":8080", handler))
}
