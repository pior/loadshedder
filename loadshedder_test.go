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
