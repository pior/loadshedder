package loadshedder

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Middleware safety tests verify that the middleware is resistant to panics
// from handlers, reporters, and rejection handlers.

type panicReporter struct {
	panicOnAccepted bool
	panicOnRejected bool
}

func (r *panicReporter) Accepted(req *http.Request, stats Stats) {
	if r.panicOnAccepted {
		panic("reporter panic on accepted")
	}
}

func (r *panicReporter) Rejected(req *http.Request, stats Stats) {
	if r.panicOnRejected {
		panic("reporter panic on rejected")
	}
}

func TestMiddleware_HandlerPanics(t *testing.T) {
	limiter := New(Config{Limit: 5})
	mw := NewMiddleware(limiter, nil, nil)

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler panic")
	}))

	// Capture panic
	var recovered interface{}
	func() {
		defer func() {
			recovered = recover()
		}()

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	// Verify panic was re-thrown
	if recovered == nil {
		t.Error("expected panic to be re-thrown")
	}
	if recovered != "handler panic" {
		t.Errorf("unexpected panic value: %v", recovered)
	}

	// Verify slot was released (can acquire again)
	stats := limiter.Stats()
	if stats.Running != 0 || stats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0 after panic, got %+v", stats)
	}

	// Verify subsequent requests work
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()

	okHandler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	okHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected subsequent request to succeed, got status %d", rec.Code)
	}
}

func TestMiddleware_HandlerPanicsUnderLoad(t *testing.T) {
	limiter := New(Config{Limit: 10})
	mw := NewMiddleware(limiter, nil, nil)

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		panic("handler panic")
	}))

	// Send multiple concurrent requests that panic
	var wg sync.WaitGroup
	panics := 0
	mu := sync.Mutex{}

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					panics++
					mu.Unlock()
				}
			}()

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}

	wg.Wait()

	// Most should have panicked (some may be rejected due to limit)
	if panics < 10 {
		t.Errorf("expected at least 10 panics, got %d", panics)
	}

	// Verify no leaks
	stats := limiter.Stats()
	if stats.Running != 0 || stats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0 after panics, got %+v", stats)
	}
}

func TestMiddleware_ReporterAcceptedPanics(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	oldDefault := slog.Default()
	defer slog.SetDefault(oldDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	limiter := New(Config{Limit: 5})
	reporter := &panicReporter{panicOnAccepted: true}
	mw := NewMiddleware(limiter, reporter, nil)

	handlerCalled := false
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify panic was suppressed and request was processed
	if !handlerCalled {
		t.Error("expected handler to be called despite reporter panic")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Verify panic was logged
	logOutput := buf.String()
	if !strings.Contains(logOutput, "loadshedder: reporter panic on accepted") {
		t.Errorf("expected panic to be logged, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "reporter panic on accepted") {
		t.Errorf("expected error message in log, got: %s", logOutput)
	}

	// Verify slot was released
	stats := limiter.Stats()
	if stats.Running != 0 || stats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0 after request, got %+v", stats)
	}
}

func TestMiddleware_ReporterRejectedPanics(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	oldDefault := slog.Default()
	defer slog.SetDefault(oldDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	limiter := New(Config{Limit: 1})
	reporter := &panicReporter{panicOnRejected: true}
	mw := NewMiddleware(limiter, reporter, nil)

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
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	time.Sleep(50 * time.Millisecond)

	// Send a request that will be rejected
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify panic was suppressed and rejection handler was called
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", rec.Code)
	}

	// Verify panic was logged
	logOutput := buf.String()
	if !strings.Contains(logOutput, "loadshedder: reporter panic on rejected") {
		t.Errorf("expected panic to be logged, got: %s", logOutput)
	}

	close(blocker)
	wg.Wait()

	// Verify state is consistent
	stats := limiter.Stats()
	if stats.Running != 0 || stats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0 after request, got %+v", stats)
	}
}

func TestMiddleware_RejectionHandlerPanics(t *testing.T) {
	limiter := New(Config{Limit: 1})

	panicHandler := func(stats Stats) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			panic("rejection handler panic")
		}
	}

	mw := NewMiddleware(limiter, nil, panicHandler)

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
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	time.Sleep(50 * time.Millisecond)

	// Send a request that will be rejected
	var recovered interface{}
	func() {
		defer func() {
			recovered = recover()
		}()

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	// Verify panic was re-thrown
	if recovered == nil {
		t.Error("expected panic to be re-thrown")
	}
	if recovered != "rejection handler panic" {
		t.Errorf("unexpected panic value: %v", recovered)
	}

	close(blocker)
	wg.Wait()

	// Verify state is consistent
	stats := limiter.Stats()
	if stats.Running != 0 || stats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0 after panic, got %+v", stats)
	}
}

func TestMiddleware_PanicDoesNotAffectOtherRequests(t *testing.T) {
	limiter := New(Config{Limit: 10})
	mw := NewMiddleware(limiter, nil, nil)

	panicHandler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler panic")
	}))

	okHandler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	successes := 0
	panics := 0
	mu := sync.Mutex{}

	// Mix of panicking and successful requests
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			if idx%2 == 0 {
				// Panicking request
				defer func() {
					if recover() != nil {
						mu.Lock()
						panics++
						mu.Unlock()
					}
				}()

				req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
				rec := httptest.NewRecorder()
				panicHandler.ServeHTTP(rec, req)
			} else {
				// Successful request
				req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
				rec := httptest.NewRecorder()
				okHandler.ServeHTTP(rec, req)

				if rec.Code == http.StatusOK {
					mu.Lock()
					successes++
					mu.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify both types completed correctly
	if panics != 10 {
		t.Errorf("expected 10 panics, got %d", panics)
	}
	if successes != 10 {
		t.Errorf("expected 10 successes, got %d", successes)
	}

	// Verify no leaks
	stats := limiter.Stats()
	if stats.Running != 0 || stats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", stats)
	}
}
