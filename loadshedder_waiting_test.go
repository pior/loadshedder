package loadshedder

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Waiting queue tests verify that the optional waiting queue behaves correctly:
// requests wait within waitingLimit, rejection beyond waitingLimit, FIFO ordering,
// context cancellation while waiting.

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

func TestLoadshedder_WaitingQueue_ExactlyAtCapacity(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        2,
		WaitingLimit: 2,
	})

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill running slots (2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				t.Error("expected running acquisition to succeed")
				return
			}
			<-blocker
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Fill waiting slots (2) - current = 4 (2 running + 2 waiting)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				t.Error("expected waiting acquisition to succeed")
				return
			}
			<-blocker
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Verify we're at capacity
	stats := ls.Stats()
	if stats.Running != 2 || stats.Waiting != 2 || stats.Limit != 2 {
		t.Errorf("expected Running=2, Waiting=2, Limit=2, got %+v", stats)
	}

	// 5th request should be hard rejected (current would be 5 > limit+waitingLimit=4)
	stats5, token5 := ls.Acquire(ctx)
	if token5.Accepted() {
		t.Error("expected 5th request to be hard rejected")
		ls.Release(token5)
	}
	if stats5.WaitTime != 0 {
		t.Errorf("expected WaitTime=0 for hard rejection, got %v", stats5.WaitTime)
	}

	close(blocker)
	wg.Wait()

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 || finalStats.Limit != 2 {
		t.Errorf("expected final stats Running=0, Waiting=0, Limit=2, got %+v", finalStats)
	}
}

func TestLoadshedder_WaitingQueue_OnlyOneWaiterProceeds(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        2,
		WaitingLimit: 10,
	})

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill running slots (2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				t.Error("expected running acquisition to succeed")
				return
			}
			<-blocker
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Start 3 waiters
	acquired := atomic.Int64{}
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				t.Error("expected waiting acquisition to succeed")
				return
			}
			acquired.Add(1)

			// Hold for a moment to prevent immediate release
			time.Sleep(100 * time.Millisecond)
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Verify 3 are waiting
	stats := ls.Stats()
	if stats.Running != 2 || stats.Waiting != 3 {
		t.Errorf("expected Running=2, Waiting=3, got %+v", stats)
	}

	// Release 1 running slot
	blocker <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Verify at least 1 waiter proceeded
	if acquired.Load() < 1 {
		t.Errorf("expected at least 1 waiter to acquire, got %d", acquired.Load())
	}
	stats = ls.Stats()
	if stats.Running != 2 {
		t.Errorf("expected Running=2 after 1 release, got %+v", stats)
	}

	// Release another slot
	blocker <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Verify at least 2 waiters have proceeded (may be 3 due to timing)
	if acquired.Load() < 2 {
		t.Errorf("expected at least 2 waiters to acquire, got %d", acquired.Load())
	}

	close(blocker)
	wg.Wait()

	// All 3 should have eventually acquired
	if acquired.Load() != 3 {
		t.Errorf("expected all 3 waiters to eventually acquire, got %d", acquired.Load())
	}

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected final stats Running=0, Waiting=0, got %+v", finalStats)
	}
}

func TestLoadshedder_WaitingQueue_AllWaitersCancelledSimultaneously(t *testing.T) {
	ls := New(Config{
		Limit:        2,
		WaitingLimit: 10,
	})

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill running slots
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(context.Background())
			if !token.Accepted() {
				t.Error("expected running acquisition to succeed")
				return
			}
			<-blocker
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Start 5 waiters with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				t.Error("expected acquisition to be cancelled")
				ls.Release(token)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Verify 5 are waiting
	stats := ls.Stats()
	if stats.Running != 2 || stats.Waiting != 5 {
		t.Errorf("expected Running=2, Waiting=5, got %+v", stats)
	}

	// Cancel all waiters
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Verify waiters are gone
	stats = ls.Stats()
	if stats.Running != 2 || stats.Waiting != 0 {
		t.Errorf("expected Running=2, Waiting=0 after cancellation, got %+v", stats)
	}

	close(blocker)
	wg.Wait()

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected final stats Running=0, Waiting=0, got %+v", finalStats)
	}
}

func TestLoadshedder_WaitingQueue_PartialWaitersCancelled(t *testing.T) {
	ls := New(Config{
		Limit:        2,
		WaitingLimit: 10,
	})

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill running slots
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(context.Background())
			if !token.Accepted() {
				t.Error("expected running acquisition to succeed")
				return
			}
			<-blocker
			ls.Release(token)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Start 2 waiters with timeout context (will fail)
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer timeoutCancel()

	timedOut := atomic.Int64{}
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(timeoutCtx)
			if token.Accepted() {
				t.Error("expected timeout acquisition to fail")
				ls.Release(token)
			} else {
				timedOut.Add(1)
			}
		}()
	}

	// Start 1 waiter with background context (will succeed)
	succeeded := atomic.Int64{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, token := ls.Acquire(context.Background())
		if !token.Accepted() {
			t.Error("expected background context acquisition to succeed")
			return
		}
		succeeded.Add(1)
		time.Sleep(50 * time.Millisecond)
		ls.Release(token)
	}()

	time.Sleep(50 * time.Millisecond)

	// Verify 3 are waiting
	stats := ls.Stats()
	if stats.Running != 2 || stats.Waiting != 3 {
		t.Errorf("expected Running=2, Waiting=3, got %+v", stats)
	}

	// Wait for timeouts to expire
	time.Sleep(100 * time.Millisecond)

	// Verify 2 timed out
	if timedOut.Load() != 2 {
		t.Errorf("expected 2 timeouts, got %d", timedOut.Load())
	}

	// Release 1 running slot
	blocker <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Verify the background context waiter acquired
	if succeeded.Load() != 1 {
		t.Errorf("expected 1 successful acquisition, got %d", succeeded.Load())
	}

	close(blocker)
	wg.Wait()

	// Verify final stats
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected final stats Running=0, Waiting=0, got %+v", finalStats)
	}
}
