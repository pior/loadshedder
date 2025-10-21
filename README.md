# loadshedder

A modern Go HTTP middleware for limiting concurrent request processing to prevent server overload.

## Goal

In distributed systems, clients often retry failed requests, which can exacerbate server overload. When a server is overwhelmed and starts rejecting requests with 429 (Too Many Requests), aggressive client retries can make the situation worse.

`loadshedder` aims to provide intelligent concurrency limiting that:

1. **Phase 1 (Current)**: Enforces a hard concurrency limit by rejecting requests when the limit is exceeded
2. **Phase 2 (Planned)**: Reduces unnecessary rejections by only rejecting requests when the projected wait time exceeds a configurable threshold

This approach minimizes the number of 429 responses that trigger client retries, improving overall system stability.

## Current State

✅ **Phase 1 is complete** with the following features:

- Hard concurrency limit enforcement
- Immediate rejection with HTTP 429 when limit exceeded
- Observability through the `Reporter` interface
- Customizable rejection responses
- Zero external dependencies (standard library only)
- Lock-free implementation using atomic operations
- Production-ready with 96.9% test coverage

## Future Improvements

🚀 **Phase 2: Projected Wait Time Limiting**

Instead of immediately rejecting all requests when the concurrency limit is reached, the middleware will:

1. Track the exponential moving average of request durations
2. Calculate projected wait time: `(current_concurrency - limit) × avg_duration`
3. Only reject requests if projected wait time exceeds a configured `maxWaitTime`
4. Accept requests that would likely complete quickly even above the limit

This reduces unnecessary 429 responses while still protecting the server from prolonged overload.

The current implementation already tracks request durations through the `Reporter` interface, making this enhancement straightforward to add.

## Installation

```bash
go get github.com/pior/loadshedder
```

## Usage

### Basic Example

```go
package main

import (
    "fmt"
    "log"
    "net/http"

    "github.com/pior/loadshedder"
)

func main() {
    // Create a load shedder with a concurrency limit of 100
    ls := loadshedder.New(100)

    // Wrap your handler
    handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Request processed successfully\n")
    }))

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### With Observability

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
    ls := loadshedder.New(100,
        loadshedder.WithReporter(&metricsReporter{}))

    handler := ls.Middleware(http.DefaultServeMux)

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### With Custom Rejection Handler

```go
package main

import (
    "encoding/json"
    "log"
    "net/http"

    "github.com/pior/loadshedder"
)

func customRejectionHandler() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Retry-After", "2")
        w.WriteHeader(http.StatusTooManyRequests)

        json.NewEncoder(w).Encode(map[string]string{
            "error": "server is currently overloaded",
            "retry": "please retry after 2 seconds",
        })
    })
}

func main() {
    ls := loadshedder.New(100,
        loadshedder.WithRejectionHandler(customRejectionHandler()))

    handler := ls.Middleware(http.DefaultServeMux)

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

## API Reference

### Creating a Loadshedder

```go
func New(limit int, opts ...Option) *Loadshedder
```

Creates a new load shedder with the specified concurrency limit. Panics if limit is not positive.

### Options

```go
func WithReporter(r Reporter) Option
```

Attaches a reporter for observability into the load shedder's behavior.

```go
func WithRejectionHandler(h http.Handler) Option
```

Sets a custom handler for rejected requests. Default returns HTTP 429 with a `Retry-After: 1` header.

### Reporter Interface

```go
type Reporter interface {
    // Called when a request is accepted
    OnAccepted(r *http.Request, current, limit int)

    // Called when a request is rejected
    OnRejected(r *http.Request, current, limit int)

    // Called when a request completes processing
    OnCompleted(r *http.Request, current, limit int, duration time.Duration)
}
```

All methods receive the full `*http.Request` for context-aware logging, metrics, or tracing.

### Middleware

```go
func (ls *Loadshedder) Middleware(next http.Handler) http.Handler
```

Returns an HTTP middleware that wraps the provided handler with concurrency limiting.

## Design Decisions

### No X-RateLimit-* Headers

This middleware implements a **per-process** concurrency limiter. In load-balanced, multi-node deployments, per-process limits don't provide meaningful rate limit information to clients. Therefore, only the `Retry-After` header is included in rejection responses.

### Standard Library Only

The implementation uses only the Go standard library, ensuring:
- Zero external dependencies
- Minimal maintenance burden
- Maximum compatibility
- Easy auditing

### Lock-Free Implementation

Uses `sync/atomic` for concurrency tracking instead of mutexes, providing:
- Better performance under high concurrency
- No lock contention
- Simple, auditable code

## Performance

The middleware has minimal overhead:

```
BenchmarkLoadshedder-10              	 2847354	       421.3 ns/op	     912 B/op	       7 allocs/op
BenchmarkLoadshedder_WithReporter-10 	 2559007	       468.8 ns/op	     912 B/op	       7 allocs/op
```

## Testing

Run the test suite:

```bash
# Run all tests with coverage
go test -cover ./...

# Run tests with race detector
go test -race ./...

# Run benchmarks
go test -bench=. -benchmem
```

Current test coverage: **96.9%**

## Examples

See the [examples/basic](examples/basic) directory for a complete working example.

## License

MIT License

Copyright (c) 2025 Pior Bastida

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

## Acknowledgments

This project aims to improve system resilience by reducing the retry storm problem common in distributed systems.
