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
