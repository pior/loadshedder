package loadshedder

import (
	"context"
	"sync"
	"testing"
)

// Token safety tests verify that Release() operations are safe in all scenarios:
// double release, releasing rejected tokens, releasing nil, concurrent releases.

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
