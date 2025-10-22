# loadshedder

A modern, framework-agnostic Go library for concurrency limiting with optional request queuing to prevent server overload.

## Goal

In distributed systems, clients often retry failed requests, which can exacerbate server overload. When a server is overwhelmed and starts rejecting requests with 429 (Too Many Requests), aggressive client retries can make the situation worse.

`loadshedder` provides simple, predictable concurrency limiting. It enforces a strict concurrency limit and optionally allows requests to wait in a bounded queue. This reduces unnecessary 429 responses during brief traffic spikes while protecting against sustained overload—improving overall system stability without complex heuristics.

## Architecture

**Framework-Agnostic Design:**
- Core `loadshedder` package with no framework dependencies
- Built-in net/http middleware
- Works with any framework that can wrap net/http handlers (Gin, Echo, Chi, etc.)

## Features

**Core Capabilities:**
- Framework-agnostic concurrency limiter
- Hard concurrency limit enforcement
- Optional bounded waiting queue (WaitingLimit)
- Semaphore-based request coordination
- Context-aware (respects cancellation)
- Minimal dependencies (only golang.org/x/sync)
- Lock-free atomic counters with semaphore coordination
- Production-ready with >95% test coverage

**Waiting Queue (Optional):**
- Configure `WaitingLimit` to allow requests to wait when at capacity
- Requests wait on a semaphore until a slot becomes available
- Hard rejection when `current > limit + waitingLimit`
- Respects context cancellation during waiting
- Real-time stats show Running vs Waiting requests

## Installation

```bash
go get github.com/pior/loadshedder
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

### Using with Gin (or other frameworks)

Gin and other frameworks can wrap standard net/http handlers:

```go
package main

import (
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/pior/loadshedder"
)

func main() {
    // Create a loadshedder
    ls := loadshedder.New(loadshedder.Config{
        Limit: 100,
    })

    // Create HTTP middleware
    mw := loadshedder.NewMiddleware(ls)

    // Wrap the entire Gin engine with the loadshedder
    engine := gin.Default()

    engine.GET("/", func(c *gin.Context) {
        c.String(200, "Request processed successfully\n")
    })

    // Wrap the Gin engine's ServeHTTP with loadshedder
    handler := mw.Handler(engine)

    http.ListenAndServe(":8080", handler)
}
```

### With Waiting Queue

```go
package main

import (
    "fmt"
    "log"
    "net/http"

    "github.com/pior/loadshedder"
)

func main() {
    // Allow up to 100 concurrent requests + 20 waiting
    ls := loadshedder.New(loadshedder.Config{
        Limit:        100,
        WaitingLimit: 20, // Allow 20 requests to wait for a slot
    })

    mw := loadshedder.NewMiddleware(ls)

    handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Request processed successfully\n")
    }))

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### With Observability - Structured Logging (slog)

```go
package main

import (
    "log"
    "net/http"

    "github.com/pior/loadshedder"
)

func main() {
    ls := loadshedder.New(loadshedder.Config{
        Limit:        100,
        WaitingLimit: 20,
    })

    mw := loadshedder.NewMiddleware(ls)
    // Use the built-in slog reporter
    mw.Reporter = loadshedder.NewLogReporter(nil)

    handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    log.Fatal(http.ListenAndServe(":8080", handler))
}
```

**Output Example:**
```
INFO Request accepted method=GET path=/api/data remote_addr=127.0.0.1:54321 running=5 waiting=0 limit=100 utilization=0.05 wait_time=0s
WARN Request rejected - concurrency limit exceeded method=POST path=/api/data remote_addr=127.0.0.1:54322 running=100 waiting=20 limit=100 utilization=1.0 wait_time=45ms
```

The `LogReporter` provides structured logging with all relevant loadshedder fields, including `wait_time` which shows how long the request waited before being accepted or rejected.

### With Observability - Prometheus Metrics

For production environments, see the comprehensive [Prometheus metrics example](examples/prometheus/) which demonstrates:
- Request counters (accepted, rejected, completed)
- Real-time concurrency and utilization gauges
- Ready-to-use alerting rules and queries
- Loadshedder-specific behavior tracking

```go
// See examples/prometheus/main.go for complete implementation
reporter := NewPrometheusReporter("myapp")
mw.Reporter = reporter
```

**Metrics exported:**
- `myapp_requests_accepted_total` - Total accepted requests
- `myapp_requests_rejected_total` - Total rejected requests
- `myapp_concurrency_running` - Current running requests
- `myapp_concurrency_waiting` - Current waiting requests
- `myapp_concurrency_limit` - Configured concurrency limit
- `myapp_utilization_ratio` - Current utilization (running/limit)

These metrics focus specifically on loadshedder behavior. For general request metrics (latency, response codes), use a separate observability middleware.

### Framework-Agnostic Usage (Direct)

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/pior/loadshedder"
)

func main() {
    ls := loadshedder.New(loadshedder.Config{
        Limit:        10,
        WaitingLimit: 5,
    })

    ctx := context.Background()

    // Acquire a slot
    stats, token := ls.Acquire(ctx)
    defer ls.Release(token)

    if !token.Accepted() {
        log.Printf("Request rejected (running: %d/%d, waiting: %d)",
            stats.Running, stats.Limit, stats.Waiting)
        return
    }

    // Process request
    fmt.Println("Processing request...")

    // Token is released automatically by defer
}
```

**How Waiting Works:**
- When `WaitingLimit = 0` (default): immediate rejection when at limit
- When `WaitingLimit > 0`: requests wait on a semaphore for up to `limit + waitingLimit` total
- Waiting requests acquire slots as they become available (FIFO via semaphore)
- Context cancellation immediately releases waiting requests

## API Reference

### Core Loadshedder

```go
type Config struct {
    Limit        int64 // Maximum concurrent requests (required, must be positive)
    WaitingLimit int64 // Maximum waiting requests (optional, default: 0, must be non-negative)
}

type Stats struct {
    Running  int64         // Current number of running requests
    Waiting  int64         // Current number of waiting requests
    Limit    int64         // The configured limit
    WaitTime time.Duration // Time spent waiting for acquisition (0 if not waited)
}

type Token struct {
    // Opaque type - use methods to interact
}

func New(cfg Config) *Loadshedder
```

Creates a new framework-agnostic concurrency limiter.

**Methods:**
- `Acquire(ctx context.Context) (Stats, *Token)` - Acquire a slot. Always returns Stats and a Token. Check `token.Accepted()` to see if accepted.
- `Release(token *Token) Stats` - Release the token and return updated Stats. Safe to call even if not accepted or already released.
- `Stats() Stats` - Get current statistics.

**Token Methods:**
- `Accepted() bool` - Returns true if the request was accepted (slot acquired), false if rejected.

**Usage Pattern:**
```go
stats, token := loadshedder.Acquire(ctx)
defer loadshedder.Release(token)

if !token.Accepted() {
    // Handle rejection
    return
}
// Process request
```

### HTTP Middleware

```go
func NewMiddleware(loadshedder *Loadshedder) *Middleware
```

Creates net/http middleware.

**Methods:**
- `Handler(next http.Handler) http.Handler` - Wrap an http.Handler

**Fields (set after creation):**
- `Reporter Reporter` - Observability hooks (optional)
- `RejectionHandler http.Handler` - Custom 429 handler (optional, has default)

**Reporter Interface:**
```go
type Reporter interface {
    OnAccepted(r *http.Request, stats Stats)
    OnRejected(r *http.Request, stats Stats)
}
```

The Reporter interface provides hooks for observability focused on request **in-flow** (accepted vs rejected). For tracking request completion, latency, or response codes, use a separate application-level observability middleware.

## Design Decisions

### Framework-Agnostic Core

The core `Loadshedder` has no HTTP or framework dependencies, making it usable with any Go application that needs concurrency limiting. The net/http middleware works with any framework that can wrap standard handlers (Gin, Echo, Chi, etc.).

### Config Struct Pattern

The library uses a simple `Config` struct for loadshedder configuration:
- Clear, self-documenting configuration with two simple fields
- Easy to understand at a glance
- No need to memorize option function names
- Middleware-specific configuration (Reporter, RejectionHandler) set on middleware instance

### Token-Based API

The `Acquire(ctx)` method always returns a `Token` pointer that:
- Provides an `Accepted()` method to check if the request was accepted
- Can be safely passed to `Release()` even if rejected or already released
- Internal `atomic.Bool` prevents double-release bugs
- Eliminates nil checks - token is never nil

This design makes the API safer and more convenient. You can always `defer loadshedder.Release(token)` immediately after `Acquire()`, regardless of whether the request was accepted.

### Semaphore-Based Waiting

Uses `golang.org/x/sync/semaphore` for coordinated waiting:
- Proven, well-tested implementation
- FIFO fairness for waiting requests
- Context-aware cancellation
- Simple and predictable behavior

### Stats-Based Observability

All operations return `Stats` showing current state:
- `Running`: actual concurrent requests being processed
- `Waiting`: requests waiting for a slot
- `Limit`: configured concurrency limit
- `WaitTime`: duration spent waiting for acquisition (0 for immediate acceptance/rejection)
- Reporter callbacks receive `Stats` for rich observability

**WaitTime Semantics:**
- Immediate acceptance (capacity available): WaitTime is near-zero (< 1ms overhead)
- Waited then accepted: WaitTime shows actual duration waiting for a slot
- Waited then rejected (context cancelled): WaitTime shows how long it waited before cancellation
- Hard rejection (exceeds limit + waitingLimit): WaitTime is 0

### No X-RateLimit-* Headers

This is a per-process limiter. In load-balanced scenarios, per-process limits don't provide meaningful rate limit information to clients. Only `Retry-After` header is included in middleware rejections.

## Performance

Minimal overhead with atomic operations and efficient semaphores:

```
BenchmarkLimiter-8                      205 ns/op     8 B/op    1 allocs/op
BenchmarkLimiter_NoContention-8         205 ns/op     8 B/op    1 allocs/op
BenchmarkLimiter_HighContention-8       206 ns/op     8 B/op    1 allocs/op
BenchmarkLimiter_WithWaiting-8          207 ns/op     8 B/op    1 allocs/op
BenchmarkLimiter_AcceptedPath-8          36 ns/op     8 B/op    1 allocs/op
BenchmarkLimiter_RejectedPath-8          14 ns/op     8 B/op    1 allocs/op
BenchmarkLimiter_Stats-8               0.30 ns/op     0 B/op    0 allocs/op
BenchmarkMiddleware-8                   925 ns/op  5314 B/op   14 allocs/op
BenchmarkMiddleware_WithReporter-8      952 ns/op  5314 B/op   14 allocs/op
BenchmarkMiddleware_Rejected-8          988 ns/op  5535 B/op   15 allocs/op
```

**Key Performance Characteristics:**
- **~200 ns/op** for typical parallel acquire+release (lock-free atomic counters)
- **~36 ns/op** for accepted-only path (no contention, direct acquire+release)
- **~14 ns/op** for rejected-only path (immediate rejection, no semaphore wait)
- **~925 ns/op** for HTTP middleware (includes httptest overhead)
- **Reporter overhead**: <30 ns/op (negligible impact on throughput)
- **Stats()**: <1 ns/op (simple atomic loads)
- **Single allocation per operation**: Token allocation only

Lock-free atomic counters provide minimal overhead. Semaphore operations only occur when at capacity, keeping the happy path fast. Performance is consistent across varying contention levels.

## Testing

```bash
# Run all tests with coverage
go test -cover ./...

# Run tests with race detector
go test -race ./...

# Run benchmarks
go test -bench=. -benchmem
```

## Examples

See the [examples](examples/) directory for complete working examples showing integration with various frameworks.

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.
