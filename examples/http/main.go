package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/pior/loadshedder"
	"github.com/pior/loadshedder/httpware"
)

// exampleReporter demonstrates observability integration
type exampleReporter struct{}

func (r *exampleReporter) OnAccepted(req *http.Request, current, limit int) {
	log.Printf("ACCEPTED: %s %s (current=%d, limit=%d)", req.Method, req.URL.Path, current, limit)
}

func (r *exampleReporter) OnRejected(req *http.Request, current, limit int) {
	log.Printf("REJECTED: %s %s (current=%d, limit=%d)", req.Method, req.URL.Path, current, limit)
}

func (r *exampleReporter) OnCompleted(req *http.Request, current, limit int, duration time.Duration) {
	log.Printf("COMPLETED: %s %s (current=%d, limit=%d, duration=%v)", req.Method, req.URL.Path, current, limit, duration)
}

func main() {
	// Create a limiter with a concurrency limit of 10
	limiter := loadshedder.NewLimiter(10)

	// Create HTTP middleware with reporter for observability
	mw := httpware.New(limiter, httpware.WithReporter(&exampleReporter{}))

	// Wrap your handler
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		fmt.Fprintf(w, "Request processed successfully\n")
	}))

	log.Println("Server starting on :8080 with concurrency limit of 10")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
