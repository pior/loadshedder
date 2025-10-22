# Prometheus Metrics Example

This example demonstrates Prometheus metrics integration for loadshedder-specific behavior.

## Metrics Exported

The `PrometheusReporter` exports the following loadshedder-specific metrics:

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

### Using `hey`

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

### Using `wrk`

```bash
# Moderate concurrency
wrk -t4 -c20 -d30s http://localhost:8080/api/fast

# High concurrency (will trigger rejections)
wrk -t8 -c50 -d30s http://localhost:8080/api/slow
```

## Viewing Metrics

### Direct Access

```bash
curl http://localhost:8080/metrics
```

### With Prometheus

Create a `prometheus.yml` configuration:

```yaml
global:
  scrape_interval: 5s

scrape_configs:
  - job_name: 'loadshedder_example'
    static_configs:
      - targets: ['localhost:8080']
```

Run Prometheus:

```bash
prometheus --config.file=prometheus.yml
```

Access Prometheus UI at `http://localhost:9090`

## Useful Prometheus Queries

### Request Rates

```promql
# Accepted requests per second
rate(myapp_requests_accepted_total[1m])

# Rejected requests per second
rate(myapp_requests_rejected_total[1m])

# Rejection rate percentage
100 * rate(myapp_requests_rejected_total[1m]) /
  (rate(myapp_requests_accepted_total[1m]) + rate(myapp_requests_rejected_total[1m]))
```

### Concurrency and Utilization

```promql
# Current running requests
myapp_concurrency_running

# Current waiting requests
myapp_concurrency_waiting

# Current utilization percentage
100 * myapp_utilization_ratio

# Utilization over time (average over 5 minutes)
avg_over_time(myapp_utilization_ratio[5m]) * 100

# Peak utilization in the last hour
max_over_time(myapp_utilization_ratio[1h]) * 100
```

### Capacity Analysis

```promql
# Total capacity used (running + waiting)
myapp_concurrency_running + myapp_concurrency_waiting

# Available capacity
myapp_concurrency_limit - myapp_concurrency_running

# Is at capacity (boolean: 1 if at limit, 0 otherwise)
myapp_concurrency_running >= myapp_concurrency_limit

# Rejection events (count rejections in last 5m)
increase(myapp_requests_rejected_total[5m])
```

### Throughput Analysis

```promql
# Request acceptance rate (%)
100 * rate(myapp_requests_accepted_total[5m]) /
  (rate(myapp_requests_accepted_total[5m]) + rate(myapp_requests_rejected_total[5m]))

# Requests pending (waiting to be processed)
myapp_concurrency_waiting

# Total request rate (accepted + rejected)
rate(myapp_requests_accepted_total[5m]) + rate(myapp_requests_rejected_total[5m])
```

## Alerting Rules

Example Prometheus alerting rules for loadshedder metrics:

```yaml
groups:
  - name: loadshedder_alerts
    rules:
      - alert: HighRejectionRate
        expr: |
          (
            rate(myapp_requests_rejected_total[5m]) /
            (rate(myapp_requests_accepted_total[5m]) + rate(myapp_requests_rejected_total[5m]))
          ) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High request rejection rate"
          description: "More than 10% of requests are being rejected (current: {{ $value | humanizePercentage }})"

      - alert: HighUtilization
        expr: myapp_utilization_ratio > 0.9
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High concurrency utilization"
          description: "Concurrency utilization is above 90% (current: {{ $value | humanizePercentage }})"

      - alert: AtCapacity
        expr: myapp_concurrency_running >= myapp_concurrency_limit
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "Loadshedder at capacity"
          description: "Concurrency limit reached - new requests will be queued or rejected"

      - alert: SustainedRejections
        expr: rate(myapp_requests_rejected_total[5m]) > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Sustained request rejections"
          description: "Requests are being rejected continuously for 10+ minutes ({{ $value | printf \"%.2f\" }} req/s)"
```

## Production Considerations

1. **Namespace**: Use a meaningful namespace for your application (not "myapp")
2. **Scrape Interval**: Align with your Prometheus scrape interval (typically 15s or 30s)
3. **Monitoring Focus**: These metrics track loadshedder behavior specifically
   - For general request metrics (latency, response codes), use a separate observability middleware
   - This separation keeps concerns clear and avoids mixing infrastructure metrics with application metrics

## Extending the Reporter

The `PrometheusReporter` focuses on loadshedder-specific behavior. For application-level metrics, use a separate middleware:

```go
// Separate middleware for application metrics
type AppMetrics struct {
    requestDuration *prometheus.HistogramVec
    requestSize     *prometheus.HistogramVec
    responseStatus  *prometheus.CounterVec
}

// Chain middlewares together
handler := loadshedderMw.Handler(
    appMetricsMw.Handler(
        yourAppHandler,
    ),
)
```

This separation ensures:
- Clear separation of concerns (infrastructure vs application metrics)
- Loadshedder reporter only tracks what it controls (concurrency limiting)
- Application metrics middleware tracks request/response details
- Better maintainability and testability
