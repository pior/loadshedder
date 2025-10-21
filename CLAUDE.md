# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`loadshedder` is a modern Go HTTP middleware library that limits concurrent request processing. It provides a simple mechanism to prevent server overload by rejecting requests with HTTP 429 (Too Many Requests) when concurrency exceeds a configured limit.

**Core Behavior:**
- Requests below the concurrency limit are accepted immediately
- Requests exceeding the limit are rejected with 429 status
- Users can add observability via the `Reporter` interface
- Rejection responses can be customized via options

**Design Philosophy:**
- Standard library only (no external dependencies)
- Modern Go (1.24+) with atomic operations
- Extensive tests that verify behavior, not language features
- Two operating modes: hard limit (Phase 1) and QoS-based (Phase 2)

## Commands

### Testing
```bash
# Run all tests with coverage
go test -cover ./...

# Run tests verbosely
go test -v ./...

# Run specific test
go test -v -run TestLoadshedder_ConcurrentRequests

# Run benchmarks
go test -bench=. -benchmem
```

### Building Examples
```bash
# Build the basic example
cd examples/basic
go build

# Run the example server
go run main.go
```

## Architecture

### Core Components

**loadshedder.go**
- `Loadshedder` struct: Main middleware implementation
- Uses `atomic.Int64` for lock-free concurrency tracking
- `Middleware()` method wraps `http.Handler` to enforce limits
- Tracks request start time and duration for future QoS phase

**reporter.go**
- `Reporter` interface: Observability hooks
- Three lifecycle events: `OnAccepted`, `OnRejected`, `OnCompleted`
- All methods receive `*http.Request` for context-aware logging/metrics
- `OnCompleted` includes request duration for future wait time calculations

**options.go**
- Functional options pattern for configuration
- `WithReporter()`: Attach observability callbacks
- `WithRejectionHandler()`: Customize 429 response behavior
- `WithMaxWaitTime()`: Enable QoS mode (Phase 2)
- `WithEMAAlpha()`: Tune EMA smoothing factor

### Concurrency Model

The middleware uses atomic operations to track concurrent requests:
1. Incoming request increments counter atomically
2. If counter > limit:
   - **Phase 1 (default)**: Immediately decrement and reject
   - **Phase 2 (with maxWaitTime)**: Calculate projected wait, only reject if it exceeds threshold
3. If accepted, defer decrement to ensure cleanup
4. Duration tracking via `time.Since()` in deferred function

This approach is:
- Lock-free (using `sync/atomic`)
- Panic-safe (defer ensures cleanup)
- Accurate (no race conditions in limit enforcement)

### QoS Implementation (Phase 2)

**Exponential Moving Average:**
- Tracks average request duration using EMA formula: `EMA_new = alpha × current + (1 - alpha) × EMA_old`
- Uses atomic Compare-And-Swap for lock-free updates
- Default alpha = 0.1 (adjustable via `WithEMAAlpha`)

**Projected Wait Time:**
- Formula: `projected_wait = (current_concurrency - limit) × avg_duration`
- Calculated only when `maxWaitTime > 0`
- Requests accepted if `projected_wait ≤ maxWaitTime`
- No historical data = no projection = accept (graceful startup)

**Benefits:**
- Reduces unnecessary 429 responses during brief bursts
- Allows system to handle spikes when requests complete quickly
- Protects against sustained overload (long request durations)

## Testing Strategy

Tests focus on verifying middleware behavior:
- **Limit enforcement**: Verify concurrency never exceeds limit (Phase 1)
- **QoS behavior**: Verify projected wait time calculations and acceptance/rejection logic (Phase 2)
- **Concurrent correctness**: Race detector enabled, atomic operations
- **Reporter integration**: Verify all callbacks fire correctly
- **Custom handlers**: Verify options work as expected
- **Slot recycling**: Verify slots are released after request completion
- **EMA tracking**: Verify exponential moving average updates correctly

Tests avoid checking language features (e.g., atomic operations work correctly) and instead verify that the middleware uses them correctly. Use `go test -timeout=30s` to avoid hanging tests.

## Key Implementation Details

**No X-RateLimit-* headers**: This is a per-process limiter. In load-balanced scenarios, per-process limits don't provide meaningful rate limit information to clients. Only `Retry-After` header is included.

**Reporter receives full request**: The `Reporter` interface receives `*http.Request` for all methods, enabling context-aware observability (log request path, method, headers, etc.).

**No graceful shutdown handling**: The middleware cannot detect when the server is shutting down, so it doesn't attempt to handle this case.
