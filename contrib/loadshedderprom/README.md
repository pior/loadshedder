# loadshedderprom

Prometheus metrics reporter for [loadshedder](https://github.com/pior/loadshedder).

## Installation

```bash
go get github.com/pior/loadshedder/contrib/loadshedderprom
```

## Usage

```go
package main

import (
    "net/http"

    "github.com/pior/loadshedder"
    "github.com/pior/loadshedder/contrib/loadshedderprom"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
    // Create loadshedder
    ls := loadshedder.New(loadshedder.Config{
        Limit:        100,
        WaitingLimit: 20,
    })

    // Create middleware with Prometheus reporter
    mw := loadshedder.NewMiddleware(ls)
    mw.Reporter = loadshedderprom.NewReporter("myapp")

    // Your HTTP handler
    handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    // Expose metrics endpoint
    http.Handle("/metrics", promhttp.Handler())
    http.Handle("/", handler)

    http.ListenAndServe(":8080", nil)
}
```

## Metrics Exported

The reporter exports the following loadshedder-specific metrics:

### Counter Metrics
- `{namespace}_requests_accepted_total` - Total number of requests accepted by the loadshedder
- `{namespace}_requests_rejected_total` - Total number of requests rejected due to capacity limits

### Gauge Metrics
- `{namespace}_concurrency_running` - Current number of running requests
- `{namespace}_concurrency_waiting` - Current number of requests waiting for a slot
- `{namespace}_concurrency_limit` - Configured concurrency limit
- `{namespace}_utilization_ratio` - Current utilization ratio (running / limit)

**Note:** These metrics focus specifically on loadshedder behavior (concurrency limiting, rejections, capacity). For general request metrics like latency and response codes, use a separate observability middleware.

## Example

See the [examples/prometheus](../../examples/prometheus/) directory for a complete working example with:
- Load testing instructions
- Prometheus queries
- Alerting rules
- Production considerations
