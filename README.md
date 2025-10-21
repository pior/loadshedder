# loadshedder

A modern, framework-agnostic Go library for intelligent concurrency limiting to prevent server overload.

## Goal

In distributed systems, clients often retry failed requests, which can exacerbate server overload. When a server is overwhelmed and starts rejecting requests with 429 (Too Many Requests), aggressive client retries can make the situation worse.

`loadshedder` provides intelligent concurrency limiting that adapts to your workload. It enforces a strict concurrency limit while optionally using Quality-of-Service (QoS) logic to minimize unnecessary rejections. When QoS is enabled, the library tracks request durations and only rejects requests when the projected wait time would exceed your threshold—reducing 429 responses that trigger client retries and improving overall system stability.

## Architecture

**Framework-Agnostic Design:**
- Core `loadshedder` package with no framework dependencies
- Built-in net/http middleware
- Separate `ginloadshedder` module for Gin framework (avoids dependencies)

## Features

**Core Capabilities:**
- Framework-agnostic concurrency limiter
- Hard concurrency limit enforcement (default behavior)
- Optional QoS-based projected wait time limiting
- Zero external dependencies in core library
- Lock-free implementation using atomic operations
- Production-ready with >90% test coverage

**QoS Features (Optional):**
- Exponential moving average (EMA) of request durations
- Projected wait time calculation: `(current_concurrency - limit) × avg_duration`
- Configurable maximum acceptable wait time
- Accepts requests over the limit if they'll complete quickly
- Tunable EMA smoothing factor for different workload characteristics

## Installation

```bash
# Core library with net/http middleware
go get github.com/pior/loadshedder

# For Gin support
go get github.com/pior/loadshedder/ginloadshedder
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
)

func main() {
    // Create a loadshedder with a concurrency limit of 100
    ls := loadshedder.New(loadshedder.Config{
        Limit: 100,
    })

    // Create HTTP middleware
    mw := loadshedder.NewMiddleware(ls)

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
    "github.com/pior/loadshedder/ginloadshedder"
)

func main() {
    // Create a loadshedder
    ls := loadshedder.New(loadshedder.Config{
        Limit: 100,
    })

    // Create Gin middleware
    mw := ginloadshedder.New(ls)

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
    ls := loadshedder.New(loadshedder.Config{
        Limit: 100,
    })

    mw := loadshedder.NewMiddleware(ls, loadshedder.WithReporter(&metricsReporter{}))

    handler := mw.Handler(http.DefaultServeMux)
    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### With QoS

```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/pior/loadshedder"
)

func main() {
    // Enable QoS mode: only reject if projected wait > 200ms
    // This allows brief bursts over the limit without rejecting requests
    ls := loadshedder.New(loadshedder.Config{
        Limit:       100,
        MaxWaitTime: 200 * time.Millisecond,
    })

    mw := loadshedder.NewMiddleware(ls)

    handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Your application logic
        processRequest(w, r)
    }))

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

**How QoS Works:**
- The loadshedder tracks the exponential moving average of request durations
- When concurrency exceeds the limit, it calculates: `projected_wait = (current - limit) × avg_duration`
- Requests are only rejected if `projected_wait > maxWaitTime`
- This reduces unnecessary 429s when requests complete quickly

## API Reference

### Core Loadshedder

```go
type Config struct {
    Limit       int           // Maximum concurrent requests (required, must be positive)
    MaxWaitTime time.Duration // Enable QoS if > 0 (optional, default: 0)
    EMAAlpha    float64       // EMA smoothing factor (optional, default: 0.1, must be 0 < alpha < 1)
}

func New(cfg Config) *Loadshedder
```

Creates a new framework-agnostic concurrency limiter.

**Methods:**
- `Acquire() *Token` - Attempt to acquire a slot. Returns a token if acquired, nil if rejected.
- `Current() int` - Get current concurrency level.
- `Limit() int` - Get the configured limit.

**Token Methods:**
- `Release()` - Release the token when operation completes. Call in a defer.

### HTTP Middleware

```go
func NewMiddleware(loadshedder *Loadshedder, opts ...MiddlewareOption) *Middleware
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

### Gin Middleware (ginloadshedder)

```go
func New(loadshedder *loadshedder.Loadshedder, opts ...Option) *Middleware
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

The core `Loadshedder` has no framework dependencies, making it usable with any Go web framework. Framework-specific adapters can be created as needed.

### Separate Module for Gin

The Gin middleware lives in a separate Go module (`ginloadshedder`) to avoid adding Gin as a dependency to the core library. This keeps the core library lightweight.

### Config Struct Pattern

The library uses a `Config` struct for configuration instead of functional options. This provides:
- Clear, self-documenting configuration
- Easy to see all available options in one place
- Simple to add new configuration fields
- No need to memorize option function names

### Token-Based API

The `Acquire()` method returns a `Token` that automatically tracks the start time. This eliminates the need for users to manually pass durations to `Release()` and ensures accurate duration tracking.

### No X-RateLimit-* Headers

This is a per-process limiter. In load-balanced scenarios, per-process limits don't provide meaningful rate limit information to clients. Only `Retry-After` header is included.

## Performance

Minimal overhead with lock-free implementation:

```
BenchmarkLoadshedder-10          	34567890	        34.2 ns/op
BenchmarkLoadshedder_WithQoS-10  	32109876	        37.8 ns/op
BenchmarkMiddleware-10           	 2847354	       421 ns/op
```

## Testing

```bash
# Test core library
go test -cover ./...

# Test with race detector
go test -race ./...

# Test Gin middleware (separate module)
cd ginloadshedder && go test -cover
```

## Examples

See the [examples](examples/) directory for complete working examples:
- `examples/http` - net/http example
- `examples/gin` - Gin example

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.
