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

func (r *exampleReporter) OnAccepted(req *http.Request, stats loadshedder.Stats) {
	log.Printf("ACCEPTED: %s %s (running=%d/%d, waiting=%d)",
		req.Method, req.URL.Path, stats.Running, stats.Limit, stats.Waiting)
}

func (r *exampleReporter) OnRejected(req *http.Request, stats loadshedder.Stats) {
	log.Printf("REJECTED: %s %s (running=%d/%d, waiting=%d)",
		req.Method, req.URL.Path, stats.Running, stats.Limit, stats.Waiting)
}

func (r *exampleReporter) OnCompleted(req *http.Request, stats loadshedder.Stats) {
	log.Printf("COMPLETED: %s %s (running=%d/%d, waiting=%d)",
		req.Method, req.URL.Path, stats.Running, stats.Limit, stats.Waiting)
}

func main() {
	// Create a loadshedder with a concurrency limit of 10
	ls := loadshedder.New(loadshedder.Config{Limit: 10})

	// Create HTTP middleware with reporter for observability
	mw := loadshedder.NewMiddleware(ls)
	mw.Reporter = &exampleReporter{}

	// Wrap your handler
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		fmt.Fprintf(w, "Request processed successfully\n")
	}))

	log.Println("Server starting on :8080 with concurrency limit of 10")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
