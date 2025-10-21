package loadshedder

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testReporter is a simple reporter implementation for testing.
type testReporter struct {
	accepted  atomic.Int64
	rejected  atomic.Int64
	completed atomic.Int64
	mu        sync.Mutex
	durations []time.Duration
}

func (tr *testReporter) OnAccepted(r *http.Request, current, limit int) {
	tr.accepted.Add(1)
}

func (tr *testReporter) OnRejected(r *http.Request, current, limit int) {
	tr.rejected.Add(1)
}

func (tr *testReporter) OnCompleted(r *http.Request, current, limit int, duration time.Duration) {
	tr.completed.Add(1)
	tr.mu.Lock()
	tr.durations = append(tr.durations, duration)
	tr.mu.Unlock()
}

func TestNew_PanicsWithZeroLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with zero limit")
		}
	}()
	New(0)
}

func TestNew_PanicsWithNegativeLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with negative limit")
		}
	}()
	New(-1)
}

func TestLoadshedder_AcceptsRequestsUnderLimit(t *testing.T) {
	ls := New(5)
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected status 200, got %d", i, rec.Code)
		}
	}
}

func TestLoadshedder_RejectsRequestsOverLimit(t *testing.T) {
	ls := New(2)

	// Create a handler that blocks until we signal it to continue
	blocker := make(chan struct{})
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Start 2 requests that will block (fill the limit)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}

	// Give the goroutines time to start and block
	time.Sleep(50 * time.Millisecond)

	// This request should be rejected
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", rec.Code)
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}

	// Unblock and wait for completion
	close(blocker)
	wg.Wait()
}

func TestLoadshedder_ConcurrentRequests(t *testing.T) {
	limit := 10
	ls := New(limit)

	maxConcurrent := atomic.Int64{}
	currentConcurrent := atomic.Int64{}

	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := currentConcurrent.Add(1)

		// Track the maximum concurrency we see
		for {
			max := maxConcurrent.Load()
			if current <= max || maxConcurrent.CompareAndSwap(max, current) {
				break
			}
		}

		// Simulate some work
		time.Sleep(10 * time.Millisecond)

		currentConcurrent.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))

	// Launch many concurrent requests
	const numRequests = 100
	var wg sync.WaitGroup
	accepted := atomic.Int64{}
	rejected := atomic.Int64{}

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusOK {
				accepted.Add(1)
			} else if rec.Code == http.StatusTooManyRequests {
				rejected.Add(1)
			}
		}()
	}

	wg.Wait()

	// Verify we never exceeded the limit
	if maxConcurrent.Load() > int64(limit) {
		t.Errorf("concurrency exceeded limit: max=%d, limit=%d", maxConcurrent.Load(), limit)
	}

	// Verify all requests were either accepted or rejected
	total := accepted.Load() + rejected.Load()
	if total != numRequests {
		t.Errorf("expected %d total requests, got %d", numRequests, total)
	}

	// We should have had some rejections given the load
	if rejected.Load() == 0 {
		t.Error("expected some rejections with high concurrency")
	}
}

func TestLoadshedder_Reporter(t *testing.T) {
	reporter := &testReporter{}
	ls := New(2, WithReporter(reporter))

	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	// Send 3 requests sequentially
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if reporter.accepted.Load() != 3 {
		t.Errorf("expected 3 accepted, got %d", reporter.accepted.Load())
	}

	if reporter.completed.Load() != 3 {
		t.Errorf("expected 3 completed, got %d", reporter.completed.Load())
	}

	if reporter.rejected.Load() != 0 {
		t.Errorf("expected 0 rejected, got %d", reporter.rejected.Load())
	}

	// Verify durations were tracked
	reporter.mu.Lock()
	if len(reporter.durations) != 3 {
		t.Errorf("expected 3 durations, got %d", len(reporter.durations))
	}
	for i, d := range reporter.durations {
		if d < 10*time.Millisecond {
			t.Errorf("duration %d too short: %v", i, d)
		}
	}
	reporter.mu.Unlock()
}

func TestLoadshedder_ReporterWithRejections(t *testing.T) {
	reporter := &testReporter{}
	ls := New(1, WithReporter(reporter))

	blocker := make(chan struct{})
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Start a blocking request
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Send a request that should be rejected
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", rec.Code)
	}

	if reporter.rejected.Load() != 1 {
		t.Errorf("expected 1 rejection, got %d", reporter.rejected.Load())
	}

	// Unblock and wait
	close(blocker)
	wg.Wait()

	if reporter.accepted.Load() != 1 {
		t.Errorf("expected 1 accepted, got %d", reporter.accepted.Load())
	}

	if reporter.completed.Load() != 1 {
		t.Errorf("expected 1 completed, got %d", reporter.completed.Load())
	}
}

func TestLoadshedder_CustomRejectionHandler(t *testing.T) {
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "rejection")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("custom rejection"))
	})

	ls := New(1, WithRejectionHandler(customHandler))

	blocker := make(chan struct{})
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Start a blocking request
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Send a request that should be rejected
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	if rec.Header().Get("X-Custom") != "rejection" {
		t.Error("expected custom header")
	}

	if rec.Body.String() != "custom rejection" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}

	// Unblock and wait
	close(blocker)
	wg.Wait()
}

func TestLoadshedder_ReleasesSlotOnCompletion(t *testing.T) {
	ls := New(2)

	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Process a request
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify the slot was released
	if ls.current.Load() != 0 {
		t.Errorf("expected current to be 0 after completion, got %d", ls.current.Load())
	}

	// Verify we can process more requests (slots are properly recycled)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected status 200, got %d", i, rec.Code)
		}

		if ls.current.Load() != 0 {
			t.Errorf("request %d: expected current to be 0, got %d", i, ls.current.Load())
		}
	}
}

func BenchmarkLoadshedder(b *testing.B) {
	ls := New(100)
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkLoadshedder_WithReporter(b *testing.B) {
	reporter := &testReporter{}
	ls := New(100, WithReporter(reporter))
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}

// QoS Tests

func TestWithEMAAlpha_PanicsWithInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
	}{
		{"zero", 0.0},
		{"negative", -0.1},
		{"one", 1.0},
		{"greater than one", 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic with alpha=%f", tt.alpha)
				}
			}()
			New(10, WithEMAAlpha(tt.alpha))
		})
	}
}

func TestQoS_WithMaxWaitTime_AcceptsOverLimit(t *testing.T) {
	// Create a load shedder with limit=2 and maxWaitTime=500ms
	ls := New(2, WithMaxWaitTime(500*time.Millisecond))

	// First, establish an average duration by processing some requests
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond) // Fast requests
		w.WriteHeader(http.StatusOK)
	}))

	// Process a few requests to establish the average
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Now create blocking requests to fill the limit
	blocker := make(chan struct{})
	blockingHandler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Start 2 requests that block (at the limit)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			blockingHandler.ServeHTTP(rec, req)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Send a request that's over the limit (in a goroutine to avoid blocking)
	// Projected wait = 1 (over limit) * ~50ms (avg) = ~50ms < 500ms threshold
	// Should be ACCEPTED
	wg.Add(1)
	var overLimitCode int
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		blockingHandler.ServeHTTP(rec, req)
		overLimitCode = rec.Code
	}()

	// Unblock all requests
	time.Sleep(50 * time.Millisecond)
	close(blocker)
	wg.Wait()

	// This should NOT be a 429, because projected wait is small
	if overLimitCode == http.StatusTooManyRequests {
		t.Error("expected request to be accepted due to low projected wait time")
	}
}

func TestQoS_WithMaxWaitTime_RejectsHighProjectedWait(t *testing.T) {
	// Create a load shedder with limit=2 and maxWaitTime=50ms
	ls := New(2, WithMaxWaitTime(50*time.Millisecond))

	// First, establish a long average duration
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // Slow requests
		w.WriteHeader(http.StatusOK)
	}))

	// Process a few requests to establish the average
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Now create blocking requests to fill the limit
	blocker := make(chan struct{})
	blockingHandler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Start 2 requests that block (at the limit)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			blockingHandler.ServeHTTP(rec, req)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Send a request that's over the limit
	// Projected wait = 1 * ~200ms = ~200ms > 50ms threshold
	// Should be REJECTED
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	blockingHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d (projected wait should exceed threshold)", rec.Code)
	}

	close(blocker)
	wg.Wait()
}

func TestQoS_WithoutMaxWaitTime_AlwaysRejects(t *testing.T) {
	// No maxWaitTime means Phase 1 behavior (always reject over limit)
	ls := New(2)

	blocker := make(chan struct{})
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Fill the limit
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// This should always be rejected (Phase 1 behavior)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 without maxWaitTime, got %d", rec.Code)
	}

	close(blocker)
	wg.Wait()
}

func TestQoS_EMAUpdates(t *testing.T) {
	ls := New(10)

	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	// Process a request
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Check that avgDuration was updated
	avgDuration := ls.durationTracker.average()
	if avgDuration == 0 {
		t.Error("expected avgDuration to be updated after request")
	}

	if avgDuration < 100*time.Millisecond {
		t.Errorf("expected avgDuration >= 100ms, got %v", avgDuration)
	}

	// Process another request with different duration
	handler2 := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	handler2.ServeHTTP(rec2, req2)

	// EMA should have moved toward the new value
	newAvgDuration := ls.durationTracker.average()
	if newAvgDuration <= avgDuration {
		t.Error("expected avgDuration to increase after slower request")
	}
}

func TestQoS_ProjectedWaitTimeCalculation(t *testing.T) {
	ls := New(5)

	// Manually set avgDuration to 100ms for predictable testing
	ls.durationTracker.avgDuration.Store(uint64(100 * time.Millisecond))

	tests := []struct {
		current  int
		expected time.Duration
	}{
		{current: 3, expected: 0},                       // Under limit
		{current: 5, expected: 0},                       // At limit
		{current: 6, expected: 100 * time.Millisecond},  // 1 over
		{current: 8, expected: 300 * time.Millisecond},  // 3 over
		{current: 10, expected: 500 * time.Millisecond}, // 5 over
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.current)), func(t *testing.T) {
			projected := ls.calculateProjectedWaitTime(tt.current)
			if projected != tt.expected {
				t.Errorf("current=%d: expected %v, got %v", tt.current, tt.expected, projected)
			}
		})
	}
}

func TestQoS_NoHistoricalData(t *testing.T) {
	// With no historical data, projected wait should be 0
	ls := New(2, WithMaxWaitTime(100*time.Millisecond))

	blocker := make(chan struct{})
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Fill the limit
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// With no history, projected wait = 0, so should be accepted (in goroutine to avoid blocking)
	wg.Add(1)
	var overLimitCode int
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		overLimitCode = rec.Code
	}()

	// Unblock all requests
	time.Sleep(50 * time.Millisecond)
	close(blocker)
	wg.Wait()

	// Should be accepted because there's no historical data yet
	if overLimitCode == http.StatusTooManyRequests {
		t.Error("expected request to be accepted with no historical data")
	}
}

func BenchmarkLoadshedder_WithQoS(b *testing.B) {
	ls := New(100, WithMaxWaitTime(100*time.Millisecond))
	handler := ls.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}
