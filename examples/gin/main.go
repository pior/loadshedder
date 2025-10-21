package main

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pior/loadshedder"
	"github.com/pior/loadshedder/ginloadshedder"
)

// exampleReporter demonstrates observability integration
type exampleReporter struct{}

func (r *exampleReporter) OnAccepted(c *gin.Context, current, limit int) {
	log.Printf("ACCEPTED: %s %s (current=%d, limit=%d)", c.Request.Method, c.Request.URL.Path, current, limit)
}

func (r *exampleReporter) OnRejected(c *gin.Context, current, limit int) {
	log.Printf("REJECTED: %s %s (current=%d, limit=%d)", c.Request.Method, c.Request.URL.Path, current, limit)
}

func (r *exampleReporter) OnCompleted(c *gin.Context, current, limit int, duration time.Duration) {
	log.Printf("COMPLETED: %s %s (current=%d, limit=%d, duration=%v)", c.Request.Method, c.Request.URL.Path, current, limit, duration)
}

func main() {
	// Create a loadshedder with a concurrency limit of 10
	ls := loadshedder.New(loadshedder.Config{Limit: 10})

	// Create Gin middleware with reporter for observability
	mw := ginloadshedder.New(ls, ginloadshedder.WithReporter(&exampleReporter{}))

	// Create Gin router
	r := gin.Default()

	// Apply the middleware
	r.Use(mw.Handler())

	r.GET("/", func(c *gin.Context) {
		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		c.String(200, "Request processed successfully\n")
	})

	log.Println("Server starting on :8080 with concurrency limit of 10")
	r.Run(":8080")
}
