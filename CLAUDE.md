# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`loadshedder` is a modern Go concurrency limiting library that prevents server overload. It provides a framework-agnostic mechanism to limit concurrent request processing with optional waiting queues.

**Core Behavior:**
- Requests below the concurrency limit are accepted immediately
- Requests at the limit can optionally wait via a semaphore-based queue
- Requests exceeding limit + waiting limit are rejected immediately
- Users can add observability via the `Reporter` interface
- HTTP middleware provides 429 responses for rejected requests

**Design Philosophy:**
- Minimal dependencies (only golang.org/x/sync for semaphores)
- Modern Go (1.24+) with atomic operations and semaphores
- Framework-agnostic core with adapter pattern for HTTP frameworks
- Extensive tests that verify behavior, not language features
- Simple, predictable behavior over complex heuristics

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
- `Loadshedder` struct: Framework-agnostic concurrency limiter
- `Config` struct: Configuration with `Limit` and optional `WaitingLimit`
- `Token` type: Value-based token with `Accepted()` method and double-release safety
- `Stats` struct: Provides `Running`, `Waiting`, and `Limit` metrics
- Uses `atomic.Int64` for lock-free concurrency tracking
- Uses `semaphore.Weighted` from golang.org/x/sync for waiting queue

**middleware.go**
- `Middleware` struct: net/http adapter for the core loadshedder
- `Reporter` interface: Observability hooks receiving `Stats`
- Three lifecycle events: `OnAccepted`, `OnRejected`, `OnCompleted`
- All methods receive `*http.Request` and `Stats` for context-aware logging/metrics
- Configurable rejection handler (default: 429 with Retry-After header)
- Works with any framework that can wrap net/http handlers (Gin, Echo, Chi, etc.)

### Concurrency Model

The loadshedder combines atomic counters with semaphore-based waiting:

**Without WaitingLimit (WaitingLimit = 0):**
1. Incoming request increments counter atomically
2. Try to acquire semaphore immediately (non-blocking)
3. If acquisition fails: decrement counter and return rejected token
4. If accepted: return accepted token, release on completion

**With WaitingLimit (WaitingLimit > 0):**
1. Incoming request increments counter atomically
2. If `counter > limit + waitingLimit`: immediately decrement and reject (hard limit)
3. Otherwise: try to acquire semaphore with context (blocking)
4. If context cancelled or acquisition fails: decrement and return rejected token
5. If accepted: return accepted token, release on completion

**Key Properties:**
- **Atomic tracking**: `current` counter tracks total requests (running + waiting)
- **Semaphore enforcement**: Ensures exactly `limit` requests run concurrently
- **Stats calculation**: `Running = min(current, limit)`, `Waiting = max(0, current - limit)`
- **Lock-free**: Uses `sync/atomic` for counter, `semaphore.Weighted` for coordination
- **Panic-safe**: Token release is idempotent and safe to call multiple times
- **Context-aware**: Respects context cancellation during waiting

**Token Safety:**
- `Token` is a value type with internal `atomic.Bool` for release tracking
- `Release()` is safe to call on rejected tokens (no-op)
- `Release()` is safe to call multiple times (idempotent)
- Always call `defer loadshedder.Release(token)` right after `Acquire()`

## Testing Strategy

Tests focus on verifying loadshedder behavior:
- **Limit enforcement**: Verify running concurrency never exceeds limit
- **Waiting behavior**: Verify requests wait within WaitingLimit and are rejected beyond
- **Context cancellation**: Verify waiting requests respect context cancellation
- **Token safety**: Verify double-release and rejected-token-release are safe
- **Stats accuracy**: Verify Stats correctly report Running, Waiting, and Limit
- **Concurrent correctness**: Race detector enabled, atomic operations
- **Reporter integration**: Verify all callbacks fire correctly with accurate Stats
- **Middleware integration**: Verify HTTP middleware properly wraps handlers
- **Custom handlers**: Verify rejection handler customization works

Tests avoid checking language features (e.g., atomic operations work correctly) and instead verify that the loadshedder uses them correctly. Use `go test -timeout=30s` to avoid hanging tests with blocking operations.

## Key Implementation Details

**Framework-agnostic core**: The `Loadshedder` type has no HTTP or framework dependencies. It works with any application that needs concurrency limiting. HTTP and framework-specific adapters are separate.

**Token-based API**: `Acquire()` always returns a Token value (never nil). Call `token.Accepted()` to check if accepted. Always safe to defer `Release(token)` immediately after `Acquire()`.

**Stats everywhere**: `Acquire()` and `Release()` both return `Stats` showing the current state. The `Stats()` method provides real-time statistics. Middleware passes `Stats` to all Reporter callbacks.

**No X-RateLimit-* headers**: This is a per-process limiter. In load-balanced scenarios, per-process limits don't provide meaningful rate limit information to clients. Only `Retry-After` header is included in middleware rejections.

**Reporter receives request and stats**: The `Reporter` interface receives both `*http.Request` (or `*gin.Context`) and `Stats` for all methods, enabling rich context-aware observability.

**WaitingLimit is optional**: When `WaitingLimit = 0` (default), requests are rejected immediately when limit is reached. When `WaitingLimit > 0`, requests can wait up to that limit before being rejected.

**No graceful shutdown handling**: The middleware cannot detect when the server is shutting down, so it doesn't attempt to handle this case. Waiting requests will be cancelled when their context is cancelled.
