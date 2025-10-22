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

func TestLoadshedder_WaitingLimit_AcceptsWithinWaitingLimit(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        2,
		WaitingLimit: 2,
	})

	// Verify initial stats
	initialStats := ls.Stats()
	if initialStats.Running != 0 || initialStats.Waiting != 0 || initialStats.Limit != 2 {
		t.Errorf("unexpected initial stats: %+v", initialStats)
	}

	// Fill the running limit
	stats1, token1 := ls.Acquire(ctx)
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	if stats1.Running != 1 || stats1.Waiting != 0 || stats1.Limit != 2 {
		t.Errorf("expected Running=1, Waiting=0, Limit=2, got %+v", stats1)
	}

	stats2, token2 := ls.Acquire(ctx)
	if !token2.Accepted() {
		t.Fatal("expected second acquisition to succeed")
	}
	if stats2.Running != 2 || stats2.Waiting != 0 || stats2.Limit != 2 {
		t.Errorf("expected Running=2, Waiting=0, Limit=2, got %+v", stats2)
	}

	// Now we're at the running limit. Acquire 2 more that should wait
	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// These should wait (within waiting limit)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				t.Errorf("request %d: expected to be accepted (waiting)", idx)
				return
			}
			<-blocker
			ls.Release(token)
		}(i)
	}

	time.Sleep(50 * time.Millisecond)

	// Check stats - should show 2 running, 2 waiting
	stats := ls.Stats()
	if stats.Running != 2 || stats.Waiting != 2 || stats.Limit != 2 {
		t.Errorf("expected Running=2, Waiting=2, Limit=2, got %+v", stats)
	}

	// Release the blocking tokens to let waiting requests proceed
	releaseStats1 := ls.Release(token1)
	if releaseStats1.Limit != 2 {
		t.Errorf("expected Limit=2, got %+v", releaseStats1)
	}

	releaseStats2 := ls.Release(token2)
	if releaseStats2.Limit != 2 {
		t.Errorf("expected Limit=2, got %+v", releaseStats2)
	}

	close(blocker)
	wg.Wait()

	// Verify final stats - all requests completed
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 2 {
		t.Errorf("expected final stats Running=0, Waiting=0, Limit=2, got %+v", finalStats)
	}
}

func TestLoadshedder_WaitingLimit_RejectsBeyondWaitingLimit(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        2,
		WaitingLimit: 1,
	})

	// Verify initial stats
	initialStats := ls.Stats()
	if initialStats.Running != 0 || initialStats.Waiting != 0 || initialStats.Limit != 2 {
		t.Errorf("unexpected initial stats: %+v", initialStats)
	}

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill the running limit with blocking requests
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				t.Error("expected acquisition to succeed")
				return
			}
			<-blocker
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Verify running limit is filled
	midStats := ls.Stats()
	if midStats.Running != 2 || midStats.Waiting != 0 || midStats.Limit != 2 {
		t.Errorf("expected Running=2, Waiting=0, Limit=2, got %+v", midStats)
	}

	// Add one waiting request (at waiting limit)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, token := ls.Acquire(ctx)
		if !token.Accepted() {
			t.Error("expected acquisition to succeed (waiting)")
			return
		}
		<-blocker
		ls.Release(token)
	}()

	time.Sleep(50 * time.Millisecond)

	// Verify we have waiting requests
	waitStats := ls.Stats()
	if waitStats.Running != 2 || waitStats.Waiting != 1 || waitStats.Limit != 2 {
		t.Errorf("expected Running=2, Waiting=1, Limit=2, got %+v", waitStats)
	}

	// This one should be rejected (exceeds waiting limit)
	stats, token := ls.Acquire(ctx)
	if token.Accepted() {
		t.Error("expected request to be rejected (exceeds waiting limit)")
		ls.Release(token)
	}

	// Stats at rejection time show the state including the rejected request
	// current=4 (2 running + 1 waiting + 1 rejected), so Running=2, Waiting=2
	if stats.Running != 2 || stats.Waiting != 2 || stats.Limit != 2 {
		t.Errorf("expected Running=2, Waiting=2, Limit=2, got %+v", stats)
	}

	close(blocker)
	wg.Wait()

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 2 {
		t.Errorf("expected final stats Running=0, Waiting=0, Limit=2, got %+v", finalStats)
	}
}

func TestLoadshedder_ContextCancellation(t *testing.T) {
	ls := New(Config{
		Limit:        1,
		WaitingLimit: 1,
	})

	// Verify initial stats
	initialStats := ls.Stats()
	if initialStats.Running != 0 || initialStats.Waiting != 0 || initialStats.Limit != 1 {
		t.Errorf("unexpected initial stats: %+v", initialStats)
	}

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
		if stats.Running != 1 || stats.Waiting != 0 || stats.Limit != 1 {
			t.Errorf("expected Running=1, Waiting=0, Limit=1, got %+v", stats)
		}
		<-blocker
		ls.Release(token)
	}()

	time.Sleep(50 * time.Millisecond)

	// Verify running limit is filled
	midStats := ls.Stats()
	if midStats.Running != 1 || midStats.Limit != 1 {
		t.Errorf("expected Running=1, Limit=1, got %+v", midStats)
	}

	// Try to acquire with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stats, token := ls.Acquire(ctx)
	if token.Accepted() {
		t.Error("expected acquisition to fail with cancelled context")
		ls.Release(token)
	}

	// Stats should reflect the rejected request was counted then decremented
	if stats.Running > 1 || stats.Limit != 1 {
		t.Errorf("unexpected stats after rejection: %+v", stats)
	}

	close(blocker)
	wg.Wait()

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 1 {
		t.Errorf("expected final stats Running=0, Waiting=0, Limit=1, got %+v", finalStats)
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

func TestLoadshedder_TokenDoubleReleaseSafety(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{Limit: 2})

	// Verify initial stats
	initialStats := ls.Stats()
	if initialStats.Running != 0 || initialStats.Waiting != 0 || initialStats.Limit != 2 {
		t.Errorf("unexpected initial stats: %+v", initialStats)
	}

	// Acquire a token
	stats1, token := ls.Acquire(ctx)
	if !token.Accepted() {
		t.Fatal("expected acquisition to succeed")
	}
	if stats1.Running != 1 || stats1.Waiting != 0 || stats1.Limit != 2 {
		t.Errorf("expected Running=1, Waiting=0, Limit=2, got %+v", stats1)
	}

	// Release once
	stats2 := ls.Release(token)
	if stats2.Running != 0 || stats2.Waiting != 0 || stats2.Limit != 2 {
		t.Errorf("expected Running=0, Waiting=0, Limit=2 after release, got %+v", stats2)
	}

	// Release again (should be no-op)
	stats3 := ls.Release(token)
	if stats3.Running != 0 || stats3.Waiting != 0 || stats3.Limit != 2 {
		t.Errorf("expected Running=0, Waiting=0, Limit=2 after double release, got %+v", stats3)
	}

	// Verify we can still acquire (counter didn't go negative)
	stats4, token2 := ls.Acquire(ctx)
	if !token2.Accepted() {
		t.Error("expected acquisition to succeed after double release")
	}
	if stats4.Running != 1 || stats4.Waiting != 0 || stats4.Limit != 2 {
		t.Errorf("expected Running=1, Waiting=0, Limit=2 after re-acquire, got %+v", stats4)
	}

	stats5 := ls.Release(token2)
	if stats5.Running != 0 || stats5.Waiting != 0 || stats5.Limit != 2 {
		t.Errorf("expected Running=0, Waiting=0, Limit=2 at end, got %+v", stats5)
	}
}

func TestLoadshedder_ReleaseRejectedTokenSafety(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{Limit: 1})

	// Verify initial stats
	initialStats := ls.Stats()
	if initialStats.Running != 0 || initialStats.Waiting != 0 || initialStats.Limit != 1 {
		t.Errorf("unexpected initial stats: %+v", initialStats)
	}

	// Fill the limit
	stats1, token1 := ls.Acquire(ctx)
	if !token1.Accepted() {
		t.Fatal("expected first acquisition to succeed")
	}
	if stats1.Running != 1 || stats1.Waiting != 0 || stats1.Limit != 1 {
		t.Errorf("expected Running=1, Waiting=0, Limit=1, got %+v", stats1)
	}

	// Try to acquire beyond limit
	stats2, token2 := ls.Acquire(ctx)
	if token2.Accepted() {
		t.Fatal("expected second acquisition to fail")
	}
	// At rejection time, counter was briefly incremented then decremented
	if stats2.Limit != 1 {
		t.Errorf("expected Limit=1, got %+v", stats2)
	}

	// Release the rejected token (should be no-op)
	stats3 := ls.Release(token2)
	if stats3.Running != 1 || stats3.Waiting != 0 || stats3.Limit != 1 {
		t.Errorf("expected Running=1, Waiting=0, Limit=1 after releasing rejected token, got %+v", stats3)
	}

	// Release the accepted token
	stats4 := ls.Release(token1)
	if stats4.Running != 0 || stats4.Waiting != 0 || stats4.Limit != 1 {
		t.Errorf("expected Running=0, Waiting=0, Limit=1 after releasing accepted token, got %+v", stats4)
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

func TestLoadshedder_ReleaseNil(t *testing.T) {
	ls := New(Config{Limit: 10})

	// Release(nil) should be safe and not panic
	stats := ls.Release(nil)
	if stats.Running != 0 || stats.Waiting != 0 || stats.Limit != 10 {
		t.Errorf("expected Running=0, Waiting=0, Limit=10, got %+v", stats)
	}
}

func TestLoadshedder_ConcurrentDoubleRelease(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{Limit: 10})

	stats, token := ls.Acquire(ctx)
	if !token.Accepted() {
		t.Fatal("expected acquisition to succeed")
	}
	if stats.Running != 1 || stats.Limit != 10 {
		t.Errorf("expected Running=1, Limit=10, got %+v", stats)
	}

	// Attempt concurrent releases of the same token
	const numGoroutines = 10
	var wg sync.WaitGroup
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ls.Release(token)
		}()
	}

	wg.Wait()

	// Should only have released once
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 10 {
		t.Errorf("expected Running=0, Waiting=0, Limit=10 after concurrent releases, got %+v", finalStats)
	}

	// Should still be able to acquire
	stats2, token2 := ls.Acquire(ctx)
	if !token2.Accepted() {
		t.Fatal("expected acquisition to succeed after concurrent releases")
	}
	if stats2.Running != 1 || stats2.Limit != 10 {
		t.Errorf("expected Running=1, Limit=10, got %+v", stats2)
	}

	ls.Release(token2)
}

func TestLoadshedder_StressTest(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        50,
		WaitingLimit: 25,
	})

	const numGoroutines = 500
	const numIterations = 100

	var wg sync.WaitGroup
	accepted := atomic.Int64{}
	rejected := atomic.Int64{}
	maxRunning := atomic.Int64{}

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range numIterations {
				stats, token := ls.Acquire(ctx)

				// Track max running we observe
				for {
					current := maxRunning.Load()
					if stats.Running <= current || maxRunning.CompareAndSwap(current, stats.Running) {
						break
					}
				}

				if !token.Accepted() {
					rejected.Add(1)
					continue
				}

				accepted.Add(1)

				// Small random delay to simulate work
				time.Sleep(time.Duration(stats.Running%5) * time.Microsecond)

				ls.Release(token)
			}
		}()
	}

	wg.Wait()

	// Verify final state
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 50 {
		t.Errorf("expected Running=0, Waiting=0, Limit=50, got %+v", finalStats)
	}

	// Verify we never exceeded limit
	if maxRunning.Load() > 50 {
		t.Errorf("exceeded limit: max running=%d, limit=50", maxRunning.Load())
	}

	// Verify all operations completed
	total := accepted.Load() + rejected.Load()
	expected := int64(numGoroutines * numIterations)
	if total != expected {
		t.Errorf("expected %d total operations, got %d", expected, total)
	}

	t.Logf("Stress test: %d operations (%d accepted, %d rejected, max running: %d)",
		total, accepted.Load(), rejected.Load(), maxRunning.Load())
}

func TestLoadshedder_WaitingWithHighConcurrency(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        10,
		WaitingLimit: 20,
	})

	const numRequests = 100
	blocker := make(chan struct{})
	var wg sync.WaitGroup

	accepted := atomic.Int64{}
	rejected := atomic.Int64{}
	maxWaiting := atomic.Int64{}

	for range numRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()

			stats, token := ls.Acquire(ctx)

			// Track max waiting
			for {
				current := maxWaiting.Load()
				if stats.Waiting <= current || maxWaiting.CompareAndSwap(current, stats.Waiting) {
					break
				}
			}

			if !token.Accepted() {
				rejected.Add(1)
				return
			}

			accepted.Add(1)
			<-blocker
			ls.Release(token)
		}()
	}

	// Give time for all goroutines to reach acquire
	time.Sleep(100 * time.Millisecond)

	// Check that we have both running and waiting
	stats := ls.Stats()
	if stats.Running != 10 {
		t.Errorf("expected Running=10, got %+v", stats)
	}
	if stats.Waiting < 1 {
		t.Errorf("expected some waiting requests, got %+v", stats)
	}

	// Release all
	close(blocker)
	wg.Wait()

	// Verify final state
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 10 {
		t.Errorf("expected Running=0, Waiting=0, Limit=10, got %+v", finalStats)
	}

	// Should have accepted 30 (10 running + 20 waiting) and rejected 70
	if accepted.Load() != 30 {
		t.Errorf("expected 30 accepted, got %d", accepted.Load())
	}
	if rejected.Load() != 70 {
		t.Errorf("expected 70 rejected, got %d", rejected.Load())
	}

	t.Logf("High concurrency: %d accepted, %d rejected, max waiting: %d",
		accepted.Load(), rejected.Load(), maxWaiting.Load())
}

func TestLoadshedder_ZeroWaitingLimitWithHighConcurrency(t *testing.T) {
	ctx := context.Background()

	// WaitingLimit=0 means immediate rejection
	ls := New(Config{
		Limit:        5,
		WaitingLimit: 0,
	})

	const numRequests = 50
	blocker := make(chan struct{})
	var wg sync.WaitGroup

	accepted := atomic.Int64{}
	rejected := atomic.Int64{}

	for range numRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()

			stats, token := ls.Acquire(ctx)

			if !token.Accepted() {
				// Should see Running=5, Waiting=1 at rejection (momentary spike)
				if stats.Running > 5 {
					t.Errorf("running exceeded limit: %+v", stats)
				}
				rejected.Add(1)
				return
			}

			accepted.Add(1)
			<-blocker
			ls.Release(token)
		}()
	}

	// Give time for goroutines to attempt acquire
	time.Sleep(100 * time.Millisecond)

	// Should have filled the limit
	stats := ls.Stats()
	if stats.Running != 5 {
		t.Errorf("expected Running=5, got %+v", stats)
	}
	if stats.Waiting != 0 {
		t.Errorf("expected Waiting=0 (no waiting allowed), got %+v", stats)
	}

	// Release all
	close(blocker)
	wg.Wait()

	// Verify final state
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 5 {
		t.Errorf("expected Running=0, Waiting=0, Limit=5, got %+v", finalStats)
	}

	// Should have accepted exactly 5 (the limit) and rejected 45
	if accepted.Load() != 5 {
		t.Errorf("expected 5 accepted, got %d", accepted.Load())
	}
	if rejected.Load() != 45 {
		t.Errorf("expected 45 rejected, got %d", rejected.Load())
	}
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
