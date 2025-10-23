package loadshedder

import (
	"context"
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
	ctx := context.Background()

	ls := New(Config{Limit: 5})

	// Verify initial stats
	stats := ls.Stats()
	if stats.Running != 0 || stats.Waiting != 0 || stats.Limit != 5 {
		t.Errorf("unexpected initial stats: %+v", stats)
	}

	var tokens []*Token
	for i := range 5 {
		stats, token := ls.Acquire(ctx)
		tokens = append(tokens, token)

		if !token.Accepted() {
			t.Errorf("request %d: expected acquisition to succeed", i)
		}

		// Verify stats show correct running count
		expectedRunning := int64(i + 1)
		if stats.Running != expectedRunning || stats.Waiting != 0 || stats.Limit != 5 {
			t.Errorf("request %d: expected Running=%d, Waiting=0, Limit=5, got %+v",
				i, expectedRunning, stats)
		}
	}

	// Clean up all tokens and verify stats
	for i, token := range tokens {
		stats := ls.Release(token)
		expectedRunning := int64(len(tokens) - i - 1)
		if stats.Running != expectedRunning || stats.Waiting != 0 || stats.Limit != 5 {
			t.Errorf("release %d: expected Running=%d, Waiting=0, Limit=5, got %+v",
				i, expectedRunning, stats)
		}
	}
}

func TestLoadshedder_RejectsOverLimit(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{Limit: 2})

	// Acquire 2 slots (at limit)
	stats1, token1 := ls.Acquire(ctx)
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	if stats1.Running != 1 || stats1.Waiting != 0 {
		t.Errorf("unexpected stats after first acquisition: %+v", stats1)
	}

	stats2, token2 := ls.Acquire(ctx)
	if !token2.Accepted() {
		t.Fatal("expected second acquisition to succeed")
	}
	if stats2.Running != 2 || stats2.Waiting != 0 {
		t.Errorf("unexpected stats after second acquisition: %+v", stats2)
	}

	// Third acquisition should fail (no waiting limit)
	stats3, token3 := ls.Acquire(ctx)
	if token3.Accepted() {
		t.Error("expected third acquisition to fail")
	}
	if stats3.Running != 2 || stats3.Waiting != 1 {
		t.Errorf("unexpected stats after third acquisition: %+v", stats3)
	}

	stats4 := ls.Release(token3) // Safe to call even if not accepted
	if stats4.Running != 2 || stats4.Waiting != 0 {
		t.Errorf("unexpected stats after release of rejected token: %+v", stats4)
	}

	// Release one slot
	stats5 := ls.Release(token1)
	if stats5.Running != 1 || stats5.Waiting != 0 {
		t.Errorf("unexpected stats after first release: %+v", stats5)
	}

	// Now should be able to acquire again
	stats6, token4 := ls.Acquire(ctx)
	if !token4.Accepted() {
		t.Error("expected acquisition to succeed after release")
	}
	if stats6.Running != 2 || stats6.Waiting != 0 {
		t.Errorf("unexpected stats after acquisition post-release: %+v", stats6)
	}

	ls.Release(token2)
	ls.Release(token4)
}

func TestLimiter_ConcurrentRequests(t *testing.T) {
	ctx := context.Background()

	var limit int64 = 10
	ls := New(Config{Limit: limit})

	// Verify initial stats
	stats := ls.Stats()
	if stats.Running != 0 || stats.Waiting != 0 || stats.Limit != limit {
		t.Errorf("unexpected initial stats: %+v", stats)
	}

	maxConcurrent := atomic.Int64{}
	currentConcurrent := atomic.Int64{}

	const numRequests = 100
	var wg sync.WaitGroup
	accepted := atomic.Int64{}
	rejected := atomic.Int64{}

	for range numRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()

			stats, token := ls.Acquire(ctx)

			// Verify stats never show running > limit
			if stats.Running > limit {
				t.Errorf("stats.Running=%d exceeded limit=%d", stats.Running, limit)
			}

			if !token.Accepted() {
				rejected.Add(1)
				return
			}

			accepted.Add(1)
			current := currentConcurrent.Add(1)

			// Track the maximum concurrency we see
			for {
				maxVal := maxConcurrent.Load()
				if current <= maxVal || maxConcurrent.CompareAndSwap(maxVal, current) {
					break
				}
			}

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			currentConcurrent.Add(-1)
			releaseStats := ls.Release(token)

			// Verify stats after release
			if releaseStats.Running > limit {
				t.Errorf("stats.Running=%d exceeded limit=%d after release", releaseStats.Running, limit)
			}
		}()
	}

	wg.Wait()

	// Verify final stats - all requests completed
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != limit {
		t.Errorf("expected final stats Running=0, Waiting=0, Limit=%d, got %+v", limit, finalStats)
	}

	// Verify we never exceeded the limit
	if maxConcurrent.Load() > limit {
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

func TestLoadshedder_Stats(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        3,
		WaitingLimit: 2,
	})

	// Initially empty
	stats := ls.Stats()
	if stats.Running != 0 || stats.Waiting != 0 || stats.Limit != 3 {
		t.Errorf("unexpected initial stats: %+v", stats)
	}

	// Acquire one
	_, token1 := ls.Acquire(ctx)
	stats = ls.Stats()
	if stats.Running != 1 || stats.Waiting != 0 || stats.Limit != 3 {
		t.Errorf("unexpected stats after one acquisition: %+v", stats)
	}

	// Acquire two more
	_, token2 := ls.Acquire(ctx)
	_, token3 := ls.Acquire(ctx)
	stats = ls.Stats()
	if stats.Running != 3 || stats.Waiting != 0 || stats.Limit != 3 {
		t.Errorf("unexpected stats at limit: %+v", stats)
	}

	// Release all
	ls.Release(token1)
	ls.Release(token2)
	ls.Release(token3)
	stats = ls.Stats()
	if stats.Running != 0 || stats.Waiting != 0 || stats.Limit != 3 {
		t.Errorf("unexpected stats after release: %+v", stats)
	}
}

func TestLoadshedder_PanicsWithNegativeWaitingLimit(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with negative waiting limit")
		}
	}()
	New(Config{Limit: 10, WaitingLimit: -1})
}

func TestLoadshedder_ContextTimeout(t *testing.T) {
	ls := New(Config{
		Limit:        1,
		WaitingLimit: 2,
	})

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill the running limit
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats, token := ls.Acquire(context.Background())
		if !token.Accepted() {
			t.Error("expected acquisition to succeed")
			return
		}
		if stats.Running != 1 || stats.Limit != 1 {
			t.Errorf("expected Running=1, Limit=1, got %+v", stats)
		}
		<-blocker
		ls.Release(token)
	}()

	time.Sleep(50 * time.Millisecond)

	// Try to acquire with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	stats, token := ls.Acquire(ctx)
	elapsed := time.Since(start)

	if token.Accepted() {
		t.Error("expected acquisition to fail due to timeout")
		ls.Release(token)
	}

	// Should have waited close to timeout
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected to wait for timeout, but returned too quickly: %v", elapsed)
	}

	if stats.Limit != 1 {
		t.Errorf("expected Limit=1, got %+v", stats)
	}

	close(blocker)
	wg.Wait()

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 1 {
		t.Errorf("expected final stats Running=0, Waiting=0, Limit=1, got %+v", finalStats)
	}
}

func TestLoadshedder_PreCancelledContext(t *testing.T) {
	ls := New(Config{Limit: 10})

	// Create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stats, token := ls.Acquire(ctx)
	if token.Accepted() {
		t.Error("expected acquisition to fail with pre-cancelled context")
		ls.Release(token)
	}

	// Stats should be consistent
	if stats.Limit != 10 {
		t.Errorf("expected Limit=10, got %+v", stats)
	}

	// Verify stats show no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected no running or waiting requests, got %+v", finalStats)
	}
}

func TestLoadshedder_LimitOne(t *testing.T) {
	ctx := context.Background()

	// Limit=1 is prone to off-by-one errors
	ls := New(Config{Limit: 1})

	// First should succeed
	stats1, token1 := ls.Acquire(ctx)
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	if stats1.Running != 1 || stats1.Limit != 1 {
		t.Errorf("unexpected stats: %+v", stats1)
	}

	// Second should fail (no waiting limit)
	stats2, token2 := ls.Acquire(ctx)
	if token2.Accepted() {
		t.Fatal("expected second acquisition to fail")
	}
	if stats2.Running != 1 || stats2.Limit != 1 {
		t.Errorf("unexpected stats: %+v", stats2)
	}

	// Release and acquire again
	releaseStats := ls.Release(token1)
	if releaseStats.Running != 0 || releaseStats.Limit != 1 {
		t.Errorf("unexpected stats after release: %+v", releaseStats)
	}

	stats3, token3 := ls.Acquire(ctx)
	if !token3.Accepted() {
		t.Fatal("expected acquisition to succeed after release")
	}
	if stats3.Running != 1 || stats3.Limit != 1 {
		t.Errorf("unexpected stats: %+v", stats3)
	}

	ls.Release(token3)
}

func TestLoadshedder_WaitTime(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        2,
		WaitingLimit: 2,
	})

	var wg sync.WaitGroup

	// Test 1: Immediate acceptance (minimal wait, < 1ms overhead)
	stats, token1 := ls.Acquire(ctx)
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	if stats.WaitTime > time.Millisecond {
		t.Errorf("expected WaitTime < 1ms for immediate acceptance, got %v", stats.WaitTime)
	}

	stats, token2 := ls.Acquire(ctx)
	if !token2.Accepted() {
		t.Fatal("expected second acquisition to succeed")
	}
	if stats.WaitTime > time.Millisecond {
		t.Errorf("expected WaitTime < 1ms for immediate acceptance, got %v", stats.WaitTime)
	}

	// Test 2: Hard rejection
	// Start two goroutines that will wait (fill the waiting queue)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, token := ls.Acquire(ctx)
		if token.Accepted() {
			defer ls.Release(token)
			time.Sleep(100 * time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		_, token := ls.Acquire(ctx)
		if token.Accepted() {
			defer ls.Release(token)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Give them time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Fifth should be hard rejected (exceeds limit + waitingLimit)
	stats, token5 := ls.Acquire(ctx)
	if token5.Accepted() {
		t.Fatal("expected fifth acquisition to fail (hard rejection)")
		ls.Release(token5)
	}
	if stats.WaitTime != 0 {
		t.Errorf("expected WaitTime=0 for hard rejection, got %v", stats.WaitTime)
	}

	// Release tokens to let waiters proceed
	ls.Release(token1)
	ls.Release(token2)
	wg.Wait()

	// Test 3: Acquisition with waiting
	// Re-acquire to fill the limit
	_, token3 := ls.Acquire(ctx)
	if !token3.Accepted() {
		t.Fatal("expected third acquisition to succeed")
	}
	_, token4 := ls.Acquire(ctx)
	if !token4.Accepted() {
		t.Fatal("expected fourth acquisition to succeed")
	}

	// Start a goroutine that will wait
	wg.Add(1)
	var waitStats Stats
	var waitToken *Token
	go func() {
		defer wg.Done()
		waitStats, waitToken = ls.Acquire(ctx)
	}()

	// Give it time to start waiting
	time.Sleep(50 * time.Millisecond)

	// Release one slot to unblock the waiter
	ls.Release(token3)

	wg.Wait()

	if !waitToken.Accepted() {
		t.Fatal("expected waiting acquisition to succeed")
	}
	if waitStats.WaitTime < 40*time.Millisecond {
		t.Errorf("expected WaitTime >= 40ms for waiting request, got %v", waitStats.WaitTime)
	}
	if waitStats.WaitTime > 200*time.Millisecond {
		t.Errorf("expected WaitTime < 200ms for waiting request, got %v (too long)", waitStats.WaitTime)
	}

	// Test 4: Context cancellation while waiting
	cancelCtx, cancel := context.WithCancel(ctx)

	wg.Add(1)
	var cancelStats Stats
	var cancelToken *Token
	go func() {
		defer wg.Done()
		cancelStats, cancelToken = ls.Acquire(cancelCtx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	wg.Wait()

	if cancelToken.Accepted() {
		t.Error("expected cancelled acquisition to fail")
		ls.Release(cancelToken)
	}
	if cancelStats.WaitTime < 40*time.Millisecond {
		t.Errorf("expected WaitTime >= 40ms for cancelled request, got %v", cancelStats.WaitTime)
	}

	// Cleanup
	ls.Release(token4)
	ls.Release(waitToken)
}

func BenchmarkLimiter(b *testing.B) {
	ctx := context.Background()

	ls := New(Config{Limit: 100})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				ls.Release(token)
			}
		}
	})
}

func BenchmarkLimiter_NoContention(b *testing.B) {
	ctx := context.Background()

	ls := New(Config{Limit: 1000})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				ls.Release(token)
			}
		}
	})
}

func BenchmarkLimiter_HighContention(b *testing.B) {
	ctx := context.Background()

	// Low limit with high concurrency = high contention
	ls := New(Config{Limit: 10})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				ls.Release(token)
			}
		}
	})
}

func BenchmarkLimiter_WithWaiting(b *testing.B) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        100,
		WaitingLimit: 50,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				ls.Release(token)
			}
		}
	})
}

func BenchmarkLimiter_AcceptedPath(b *testing.B) {
	ctx := context.Background()

	// Very high limit ensures all requests are accepted
	ls := New(Config{Limit: 10000})

	for b.Loop() {
		_, token := ls.Acquire(ctx)
		ls.Release(token)
	}
}

func BenchmarkLimiter_RejectedPath(b *testing.B) {
	ctx := context.Background()

	// Limit=1, no waiting, pre-fill it
	ls := New(Config{Limit: 1})

	// Fill the limit
	_, token := ls.Acquire(ctx)
	defer ls.Release(token)

	for b.Loop() {
		_, rejectedToken := ls.Acquire(ctx)
		ls.Release(rejectedToken) // Safe no-op
	}
}

func BenchmarkLimiter_Stats(b *testing.B) {
	ls := New(Config{Limit: 100})

	for b.Loop() {
		_ = ls.Stats()
	}
}
