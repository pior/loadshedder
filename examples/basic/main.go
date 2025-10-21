package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/pior/loadshedder"
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
	// Create a load shedder with a concurrency limit of 10
	// and attach a reporter for observability
	ls := loadshedder.New(10, loadshedder.WithReporter(&exampleReporter{}))

	// Wrap your handler with the load shedder middleware
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		fmt.Fprintf(w, "Request processed successfully\n")
	}))

	log.Println("Server starting on :8080 with concurrency limit of 10")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
