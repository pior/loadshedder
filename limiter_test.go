package loadshedder

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewLimiter_PanicsWithZeroLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with zero limit")
		}
	}()
	NewLimiter(0)
}

func TestNewLimiter_PanicsWithNegativeLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with negative limit")
		}
	}()
	NewLimiter(-1)
}

func TestLimiter_AcceptsUnderLimit(t *testing.T) {
	limiter := NewLimiter(5)

	for i := 0; i < 5; i++ {
		if !limiter.Acquire() {
			t.Errorf("request %d: expected acquisition to succeed", i)
		}
		limiter.Release(10 * time.Millisecond)
	}
}

func TestLimiter_RejectsOverLimit(t *testing.T) {
	limiter := NewLimiter(2)

	// Acquire 2 slots (at limit)
	if !limiter.Acquire() {
		t.Fatal("expected first acquisition to succeed")
	}
	if !limiter.Acquire() {
		t.Fatal("expected second acquisition to succeed")
	}

	// Third acquisition should fail
	if limiter.Acquire() {
		t.Error("expected third acquisition to fail")
		limiter.Release(0)
	}

	// Release one slot
	limiter.Release(10 * time.Millisecond)

	// Now should be able to acquire again
	if !limiter.Acquire() {
		t.Error("expected acquisition to succeed after release")
	}
	limiter.Release(10 * time.Millisecond)
	limiter.Release(10 * time.Millisecond)
}

func TestLimiter_ConcurrentRequests(t *testing.T) {
	limit := 10
	limiter := NewLimiter(limit)

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

			if !limiter.Acquire() {
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
			limiter.Release(10 * time.Millisecond)
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
	limiter := NewLimiter(2, WithMaxWaitTime(500*time.Millisecond))

	// Establish an average duration
	for i := 0; i < 5; i++ {
		if !limiter.Acquire() {
			t.Fatal("expected acquisition to succeed")
		}
		limiter.Release(50 * time.Millisecond) // Fast requests
	}

	// Fill the limit with blocking operations
	var wg sync.WaitGroup
	blocker := make(chan struct{})

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !limiter.Acquire() {
				t.Error("expected acquisition to succeed")
				return
			}
			<-blocker
			limiter.Release(10 * time.Millisecond)
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
		if limiter.Acquire() {
			accepted.Store(true)
			<-blocker
			limiter.Release(10 * time.Millisecond)
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
	limiter := NewLimiter(2, WithMaxWaitTime(50*time.Millisecond))

	// Establish a long average duration
	for i := 0; i < 5; i++ {
		if !limiter.Acquire() {
			t.Fatal("expected acquisition to succeed")
		}
		limiter.Release(200 * time.Millisecond) // Slow requests
	}

	// Fill the limit
	if !limiter.Acquire() {
		t.Fatal("expected first acquisition to succeed")
	}
	if !limiter.Acquire() {
		t.Fatal("expected second acquisition to succeed")
	}

	// Try to acquire over the limit
	// Projected wait = 1 * ~200ms = ~200ms > 50ms threshold
	// Should be REJECTED
	if limiter.Acquire() {
		t.Error("expected acquisition to fail due to high projected wait time")
		limiter.Release(0)
	}

	limiter.Release(10 * time.Millisecond)
	limiter.Release(10 * time.Millisecond)
}

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
			NewLimiter(10, WithEMAAlpha(tt.alpha))
		})
	}
}

func BenchmarkLimiter(b *testing.B) {
	limiter := NewLimiter(100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if limiter.Acquire() {
				limiter.Release(time.Microsecond)
			}
		}
	})
}

func BenchmarkLimiter_WithQoS(b *testing.B) {
	limiter := NewLimiter(100, WithMaxWaitTime(100*time.Millisecond))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if limiter.Acquire() {
				limiter.Release(time.Microsecond)
			}
		}
	})
}
