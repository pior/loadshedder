# Prometheus Metrics Example

This example demonstrates Prometheus metrics integration for loadshedder using the `contrib/loadshedderprom` package.

## Quick Start

```go
import "github.com/pior/loadshedder/contrib/loadshedderprom"

mw := loadshedder.NewMiddleware(ls)
mw.Reporter = loadshedderprom.NewReporter("myapp")
```

## Metrics Exported

The reporter exports the following loadshedder-specific metrics:

### Counter Metrics
- `myapp_requests_accepted_total` - Total number of requests accepted by the loadshedder
- `myapp_requests_rejected_total` - Total number of requests rejected due to capacity limits

### Gauge Metrics
- `myapp_concurrency_running` - Current number of running requests
- `myapp_concurrency_waiting` - Current number of requests waiting for a slot
- `myapp_concurrency_limit` - Configured concurrency limit
- `myapp_utilization_ratio` - Current utilization ratio (running / limit)

**Note:** These metrics focus specifically on loadshedder behavior (concurrency limiting, rejections, capacity). For general request metrics like latency and response codes, use a separate observability middleware.

## Running the Example

```bash
cd examples/prometheus
go mod download
go run main.go
```

The server starts on `http://localhost:8080` with the following endpoints:

- `/api/fast` - Fast endpoint (10-50ms latency)
- `/api/slow` - Slow endpoint (100-500ms latency)
- `/health` - Health check endpoint
- `/metrics` - Prometheus metrics endpoint

## Load Testing

```bash
# Install hey
go install github.com/rakyll/hey@latest

# Light load (should mostly succeed)
hey -n 100 -c 5 http://localhost:8080/api/fast

# Heavy load on slow endpoint (will trigger rejections)
hey -n 1000 -c 50 http://localhost:8080/api/slow

# Sustained load
hey -z 60s -c 20 http://localhost:8080/api/slow
```
