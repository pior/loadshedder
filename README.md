# loadshedder

A modern, framework-agnostic Go library for intelligent concurrency limiting to prevent server overload.

## Goal

In distributed systems, clients often retry failed requests, which can exacerbate server overload. When a server is overwhelmed and starts rejecting requests with 429 (Too Many Requests), aggressive client retries can make the situation worse.

`loadshedder` provides intelligent concurrency limiting with two operating modes:

1. **Phase 1 - Hard Limit**: Enforces a strict concurrency limit by rejecting all requests when the limit is exceeded
2. **Phase 2 - QoS-Based Limiting**: Reduces unnecessary rejections by only rejecting requests when the projected wait time exceeds a configurable threshold

This approach minimizes the number of 429 responses that trigger client retries, improving overall system stability.

## Architecture

**Framework-Agnostic Design:**
- `loadshedder` - Core limiter with no framework dependencies
- `loadshedder/httpware` - net/http middleware adapter
- `loadshedder/ginware` - Gin middleware (separate module to avoid dependencies)

## Features

✅ **Both phases are complete** with the following capabilities:

**Core Features:**
- Framework-agnostic concurrency limiter
- Hard concurrency limit enforcement (Phase 1 behavior, default)
- QoS-based projected wait time limiting (Phase 2 behavior, opt-in)
- Zero external dependencies in core library
- Lock-free implementation using atomic operations
- Production-ready with >90% test coverage

**QoS Features (Phase 2):**
- Exponential moving average (EMA) of request durations
- Projected wait time calculation: `(current_concurrency - limit) × avg_duration`
- Configurable maximum acceptable wait time
- Accepts requests over the limit if they'll complete quickly
- Tunable EMA smoothing factor for different workload characteristics

## Installation

```bash
# Core library (framework-agnostic)
go get github.com/pior/loadshedder

# For net/http support
# (httpware is included in the main module)

# For Gin support
go get github.com/pior/loadshedder/ginware
```

## Usage

### net/http Basic Example

```go
package main

import (
    "fmt"
    "log"
    "net/http"

    "github.com/pior/loadshedder"
    "github.com/pior/loadshedder/httpware"
)

func main() {
    // Create a limiter with a concurrency limit of 100
    limiter := loadshedder.NewLimiter(100)

    // Create HTTP middleware
    mw := httpware.New(limiter)

    // Wrap your handler
    handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Request processed successfully\n")
    }))

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### Gin Example

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/pior/loadshedder"
    "github.com/pior/loadshedder/ginware"
)

func main() {
    // Create a limiter
    limiter := loadshedder.NewLimiter(100)

    // Create Gin middleware
    mw := ginware.New(limiter)

    // Apply to router
    r := gin.Default()
    r.Use(mw.Handler())

    r.GET("/", func(c *gin.Context) {
        c.String(200, "Request processed successfully\n")
    })

    r.Run(":8080")
}
```

### With Observability (net/http)

```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/pior/loadshedder"
    "github.com/pior/loadshedder/httpware"
)

type metricsReporter struct{}

func (r *metricsReporter) OnAccepted(req *http.Request, current, limit int) {
    log.Printf("Accepted: %s %s (concurrency: %d/%d)",
        req.Method, req.URL.Path, current, limit)
}

func (r *metricsReporter) OnRejected(req *http.Request, current, limit int) {
    log.Printf("Rejected: %s %s (concurrency: %d/%d)",
        req.Method, req.URL.Path, current, limit)
}

func (r *metricsReporter) OnCompleted(req *http.Request, current, limit int, duration time.Duration) {
    log.Printf("Completed: %s %s (duration: %v, remaining: %d/%d)",
        req.Method, req.URL.Path, duration, current, limit)
}

func main() {
    limiter := loadshedder.NewLimiter(100)
    mw := httpware.New(limiter, httpware.WithReporter(&metricsReporter{}))

    handler := mw.Handler(http.DefaultServeMux)
    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### With QoS (Phase 2)

```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/pior/loadshedder"
    "github.com/pior/loadshedder/httpware"
)

func main() {
    // Enable QoS mode: only reject if projected wait > 200ms
    // This allows brief bursts over the limit without rejecting requests
    limiter := loadshedder.NewLimiter(100,
        loadshedder.WithMaxWaitTime(200*time.Millisecond))

    mw := httpware.New(limiter)

    handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Your application logic
        processRequest(w, r)
    }))

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

**How QoS Works:**
- The limiter tracks the exponential moving average of request durations
- When concurrency exceeds the limit, it calculates: `projected_wait = (current - limit) × avg_duration`
- Requests are only rejected if `projected_wait > maxWaitTime`
- This reduces unnecessary 429s when requests complete quickly

## API Reference

### Core Limiter

```go
func NewLimiter(limit int, opts ...LimiterOption) *Limiter
```

Creates a new framework-agnostic concurrency limiter.

**Methods:**
- `Acquire() bool` - Attempt to acquire a slot. Returns true if acquired.
- `Release(duration time.Duration)` - Release a slot and record the duration.
- `Current() int` - Get current concurrency level.
- `Limit() int` - Get the configured limit.

**Options:**
- `WithMaxWaitTime(maxWaitTime time.Duration)` - Enable QoS mode
- `WithEMAAlpha(alpha float64)` - Set EMA smoothing factor (0 < alpha < 1)

### HTTP Middleware (httpware)

```go
func New(limiter *Limiter, opts ...Option) *Middleware
```

Creates net/http middleware.

**Methods:**
- `Handler(next http.Handler) http.Handler` - Wrap an http.Handler

**Options:**
- `WithReporter(r Reporter)` - Add observability hooks
- `WithRejectionHandler(h http.Handler)` - Custom 429 handler

**Reporter Interface:**
```go
type Reporter interface {
    OnAccepted(r *http.Request, current, limit int)
    OnRejected(r *http.Request, current, limit int)
    OnCompleted(r *http.Request, current, limit int, duration time.Duration)
}
```

### Gin Middleware (ginware)

```go
func New(limiter *Limiter, opts ...Option) *Middleware
```

Creates Gin middleware.

**Methods:**
- `Handler() gin.HandlerFunc` - Returns a Gin middleware function

**Options:**
- `WithReporter(r Reporter)` - Add observability hooks
- `WithRejectionHandler(h gin.HandlerFunc)` - Custom 429 handler

**Reporter Interface:**
```go
type Reporter interface {
    OnAccepted(c *gin.Context, current, limit int)
    OnRejected(c *gin.Context, current, limit int)
    OnCompleted(c *gin.Context, current, limit int, duration time.Duration)
}
```

## Design Decisions

### Framework-Agnostic Core

The core `Limiter` has no framework dependencies, making it usable with any Go web framework. Framework-specific adapters live in separate packages.

### Separate Module for Gin

The Gin middleware lives in a separate Go module (`ginware`) to avoid adding Gin as a dependency to the core library. This keeps the core library lightweight.

### No X-RateLimit-* Headers

This is a per-process limiter. In load-balanced scenarios, per-process limits don't provide meaningful rate limit information to clients. Only `Retry-After` header is included.

## Performance

Minimal overhead with lock-free implementation:

```
BenchmarkLimiter-10              	34567890	        34.2 ns/op
BenchmarkLimiter_WithQoS-10      	32109876	        37.8 ns/op
BenchmarkMiddleware-10           	 2847354	       421 ns/op
```

## Testing

```bash
# Test core library
go test -cover ./...

# Test with race detector
go test -race ./...

# Test Gin middleware (separate module)
cd ginware && go test -cover
```

## Examples

See the [examples](examples/) directory for complete working examples:
- `examples/http` - net/http example
- `examples/gin` - Gin example

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.
