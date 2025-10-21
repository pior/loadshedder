package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

	// Create Gin router and define routes
	engine := gin.Default()

	engine.GET("/", func(c *gin.Context) {
		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		c.String(200, "Request processed successfully\n")
	})

	// Wrap the entire Gin engine with the loadshedder
	handler := mw.Handler(engine)

	log.Println("Server starting on :8080 with concurrency limit of 10")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
