package loadshedder

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew_PanicsWithZeroLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with zero limit")
		}
	}()
	New(Config{Limit: 0})
}

func TestNew_PanicsWithNegativeLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with negative limit")
		}
	}()
	New(Config{Limit: -1})
}

func TestLoadshedder_AcceptsUnderLimit(t *testing.T) {
	ls := New(Config{Limit: 5})

	for i := 0; i < 5; i++ {
		token := ls.Acquire()
		defer token.Release()
		if !token.Accepted() {
			t.Errorf("request %d: expected acquisition to succeed", i)
		}
	}
}

func TestLoadshedder_RejectsOverLimit(t *testing.T) {
	ls := New(Config{Limit: 2})

	// Acquire 2 slots (at limit)
	token1 := ls.Acquire()
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	token2 := ls.Acquire()
	if !token2.Accepted() {
		t.Fatal("expected second acquisition to succeed")
	}

	// Third acquisition should fail
	token3 := ls.Acquire()
	if token3.Accepted() {
		t.Error("expected third acquisition to fail")
	}
	token3.Release() // Safe to call even if not accepted

	// Release one slot
	token1.Release()

	// Now should be able to acquire again
	token4 := ls.Acquire()
	if !token4.Accepted() {
		t.Error("expected acquisition to succeed after release")
	}
	token2.Release()
	token4.Release()
}

func TestLimiter_ConcurrentRequests(t *testing.T) {
	limit := 10
	ls := New(Config{Limit: limit})

	maxConcurrent := atomic.Int64{}
	currentConcurrent := atomic.Int64{}

	const numRequests = 100
	var wg sync.WaitGroup
	accepted := atomic.Int64{}
	rejected := atomic.Int64{}

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			token := ls.Acquire()
			if !token.Accepted() {
				rejected.Add(1)
				return
			}

			accepted.Add(1)
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
			token.Release()
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

func TestLimiter_QoS_AcceptsOverLimitWithLowWait(t *testing.T) {
	ls := New(Config{
		Limit:       2,
		MaxWaitTime: 500 * time.Millisecond,
	})

	// Establish an average duration
	for i := 0; i < 5; i++ {
		token := ls.Acquire()
		if !token.Accepted() {
			t.Fatal("expected acquisition to succeed")
		}
		time.Sleep(50 * time.Millisecond) // Simulate fast requests
		token.Release()
	}

	// Fill the limit with blocking operations
	var wg sync.WaitGroup
	blocker := make(chan struct{})

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token := ls.Acquire()
			if !token.Accepted() {
				t.Error("expected acquisition to succeed")
				return
			}
			<-blocker
			token.Release()
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Try to acquire over the limit
	// Projected wait = 1 * ~50ms = ~50ms < 500ms threshold
	// Should be ACCEPTED
	wg.Add(1)
	accepted := atomic.Bool{}
	go func() {
		defer wg.Done()
		token := ls.Acquire()
		if token.Accepted() {
			accepted.Store(true)
			<-blocker
			token.Release()
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(blocker)
	wg.Wait()

	if !accepted.Load() {
		t.Error("expected request to be accepted due to low projected wait time")
	}
}

func TestLimiter_QoS_RejectsWithHighWait(t *testing.T) {
	ls := New(Config{
		Limit:       2,
		MaxWaitTime: 50 * time.Millisecond,
	})

	// Establish a long average duration
	for i := 0; i < 5; i++ {
		token := ls.Acquire()
		if !token.Accepted() {
			t.Fatal("expected acquisition to succeed")
		}
		time.Sleep(200 * time.Millisecond) // Simulate slow requests
		token.Release()
	}

	// Fill the limit
	token1 := ls.Acquire()
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	token2 := ls.Acquire()
	if !token2.Accepted() {
		t.Fatal("expected second acquisition to succeed")
	}

	// Try to acquire over the limit
	// Projected wait = 1 * ~200ms = ~200ms > 50ms threshold
	// Should be REJECTED
	token3 := ls.Acquire()
	if token3.Accepted() {
		t.Error("expected acquisition to fail due to high projected wait time")
	}
	token3.Release() // Safe to call even if not accepted

	token1.Release()
	token2.Release()
}

func TestConfig_EMAAlphaPanicsWithInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
	}{
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
			New(Config{
				Limit:    10,
				EMAAlpha: tt.alpha,
			})
		})
	}
}

func BenchmarkLimiter(b *testing.B) {
	ls := New(Config{Limit: 100})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			token := ls.Acquire()
			if token.Accepted() {
				token.Release()
			}
		}
	})
}

func BenchmarkLimiter_WithQoS(b *testing.B) {
	ls := New(Config{
		Limit:       100,
		MaxWaitTime: 100 * time.Millisecond,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			token := ls.Acquire()
			if token.Accepted() {
				token.Release()
			}
		}
	})
}
