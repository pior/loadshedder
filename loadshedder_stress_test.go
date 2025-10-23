package loadshedder

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Stress tests verify behavior under high concurrency, rapid operations, and sustained load.
// These tests may take longer (seconds) but should complete within 30 seconds.

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

// New load tests

func TestLoadshedder_RapidAcquireReleaseCycles(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit: 10,
	})

	const numGoroutines = 100
	const numIterations = 1000

	var wg sync.WaitGroup
	maxRunning := atomic.Int64{}
	operations := atomic.Int64{}

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range numIterations {
				stats, token := ls.Acquire(ctx)

				// Track max running
				for {
					current := maxRunning.Load()
					if stats.Running <= current || maxRunning.CompareAndSwap(current, stats.Running) {
						break
					}
				}

				if token.Accepted() {
					// Immediate release - maximum churn
					ls.Release(token)
				}

				operations.Add(1)
			}
		}()
	}

	wg.Wait()

	// Verify no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", finalStats)
	}

	// Verify never exceeded limit
	if maxRunning.Load() > 10 {
		t.Errorf("exceeded limit: max running=%d", maxRunning.Load())
	}

	// Verify all operations completed
	expected := int64(numGoroutines * numIterations)
	if operations.Load() != expected {
		t.Errorf("expected %d operations, got %d", expected, operations.Load())
	}

	t.Logf("Rapid cycles: %d operations, max running: %d", operations.Load(), maxRunning.Load())
}

func TestLoadshedder_RapidAcquireWithVariableDelay(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit: 20,
	})

	var wg sync.WaitGroup
	maxRunning := atomic.Int64{}

	// Fast goroutines (50): acquire, sleep 1ms, release
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range 20 {
				stats, token := ls.Acquire(ctx)
				if token.Accepted() {
					// Track max
					for {
						current := maxRunning.Load()
						if stats.Running <= current || maxRunning.CompareAndSwap(current, stats.Running) {
							break
						}
					}

					time.Sleep(1 * time.Millisecond)
					ls.Release(token)
				}
			}
		}()
	}

	// Slow goroutines (20): acquire, sleep 100ms, release
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			stats, token := ls.Acquire(ctx)
			if token.Accepted() {
				// Track max
				for {
					current := maxRunning.Load()
					if stats.Running <= current || maxRunning.CompareAndSwap(current, stats.Running) {
						break
					}
				}

				time.Sleep(100 * time.Millisecond)
				ls.Release(token)
			}
		}()
	}

	// Burst goroutines (30): acquire/release as fast as possible
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range 50 {
				stats, token := ls.Acquire(ctx)
				if token.Accepted() {
					// Track max
					for {
						current := maxRunning.Load()
						if stats.Running <= current || maxRunning.CompareAndSwap(current, stats.Running) {
							break
						}
					}

					ls.Release(token)
				}
			}
		}()
	}

	wg.Wait()

	// Verify no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", finalStats)
	}

	// Verify never exceeded limit
	if maxRunning.Load() > 20 {
		t.Errorf("exceeded limit: max running=%d", maxRunning.Load())
	}

	t.Logf("Mixed workload: max running: %d", maxRunning.Load())
}

func TestLoadshedder_WaitingQueue_RapidReleaseWithWaiters(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        10,
		WaitingLimit: 50,
	})

	var wg sync.WaitGroup
	blocker := make(chan struct{})

	// Fill running slots
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				<-blocker
				ls.Release(token)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Queue 50 waiters
	acquired := atomic.Int64{}
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				acquired.Add(1)
				time.Sleep(10 * time.Millisecond)
				ls.Release(token)
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)

	// Verify 50 are waiting
	stats := ls.Stats()
	if stats.Running != 10 || stats.Waiting != 50 {
		t.Errorf("expected Running=10, Waiting=50, got %+v", stats)
	}

	// Rapidly release all 10 running slots at once
	close(blocker)

	// Wait for all to complete
	wg.Wait()

	// All 50 waiters should have eventually acquired
	if acquired.Load() != 50 {
		t.Errorf("expected 50 waiters to acquire, got %d", acquired.Load())
	}

	// Verify no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", finalStats)
	}

	t.Logf("Rapid release: all %d waiters acquired successfully", acquired.Load())
}

func TestLoadshedder_WaitingQueue_BurstFillAndDrain(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        10,
		WaitingLimit: 20,
	})

	var wg sync.WaitGroup
	accepted := atomic.Int64{}
	rejected := atomic.Int64{}

	// Send 100 concurrent requests
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			_, token := ls.Acquire(ctx)
			if !token.Accepted() {
				rejected.Add(1)
				return
			}

			accepted.Add(1)
			time.Sleep(50 * time.Millisecond)
			ls.Release(token)
		}()
	}

	// Give time for burst to hit
	time.Sleep(10 * time.Millisecond)

	// Should see: 10 running, ~20 waiting
	stats := ls.Stats()
	if stats.Running != 10 {
		t.Errorf("expected Running=10 during burst, got %+v", stats)
	}

	wg.Wait()

	// Should have accepted 30 (10 immediate + 20 waiting) and rejected 70
	if accepted.Load() != 30 {
		t.Errorf("expected 30 accepted, got %d", accepted.Load())
	}
	if rejected.Load() != 70 {
		t.Errorf("expected 70 rejected, got %d", rejected.Load())
	}

	// Verify no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", finalStats)
	}

	t.Logf("Burst: %d accepted, %d rejected", accepted.Load(), rejected.Load())
}

func TestLoadshedder_WaitingQueue_ContinuousPressure(t *testing.T) {
	ctx := context.Background()

	ls := New(Config{
		Limit:        10,
		WaitingLimit: 20,
	})

	// Run for 2 seconds
	done := make(chan struct{})
	time.AfterFunc(2*time.Second, func() { close(done) })

	var wg sync.WaitGroup
	maxRunning := atomic.Int64{}
	maxWaiting := atomic.Int64{}

	// Continuously submit requests
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-done:
					return
				default:
					stats, token := ls.Acquire(ctx)

					// Track maxes
					for {
						current := maxRunning.Load()
						if stats.Running <= current || maxRunning.CompareAndSwap(current, stats.Running) {
							break
						}
					}
					for {
						current := maxWaiting.Load()
						if stats.Waiting <= current || maxWaiting.CompareAndSwap(current, stats.Waiting) {
							break
						}
					}

					if token.Accepted() {
						// Variable work time
						time.Sleep(time.Duration(10+stats.Running%40) * time.Millisecond)
						ls.Release(token)
					} else {
						// Back off on rejection
						time.Sleep(5 * time.Millisecond)
					}
				}
			}
		}()
	}

	wg.Wait()

	// Verify constraints were maintained
	// Note: Due to timing, waiting might temporarily spike slightly above limit
	// but should be close to the limit
	if maxRunning.Load() > 10 {
		t.Errorf("running exceeded limit: max=%d", maxRunning.Load())
	}
	if maxWaiting.Load() > 25 {
		t.Errorf("waiting far exceeded limit: max=%d (limit=20, allow some tolerance)", maxWaiting.Load())
	}

	// Verify no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", finalStats)
	}

	t.Logf("Continuous pressure: max running=%d, max waiting=%d", maxRunning.Load(), maxWaiting.Load())
}

func TestLoadshedder_MixedContextCancellation(t *testing.T) {
	ls := New(Config{
		Limit:        5,
		WaitingLimit: 30,
	})

	blocker := make(chan struct{})
	var wg sync.WaitGroup

	// Fill running slots
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(context.Background())
			if token.Accepted() {
				<-blocker
				ls.Release(token)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)

	succeeded := atomic.Int64{}
	timeout50 := atomic.Int64{}
	timeout200 := atomic.Int64{}

	// 10 with background context (should eventually succeed)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token := ls.Acquire(context.Background())
			if token.Accepted() {
				succeeded.Add(1)
				time.Sleep(20 * time.Millisecond)
				ls.Release(token)
			}
		}()
	}

	// 10 with 50ms timeout (should timeout)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				t.Error("expected 50ms timeout to fail")
				ls.Release(token)
			} else {
				timeout50.Add(1)
			}
		}()
	}

	// 10 with 200ms timeout (might succeed or timeout)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, token := ls.Acquire(ctx)
			if token.Accepted() {
				succeeded.Add(1)
				time.Sleep(20 * time.Millisecond)
				ls.Release(token)
			} else {
				timeout200.Add(1)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Randomly release running slots over time
	for range 5 {
		time.Sleep(20 * time.Millisecond)
		blocker <- struct{}{}
	}

	wg.Wait()

	// All 10 with 50ms timeout should have failed
	if timeout50.Load() != 10 {
		t.Errorf("expected 10 short timeouts, got %d", timeout50.Load())
	}

	// Total should account for all 30 requests
	total := succeeded.Load() + timeout50.Load() + timeout200.Load()
	if total != 30 {
		t.Errorf("expected 30 total outcomes, got %d", total)
	}

	// Verify no leaks
	finalStats := ls.Stats()
	if finalStats.Running != 0 || finalStats.Waiting != 0 {
		t.Errorf("expected Running=0, Waiting=0, got %+v", finalStats)
	}

	t.Logf("Mixed contexts: %d succeeded, %d timeout(50ms), %d timeout(200ms)",
		succeeded.Load(), timeout50.Load(), timeout200.Load())
}
