package httpware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pior/loadshedder"
)

type testReporter struct {
	accepted  atomic.Int64
	rejected  atomic.Int64
	completed atomic.Int64
}

func (tr *testReporter) OnAccepted(r *http.Request, current, limit int) {
	tr.accepted.Add(1)
}

func (tr *testReporter) OnRejected(r *http.Request, current, limit int) {
	tr.rejected.Add(1)
}

func (tr *testReporter) OnCompleted(r *http.Request, current, limit int, duration time.Duration) {
	tr.completed.Add(1)
}

func TestMiddleware_AcceptsUnderLimit(t *testing.T) {
	limiter := loadshedder.NewLimiter(5)
	mw := New(limiter)

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestMiddleware_RejectsOverLimit(t *testing.T) {
	limiter := loadshedder.NewLimiter(2)
	mw := New(limiter)

	blocker := make(chan struct{})
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocker
		w.WriteHeader(http.StatusOK)
	}))

	// Start 2 requests that block (fill the limit)
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

	close(blocker)
	wg.Wait()
}

func TestMiddleware_WithReporter(t *testing.T) {
	limiter := loadshedder.NewLimiter(2)
	reporter := &testReporter{}
	mw := New(limiter, WithReporter(reporter))

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}

func TestMiddleware_CustomRejectionHandler(t *testing.T) {
	limiter := loadshedder.NewLimiter(1)
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "rejection")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("custom rejection"))
	})

	mw := New(limiter, WithRejectionHandler(customHandler))

	blocker := make(chan struct{})
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	close(blocker)
	wg.Wait()
}

func BenchmarkMiddleware(b *testing.B) {
	limiter := loadshedder.NewLimiter(100)
	mw := New(limiter)

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
